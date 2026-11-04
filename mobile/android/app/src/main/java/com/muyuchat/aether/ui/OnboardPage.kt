package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.AccountQuotaRow
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ProviderSummary
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize

/**
 * 「接入渠道」页：1:1 对标桌面 `internal/web/ui2/js/views/onboard.js`。
 *
 * - 标题/副标题照抄桌面（`onboard.js:40-41`）；
 * - 工具条照抄桌面（`onboard.js:122-128`）里手机上做得成的部分：刷新渠道。
 *   桌面还有「扫描本机登录态」(`importapps`) 和「从 Alma 导入」(`import/alma`)，
 *   两者都要扫/写桌面客户端目录，手机上不做（`DESKTOP_PARITY_GAPS.md` §2-14 已判 ⛔）；
 * - 渠道卡片照抄桌面（`onboard.js:181-196`）：名称、已有账号数、可用的接入方式、「接入 / 再加一个」；
 * - 接入方式清单照抄桌面 `METHOD_LABEL`（`onboard.js:11-26`）：手机上真正走得通的只有
 *   **Qoder 设备码登录**（其余方式要么要桌面浏览器回调、要么要本机凭据文件）。
 *
 * 账号包导入/导出按需求从「账号与额度」页搬到这里（桌面在渠道页菜单里有导出，见 `channels.js:819`）。
 */
@Composable
internal fun OnboardPage(
    status: GatewayStatus,
    providers: List<ProviderSummary>,
    accounts: List<AccountQuotaRow>,
    qoderLoginState: QoderLoginUiState,
    onStartQoderLogin: () -> Unit,
    onOpenQoderLogin: (String) -> Boolean,
    onCancelQoderLogin: () -> Unit,
    onDismissQoderLogin: () -> Unit,
    onPickBundle: () -> Unit,
    onImportBundle: () -> Unit,
    bundleHost: @Composable () -> Unit,
    onRefresh: () -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var pickedProviderId by rememberSaveable { mutableStateOf<String?>(null) }
    LaunchedRefresh(status, onRefresh)

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item { MobileHeader(status) }
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                McaIconButton(contentDescription = "返回", onClick = onBack) {
                    Icon(
                        painter = painterResource(R.drawable.ic_back),
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onBackground,
                        modifier = Modifier.size(McaIconSize.Optical.Action),
                    )
                }
                Spacer(Modifier.width(8.dp))
                Column(Modifier.weight(1f)) {
                    // 文案照抄桌面 onboard.js:40-41
                    Text(
                        text = "接入渠道",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "多平台登录：本机导入、浏览器授权、设备码、手动凭据",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaIconButton(
                    contentDescription = "刷新渠道",
                    onClick = onRefresh,
                    enabled = status.running,
                ) {
                    Icon(
                        painter = painterResource(R.drawable.ic_refresh),
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(McaIconSize.Optical.Action),
                    )
                }
            }
        }

        // ——— 渠道列表（桌面 onboard.js:168-196 的 grid-3 卡片） ———
        item { DeepSectionTitle(text = "按渠道接入", trailing = "${providers.size} 个渠道") }
        if (providers.isEmpty()) {
            item {
                DeepStateCard(
                    title = "还没有可接入的渠道",
                    detail = "网关启动后会带回可用渠道清单；如果是刚启动，点右上角刷新一次。",
                    actionLabel = "刷新渠道",
                    onAction = onRefresh,
                )
            }
        } else {
            items(providers.size) { index ->
                val provider = providers[index]
                OnboardProviderCard(
                    provider = provider,
                    accountCount = accounts.count { it.providerId == provider.id },
                    onConnect = { pickedProviderId = provider.id },
                )
            }
        }

        // ——— 账号包（从额度页搬来：桌面在渠道页菜单里有「导出账号包」channels.js:819-838） ———
        item { DeepSectionTitle(text = "账号包") }
        item {
            DeepCard {
                DeepCardBody("导入把加密账号包里的账号写进本机账号池；导出把本机账号打成加密包分享回桌面。")
                Spacer(Modifier.height(8.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(onClick = onImportBundle, style = McaButtonStyle.Primary) {
                        Text("导入账号包")
                    }
                    McaButton(onClick = onPickBundle, style = McaButtonStyle.Outlined) {
                        Text("选择账号包")
                    }
                }
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "导入要选文件并输入口令；口令只用于本次解密，不保存、不上传。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        item { AccountExportSection(status = status) }
    }

    val picked = providers.firstOrNull { it.id == pickedProviderId }
    if (picked != null) {
        OnboardConnectDialog(
            provider = picked,
            qoderLoginState = qoderLoginState,
            onStartQoderLogin = onStartQoderLogin,
            onOpenQoderLogin = onOpenQoderLogin,
            onCancelQoderLogin = onCancelQoderLogin,
            onDismissQoderLogin = onDismissQoderLogin,
            onDismiss = {
                pickedProviderId = null
                onDismissQoderLogin()
            },
        )
    }
    // 口令弹窗（预览/导入共用一份逻辑，见 BundleImportHost.kt）
    bundleHost()
}

/** 首屏拉一次（与其它页一致：进入就刷新）。 */
@Composable
private fun LaunchedRefresh(status: GatewayStatus, onRefresh: () -> Unit) {
    androidx.compose.runtime.LaunchedEffect(status.addr, status.running) {
        if (status.running) {
            onRefresh()
        }
    }
}

/** 一个渠道卡片：名称 + 描述 + 已有账号数 + 接入按钮（桌面 `onboard.js:181-196`）。 */
@Composable
private fun OnboardProviderCard(
    provider: ProviderSummary,
    accountCount: Int,
    onConnect: () -> Unit,
) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(
                    text = provider.name.ifBlank { provider.id },
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                if (provider.description.isNotBlank()) {
                    Text(
                        text = provider.description,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                Spacer(Modifier.height(4.dp))
                Text(
                    text = onboardProviderMeta(provider.id, accountCount),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.width(8.dp))
            McaButton(
                onClick = onConnect,
                style = McaButtonStyle.Outlined,
            ) { Text(if (accountCount > 0) "再加一个" else "接入") }
        }
    }
}

/** 接入方式弹窗：Qoder 走现有设备码流程，其余方式照抄桌面文案并说明为什么手机上不行。 */
@Composable
private fun OnboardConnectDialog(
    provider: ProviderSummary,
    qoderLoginState: QoderLoginUiState,
    onStartQoderLogin: () -> Unit,
    onOpenQoderLogin: (String) -> Boolean,
    onCancelQoderLogin: () -> Unit,
    onDismissQoderLogin: () -> Unit,
    onDismiss: () -> Unit,
) {
    val supportsQoder = hasQoderDeviceLogin(provider.id)
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = "接入 · ${provider.name.ifBlank { provider.id }}",
                fontWeight = FontWeight.Bold,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        },
        text = {
            Column {
                if (supportsQoder) {
                    QoderLoginSection(
                        state = qoderLoginState,
                        onStart = onStartQoderLogin,
                        onOpenUrl = onOpenQoderLogin,
                        onCancel = onCancelQoderLogin,
                        onDismiss = onDismissQoderLogin,
                    )
                } else {
                    Text(
                        text = onboardMethodsLine(provider.id),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = onboardUnsupportedHint(),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        },
        confirmButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )
}

/** 桌面 `onboard.js:209`：`qoder-` 前缀的渠道额外支持设备码登录。 */
internal fun hasQoderDeviceLogin(providerId: String): Boolean =
    providerId.trim().lowercase().startsWith("qoder-")

/** 桌面 `METHOD_LABEL`（`onboard.js:11-26`）里手机端真正能用的方式。 */
internal val ONBOARD_METHOD_LABELS: Map<String, String> = mapOf(
    "local_import" to "本机导入",
    "oauth_pkce" to "浏览器授权",
    "device_code" to "设备码",
    "manual_input" to "手动凭据",
    "webview_login" to "浏览器登录",
    "minimax_device" to "MiniMax 设备码",
    "qoder_device" to "Qoder 登录",
    "workbuddy_device" to "WorkBuddy 登录",
    "codebuddy_device" to "CodeBuddy 登录",
    "zed_device" to "Zed 登录",
    "kiro_device" to "Kiro 登录",
    "trae_device" to "Trae 登录",
    "windsurf_login" to "Windsurf 登录",
    "token_json" to "Token / JSON",
)

/** 渠道卡片副标题：账号数 + 手机端可用的接入方式（Qoder 系给设备码登录）。 */
internal fun onboardProviderMeta(providerId: String, accountCount: Int): String = buildString {
    append("已有 $accountCount 个账号")
    append(" · ")
    append(if (hasQoderDeviceLogin(providerId)) "可用：Qoder 登录" else "可用：账号包导入")
}

/** 不支持设备码登录的渠道：把桌面列出的方式如实念一遍，不假装手机上能用。 */
internal fun onboardMethodsLine(providerId: String): String {
    val methods = if (hasQoderDeviceLogin(providerId)) {
        listOf("qoder_device")
    } else {
        listOf("local_import", "oauth_pkce", "device_code", "manual_input")
    }
    return "桌面端支持的接入方式：" + methods.mapNotNull { ONBOARD_METHOD_LABELS[it] }.joinToString(" · ")
}

/** 为什么手机上只能做一部分（如实说明，不给假按钮）。 */
internal fun onboardUnsupportedHint(): String =
    "手机上目前只能完成 Qoder 设备码登录与账号包导入；" +
        "其余方式（本机导入 / 浏览器授权 / 手动凭据）需要桌面端的浏览器回调或本机凭据文件，请在桌面端完成。"
