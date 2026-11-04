package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
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
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.runtime.AccountSummary
import com.muyuchat.aether.runtime.WakeDraft
import com.muyuchat.aether.runtime.WakeDraftProblems
import com.muyuchat.aether.runtime.wakeIntervalLabel
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape

/**
 * 所有表单/确认对话框的内容限高。
 *
 * 320dp 是「软键盘弹起后仍给确认按钮留出可点区域」的上限：再高的内容由
 * `verticalScroll` 吸收，不让 AlertDialog 的按钮行被顶出窗口。
 */
internal val DialogContentMaxHeight: Dp = 320.dp

/**
 * 新建唤醒任务。
 *
 * 三句如实表述：
 * 1. 同一账号重复保存是**更新**已有任务，不会重复建；
 * 2. 建成功只说"已创建"，**不承诺准点**——手机端调度是尽力而为；
 * 3. 间隔按服务端区间（15 分钟 ~ 7 天）本地先校验，并给一句人话解释。
 */
@Composable
internal fun WakeCreateDialog(
    accounts: List<AccountSummary>,
    creating: Boolean,
    problems: WakeDraftProblems?,
    error: String?,
    onDismiss: () -> Unit,
    onSave: (WakeDraft) -> Unit,
) {
    var accountId by remember { mutableStateOf(accounts.firstOrNull { it.enabled }?.id ?: "") }
    var model by remember { mutableStateOf("") }
    var minutes by remember { mutableStateOf("60") }
    var maxTokens by remember { mutableStateOf("16") }

    val minutesValue = minutes.trim().toIntOrNull() ?: 0

    McaDialog(
        onDismissRequest = { if (!creating) onDismiss() },
        title = {
            Text(
                text = "新建唤醒任务",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column(
                Modifier.heightIn(max = DialogContentMaxHeight).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Text(
                    text = "同一账号再次保存是更新已有任务，不会重复建。手机端执行是尽力而为，系统省电策略可能停掉网关。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (accounts.isEmpty()) {
                    Text(
                        text = "本机还没有可用账号：先到「渠道」页添加或导入账号。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                } else {
                    Text(
                        text = "账号（共 ${accounts.size} 个）",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    accounts.take(12).forEach { account ->
                        McaButton(
                            onClick = { accountId = account.id },
                            style = if (account.id == accountId) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                        ) {
                            Text(
                                text = account.label.ifBlank { account.id },
                                maxLines = 1,
                            )
                        }
                    }
                    if (accounts.size > 12) {
                        Text(
                            text = "…另有 ${accounts.size - 12} 个账号（只列前 12 个）",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                WakeField("间隔（分钟，15 ~ 10080）", minutes, problems?.everyMinutes, numeric = true) { minutes = it }
                Text(
                    text = if (minutesValue > 0) wakeIntervalLabel(minutesValue) else "间隔未知",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                WakeField("模型（可留空，用渠道默认）", model, null) { model = it }
                WakeField("max_tokens（≤ 64，留空用 16）", maxTokens, null, numeric = true) { maxTokens = it }
                if (problems?.accountId != null) {
                    Text(
                        text = problems.accountId,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                if (error != null) {
                    Text(
                        text = error,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        confirmButton = {
            McaButton(
                onClick = {
                    onSave(
                        WakeDraft(
                            accountId = accountId,
                            model = model,
                            everyMinutes = minutesValue,
                            maxTokens = maxTokens.trim().toIntOrNull() ?: 16,
                        ),
                    )
                },
                style = McaButtonStyle.Primary,
                enabled = !creating && accounts.isNotEmpty(),
            ) { Text(if (creating) "创建中…" else "创建") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined, enabled = !creating) { Text("取消") }
        },
    )
}

@Composable
private fun WakeField(
    label: String,
    value: String,
    error: String?,
    numeric: Boolean = false,
    onValueChange: (String) -> Unit,
) {
    Column(Modifier.fillMaxWidth()) {
        Text(
            text = label,
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
                value = value,
                onValueChange = onValueChange,
                singleLine = true,

                textStyle = MaterialTheme.typography.bodySmall.copy(
                    color = MaterialTheme.colorScheme.onSurface,
                ),
                cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = 48.dp)
                    .wrapContentHeight(Alignment.CenterVertically)
                    .padding(horizontal = 12.dp)
                    .semantics { contentDescription = label },
            )
        }
        if (error != null) {
            Text(
                text = error,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}
