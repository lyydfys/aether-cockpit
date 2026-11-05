package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 设置分组分发的纯数据契约：
 *  - 10 组都有 route（01–08 Partition、09 Backup、10 About）且都可用；
 *  - 块表过滤：`initialGroup="05"` 只出定价；null 返回全部 9 个分区块；未知 id 不崩。
 */
class SettingsRouteTest {

    @Test
    fun tenGroupsHaveRoutesInOrder() {
        val groups = settingsGroups()
        assertEquals(10, groups.size)
        assertEquals((1..10).map { it.toString().padStart(2, '0') }, groups.map { it.index })
        groups.forEach { group ->
            val expected = when (group.index) {
                "09" -> SettingsRoute.Backup
                "10" -> SettingsRoute.About
                else -> SettingsRoute.Partition(group.index)
            }
            assertEquals("${group.index} 的 route 不对", expected, group.route)
            assertTrue("${group.index} 应 available", group.available)
        }
    }

    @Test
    fun focusedGroupOnlyRendersItsBlock() {
        val focused = settingsBlocksFor("05")
        assertEquals(1, focused.size)
        assertEquals("05", focused.single().id)
        assertEquals("定价", focused.single().title)

        // 每个 01–08 的 id 都能精确命中自己的区块。
        (1..8).forEach { i ->
            val id = i.toString().padStart(2, '0')
            assertEquals("分组 $id 应命中一块", listOf(id), settingsBlocksFor(id).map { it.id })
        }
    }

    @Test
    fun settingsHomeHasTenNavigableEntriesWithDescriptions() {
        val groups = settingsGroups()
        assertEquals(10, groups.size)
        assertEquals((1..10).map { it.toString().padStart(2, '0') }, groups.map { it.index })
        groups.forEach { group ->
            assertTrue("${group.index} 标题不能空", group.title.isNotBlank())
            assertTrue("${group.index} 导航说明不能空", group.subtitle.isNotBlank())
            assertTrue("${group.index} 应 available", group.available)
        }
        // 首页只有 10 个入口；09 走备份页，10 走关于，其余 8 个是设置分区二级页。
        assertEquals(8, settingsGroups().count { it.route is SettingsRoute.Partition })
        assertEquals(SettingsRoute.Backup, groups.first { it.index == "09" }.route)
        assertEquals(SettingsRoute.About, groups.first { it.index == "10" }.route)
    }

    @Test
    fun nullFilterReturnsAllNinePartitionBlocksInIdOrder() {
        assertEquals(
            (1..8).map { it.toString().padStart(2, '0') } + "10",
            settingsBlocksFor(null).map { it.id },
        )
        assertEquals(9, SETTINGS_PARTITION_BLOCKS.size)
    }

    @Test
    fun unknownGroupDoesNotCrash() {
        assertTrue(settingsBlocksFor("99").isEmpty())
        assertTrue(settingsBlocksFor("").isEmpty())
        assertNull(settingsGroupTitle("99"))
        assertNull(settingsGroupTitle(null))
        assertEquals("常规", settingsGroupTitle("01"))
        assertEquals("同步与备份", settingsGroupTitle("09"))
    }
}
