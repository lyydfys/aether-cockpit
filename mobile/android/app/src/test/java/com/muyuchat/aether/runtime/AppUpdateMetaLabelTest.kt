package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * 关于页"更新卡"两段纯文案的边界（MCA 关于页同款展示）：
 *  - [formatUpdateAssetSize]：KB/MB/GB 三档、1 位小数、≤0 返回空串（调用方整行不渲染）；
 *  - [buildApplicableDevicesLabel]：ABI 已知/未知、minSdk 缺失不得出现 `null`。
 */
class AppUpdateMetaLabelTest {

    @Test
    fun assetSizePicksTheRightUnit() {
        // 1024 字节 = 1.0 KB；未到 1 MB 仍是 KB。
        assertEquals("1.0 KB", formatUpdateAssetSize(1_024))
        assertEquals("1.2 KB", formatUpdateAssetSize(1_200))
        assertEquals("1023.0 KB", formatUpdateAssetSize(1_024L * 1_023))
        // 1 MB 起用 MB。
        assertEquals("1.0 MB", formatUpdateAssetSize(1_024L * 1_024))
        assertEquals("477.8 MB", formatUpdateAssetSize(501_000_000))
        // 1 GB 起用 GB。
        assertEquals("1.0 GB", formatUpdateAssetSize(1_024L * 1_024 * 1_024))
        assertEquals("2.5 GB", formatUpdateAssetSize((2.5 * 1024 * 1024 * 1024).toLong()))
    }

    @Test
    fun assetSizeKeepsOneDecimalPlace() {
        // 1.02 KB → 1.0 KB（四舍五入到 1 位），不是 "1 KB"、也不是 "1.02 KB"。
        assertEquals("1.0 KB", formatUpdateAssetSize(1_044))
        assertEquals("3.3 KB", formatUpdateAssetSize(3_400))
        for (bytes in listOf(1_024L, 1_200L, 501_000_000L, 1L shl 40)) {
            val text = formatUpdateAssetSize(bytes)
            val number = text.removeSuffix(" KB").removeSuffix(" MB").removeSuffix(" GB")
            assertEquals(
                "必须保留 1 位小数: $text",
                1,
                number.substringAfter('.', "").length,
            )
        }
    }

    @Test
    fun assetSizeIsEmptyWhenUnknown() {
        // 0 / 负数表示"不知道多大"，调用方整行不显示，不能显示 "0.0 KB"。
        assertEquals("", formatUpdateAssetSize(0))
        assertEquals("", formatUpdateAssetSize(-1))
        assertEquals("", formatUpdateAssetSize(Long.MIN_VALUE))
    }

    @Test
    fun applicableDevicesShowsAbiAndMinSdk() {
        // 实现是 joinToString(", ")：三段之间都用逗号，别在这里改成空格。
        assertEquals(
            "Android, arm64-v8a, minSdk 26",
            buildApplicableDevicesLabel(abis = listOf("arm64-v8a", "armeabi-v7a"), sdkInt = 26),
        )
    }

    @Test
    fun applicableDevicesDegradesWithoutAbiOrSdk() {
        // 拿不到 ABI 就只写 Android（不瞎编）；sdk 缺失不能出现 "minSdk 0" 或 "null"。
        assertEquals("Android", buildApplicableDevicesLabel())
        assertEquals("Android", buildApplicableDevicesLabel(abis = emptyList(), sdkInt = 0))
        assertEquals("Android", buildApplicableDevicesLabel(abis = listOf("  "), sdkInt = 0))
        assertEquals("Android, arm64-v8a", buildApplicableDevicesLabel(abis = listOf("arm64-v8a")))
        assertEquals("Android, minSdk 26", buildApplicableDevicesLabel(sdkInt = 26))
        for (label in listOf(
            buildApplicableDevicesLabel(),
            buildApplicableDevicesLabel(abis = listOf("arm64-v8a")),
            buildApplicableDevicesLabel(sdkInt = 26),
        )) {
            assertEquals("不能出现 null: $label", false, label.contains("null"))
        }
    }

    @Test
    fun applicableDevicesUsesOnlyTheFirstAbi() {
        assertEquals(
            "Android, arm64-v8a",
            buildApplicableDevicesLabel(abis = listOf("arm64-v8a", "armeabi-v7a"), sdkInt = 0),
        )
    }
}
