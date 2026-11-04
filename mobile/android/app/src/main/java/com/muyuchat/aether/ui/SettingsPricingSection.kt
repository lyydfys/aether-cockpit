package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ManagementApiException
import com.muyuchat.aether.runtime.PriceRow
import com.muyuchat.aether.runtime.PricingCatalog
import com.muyuchat.aether.runtime.UsageDetailApiClient
import com.muyuchat.aether.runtime.formatMoney
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaChip
import kotlinx.coroutines.launch

/**
 * 设置 → 定价（桌面 `settings.js:710-806` 的「模型定价」卡片）。
 *
 * 这是用量页删除的那张只读价格表的新家（用户点名的缺口）：**只读** +
 * 计价币种切换（`POST /api/pricing/save {price_currency}`，桌面 `:731-735`）。
 * 逐条改价（桌面 `priceDialog :791-`）留在桌面端：手机上编辑单价会直接改账本口径。
 *
 * 自加载：与设置页的 SettingsViewModel 解耦（一次 GET /api/pricing），
 * 失败只在本卡内报错，不连坐整页。
 */
@Composable
internal fun SettingsPricingSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
) {
    val client = remember { UsageDetailApiClient() }
    val scope = rememberCoroutineScope()
    var pricing by remember { mutableStateOf<PricingCatalog?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var saving by remember { mutableStateOf(false) }

    LaunchedEffect(status.running, status.addr) {
        if (!status.running) {
            pricing = null
            error = null
            return@LaunchedEffect
        }
        error = null
        runCatching { client.fetchPricing(status) }
            .onSuccess { pricing = it }
            .onFailure { error = it.message ?: "读取价格表失败" }
    }

    DeepCard {
        DeepCardTitle("模型定价")
        val current = pricing
        if (!status.running) {
            DeepCardBody("网关未启动：启动后才能读取价格表。")
            return@DeepCard
        }
        if (error != null) {
            Text(
                text = "读不到价格表：$error",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            McaButton(onClick = { error = null }, style = McaButtonStyle.Outlined) { Text("重试") }
            return@DeepCard
        }
        if (current == null) {
            DeepCardBody("正在读取价格表…")
            return@DeepCard
        }
        // 币种切换：桌面 `settings.js:731-735`，同一个端点。
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "计价币种",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.width(8.dp))
            listOf("USD", "CNY").forEach { code ->
                McaChip(
                    selected = current.currency.equals(code, ignoreCase = true),
                    enabled = !saving,
                    onClick = {
                        if (saving || current.currency.equals(code, ignoreCase = true)) {
                            return@McaChip
                        }
                        saving = true
                        scope.launch {
                            try {
                                pricing = client.saveCurrency(status, code)
                            } catch (e: ManagementApiException) {
                                error = "切换币种失败：${e.message}（错误码：${e.code}）"
                            } catch (_: Exception) {
                                error = "切换币种失败，请重试"
                            } finally {
                                saving = false
                            }
                        }
                    },
                    label = { Text(if (code == "USD") "\$ USD" else "¥ CNY") },
                )
                Spacer(Modifier.width(8.dp))
            }
        }
        Text(
            text = pricingRateLine(current),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        val total = current.overrides.size + current.defaults.size
        if (total == 0) {
            Text(
                text = pricingEmptyCopy(),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            return@DeepCard
        }
        // 覆盖项排前面（桌面也是先 overrides 再 defaults，`settings.js:723-727`）。
        current.overrides.forEach { row ->
            PricingLine(row = row, currency = current.currency, overridden = true)
        }
        current.defaults.take(12).forEach { row ->
            PricingLine(row = row, currency = current.currency, overridden = false)
        }
        if (current.defaults.size > 12) {
            Text(
                text = "…另有 ${current.defaults.size - 12} 条内置价格（完整列表与逐条编辑在桌面端）",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Text(
            text = "价格单位：每百万 tokens；内部按美元存储，展示按所选币种换算。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun PricingLine(row: PriceRow, currency: String, overridden: Boolean) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = row.pattern,
                modifier = Modifier.weight(1f),
                style = MaterialTheme.typography.bodySmall,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            McaChip(
                selected = overridden,
                onClick = {},
                enabled = false,
                label = { Text(if (overridden) "自定义" else "官方") },
            )
        }
        Text(
            text = "输入 ${formatMoney(row.inputPerM, currency)} · 输出 ${formatMoney(row.outputPerM, currency)}" +
                " · 缓存读 ${formatMoney(row.cachedReadPerM, currency)}",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** 汇率行（桌面 `settings.js:736-743`）；`usd_cny` 恒表示 1 USD 折合多少 CNY。 */
internal fun pricingRateLine(pricing: PricingCatalog): String =
    if (pricing.usdCny > 0) {
        "汇率 1 USD = ${pricing.usdCny} CNY · 当前展示 ${pricing.currency}"
    } else {
        "汇率未提供 · 当前展示 ${pricing.currency}"
    }

/** 价格表空态：为什么空 + 下一步（不许写「暂无数据」）。 */
internal fun pricingEmptyCopy(): String =
    "为什么空：后端没有返回任何定价目录（既无内置项也无自定义覆盖）。" +
        "下一步：确认网关在运行后刷新；逐条改价请在桌面端「设置 → 定价」。"
