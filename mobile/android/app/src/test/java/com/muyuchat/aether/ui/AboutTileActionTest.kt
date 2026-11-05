package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.GITHUB_SPONSORS_URL
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 关于页四宫格的 App 内行为。
 *
 * MCA 原实现（`E:\MAC .../SettingsAboutScreen.kt:192-224`）：
 * 主作者 / 开源仓库 / 意见反馈 = `onOpenWebPage(URL)`（外部浏览器），
 * 赞助支持 = `showSponsor = true`（App 内抽屉）。
 * 用户要求「不跳外部浏览器」→ 四格统一改成 App 内弹窗（标题 + 说明 + 链接 + 复制）。
 *
 * 这里钉住的是**数据契约**：每格都有标题/说明，链接只允许 GitHub HTTPS，
 * 且 `canCopy` 语义与链接存在一致（页面据此决定是否给「复制链接」）。
 */
class AboutTileActionTest {
    private val tiles = AboutTile.entries

    @Test
    fun everyTileHasTitleAndBody() {
        tiles.forEach { tile ->
            val action = aboutTileAction(tile)
            assertEquals("tile 字段要自洽", tile, action.tile)
            assertTrue("${tile.name} 缺标题", action.title.isNotBlank())
            assertTrue("${tile.name} 缺说明正文", action.body.isNotBlank())
        }
    }

    @Test
    fun titlesMatchTheFourGridCells() {
        assertEquals("主作者", aboutTileAction(AboutTile.AUTHOR).title)
        assertEquals("开源仓库", aboutTileAction(AboutTile.REPOSITORY).title)
        assertEquals("赞助支持", aboutTileAction(AboutTile.SPONSOR).title)
        assertEquals("意见反馈", aboutTileAction(AboutTile.FEEDBACK).title)
    }

    @Test
    fun linksAreGitHubHttpsOnly() {
        tiles.forEach { tile ->
            val link = aboutTileAction(tile).link
            assertTrue("${tile.name} 必须有链接", !link.isNullOrBlank())
            assertTrue("${tile.name} 只允许 https", link!!.startsWith("https://"))
            assertTrue("${tile.name} 只允许 github.com 域", link.startsWith("https://github.com/"))
        }
    }

    @Test
    fun canCopyFollowsLinkPresence() {
        tiles.forEach { tile ->
            val action = aboutTileAction(tile)
            assertTrue("${tile.name} 有链接就该能复制", action.canCopy)
            assertEquals("github.com", action.linkHost)
        }
        // 无链接时不能复制（防御性断言，防止将来新增"纯说明"格子却仍给复制按钮）
        val noLink = AboutTileAction(AboutTile.SPONSOR, "x", "y", link = null, linkHost = null)
        assertFalse(noLink.canCopy)
    }

    @Test
    fun sponsorTileOpensTheGitHubSponsorsPage() {
        // 本项目赞助页：https://github.com/sponsors/lyydfys（MCA 对应 onOpenDonatePage）
        val action = aboutTileAction(AboutTile.SPONSOR)
        assertEquals("https://github.com/sponsors/lyydfys", action.link)
        assertEquals("github.com", action.linkHost)
        assertTrue("赞助格必须可复制（App 内不跳浏览器）", action.canCopy)
    }

    @Test
    fun feedbackPointsAtIssuesAndRepositoryAtRepo() {
        assertTrue(aboutTileAction(AboutTile.FEEDBACK).link!!.endsWith("/issues"))
        val repo = aboutTileAction(AboutTile.REPOSITORY).link!!
        assertFalse("仓库链接不该是 issues", repo.endsWith("/issues"))
    }

    @Test
    fun starGuideStepsAreActionable() {
        val steps = aboutStarGuideSteps()
        assertTrue("教程至少 3 步", steps.size >= 3)
        // 每步都要是"真能做的动作"，不能是"按提示操作"这种废话
        steps.forEach { step ->
            assertTrue("步骤不能为空", step.isNotBlank())
            assertTrue("步骤不能过短（${step.length} 字）", step.length >= 12)
            assertFalse("不许出现空话", step.contains("按提示操作"))
            assertFalse("不许出现点击链接的跳转指令", step.contains("点击链接打开"))
        }
        // 必须包含仓库地址、Star 动作、以及可选的 Watch→Releases
        val all = steps.joinToString("\n")
        assertTrue("要给出仓库地址", all.contains("github.com/lyydfys/aether-cockpit"))
        assertTrue("要说清点哪里", all.contains("Star"))
        assertTrue("要提到 Starred 状态", all.contains("Starred"))
        assertTrue("可选项要写 Watch/Releases", all.contains("Watch") && all.contains("Releases"))
    }

    @Test
    fun starGuideFooterExplainsNoCost() {
        assertTrue(aboutStarGuideFooter().contains("不消耗任何资源"))
    }

    @Test
    fun sponsorDialogMentionsSponsorsPageForDirectSupport() {
        // 离线赞赏码无素材被省略，用户不该以为漏了什么 → 弹窗里必须给一句引导
        val link = aboutTileAction(AboutTile.SPONSOR).link.orEmpty()
        assertTrue("赞助格要指向 Sponsors 页面", link.contains("/sponsors/"))
        assertTrue(GITHUB_SPONSORS_URL.startsWith("https://github.com/sponsors/"))
    }

    @Test
    fun primaryActionIsOpenForEveryTile() {
        // 四格的弹窗主按钮都是「打开链接」（Custom Tabs），不是分享
        AboutTile.entries.forEach { tile ->
            assertEquals(
                "${tile.name} 主按钮应为 OPEN",
                AboutTilePrimaryAction.OPEN,
                aboutTileAction(tile).primaryAction,
            )
        }
    }

    @Test
    fun sharePayloadCarriesProjectNameAndRepo() {
        // 分享走系统分享面板，正文必须是"项目名 + 仓库链接"（MCA shareProject 同款）
        val text = shareProjectText()
        assertTrue(text.contains("Aether Cockpit"))
        assertTrue(text.contains("https://github.com/"))
        assertTrue("正文里要有仓库链接", text.contains("aether-cockpit"))
    }
}
