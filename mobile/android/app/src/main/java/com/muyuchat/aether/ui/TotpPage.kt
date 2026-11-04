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
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
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
import androidx.compose.ui.unit.sp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.TotpCode
import com.muyuchat.aether.runtime.TotpEntry
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import kotlinx.coroutines.delay

/** 服务端只给 `remaining` 秒；页面按这个间隔重拉，避免自己再实现一套 TOTP 计时。 */
private const val REFRESH_INTERVAL_MS = 5_000L

/**
 * 2FA（TOTP）页。
 *
 * 这里**不显示也不接收明文 seed**：列表只有 `secret_tail`（末 4 位），验证码由 Go 侧
 * 本机计算后下发。新增时输入的 seed 只进请求体，不落到任何 UI 状态里。
 */
@Composable
internal fun TotpPage(
    status: GatewayStatus,
    state: TotpUiState,
    adding: Boolean,
    addError: String?,
    deleting: String?,
    actionError: String?,
    onRefresh: () -> Unit,
    onAdd: (String, String, String, String) -> Unit,
    onDelete: (String) -> Unit,
    onDismissAddError: () -> Unit,
    onDismissActionError: () -> Unit,
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var showAdd by remember { mutableStateOf(false) }
    var pendingDelete by remember { mutableStateOf<TotpEntry?>(null) }
    // 新增弹窗的关闭条件是「服务端回读刷新过一次」，不是 adding 的中间态——
    // 后者会被 StateFlow 的收集延迟骗到，可能在请求还没发出时就把弹窗关掉。
    var addBaseline by remember { mutableStateOf<Long?>(null) }

    // 进页面先拉一次：不拉的话状态会永远停在 Idle（页面上就是「正在读取…」），
    // 连「网关未启动」都显示不出来。
    LaunchedEffect(Unit) { onRefresh() }

    // 只在网关运行时按固定间隔重拉；停下立即停止轮询，避免对着已关闭的端口刷请求。
    LaunchedEffect(status.running) {
        if (!status.running) {
            return@LaunchedEffect
        }
        while (true) {
            delay(REFRESH_INTERVAL_MS)
            onRefresh()
        }
    }

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
                    Text(
                        text = "2FA",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "TOTP 验证码 · 本机计算",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaIconButton(
                    contentDescription = "刷新验证码",
                    onClick = onRefresh,
                    enabled = state !is TotpUiState.Loading,
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
        when (state) {
            TotpUiState.Idle, TotpUiState.Loading -> item {
                InfoCard(
                    title = "正在读取 2FA 条目…",
                    detail = "验证码由网关在本机计算，页面不接触密钥原文。",
                )
            }
            is TotpUiState.Failed -> item {
                InfoCard(
                    title = if (state.code == "gateway_not_running") "网关未启动" else "读取失败",
                    detail = if (state.code == "gateway_not_running") {
                        "启动本地网关后即可管理 2FA 条目。"
                    } else {
                        "${state.message}（错误码：${state.code}）"
                    },
                    actionLabel = if (state.code == "gateway_not_running") "前往概览" else "重试",
                    onAction = if (state.code == "gateway_not_running") onOpenOverview else onRefresh,
                )
            }
            is TotpUiState.Ready -> {
                if (state.entries.isEmpty()) {
                    item {
                        InfoCard(
                            title = "还没有 2FA 条目",
                            detail = "点下面的「添加条目」输入服务商给的 Base32 密钥。密钥由网关用设备凭据加密后落盘，不会显示在页面上。",
                        )
                    }
                } else {
                    item {
                        Text(
                            text = "共 ${state.entries.size} 条 · 每 ${REFRESH_INTERVAL_MS / 1000} 秒自动刷新",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    val codesById = state.codes.associateBy { it.id }
                    items(state.entries.size) { index ->
                        val entry = state.entries[index]
                        TotpEntryCard(
                            entry = entry,
                            code = codesById[entry.id],
                            deleting = deleting == entry.id,
                            onDelete = { pendingDelete = entry },
                        )
                    }
                }
            }
        }
        actionError?.let { message ->
            item {
                Text(
                    text = message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
                McaButton(onClick = onDismissActionError, style = McaButtonStyle.Outlined) {
                    Text("知道了")
                }
            }
        }
        item {
            McaButton(
                onClick = { showAdd = true },
                style = McaButtonStyle.Primary,
                enabled = status.running && !adding,
            ) { Text("添加条目") }
        }
        item {
            // 三条如实说明：seed 的存储形态、换机后果、页面不落明文。
            Text(
                text = "手机端的 TOTP 密钥由设备凭据加密（Keystore 派生的 key + 条目绑定），" +
                    "换设备或清除应用数据后需要重新添加；页面只显示密钥末 4 位，验证码始终由网关本机计算。",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }

    if (showAdd) {
        TotpAddDialog(
            adding = adding,
            error = addError,
            onDismiss = {
                showAdd = false
                addBaseline = null
                onDismissAddError()
            },
            onConfirm = { issuer, account, note, secret ->
                addBaseline = (state as? TotpUiState.Ready)?.sampledAtMs ?: 0L
                onAdd(issuer, account, note, secret)
            },
        )
    }
    LaunchedEffect(state, adding, addError) {
        val baseline = addBaseline ?: return@LaunchedEffect
        if (!adding && addError != null) {
            // 保存失败：保留弹窗与用户输入，等用户改完再提交。
            addBaseline = null
            return@LaunchedEffect
        }
        val ready = state as? TotpUiState.Ready ?: return@LaunchedEffect
        if (!adding && ready.sampledAtMs > baseline) {
            showAdd = false
            addBaseline = null
            onDismissAddError()
        }
    }
    pendingDelete?.let { entry ->
        McaDialog(
            onDismissRequest = { pendingDelete = null },
            title = { Text("删除 2FA 条目", fontWeight = FontWeight.Bold) },
            text = {
                Text(
                    text = "将删除「${entry.issuer.ifBlank { "未命名" }}」这条。删除后验证码不再可用，" +
                        "如果还需要请重新添加密钥。",
                    style = MaterialTheme.typography.bodyMedium,
                )
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        pendingDelete = null
                        onDelete(entry.id)
                    },
                    style = McaButtonStyle.Primary,
                    enabled = deleting == null,
                ) { Text(if (deleting != null) "删除中…" else "删除") }
            },
            dismissButton = {
                McaButton(onClick = { pendingDelete = null }, style = McaButtonStyle.Outlined) {
                    Text("取消")
                }
            },
        )
    }
}

@Composable
private fun TotpEntryCard(
    entry: TotpEntry,
    code: TotpCode?,
    deleting: Boolean,
    onDelete: () -> Unit,
) {
    Surface(
        shape = McaShape.Card,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = entry.issuer.ifBlank { "未命名" },
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    if (entry.account.isNotBlank()) {
                        Text(
                            text = entry.account,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                Text(
                    text = "末 ${entry.secretTail.ifBlank { "—" }}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.height(8.dp))
            if (code == null) {
                Text(
                    text = "等待验证码…",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            } else if (code.error.isNotBlank()) {
                // 解不开就如实说，不给一个可能是错的码。
                Text(
                    text = code.error,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            } else {
                Text(
                    text = formatTotpCode(code.code),
                    style = MaterialTheme.typography.headlineMedium.copy(letterSpacing = 4.sp),
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Spacer(Modifier.height(6.dp))
                LinearProgressIndicator(
                    progress = { totpRemainingFraction(code.remaining, entry.period) },
                    modifier = Modifier.fillMaxWidth().height(4.dp),
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "${code.remaining} 秒后刷新",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (entry.note.isNotBlank()) {
                Spacer(Modifier.height(6.dp))
                Text(
                    text = entry.note,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.height(8.dp))
            Row(horizontalArrangement = Arrangement.End, modifier = Modifier.fillMaxWidth()) {
                McaButton(
                    onClick = onDelete,
                    style = McaButtonStyle.Outlined,
                    enabled = !deleting,
                ) { Text(if (deleting) "删除中…" else "删除") }
            }
        }
    }
}

@Composable
private fun TotpAddDialog(
    adding: Boolean,
    error: String?,
    onDismiss: () -> Unit,
    onConfirm: (String, String, String, String) -> Unit,
) {
    var issuer by remember { mutableStateOf("") }
    var account by remember { mutableStateOf("") }
    var note by remember { mutableStateOf("") }
    var secret by remember { mutableStateOf("") }
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("添加 2FA 条目", fontWeight = FontWeight.Bold) },
        text = {
            Column {
                Text(
                    text = "粘贴服务商提供的 Base32 密钥。密钥只随这次请求提交，不会留在页面上。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(8.dp))
                TotpField("服务商（如 GitHub）", issuer) { issuer = it }
                TotpField("账号（可选）", account) { account = it }
                TotpField("备注（可选）", note) { note = it }
                TotpField("Base32 密钥", secret, password = true) { secret = it }
                error?.let {
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        },
        confirmButton = {
            McaButton(
                onClick = { onConfirm(issuer, account, note, secret) },
                style = McaButtonStyle.Primary,
                enabled = !adding && secret.isNotBlank(),
            ) { Text(if (adding) "保存中…" else "保存") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("取消") }
        },
    )
}

@Composable
private fun TotpField(
    label: String,
    value: String,
    password: Boolean = false,
    onValueChange: (String) -> Unit,
) {
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
                cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                visualTransformation = if (password) {
                    PasswordVisualTransformation()
                } else {
                    androidx.compose.ui.text.input.VisualTransformation.None
                },
                // 与其它页面一致：BasicTextField 不吃触摸目标扩展，自己撑到 48dp。
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

@Composable
private fun InfoCard(
    title: String,
    detail: String,
    actionLabel: String? = null,
    onAction: (() -> Unit)? = null,
) {
    Surface(
        shape = McaShape.Card,
        color = McaTonal.fill(),
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(14.dp)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Spacer(Modifier.height(4.dp))
            Text(
                text = detail,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (actionLabel != null && onAction != null) {
                Spacer(Modifier.height(10.dp))
                McaButton(onClick = onAction, style = McaButtonStyle.Primary) { Text(actionLabel) }
            }
        }
    }
}

/**
 * 把纯数字验证码按 3 位分组，便于朗读与抄写。
 *
 * 只有**整串都是数字**时才分组；服务端返回异常形状（空串、带空格的、含字母的）
 * 一律原样透出——宁可难看，也不要把诊断信息"美化"掉。
 */
internal fun formatTotpCode(code: String): String {
    if (code.isEmpty() || !code.all { it.isDigit() }) {
        return code
    }
    return code.chunked(3).joinToString(" ")
}

/** 剩余时间占比，做进度条；越界一律夹到 0..1。 */
internal fun totpRemainingFraction(remaining: Int, period: Int): Float {
    if (period <= 0) {
        return 0f
    }
    return (remaining.toFloat() / period.toFloat()).coerceIn(0f, 1f)
}
