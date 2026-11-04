package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

/**
 * 备份导出页。
 *
 * 只做导出：手机上有意义的动作是「把本机配置打包带走」，而导入会覆盖本机设置，
 * 误触代价太大，桌面端已有完整流程。页面如实写清这一点。
 */
@Composable
internal fun BackupPage(
    status: GatewayStatus,
    state: BackupUiState,
    onExport: (String) -> Unit,
    onShare: (BackupUiState.Ready) -> Unit,
    onReset: () -> Unit,
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var passphrase by remember { mutableStateOf("") }
    var validation by remember { mutableStateOf<String?>(null) }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = bubbleSafeContentPadding(),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
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
                    DeepPageTitle(
                        title = "备份导出",
                        subtitle = "口令密封 · 只在手机端生成",
                    )
                }
            }
        }
        item {
            Surface(
                shape = McaShape.Sheet,
                color = McaTonal.fill(),
                border = McaHairline.stroke(),
            ) {
                Column(Modifier.fillMaxWidth().padding(16.dp)) {
                    Text(
                        text = "这份备份包含账号与设置",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = "导出内容：运行时设置、虚拟组、渠道配置、账号标签、资源库与图标。" +
                            "不含账号凭证与代理密码——那些是本机绑定加密的，离开设备本来也解不开。" +
                            "文件用你的口令密封（PBKDF2 + AES-GCM），请妥善保管口令：丢了就打不开，泄露了就等于泄露配置。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        item {
            Text(
                text = "口令（至少 8 位）",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(6.dp))
            Surface(
                shape = McaShape.CardAction,
                color = MaterialTheme.colorScheme.surface,
                border = McaHairline.stroke(),
            ) {
                BasicTextField(
                    value = passphrase,
                    onValueChange = {
                        passphrase = it
                        validation = null
                    },
                    singleLine = true,
                    textStyle = MaterialTheme.typography.bodySmall.copy(
                        color = MaterialTheme.colorScheme.onSurface,
                    ),
                    cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                    visualTransformation = PasswordVisualTransformation(),
                    modifier = Modifier
                        .fillMaxWidth()
                        .heightIn(min = 48.dp)
                        .wrapContentHeight(Alignment.CenterVertically)
                        .padding(horizontal = 12.dp)
                        .semantics { contentDescription = "备份口令（至少 8 位）" },
                )
            }
            validation?.let {
                Spacer(Modifier.height(6.dp))
                Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            }
            Spacer(Modifier.height(12.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                McaButton(
                    onClick = {
                        val problem = com.muyuchat.aether.runtime.validateBackupPassphrase(passphrase)
                        validation = problem
                        if (problem == null) {
                            onExport(passphrase)
                        }
                    },
                    style = McaButtonStyle.Primary,
                    enabled = state !is BackupUiState.Exporting,
                ) { Text(if (state is BackupUiState.Exporting) "生成中…" else "生成备份文件") }
                if (state is BackupUiState.Ready || state is BackupUiState.Failed) {
                    McaButton(onClick = onReset, style = McaButtonStyle.Outlined) { Text("重置") }
                }
            }
        }
        // 导入与导出成对出现：导出留底、导入覆盖，页面必须挨着放。
        item { BackupImportSection(status = status) }
        when (val current = state) {
            BackupUiState.Idle, BackupUiState.Exporting -> Unit
            is BackupUiState.Ready -> item {
                Surface(
                    shape = McaShape.Sheet,
                    color = McaTonal.fill(),
                    border = McaHairline.stroke(),
                ) {
                    Column(Modifier.fillMaxWidth().padding(16.dp)) {
                        Text(
                            text = "已生成：${current.fileName}",
                            style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.Bold,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        Spacer(Modifier.height(6.dp))
                        Text(
                            text = "${current.bytes} 字节，已加密。下一步请选择保存位置（例如「文件」应用）。" +
                                "手机端不提供导入：恢复请用桌面端导入这个文件。",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.height(12.dp))
                        ShareButton(current, onShare)
                    }
                }
            }
            is BackupUiState.Failed -> item {
                val failure = backupExportFailureCopy(current.code, current.message)
                Surface(
                    shape = McaShape.Sheet,
                    color = McaTonal.fill(),
                    border = McaHairline.stroke(),
                ) {
                    Column(Modifier.fillMaxWidth().padding(16.dp)) {
                        // 只给状态词染 error；错误原文保持正文色。
                        Text(
                            text = failure.status,
                            style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.Bold,
                            color = MaterialTheme.colorScheme.error,
                        )
                        Spacer(Modifier.height(6.dp))
                        Text(
                            text = failure.detail,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurface,
                        )
                        if (failure.offerOverview) {
                            Spacer(Modifier.height(12.dp))
                            McaButton(onClick = onOpenOverview, style = McaButtonStyle.Primary) {
                                Text("前往概览")
                            }
                        }
                    }
                }
            }
        }
        item {
            Text(
                text = "备份文件导出后保存在应用缓存目录并立即交给分享面板；应用清缓存或重装会删掉它，" +
                    "所以请在选择的目标位置里留一份。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * 分享按钮。
 *
 * 意图由 ViewModel 组装（它才知道 FileProvider authority 与那个唯一的 content:// URI），
 * 页面只负责把用户动作转成一次回调——这样"分享什么"只有一个来源。
 */
@Composable
private fun ShareButton(ready: BackupUiState.Ready, onShare: (BackupUiState.Ready) -> Unit) {
    McaButton(onClick = { onShare(ready) }, style = McaButtonStyle.Primary) {
        Text("分享 / 保存")
    }
}

/** 导出失败的三段式文案；只有"网关未启动"才提供前往概览的下一步。 */
internal data class BackupExportFailureCopy(
    val status: String,
    val detail: String,
    val offerOverview: Boolean,
)

internal fun backupExportFailureCopy(code: String, message: String): BackupExportFailureCopy = when (code) {
    "gateway_not_running" -> BackupExportFailureCopy(
        status = "网关未启动",
        detail = "启动本地网关后即可生成备份。",
        offerOverview = true,
    )
    "weak_passphrase" -> BackupExportFailureCopy(
        status = "口令不符合要求",
        detail = message.ifBlank { "口令至少 8 位。" },
        offerOverview = false,
    )
    else -> BackupExportFailureCopy(
        status = "生成失败",
        detail = "${message.ifBlank { "服务端没有返回原因" }}（错误码：${code.ifBlank { "unknown" }}）",
        offerOverview = false,
    )
}
