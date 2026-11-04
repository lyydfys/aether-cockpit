package com.muyuchat.aether.runtime

import org.json.JSONObject
import java.io.IOException
import java.io.Reader
import java.net.HttpURLConnection
import java.net.URL

/**
 * 资源库草稿（只覆盖**纯文本**三类：指令集 / 技能 / 子代理）。
 *
 * 为什么没有 MCP 的 config：`POST /api/library/mcp` 收的是原始 JSON，而列表下发的是
 * **掩码后**的 config（`env`/`headers`/`args` 打码）。保存接口没有"保持不变"的哨兵，
 * 回写等于把 `***` 写成真实密钥。所以这里**结构上就没有**承载 config 的字段。
 */
data class LibraryDraft(
    val kind: LibraryKind,
    val name: String,
    val description: String = "",
    val content: String = "",
    val tools: String = "",
    val model: String = "",
) {
    /** 名称/说明去首尾空白；正文**原样保留**（用户写的指令文本，前后空白可能是有意的）。 */
    fun normalized(): LibraryDraft = copy(name = name.trim(), description = description.trim())
}

data class LibraryDraftProblems(
    val name: String? = null,
    val content: String? = null,
) {
    val hasProblem: Boolean get() = name != null || content != null
}

internal fun validateLibraryDraft(draft: LibraryDraft): LibraryDraftProblems =
    LibraryDraftProblems(
        name = if (draft.name.isBlank()) "名称不能为空" else null,
        content = if (draft.content.isBlank()) "内容不能为空" else null,
    )

internal fun librarySavePath(kind: LibraryKind): String = when (kind) {
    LibraryKind.PROMPT -> "/api/library/prompts"
    LibraryKind.SKILL -> "/api/library/skills"
    LibraryKind.SUBAGENT -> "/api/library/agents"
    // MCP 只能在桌面端编辑：见类注释。这里直接拒绝，而不是构造一个半截请求。
    LibraryKind.MCP -> throw ManagementApiException("mcp_desktop_only", "MCP 配置只能在桌面端编辑")
}

internal fun buildLibrarySaveJson(rawDraft: LibraryDraft): String {
    val draft = rawDraft.normalized()
    if (draft.kind == LibraryKind.MCP) {
        throw ManagementApiException("mcp_desktop_only", "MCP 配置只能在桌面端编辑")
    }
    // 构造前先校验：空名/空内容不发给服务端（服务端会 400，但错误信息不如本地直白）。
    val problems = validateLibraryDraft(draft)
    if (problems.hasProblem) {
        throw ManagementApiException("invalid_draft", problems.name ?: problems.content!!)
    }
    val json = JSONObject()
        .put("name", draft.name)
        .put("description", draft.description)
        .put("content", draft.content)
    if (draft.kind == LibraryKind.SUBAGENT) {
        json.put("tools", draft.tools.trim())
        json.put("model", draft.model.trim())
    }
    return json.toString()
}

/** 新增一条资源（三类之一）。返回服务端回的条目 id（若给出）。 */
class LibraryWriteApiClient(
    private val connectTimeoutMs: Int = 5_000,
    private val readTimeoutMs: Int = 15_000,
) {
    fun save(status: GatewayStatus, draft: LibraryDraft): String {
        val body = buildLibrarySaveJson(draft)
        val path = librarySavePath(draft.kind)
        if (!status.running || status.addr.isBlank() || status.adminSession.isBlank()) {
            throw ManagementApiException("gateway_not_running", "网关未启动")
        }
        val connection = (URL("http://${status.addr}$path").openConnection() as HttpURLConnection)
        connection.connectTimeout = connectTimeoutMs
        connection.readTimeout = readTimeoutMs
        connection.requestMethod = "POST"
        connection.doOutput = true
        connection.setRequestProperty("Accept", "application/json")
        connection.setRequestProperty("Content-Type", "application/json")
        connection.setRequestProperty("Authorization", "Bearer ${status.adminSession}")
        connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
        try {
            val code = connection.responseCode
            val text = if (code in 200..299) {
                connection.inputStream.bufferedReader(Charsets.UTF_8).use { readLimited(it) }
            } else {
                val detail = runCatching {
                    connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().take(1_024) }
                        ?.let { JSONObject(it).optString("error") }
                }.getOrNull().orEmpty()
                throw ManagementApiException("http_$code", detail.ifBlank { "保存失败（HTTP $code）" }.take(160))
            }
            return runCatching { JSONObject(text).optString("id") }.getOrDefault("")
        } catch (e: ManagementApiException) {
            throw e
        } catch (_: IOException) {
            throw ManagementApiException("network_error", "无法连接本机管理接口")
        } finally {
            connection.disconnect()
        }
    }

    private fun readLimited(reader: Reader, limit: Int = 1_024 * 1024): String {
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
