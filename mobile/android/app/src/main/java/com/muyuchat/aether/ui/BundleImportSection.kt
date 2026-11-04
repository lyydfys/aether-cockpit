package com.muyuchat.aether.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.CircularProgressIndicator
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.AccountBundlePreview
import com.muyuchat.aether.runtime.BundleAccountPreview
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/** Read-only bundle preview. It never imports or persists anything. */
@Composable
internal fun BundlePreviewSection(
    state: BundlePreviewUiState,
    importState: BundleImportUiState,
    onPick: () -> Unit,
    onImport: () -> Unit,
    onDismiss: () -> Unit,
) {
    Surface(
        shape = McaShape.Card,
        color = McaTonal.fill(),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(18.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Surface(
                    modifier = Modifier.size(30.dp),
                    shape = McaShape.Badge,
                    color = MaterialTheme.colorScheme.primary.copy(alpha = 0.10f),
                    border = McaHairline.stroke(),
                ) {
                    Box(contentAlignment = Alignment.Center) {
                        Icon(
                            painter = painterResource(R.drawable.ic_shield),
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary,
                            modifier = Modifier.size(McaIconSize.Xs),
                        )
                    }
                }
                Spacer(Modifier.width(10.dp))
                Column {
                    Text(
                        text = "账号包预览与导入",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Text(
                        text = "来自桌面端 .aether-accounts",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Spacer(Modifier.height(12.dp))
            when (state) {
                BundlePreviewUiState.Idle -> IdleBody(onPick)
                BundlePreviewUiState.Working -> WorkingBody()
                is BundlePreviewUiState.Ready -> ReadyBody(state.preview, importState, onImport, onDismiss)
                is BundlePreviewUiState.Failed -> FailedBody(state.code, state.message, onPick, onDismiss)
            }
        }
    }
}

@Composable
private fun IdleBody(onPick: () -> Unit) {
    Text(
        text = "选择加密账号包并输入导出时的口令，先查看渠道与账号摘要；确认后才写入账号池，口令不保存。",
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Spacer(Modifier.height(12.dp))
    McaButton(onClick = onPick, style = McaButtonStyle.Primary) {
        Text("选择账号包")
    }
}

@Composable
private fun WorkingBody() {
    Row(verticalAlignment = Alignment.CenterVertically) {
        CircularProgressIndicator(
            modifier = Modifier.size(20.dp),
            strokeWidth = 2.dp,
            color = MaterialTheme.colorScheme.primary,
        )
        Spacer(Modifier.width(10.dp))
        Text(
            text = "正在本地解密账号包…",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun ReadyBody(
    preview: AccountBundlePreview,
    importState: BundleImportUiState,
    onImport: () -> Unit,
    onDismiss: () -> Unit,
) {
    Text(
        text = "${preview.accounts.size} 个账号 · ${preview.providerCount} 个渠道",
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.SemiBold,
        color = MaterialTheme.colorScheme.onSurface,
    )
    val exportedAt = formatExportedAt(preview.exportedAtSec)
    if (exportedAt.isNotEmpty()) {
        Spacer(Modifier.height(2.dp))
        Text(
            text = "导出时间 $exportedAt",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    if (preview.accounts.isEmpty()) {
        Spacer(Modifier.height(8.dp))
        Text(
            text = "账号包里没有账号。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    } else {
        Spacer(Modifier.height(8.dp))
        preview.accounts.take(8).forEach { account -> AccountPreviewRow(account) }
        if (preview.accounts.size > 8) {
            Text(
                text = "其余 ${preview.accounts.size - 8} 个账号未展开",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    Spacer(Modifier.height(10.dp))
    when (importState) {
        BundleImportUiState.Idle -> {
            Text(
                text = "核对无误后可导入；导入会把账号写入本机账号池。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(12.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) {
                    Text("关闭预览")
                }
                McaButton(onClick = onImport, style = McaButtonStyle.Primary) {
                    Text("导入账号")
                }
            }
        }
        BundleImportUiState.Working -> Row(verticalAlignment = Alignment.CenterVertically) {
            CircularProgressIndicator(
                modifier = Modifier.size(18.dp),
                strokeWidth = 2.dp,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.width(10.dp))
            Text(
                text = "正在写入本机账号池…",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        is BundleImportUiState.Done -> {
            Text(
                text = if (importState.result.imported.isEmpty()) {
                    "没有账号被导入"
                } else {
                    "已导入 ${importState.result.imported.size} 个账号"
                },
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
                color = if (importState.result.imported.isEmpty()) {
                    MaterialTheme.colorScheme.error
                } else {
                    MaterialTheme.colorScheme.primary
                },
            )
            if (importState.result.skipped > 0) {
                Spacer(Modifier.height(2.dp))
                Text(
                    text = "跳过 ${importState.result.skipped} 个重复账号",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            val firstFailure = importState.result.failed.firstOrNull()
            if (firstFailure != null) {
                Spacer(Modifier.height(2.dp))
                Text(
                    text = "失败 ${importState.result.failed.size} 个：${firstFailure.label.ifBlank { "未命名账号" }} — ${firstFailure.error}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Spacer(Modifier.height(12.dp))
            McaButton(onClick = onDismiss, style = McaButtonStyle.Primary) {
                Text("完成")
            }
        }
        is BundleImportUiState.Failed -> {
            Text(
                text = "导入失败",
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.error,
            )
            Spacer(Modifier.height(2.dp))
            Text(
                text = importState.message,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = "错误码：${importState.code}",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(12.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) {
                    Text("关闭预览")
                }
                McaButton(onClick = onImport, style = McaButtonStyle.Primary) {
                    Text("重试导入")
                }
            }
        }
    }
}

@Composable
private fun FailedBody(code: String, message: String, onPick: () -> Unit, onDismiss: () -> Unit) {
    Text(
        text = "无法预览账号包",
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.SemiBold,
        color = MaterialTheme.colorScheme.error,
    )
    Spacer(Modifier.height(4.dp))
    Text(
        text = message,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurface,
    )
    Spacer(Modifier.height(2.dp))
    Text(
        text = "错误码：$code",
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Spacer(Modifier.height(12.dp))
    PreviewActions(onPick = onPick, onDismiss = onDismiss)
}

@Composable
private fun PreviewActions(onPick: () -> Unit, onDismiss: () -> Unit) {
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) {
            Text("关闭预览")
        }
        McaButton(onClick = onPick, style = McaButtonStyle.Primary) {
            Text("重新选择")
        }
    }
}

@Composable
private fun AccountPreviewRow(account: BundleAccountPreview) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 5.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            Modifier
                .size(6.dp)
                .clip(CircleShape)
                .background(
                    if (account.disabled) {
                        MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.4f)
                    } else {
                        MaterialTheme.colorScheme.primary
                    },
                ),
        )
        Spacer(Modifier.width(8.dp))
        Column(Modifier.weight(1f)) {
            Text(
                text = account.label.ifBlank { "未命名账号" },
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = account.providerId.ifBlank { "未标明渠道" },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (account.disabled) {
            Spacer(Modifier.width(8.dp))
            Text(
                text = "已停用",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
internal fun BundlePassphraseDialog(
    onConfirm: (String) -> Unit,
    onDismiss: () -> Unit,
    title: String = "输入账号包口令",
    detail: String = "口令只用于本次本地解密，不会保存或上传。",
    confirmLabel: String = "解密预览",
) {
    var passphrase by remember { mutableStateOf("") }
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = title,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column {
                Text(
                    text = detail,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(12.dp))
                PassphraseField(value = passphrase, onValueChange = { passphrase = it })
            }
        },
        confirmButton = {
            McaButton(
                onClick = { onConfirm(passphrase) },
                style = McaButtonStyle.Primary,
                enabled = passphrase.isNotEmpty(),
            ) {
                Text(confirmLabel)
            }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) {
                Text("取消")
            }
        },
    )
}

@Composable
private fun PassphraseField(value: String, onValueChange: (String) -> Unit) {
    Surface(
        shape = McaShape.CardAction,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            modifier = Modifier
                .fillMaxWidth()
                // 口令框是密码框：BasicTextField 不吃 Compose 的触摸目标扩展，
                // 靠 heightIn 自己撑到 48dp；垂直 padding 放在它之后会把点按区压回一行字。
                .heightIn(min = 48.dp)
                .wrapContentHeight(Alignment.CenterVertically)
                .padding(horizontal = 14.dp)
                .semantics { contentDescription = "账号包口令" },
            singleLine = true,
            textStyle = MaterialTheme.typography.bodyMedium.copy(
                color = MaterialTheme.colorScheme.onSurface,
            ),
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.Password,
                imeAction = ImeAction.Done,
            ),
            decorationBox = { inner ->
                Box {
                    if (value.isEmpty()) {
                        Text(
                            text = "口令",
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    inner()
                }
            },
        )
    }
}

private fun formatExportedAt(seconds: Long): String {
    if (seconds <= 0L) {
        return ""
    }
    return Instant.ofEpochSecond(seconds)
        .atZone(ZoneId.systemDefault())
        .format(DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm"))
}
