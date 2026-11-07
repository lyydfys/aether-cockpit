package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * WorkBuddy 出网身份：
 *
 * - 档位 wire 值只可能是 cli / desktop / quiet / browser（与 Go 侧对齐）；
 * - UA / 版本 / 间隔 / Stainless **全部来自 `GET /api/providers/workbuddy/identity`**，
 *   客户端不再镜像 Go 常量——下面的夹具故意用"未来版本号"，证明页面不会显示写死的值；
 * - 保存值（settings.json）与生效值（接口）是两件事，不一致时要有准确提示。
 */
class WorkBuddyIdentityTest {

    /** 故意用非当前版本的 UA/版本号：只要页面跟着它走，就说明没有本地镜像。 */
    private val livePayload = """
        {
          "identity": "cli",
          "ua": "WorkBuddy/9.9.9 WorkBuddy/9.9.9 CLI/3.0.0",
          "product": "SaaS",
          "ide_version": "9.9.9",
          "cli_version": "3.0.0",
          "stainless": true,
          "stainless_version": "7.0.0",
          "pace_ms": 350,
          "machine_headers": false,
          "available": ["cli","desktop","quiet","browser","bogus"],
          "saved": "desktop"
        }
    """.trimIndent()

    @Test
    fun onlyTheFourWireValuesExist() {
        assertEquals(
            listOf("cli", "desktop", "quiet", "browser"),
            WorkBuddyIdentity.entries.map { it.wireValue },
        )
        assertEquals(WorkBuddyIdentity.CLI, WorkBuddyIdentity.Default)
    }

    @Test
    fun unknownOrEmptyReadsAsCliButStrictParseReturnsNull() {
        // 设置页的"保存值"解析：空/未知按 cli（与服务端回退一致）。
        assertEquals(WorkBuddyIdentity.CLI, WorkBuddyIdentity.fromWire(null))
        assertEquals(WorkBuddyIdentity.CLI, WorkBuddyIdentity.fromWire(""))
        assertEquals(WorkBuddyIdentity.CLI, WorkBuddyIdentity.fromWire("bogus"))
        assertEquals(WorkBuddyIdentity.CLI, WorkBuddyIdentity.fromWire("official"))
        assertEquals(WorkBuddyIdentity.QUIET, WorkBuddyIdentity.fromWire("  QUIET "))
        // 生效值解析要严格：认不出就是认不出，不能拿 cli 冒充。
        assertNull(WorkBuddyIdentity.fromWireOrNull("bogus"))
        assertNull(WorkBuddyIdentity.fromWireOrNull(""))
        assertEquals(WorkBuddyIdentity.BROWSER, WorkBuddyIdentity.fromWireOrNull("browser"))
    }

    @Test
    fun parsesTheLiveIdentityPayload() {
        val state = parseWorkBuddyIdentity(livePayload)
        assertEquals(WorkBuddyIdentity.CLI, state.effective)
        assertEquals("cli", state.effectiveRaw)
        // 这些值必须来自接口（夹具里是 9.9.9 / 3.0.0 / 7.0.0）。
        assertEquals("WorkBuddy/9.9.9 WorkBuddy/9.9.9 CLI/3.0.0", state.userAgent)
        assertEquals("SaaS", state.product)
        assertEquals("9.9.9", state.ideVersion)
        assertEquals("3.0.0", state.cliVersion)
        assertTrue(state.stainless)
        assertEquals("7.0.0", state.stainlessVersion)
        assertEquals(350, state.paceMs)
        assertFalse(state.machineHeaders)
        // available 里认不出的档位直接丢弃，不猜第五档。
        assertEquals(
            listOf(
                WorkBuddyIdentity.CLI,
                WorkBuddyIdentity.DESKTOP,
                WorkBuddyIdentity.QUIET,
                WorkBuddyIdentity.BROWSER,
            ),
            state.available,
        )
        assertEquals(WorkBuddyIdentity.DESKTOP, state.saved)
    }

    @Test
    fun livePayloadMustCarryIdentity() {
        for (body in listOf("{}", """{"ua":"WorkBuddy/1.0"}""")) {
            val error = runCatching { parseWorkBuddyIdentity(body) }.exceptionOrNull()
            assertTrue("body=$body 应报错", error is ManagementApiException)
            assertTrue(error?.message.orEmpty().contains("identity"))
        }
        for (body in listOf("not json", "[]", "\"oops\"")) {
            val error = runCatching { parseWorkBuddyIdentity(body) }.exceptionOrNull()
            assertTrue("body=$body 应报格式错误", error is ManagementApiException)
        }
        assertTrue(parseWorkBuddyIdentityOptions(null).isEmpty())
    }

    @Test
    fun unknownEffectivePresetIsShownVerbatimNotAsCli() {
        val state = parseWorkBuddyIdentity("""{"identity":"future-mode"}""")
        assertNull(state.effective)
        assertEquals("future-mode", state.effectiveRaw)
        assertEquals("future-mode", workBuddyIdentityLiveName(state))
    }

    @Test
    fun stainlessLabelUsesTheReportedVersion() {
        assertTrue(workBuddyStainlessLabel(parseWorkBuddyIdentity(livePayload)).contains("7.0.0"))
        val off = parseWorkBuddyIdentity("""{"identity":"desktop","stainless":false}""")
        assertEquals("不发", workBuddyStainlessLabel(off))
        val noVersion = parseWorkBuddyIdentity("""{"identity":"cli","stainless":true}""")
        assertEquals("发送（X-Stainless-* 头族）", workBuddyStainlessLabel(noVersion))
    }

    @Test
    fun mismatchNoteNamesBothPresets() {
        val state = parseWorkBuddyIdentity(livePayload) // 生效 cli，保存 desktop
        assertEquals(
            "已保存 桌面客户端，当前运行仍是 官方 CLI（默认），重启网关后生效",
            workBuddyIdentityMismatchNote(WorkBuddyIdentity.DESKTOP, state),
        )
        // 一致时不出提示（大小写不敏感）。
        assertNull(workBuddyIdentityMismatchNote(WorkBuddyIdentity.CLI, state))
        assertNull(
            workBuddyIdentityMismatchNote(
                WorkBuddyIdentity.DESKTOP,
                parseWorkBuddyIdentity("""{"identity":"DESKTOP"}"""),
            ),
        )
        // 服务端报了客户端不认识的档位：提示里原样带出，不翻译成四档之一。
        assertEquals(
            "已保存 官方 CLI（默认），当前运行仍是 future-mode，重启网关后生效",
            workBuddyIdentityMismatchNote(
                WorkBuddyIdentity.CLI,
                parseWorkBuddyIdentity("""{"identity":"future-mode"}"""),
            ),
        )
    }

    @Test
    fun copyTextCarriesLiveValues() {
        val text = workBuddyIdentityCopyText(parseWorkBuddyIdentity(livePayload))
        assertTrue(text.contains("运行中）：官方 CLI（默认）"))
        assertTrue(text.contains("User-Agent: WorkBuddy/9.9.9 WorkBuddy/9.9.9 CLI/3.0.0"))
        assertTrue(text.contains("X-Product: SaaS"))
        assertTrue(text.contains("X-IDE-Version: 9.9.9"))
        assertTrue(text.contains("CLI 版本: 3.0.0"))
        assertTrue(text.contains("请求间隔: 350ms"))
        assertTrue(text.contains("包版本 7.0.0"))
        assertTrue(text.contains("X-Machine-ID / X-Session-ID: 不发"))
    }

    @Test
    fun quietPaceComesFromThePayload() {
        val state = parseWorkBuddyIdentity(
            """{"identity":"quiet","pace_ms":900,"stainless":false}""",
        )
        assertEquals(WorkBuddyIdentity.QUIET, state.effective)
        assertEquals(900, state.paceMs)
    }
}
