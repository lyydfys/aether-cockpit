package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.RoutingGroup
import com.muyuchat.aether.runtime.RoutingMember
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 路由页的排版合同：成员计数（右对齐那串数字）与空态文案。
 *
 * 计数只读服务端给的 `providerOff`，不解账号健康度、不猜缺失成员的状态。
 */
class RoutingPageLayoutTest {

    @Test
    fun tallyCountsOnlineMembersFromProviderOff() {
        val group = routingGroup(
            members = listOf("a/1", "b/2"),
            memberInfo = listOf(member("a/1", providerOff = false), member("b/2", providerOff = false)),
        )
        val tally = routingGroupMemberTally(group)
        assertEquals(2, tally.total)
        assertEquals(2, tally.online)
        assertEquals(0, tally.unresolved)
        assertEquals("2/2 在线", routingMemberTallyText(tally))
    }

    @Test
    fun tallyMarksDisabledChannelAsNotOnline() {
        val group = routingGroup(
            members = listOf("a/1", "b/2", "c/3"),
            memberInfo = listOf(
                member("a/1", providerOff = false),
                member("b/2", providerOff = true),
                member("c/3", providerOff = false),
            ),
        )
        assertEquals("2/3 在线", routingMemberTallyText(routingGroupMemberTally(group)))
    }

    @Test
    fun tallySeparatesUnresolvedMembersFromOnlineOnes() {
        // members 比 memberInfo 长：缺条目的成员算「未解析」，不能算成离线，也不能算成在线。
        val group = routingGroup(
            members = listOf("a/1", "ghost/9"),
            memberInfo = listOf(member("a/1", providerOff = false)),
        )
        val tally = routingGroupMemberTally(group)
        assertEquals(2, tally.total)
        assertEquals(1, tally.online)
        assertEquals(1, tally.unresolved)
        assertEquals("1/2 在线 · 1 个未解析", routingMemberTallyText(tally))
    }

    @Test
    fun tallyFallsBackToMemberInfoWhenMembersArrayIsMissing() {
        // 老载荷没有 members 数组：以 memberInfo 的 id 为准（routingGroupMembers 的兜底）。
        val group = routingGroup(
            members = emptyList(),
            memberInfo = listOf(member("a/1", providerOff = false), member("b/2", providerOff = true)),
        )
        assertEquals("1/2 在线", routingMemberTallyText(routingGroupMemberTally(group)))
    }

    @Test
    fun emptyGroupShowsUnconfiguredInsteadOfZeroOverZero() {
        val group = routingGroup(members = emptyList(), memberInfo = emptyList())
        assertEquals("未配置", routingMemberTallyText(routingGroupMemberTally(group)))
        assertEquals("", routingMemberNamesText(group))
    }

    @Test
    fun memberNamesTruncateAfterThreeEntries() {
        val ids = listOf("a/1", "b/2", "c/3", "d/4", "e/5")
        val group = routingGroup(
            members = ids,
            memberInfo = ids.map { member(it, providerOff = false) },
        )
        assertEquals("prov/1、prov/2、prov/3 等 2 个", routingMemberNamesText(group))
    }

    @Test
    fun memberNamesKeepTheSingleEntryAsIs() {
        val group = routingGroup(
            members = listOf("a/1"),
            memberInfo = listOf(member("a/1", providerOff = false)),
        )
        assertEquals("prov/1", routingMemberNamesText(group))
    }

    @Test
    fun groupsEmptyStateSaysWhyAndWhatNext() {
        val detail = routingGroupsEmptyDetail()
        assertTrue("要说清路由组在哪里建", detail.contains("桌面端"))
        assertTrue("要说明手机端不能新建", detail.contains("只读"))
        assertTrue("要给出下一步动作", detail.contains("重新读取"))
    }

    private fun member(id: String, providerOff: Boolean): RoutingMember = RoutingMember(
        id = id,
        // 名字故意取成一致的 "1"：labels 的 provider 前缀要能拼出 prov/1。
        name = id.substringAfterLast("/"),
        providerName = "prov",
        providerOff = providerOff,
    )

    private fun routingGroup(
        members: List<String>,
        memberInfo: List<RoutingMember>,
    ): RoutingGroup = RoutingGroup(
        id = "grp-1",
        name = "测试组",
        members = members,
        routing = "order",
        routingEffective = "order",
        warnings = emptyList(),
        ready = true,
        auto = false,
        disabled = false,
        memberInfo = memberInfo,
    )
}
