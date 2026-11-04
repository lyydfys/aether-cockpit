package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.text.BasicTextField
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

/**
 * 「导出账号包」区块：把本机账号封成 `.aether-accounts` 交给桌面端导入。
 *
 * 文案必须说实话：这份文件**包含真实凭证**，等于把账号交出去；口令丢了就打不开，
 * 泄露了就等于泄露账号。成功文案只说"已生成，请在分享面板选择目标"，不说"已备份"。
 */
@Composable
internal fun AccountExportSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
    viewModel: AccountExportViewModel = viewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    var showPassphrase by remember { mutableStateOf(false) }
    var passphrase by remember { mutableStateOf("") }
    var validation by remember { mutableStateOf<String?>(null) }

    Surface(
        modifier = modifier.fillMaxWidth(),
        shape = McaShape.Card,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = "导出账号包",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = "把本机账号封成 .aether-accounts 交给桌面端导入。文件包含真实凭证：" +
                    "口令丢了就打不开、泄露了就等于泄露账号——请只导到你自己的设备上。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(10.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                McaButton(
                    onClick = {
                        passphrase = ""
                        validation = null
                        showPassphrase = true
                    },
                    style = McaButtonStyle.Outlined,
                    enabled = status.running && state !is AccountExportUiState.Exporting,
                ) { Text(if (state is AccountExportUiState.Exporting) "生成中…" else "导出账号包…") }
            }
            when (val current = state) {
                AccountExportUiState.Idle, AccountExportUiState.Exporting -> Unit
                is AccountExportUiState.Ready -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = "已生成：${current.fileName}",
                        style = MaterialTheme.typography.titleSmall,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = "含 ${current.accounts} 个账号，${current.bytes} 字节（已加密）。" +
                            "下一步请选择保存位置，然后在桌面端用「导入账号包」恢复。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(8.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        McaButton(
                            onClick = {
                                runCatching {
                                    context.startActivity(viewModel.shareIntent(current))
                                }
                            },
                            style = McaButtonStyle.Primary,
                        ) { Text("分享 / 保存") }
                        McaButton(onClick = viewModel::reset, style = McaButtonStyle.Outlined) { Text("关闭") }
                    }
                }
                is AccountExportUiState.Failed -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = when (current.code) {
                            "gateway_not_running" -> "网关未启动：先回「概览」启动网关"
                            "weak_passphrase" -> "口令不符合要求：${current.message}"
                            else -> "导出失败：${current.message}（错误码：${current.code}）"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.height(8.dp))
                    McaButton(onClick = viewModel::reset, style = McaButtonStyle.Outlined) { Text("知道了") }
                }
            }
        }
    }

    if (showPassphrase) {
        McaDialog(
            onDismissRequest = { showPassphrase = false },
            title = { Text("设置账号包口令", fontWeight = FontWeight.Bold) },
            text = {
                Column {
                    Text(
                        text = "口令至少 8 位。桌面端导入时要输入同一个口令；口令不会保存在手机里。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(8.dp))
                    AccountExportField("口令（至少 8 位）", passphrase) {
                        passphrase = it
                        validation = null
                    }
                    validation?.let {
                        Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    }
                }
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        val problem = viewModel.passphraseProblem(passphrase)
                        validation = problem
                        if (problem == null) {
                            showPassphrase = false
                            viewModel.export(passphrase)
                        }
                    },
                    style = McaButtonStyle.Primary,
                    enabled = state !is AccountExportUiState.Exporting,
                ) { Text("导出") }
            },
            dismissButton = {
                McaButton(onClick = { showPassphrase = false }, style = McaButtonStyle.Outlined) { Text("取消") }
            },
        )
    }
}

/** 口令输入框：与其它页面一致，BasicTextField 自己撑到 48dp，密码态显示。 */
@Composable
private fun AccountExportField(label: String, value: String, onValueChange: (String) -> Unit) {
    Column(Modifier.padding(vertical = 4.dp)) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(4.dp))
        Surface(
            shape = McaShape.CardAction,
            color = MaterialTheme.colorScheme.surface,
            border = McaHairline.stroke(),
        ) {
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                singleLine = true,
                textStyle = MaterialTheme.typography.bodyMedium.copy(
                    color = MaterialTheme.colorScheme.onSurface,
                ),
                visualTransformation = PasswordVisualTransformation(),
                cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = 48.dp)
                    .wrapContentHeight(Alignment.CenterVertically)
                    .padding(horizontal = 12.dp)
                    .semantics { contentDescription = label },
            )
        }
    }
}
