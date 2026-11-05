package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 设置首页「一组一卡」的合同。
 *
 * 锁三件事：
 *  1. 首页**恰好 8 张卡**，与 `settingsGroups()` 去掉 04/10 后一一对应（不合并、不遗漏）；
 *  2. 卡片标题/副标题直接来自分组自己（不另起命名）；
 *  3. 只有读得到真实运行状态的分区才给胶囊文案，其余为空串（页面据此不渲染胶囊）。
 */
class SettingsPartitionTest {

    /** 首页应出现的 8 组：04 预设、10 关于在「更多」页，不进设置首页。 */
    private val homeGroupIds = listOf("01", "02", "03", "05", "06", "07", "08", "09")

    @Test
    fun homeHasExactlyOneCardPerGroup() {
        val partitions = settingsPartitions()
        assertEquals("一组一卡：首页恰好 8 张", 8, partitions.size)
        assertEquals("卡片 id 不能重复", partitions.size, partitions.map { it.id }.toSet().size)
        assertEquals("卡片 id 必须正好是首页 8 组", homeGroupIds, partitions.map { it.id })
        // 不再合并：每张卡只落一个分组。
        partitions.forEach { partition ->
            assertEquals("分区 ${partition.id} 不应捆绑多个分组", listOf(partition.id), partition.groupIds)
        }
    }

    @Test
    fun cardTitleAndDescriptionComeFromTheGroupItself() {
        val groups = settingsGroups().filter { it.index != "04" && it.index != "10" }
        val partitions = settingsPartitions()
        assertEquals(groups.size, partitions.size)
        groups.zip(partitions).forEach { (group, partition) ->
            assertEquals("卡片标题应等于分组标题", group.title, partition.title)
            assertEquals("卡片说明应等于分组副标题", group.subtitle, partition.description)
        }
    }

    @Test
    fun onlyGroupsWithLiveStatusCarryAChip() {
        // 只有「01 常规」接得到网关卡运行状态，其余一律不给胶囊文案（空串 → 页面不渲染）。
        val live = settingsPartitions().filter { it.hasLiveStatus }
        assertEquals(listOf("01"), live.map { it.id })
        settingsPartitions().forEach { partition ->
            val label = settingsPartitionStatus(partition, running = true)
            if (partition.hasLiveStatus) {
                assertTrue("有实时状态的分区语义应为非空", label.isNotBlank())
            } else {
                assertEquals("分区 ${partition.id} 不该有胶囊文案", "", label)
            }
            // 任何分区都不许出现"功能被关掉"式的假状态。
            listOf("已停用", "未加载", "已关闭").forEach { bad ->
                assertFalse("分区 ${partition.id} 出现假状态「$bad」", label.contains(bad))
            }
        }
    }

    @Test
    fun runningGroupSwitchesBetweenRunningAndIdle() {
        val normal = settingsPartitions().first { it.id == "01" }
        assertEquals("运行中", settingsPartitionStatus(normal, running = true))
        assertEquals("待机", settingsPartitionStatus(normal, running = false))
        assertTrue(settingsPartitionAccent(normal, running = true))
        assertFalse("网关没跑时不应再用强调色", settingsPartitionAccent(normal, running = false))
    }

    @Test
    fun siblingsEntryIsGoneAfterOneCardPerGroup() {
        // 一组一卡后不再需要「同区其它分组」入口：任何 id 都应返回空。
        homeGroupIds.forEach { id ->
            assertTrue("$id 不应再有兄弟入口", settingsPartitionSiblings(id).isEmpty())
        }
        assertTrue(settingsPartitionSiblings("").isEmpty())
        assertTrue(settingsPartitionSiblings("99").isEmpty())
    }
}
