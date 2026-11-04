package com.muyuchat.aether.runtime

import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder
import java.util.Locale

/** 资源库条目所属的仓库。四个仓库的删除端点各不相同，不能混用。 */
enum class LibraryKind(val label: String) {
    PROMPT("指令集"),
    SKILL("技能"),
    MCP("MCP"),
    SUBAGENT("子代理"),
    ;

    companion object {
        /** 页签状态是 Saveable 的字符串，回来时找不到就退回「指令」（桌面默认页签）。 */
        fun fromName(name: String): LibraryKind =
            entries.firstOrNull { it.name == name } ?: PROMPT
    }
}

/**
 * 一条资源库条目。
 *
 * 只有 id / 名称 / 时间：**没有** `config`、也没有 `content` 字段。
 * 这不是省事，是结构性的保证——MCP 的 `config` 里可能带 env 密钥（如 `API_KEY`），
 * 不该出现在手机上。
 */
data class LibraryEntry(
    val id: String,
    val name: String,
    val kind: LibraryKind,
    val updatedAt: Long,
    /**
     * MCP 服务器上被停用的工具名（`library_api.go:284-288`）。
     *
     * 只对 `kind == MCP` 有意义：桌面在详情抽屉里用它做「已停用 N 个工具」的初始状态
     * （`library.js:491`）。其它类型恒为空。
     */
    val disabledTools: List<String> = emptyList(),
)

/**
 * 库里的一个 Hook（`GET /api/library` 的 `hooks[]`，`library_api.go:244-250`）。
 *
 * 只读：桌面保存 Hook 时会同时写网关自有库与各 agent 配置文件；手机上没有 agent 配置目录，
 * 所以手机端只展示库里已同步的条目，**不提供新建/编辑/删除**（也不能触发）。
 */
data class HookEntry(
    val id: String,
    val name: String,
    val event: String,
    val matcher: String,
    val command: String,
    val timeout: Int,
    val agents: List<String>,
    val updatedAt: Long,
)

data class LibrarySnapshot(
    val entries: List<LibraryEntry>,
    /** 只读 Hook 列表；带默认值保证现有构造点不破。 */
    val hooks: List<HookEntry> = emptyList(),
)

/**
 * 一个 MCP 服务器的工具清单（`mcp_check.go` 的 `mcpCheckResult`）。
 *
 * `why`/`detail` 是服务端给出的"为什么没有工具"（未登录、连不上、超时…），
 * 桌面就是用它渲染那句提示（`library.js:509`），手机端照抄。
 */
data class McpToolList(
    val tools: List<String>,
    val why: String,
    val detail: String,
)

/**
 * 资源库只读投影 + 删除。
 *
 * 契约（`library_api.go:handleLibrary`）：`GET /api/library` 会同时下发多套字段。
 * 这里**只**读其中三套轻量/无密钥的：
 * - `prompts[]`    → 指令集（含 content，本类不解析）
 * - `skills_raw[]` → 技能（含 content，本类不解析）
 * - `mcp[]`        → MCP 的轻量行（id/name/kind，**不含 config**）
 * - `subagents[]`  → 子代理的轻量行
 *
 * ⚠ 刻意**不读** `mcp_servers[]`：那份带完整 `config`（含 env 密钥），服务端只掩掉了
 * OAuth 的 `clientSecret`，其余原样下发。手机端不解析它，等于不给密钥落地的机会。
 */
class LibraryApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 10_000,
) {
    /**
     * 仓库类动作（probe/install/check/update-all）的超时：服务端自己的预算是 20/30/60 秒
     * （`library_skills_install.go:351/394`、`library_skills_update.go:123/169`），
     * 客户端必须比它长，否则会先超时把"服务端还在干活"误报成网络错误。
     */
    private val SkillsNetworkTimeoutMs: Int = 90_000

    fun fetchLibrary(status: GatewayStatus): LibrarySnapshot =
        parseLibrary(request(status, "GET", "/api/library", null))

    fun deleteEntry(status: GatewayStatus, kind: LibraryKind, id: String) {
        request(status, "DELETE", libraryDeletePath(kind, id), null)
    }

    /**
     * 读某个 MCP 服务器的工具清单：`POST /api/library/mcp/check {names:[name], fresh:true}`
     * （`gateway.go:1002` → `mcp_check.go:168+`）。桌面点「读取工具清单」时调的同一个端点
     * （`library.js:505`）。
     */
    fun fetchMcpTools(status: GatewayStatus, name: String): McpToolList =
        parseMcpToolList(request(status, "POST", "/api/library/mcp/check", buildMcpCheckJson(name)), name)

    /**
     * 保存工具开关：`POST /api/library/servers/tools {name, names:[停用的工具名]}`
     * （`gateway.go:1050` → `mcp_tools.go:33+`，桌面 `library.js:496`）。
     *
     * 只改本机资源库里这条 MCP 的 `DisabledTools`（`UpdateMCPDisabledTools`），不写桌面 Agent 目录。
     */
    fun saveMcpDisabledTools(status: GatewayStatus, name: String, disabled: List<String>) {
        request(status, "POST", "/api/library/servers/tools", buildMcpToolsJson(name, disabled))
    }

    /**
     * 探测仓库里的技能：`POST /api/library/skills/probe {source}`（`gateway.go:1041`）。
     *
     * `source` 可以是 GitHub `owner/repo`、GitLab 地址或本机文件夹。**手机端只对网络仓库有意义**：
     * 服务端会用 `classifySkillSource` 判断类型，本机文件夹那条路在手机上读不到桌面路径。
     */
    fun probeSkills(status: GatewayStatus, source: String): SkillProbe =
        parseSkillProbe(
            request(
                status = status,
                method = "POST",
                path = "/api/library/skills/probe",
                body = buildSkillSourceJson(source),
                timeoutMs = SkillsNetworkTimeoutMs,
            ),
        )

    /**
     * 安装选中的技能：`POST /api/library/skills/install {source, paths, agents}`（`gateway.go:1042`）。
     *
     * `agents` 恒为空（见 [buildSkillInstallJson]）：只写网关自有库，不写 agent 目录。
     */
    fun installSkills(status: GatewayStatus, source: String, paths: List<String>): SkillInstallResult =
        parseSkillInstallResult(
            request(
                status = status,
                method = "POST",
                path = "/api/library/skills/install",
                body = buildSkillInstallJson(source, paths),
                timeoutMs = SkillsNetworkTimeoutMs,
            ),
        )

    /** 检查更新：`POST /api/library/skills/check`（服务端逐个比对远端内容，可能跑满 30s）。 */
    fun checkSkills(status: GatewayStatus): SkillCheckReport =
        parseSkillChecks(
            request(
                status = status,
                method = "POST",
                path = "/api/library/skills/check",
                body = "{}",
                timeoutMs = SkillsNetworkTimeoutMs,
            ),
        )

    /** 更新全部：`POST /api/library/skills/update-all`（服务端自己挑出"从仓库安装"的技能）。 */
    fun updateAllSkills(status: GatewayStatus): SkillUpdateResult =
        parseSkillUpdateResult(
            request(
                status = status,
                method = "POST",
                path = "/api/library/skills/update-all",
                body = "{}",
                timeoutMs = SkillsNetworkTimeoutMs,
            ),
        )

    private fun request(
        status: GatewayStatus,
        method: String,
        path: String,
        body: String?,
        timeoutMs: Int = readTimeoutMs,
    ): String {
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = timeoutMs
        connection.requestMethod = method
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        if (body != null) {
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "application/json")
            connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
        }
        try {
            val code = connection.responseCode
            if (code !in 200..299) {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(2_048) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "管理接口返回 $code" }.take(160))
            }
            return connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun readLimited(reader: Reader, limit: Int = 8 * 1024 * 1024): String {
        val buffer = CharArray(8 * 1024)
        val out = StringBuilder()
        while (true) {
            val read = reader.read(buffer)
            if (read < 0) {
                return out.toString()
            }
            out.append(buffer, 0, read)
            if (out.length > limit) {
                throw ManagementApiException("invalid_response", "管理接口响应过大")
            }
        }
    }
}

/** 四个仓库的删除端点不同：子代理删的是 `agents`，不是 `subagents`。 */
internal fun libraryDeletePath(kind: LibraryKind, id: String): String {
    val segment = when (kind) {
        LibraryKind.PROMPT -> "prompts"
        LibraryKind.SKILL -> "skills"
        LibraryKind.MCP -> "mcp"
        LibraryKind.SUBAGENT -> "agents"
    }
    return "/api/library/$segment/" + URLEncoder.encode(id, Charsets.UTF_8.name())
}

internal fun parseLibrary(body: String): LibrarySnapshot {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "管理接口返回的资源库数据格式不正确")
    }
    val entries = mutableListOf<LibraryEntry>()
    entries += libraryRows(root.optJSONArray("prompts"), LibraryKind.PROMPT)
    entries += libraryRows(root.optJSONArray("skills_raw"), LibraryKind.SKILL)
    entries += libraryRows(root.optJSONArray("mcp"), LibraryKind.MCP)
    entries += libraryRows(root.optJSONArray("subagents"), LibraryKind.SUBAGENT)
    return LibrarySnapshot(
        entries = entries,
        hooks = parseHooks(root.optJSONArray("hooks")),
    )
}

/**
 * 解析只读 Hook 列表。规则与服务端字段（`library_api.go:244-250`）对齐：
 * - `null` 数组 → 空表；数组项不是对象 → 跳过；
 * - `id`/`name` 都空 → 跳过（服务端视图没有 id 时用 name 兜底展示）；
 * - `agents` 缺失或空数组 → 空表；`timeout` 缺省 0；
 * - `updated_at` 缺省回落到 `created_at`，再缺省为 0。
 *
 * 不在这里 catch：坏 JSON 由 [parseLibrary]/上层统一抛 `invalid_response`。
 */
internal fun parseHooks(array: JSONArray?): List<HookEntry> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        val name = item.optString("name").trim()
        if (id.isEmpty() && name.isEmpty()) {
            return@mapNotNull null
        }
        val agents = item.optJSONArray("agents")?.let { list ->
            (0 until list.length()).mapNotNull { i ->
                list.optString(i).trim().takeIf { it.isNotEmpty() }
            }
        }.orEmpty()
        HookEntry(
            id = id,
            name = name.ifBlank { id },
            event = item.optString("event").trim(),
            matcher = item.optString("matcher").trim(),
            command = item.optString("command").trim(),
            timeout = item.optInt("timeout", 0),
            agents = agents,
            updatedAt = item.optLong("updated_at", item.optLong("created_at", 0L)),
        )
    }
}

/**
 * 解析一组行。只取 id / name / updated_at：即使数组里带着 `config` 或 `content`，
 * 也不会被搬进 [LibraryEntry]。
 */
private fun libraryRows(array: JSONArray?, kind: LibraryKind): List<LibraryEntry> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val id = item.optString("id").trim()
        if (id.isEmpty()) {
            return@mapNotNull null
        }
        val name = item.optString("name").trim().ifBlank { item.optString("title").trim() }
        val disabledTools = if (kind == LibraryKind.MCP) {
            item.optJSONArray("disabledTools")?.let { array ->
                (0 until array.length()).mapNotNull { i ->
                    array.optString(i).trim().takeIf { it.isNotEmpty() }
                }
            }.orEmpty()
        } else {
            emptyList()
        }
        LibraryEntry(
            id = id,
            name = name.ifBlank { id },
            kind = kind,
            updatedAt = item.optLong("updated_at", item.optLong("created_at", 0L)),
            disabledTools = disabledTools,
        )
    }
}

/** `{"names":[name],"fresh":true}`（桌面 `library.js:505` 逐字一致）。 */
internal fun buildMcpCheckJson(name: String): String {
    val array = JSONArray()
    array.put(name.trim())
    return JSONObject()
        .put("names", array)
        .put("fresh", true)
        .toString()
}

/** `{"name":name,"names":[停用的工具]}`：去空、去重、保序（服务端也会再清一次）。 */
internal fun buildMcpToolsJson(name: String, disabled: List<String>): String {
    val array = JSONArray()
    disabled.map { it.trim() }.filter { it.isNotEmpty() }.distinct().forEach { array.put(it) }
    return JSONObject()
        .put("name", name.trim())
        .put("names", array)
        .toString()
}

/**
 * 解析 `mcp/check` 的回包：桌面按 `r.servers[name] || r[name] || {}` 取（`library.js:506`），
 * 这里两种形态都兼容；`toolList` 缺省为空数组。
 */
internal fun parseMcpToolList(body: String, name: String): McpToolList {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "读取 MCP 工具清单返回的数据格式不正确")
    }
    val result = root.optJSONObject("servers")?.optJSONObject(name) ?: root.optJSONObject(name)
    val tools = result?.optJSONArray("toolList")?.let { array ->
        (0 until array.length()).mapNotNull { index ->
            array.optString(index).trim().takeIf { it.isNotEmpty() }
        }
    }.orEmpty()
    return McpToolList(
        tools = tools,
        why = result?.optString("why").orEmpty().trim(),
        detail = result?.optString("detail").orEmpty().trim(),
    )
}

/* ============================================================================
 * 技能：从仓库安装 / 检查更新 / 更新全部
 *
 * 端点（`internal/core/gateway/gateway.go`）：
 * - `POST /api/library/skills/probe`      `gateway.go:1041` → `handleSkillsProbe`（library_skills_install.go:346）
 * - `POST /api/library/skills/install`    `gateway.go:1042` → `handleSkillsInstall`（:380）
 * - `POST /api/library/skills/check`      `gateway.go:1043` → `handleSkillsCheck`（library_skills_update.go:122）
 * - `POST /api/library/skills/update-all` `gateway.go:1045` → `handleSkillsUpdateBulk`（:156）
 *
 * **写的是网关自有库，不是 agent 目录**：install 最终落到 `g.library.SaveSkill(...)`
 * （library_skills_install.go:168），update 走 `g.library.BackupContent` + `SaveSkill`
 * （library_skills_update.go:107-116）。只有请求体里 `agents` 非空时才会额外
 * `ApplySkillAs` 写进 agent 目录（:178-192）——所以手机端**恒传 `agents: []`**，
 * 只动本机库，绝不碰桌面 agent 配置。
 * ========================================================================== */

/** 仓库里探测到的一个候选技能（`handleSkillsProbe` 的 `candidates[]`）。 */
data class SkillCandidate(
    val name: String,
    val path: String,
    val description: String,
    /** 库里已经有同名技能（桌面把它置灰，不允许重复勾选）。 */
    val have: Boolean,
)

data class SkillProbe(
    val source: String,
    val kind: String,
    val candidates: List<SkillCandidate>,
)

/** 一条"哪一项失败了、服务端原话是什么"。 */
data class SkillProblem(val what: String, val error: String)

/** install/move 的结果（`handleSkillsInstall` 的 `result`）。 */
data class SkillInstallResult(
    val installed: List<String> = emptyList(),
    val had: List<String> = emptyList(),
    val applied: List<String> = emptyList(),
    val skipped: List<SkillProblem> = emptyList(),
)

/** 单个技能的更新检查结果（library view 里 `skills[].check`，library_api.go:326-328）。 */
data class SkillCheckRow(val name: String, val status: String, val error: String)

data class SkillCheckReport(val rows: List<SkillCheckRow>)

/** 更新全部的结果（`handleSkillsUpdateBulk` 的 `result`）。 */
data class SkillUpdateResult(
    val updated: List<String> = emptyList(),
    val unupdated: List<SkillProblem> = emptyList(),
    val gone: List<String> = emptyList(),
)

/** `{"source":…}`：探测与安装共用（桌面 `library.js:655/685`）。 */
internal fun buildSkillSourceJson(source: String): String =
    JSONObject().put("source", source.trim()).toString()

/**
 * `{"source":…,"paths":[…],"agents":[]}`。
 *
 * **`agents` 恒为空数组**：服务端只有 agents 非空才会往 agent 目录写（install:178-192），
 * 手机端不提供"装完写进 agent"的选项，避免越过写集去改桌面配置。
 */
internal fun buildSkillInstallJson(source: String, paths: List<String>): String {
    val array = JSONArray()
    paths.map { it.trim() }.filter { it.isNotEmpty() }.distinct().forEach { array.put(it) }
    return JSONObject()
        .put("source", source.trim())
        .put("paths", array)
        .put("agents", JSONArray())
        .toString()
}

/**
 * 从候选路径推技能名：取 `SKILL.md` 的**父目录名**，不是文件名。
 *
 * 与桌面/服务端同一口径：`probeGitHubSkills` 用 `name := path.Base(path.Dir(p))`
 * （`library_skills_install.go:213-217`），所以 `skills/pdf/SKILL.md` 的名字是 `pdf`。
 * 早先这里写成 `path.substringAfterLast('/')` 会得到 `SKILL.md`——一个所有技能都同名的错值。
 *
 * 根目录的 `SKILL.md`（`path.Dir` 为 `.`）服务端会回退成仓库名，客户端拿不到仓库名，
 * 只好回退成文件名；这种情况会被页面的勾选列表如实显示出来，不做猜测。
 */
internal fun skillNameFromPath(path: String): String {
    val trimmed = path.trim()
    val parent = trimmed.substringBeforeLast('/', missingDelimiterValue = "").substringAfterLast('/')
    return parent.ifBlank { trimmed.substringAfterLast('/') }
}

internal fun parseSkillProbe(body: String): SkillProbe {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "探测仓库技能返回的数据格式不正确")
    }
    val candidates = ArrayList<SkillCandidate>()
    val array = root.optJSONArray("candidates")
    if (array != null) {
        for (i in 0 until array.length()) {
            val item = array.optJSONObject(i) ?: continue
            val path = item.optString("path").trim()
            if (path.isEmpty()) {
                continue
            }
            candidates.add(
                SkillCandidate(
                    // 服务端一般已经给了 name；缺的时候按父目录名补（与桌面同一口径）。
                    name = item.optString("name").trim().ifBlank { skillNameFromPath(path) },
                    path = path,
                    description = item.optString("description").trim(),
                    have = item.optBoolean("have", false),
                ),
            )
        }
    }
    return SkillProbe(
        source = root.optString("source").trim(),
        kind = root.optString("kind").trim(),
        candidates = candidates,
    )
}

internal fun parseSkillInstallResult(body: String): SkillInstallResult {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "安装技能返回的数据格式不正确")
    }
    val result = root.optJSONObject("result") ?: JSONObject()
    return SkillInstallResult(
        installed = stringList(result.optJSONArray("installed")),
        had = stringList(result.optJSONArray("had")),
        applied = stringList(result.optJSONArray("applied")),
        skipped = skillProblems(result.optJSONArray("skipped")),
    )
}

/**
 * 更新检查的回包就是资源库全量视图（`writeLibraryView` → `libraryPluginView`）。
 * 只读 `skills[].check{status,error}`：没有 check 的技能表示本轮没查到（不是"最新"）。
 */
internal fun parseSkillChecks(body: String): SkillCheckReport {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "检查技能更新返回的数据格式不正确")
    }
    val rows = ArrayList<SkillCheckRow>()
    val array = root.optJSONArray("skills")
    if (array != null) {
        for (i in 0 until array.length()) {
            val item = array.optJSONObject(i) ?: continue
            val name = item.optString("name").trim()
            if (name.isEmpty()) {
                continue
            }
            val check = item.optJSONObject("check") ?: continue
            val status = check.optString("status").trim()
            if (status.isEmpty()) {
                continue
            }
            rows.add(
                SkillCheckRow(
                    name = name,
                    status = status,
                    error = check.optString("error").trim(),
                ),
            )
        }
    }
    return SkillCheckReport(rows = rows.sortedBy { it.name })
}

internal fun parseSkillUpdateResult(body: String): SkillUpdateResult {
    val root = try {
        JSONObject(body.trim())
    } catch (_: JSONException) {
        throw ManagementApiException("invalid_response", "更新技能返回的数据格式不正确")
    }
    val result = root.optJSONObject("result") ?: JSONObject()
    return SkillUpdateResult(
        updated = stringList(result.optJSONArray("updated")),
        unupdated = skillProblems(result.optJSONArray("unupdated")),
        gone = stringList(result.optJSONArray("gone")),
    )
}

private fun stringList(array: JSONArray?): List<String> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        array.optString(index).trim().takeIf { it.isNotEmpty() }
    }
}

/**
 * 把服务端的 `skipped[]` / `unupdated[]` 折成可显示的行。
 *
 * **一条都不能静默丢弃**：这两个数组的长度就是结论行里"跳过 N 个 / 未更新 N 个"的来源
 * （[skillInstallSummary] / [skillUpdateSummary] 数的是折完之后的条数）。如果某个元素
 * 字段缺失就把它丢掉，用户会看到"跳过 1 个"却一条明细都没有，等于把失败原因咽掉。
 * 所以字段全空时补成「未知项：未返回原因」——用户看到的是"有一项失败了，但服务端没说是哪项"。
 */
private fun skillProblems(array: JSONArray?): List<SkillProblem> {
    if (array == null) {
        return emptyList()
    }
    return (0 until array.length()).mapNotNull { index ->
        // 数组里塞 null 是畸形元素（连对象都不是），不是"一条失败"，这种才跳过。
        val item = array.optJSONObject(index) ?: return@mapNotNull null
        val what = item.optString("what").trim()
        val error = item.optString("error").trim()
        SkillProblem(what = what.ifBlank { "未知项" }, error = error.ifBlank { "未返回原因" })
    }
}

/** 检查结果的用户可读说法（桌面 `checkChip`，library.js:625-630）。 */
internal fun skillCheckLabel(status: String): String = when (status.lowercase(Locale.US)) {
    "update" -> "可更新"
    "current" -> "最新"
    "gone" -> "来源已删除"
    "unknown" -> "检查失败"
    else -> status.ifBlank { "未检查" }
}

/** 只有 `status == update` 的技能能被更新（桌面「可更新」chip 同一口径）。 */
internal fun updatableSkillNames(report: SkillCheckReport): List<String> =
    report.rows.filter { it.status.equals("update", ignoreCase = true) }.map { it.name }

/** 结论行：可更新 / 最新 / 来源已删除 / 检查失败；空报告直说"没查到"。 */
internal fun skillCheckSummary(report: SkillCheckReport): String {
    if (report.rows.isEmpty()) {
        return "没有查到可比较的技能：只有「从仓库安装」的技能（带 repo + 路径）才会参与检查。"
    }
    fun count(status: String) = report.rows.count { it.status.equals(status, ignoreCase = true) }
    return "可更新 ${count("update")} · 最新 ${count("current")} · " +
        "来源已删除 ${count("gone")} · 检查失败 ${count("unknown")}"
}

/** 安装结论：安装 / 已存在 / 跳过 / 写入 agent（恒 0，手机端不写 agent 目录）。 */
internal fun skillInstallSummary(result: SkillInstallResult): String {
    val parts = mutableListOf("已安装 ${result.installed.size} 个")
    if (result.had.isNotEmpty()) {
        parts.add("已存在 ${result.had.size} 个")
    }
    if (result.skipped.isNotEmpty()) {
        parts.add("跳过 ${result.skipped.size} 个")
    }
    parts.add("写入 agent ${result.applied.size} 个")
    return parts.joinToString(" · ")
}

/** 更新结论：已更新 / 未更新 / 来源已删除。 */
internal fun skillUpdateSummary(result: SkillUpdateResult): String {
    val parts = mutableListOf("已更新 ${result.updated.size} 个")
    if (result.unupdated.isNotEmpty()) {
        parts.add("未更新 ${result.unupdated.size} 个")
    }
    if (result.gone.isNotEmpty()) {
        parts.add("来源已删除 ${result.gone.size} 个")
    }
    return parts.joinToString(" · ")
}

/** 失败项逐条文案（`what：error`），给结果面板用。 */
internal fun skillProblemLines(problems: List<SkillProblem>): List<String> =
    problems.map { "${it.what}：${it.error}" }

