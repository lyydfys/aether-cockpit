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
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.runtime.LibraryDraft
import com.muyuchat.aether.runtime.LibraryDraftProblems
import com.muyuchat.aether.runtime.LibraryKind
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape

/** 手机端可新增的三类（MCP 不在其中：见 `LibraryWriteApiClient` 的类注释）。 */
internal val MOBILE_CREATABLE_KINDS = listOf(LibraryKind.PROMPT, LibraryKind.SKILL, LibraryKind.SUBAGENT)

/**
 * 新增资源对话框。
 *
 * 两条如实表述：
 * 1. 保存成功**只说"已保存到本机资源库"**，不说"已下发到 Agent"——下发要写桌面配置文件，是仅 Windows 能力；
 * 2. MCP 不在可选类型里，并在底部说明原因（config 是掩码下发的，回写会把 `***` 存成真实值）。
 */
@Composable
internal fun LibraryCreateDialog(
    saving: Boolean,
    problems: LibraryDraftProblems?,
    error: String? = null,
    onDismiss: () -> Unit,
    onSave: (LibraryDraft) -> Unit,
) {
    var kind by remember { mutableStateOf(LibraryKind.PROMPT) }
    var name by remember { mutableStateOf("") }
    var description by remember { mutableStateOf("") }
    var content by remember { mutableStateOf("") }
    var tools by remember { mutableStateOf("") }
    var model by remember { mutableStateOf("") }

    McaDialog(
        onDismissRequest = { if (!saving) onDismiss() },
        title = {
            Text(
                text = "新增资源",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
            )
        },
        text = {
            Column(
                Modifier.heightIn(max = DialogContentMaxHeight).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(
                    text = "只会保存到本机资源库，不会自动下发到桌面 Agent。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    MOBILE_CREATABLE_KINDS.forEach { value ->
                        McaButton(
                            onClick = { kind = value },
                            style = if (value == kind) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                        ) { Text(value.label) }
                    }
                }
                LibraryField("名称", name, problems?.name) { name = it }
                LibraryField("说明（可选）", description, null) { description = it }
                LibraryField(
                    label = if (kind == LibraryKind.SUBAGENT) "系统提示词" else "内容",
                    value = content,
                    error = problems?.content,
                    minHeight = 120.dp,
                ) { content = it }
                if (kind == LibraryKind.SUBAGENT) {
                    LibraryField("工具（逗号分隔，可留空）", tools, null) { tools = it }
                    LibraryField("模型（可留空）", model, null) { model = it }
                }
                if (error != null) {
                    // 保存失败时必须**在对话框里**说清楚：以前错误只画在对话框后面的页面上，
                    // 用户点「保存」会觉得"没反应"。
                    Text(
                        text = error,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                Text(
                    text = "MCP 配置只能在桌面端编辑：列表下发的是掩码后的 config，手机端回写会把掩码存成真实值。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            McaButton(
                onClick = {
                    onSave(
                        LibraryDraft(
                            kind = kind,
                            name = name,
                            description = description,
                            content = content,
                            tools = tools,
                            model = model,
                        ),
                    )
                },
                style = McaButtonStyle.Primary,
                enabled = !saving,
            ) { Text(if (saving) "保存中…" else "保存") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined, enabled = !saving) { Text("取消") }
        },
    )
}

@Composable
private fun LibraryField(
    label: String,
    value: String,
    error: String?,
    minHeight: androidx.compose.ui.unit.Dp = 48.dp,
    onValueChange: (String) -> Unit,
) {
    Column(Modifier.fillMaxWidth()) {
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
                textStyle = MaterialTheme.typography.bodyMedium.copy(
                    color = MaterialTheme.colorScheme.onSurface,
                ),
                cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = minHeight)
                    .wrapContentHeight(Alignment.CenterVertically)
                    .padding(horizontal = 12.dp, vertical = 8.dp)
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
