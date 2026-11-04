package com.muyuchat.aether.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import androidx.compose.runtime.rememberCoroutineScope
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
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * 备份导入。
 *
 * 三条必须说清的话：
 * 1. **导入不是合并设置**：隐私脱敏、价格表、虚拟组、CORS、IP 白名单等会被备份**覆盖**；
 *    资源库是按 ID 合并；账号池不会被清空（只更新匹配账号的标签）。
 * 2. 凭据不随备份回来，导入后要按提示重填。
 * 3. 导入前会用**同一个口令**自动导出一份当前配置留底（`rollback-*` 在 cache 里）。
 */
@Composable
internal fun BackupImportSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
    viewModel: BackupViewModel = viewModel(),
) {
    val state by viewModel.import.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var pendingBytes by remember { mutableStateOf<ByteArray?>(null) }
    var askPassphrase by remember { mutableStateOf(false) }
    var passphrase by remember { mutableStateOf("") }
    var validation by remember { mutableStateOf<String?>(null) }

    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri == null) {
            return@rememberLauncherForActivityResult
        }
        scope.launch {
            val bytes = withContext(Dispatchers.IO) {
                runCatching {
                    context.contentResolver.openInputStream(uri)?.use { it.readBytes() }
                }.getOrNull()
            }
            if (bytes == null || bytes.isEmpty()) {
                validation = "读不到这个文件的内容"
                return@launch
            }
            pendingBytes = bytes
            passphrase = ""
            validation = null
            askPassphrase = true
        }
    }

    Surface(
        modifier = modifier.fillMaxWidth(),
        shape = McaShape.Card,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = "导入备份",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = "导入会覆盖本机设置（脱敏规则、价格表、虚拟组、CORS、IP 白名单、配置档等）；" +
                    "资源库按 ID 合并；账号池不会被清空。凭据不随备份回来，导入后要按提示重填。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(10.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                McaButton(
                    onClick = { picker.launch(arrayOf("*/*")) },
                    style = McaButtonStyle.Outlined,
                    enabled = status.running && state !is BackupImportUiState.Importing,
                ) { Text(if (state is BackupImportUiState.Importing) "导入中…" else "选择备份文件…") }
                if (state is BackupImportUiState.Done || state is BackupImportUiState.Failed) {
                    McaButton(onClick = viewModel::resetImport, style = McaButtonStyle.Outlined) { Text("关闭") }
                }
            }
            when (val current = state) {
                BackupImportUiState.Idle, BackupImportUiState.Importing -> Unit
                is BackupImportUiState.Done -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = current.summary,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        text = if (current.rollbackFileName == null) {
                            "注意：这次没能自动留底（导出当前配置失败），如需回退只能靠你自己手上的备份。"
                        } else {
                            "已自动留底：${current.rollbackFileName}（在应用缓存里，重启后可能被系统清理）"
                        },
                        style = MaterialTheme.typography.labelSmall,
                        color = if (current.rollbackFileName == null) {
                            MaterialTheme.colorScheme.error
                        } else {
                            MaterialTheme.colorScheme.onSurfaceVariant
                        },
                    )
                }
                is BackupImportUiState.Failed -> {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = when (current.code) {
                            "gateway_not_running" -> "网关未启动：先回「概览」启动网关"
                            "weak_passphrase" -> "口令不符合要求：${current.message}"
                            "no_data" -> "没有读到备份内容"
                            else -> "导入失败：${current.message}（错误码：${current.code}）"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        }
    }

    if (askPassphrase) {
        McaDialog(
            onDismissRequest = {
                askPassphrase = false
                pendingBytes = null
            },
            title = { Text("输入备份口令", fontWeight = FontWeight.Bold) },
            text = {
                Column {
                    Text(
                        text = "这是导出那份备份时用的口令。导入前会先用同一个口令把当前配置导出一份留底。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(8.dp))
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
                                .semantics { contentDescription = "备份口令（至少 8 位）" },
                        )
                    }
                    validation?.let {
                        Spacer(Modifier.height(4.dp))
                        Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    }
                }
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        val problem = viewModel.importPassphraseProblem(passphrase)
                        validation = problem
                        val bytes = pendingBytes
                        if (problem == null && bytes != null) {
                            askPassphrase = false
                            pendingBytes = null
                            viewModel.importBackup(bytes, passphrase)
                        }
                    },
                    style = McaButtonStyle.Primary,
                    enabled = state !is BackupImportUiState.Importing,
                ) { Text("导入") }
            },
            dismissButton = {
                McaButton(
                    onClick = {
                        askPassphrase = false
                        pendingBytes = null
                    },
                    style = McaButtonStyle.Outlined,
                ) { Text("取消") }
            },
        )
    }
}
