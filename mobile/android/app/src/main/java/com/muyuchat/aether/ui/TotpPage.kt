package com.muyuchat.aether.ui

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.TotpCode
import com.muyuchat.aether.runtime.TotpEntry
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaChip
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconPlate
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaPlateSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal
import kotlinx.coroutines.delay

/** 服务端只给 `remaining` 秒；页面按这个间隔重拉，避免自己再实现一套 TOTP 计时。 */
private const val REFRESH_INTERVAL_MS = 5_000L

/**
 * 新增 2FA 的入参，字段与桌面 `twofa.js:103-127` 的添加抽屉一一对应：
 * 平台 / 账号 / 密钥 / 位数 / 周期 / 算法 / 备注。
 *
 * 服务端契约见 `TotpApiClient`（`POST /api/2fa` 支持 digits 4-10、period 5-300、
 * algorithm SHA1/SHA256/SHA512）；这里只给桌面同款的 6|8、30|60、三种算法。
 */
data class TotpAddRequest(
    val issuer: String,
    val account: String,
    val note: String,
    val secret: String,
    val digits: Int = 6,
    val period: Int = 30,
    val algorithm: String = "SHA1",
)

/** 桌面 `twofa.js:106-108` 的三个下拉：位数 / 周期 / 算法。 */
internal val TOTP_DIGIT_OPTIONS = listOf(6, 8)
internal val TOTP_PERIOD_OPTIONS = listOf(30, 60)
internal val TOTP_ALGORITHM_OPTIONS = listOf("SHA1", "SHA256", "SHA512")

/**
 * 2FA（TOTP）页，1:1 对标桌面 `views/twofa.js`。
 *
 * 桌面结构（行号）：
 *  - 顶部条：`N 个条目` chip + 「验证码只在本机计算；列表接口不回传密钥。」+ 「添加 2FA」（twofa.js:54-58）；
 *  - 空态：`还没有 2FA 条目` + 「添加第一个」（:60-62）；
 *  - 条目卡：首字牌 + 平台 + 账号 + 算法·周期 chip（:72-79）、验证码 + 复制（:83-88）、
 *    备注（:90）、`密钥尾号` + 删除（:91-98）；
 *  - 添加抽屉：平台/账号/密钥（必填）/位数/周期/算法/备注（:102-131）。
 *
 * 桌面没有的开关、扫码、备份码在这里**不提供**（桌面实现里也没有，见汇报）。
 * 列表只含 `secret_tail`（末 4 位），验证码由 Go 侧本机计算。
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
    onAdd: (TotpAddRequest) -> Unit,
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
    val context = LocalContext.current

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

    val canAdd = status.running && !adding
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
                        title = "2FA 管理",
                        subtitle = "本机 TOTP 验证码，密钥加密存储",
                    )
                }
            }
        }
        when (state) {
            TotpUiState.Idle, TotpUiState.Loading -> item {
                DeepStateCard(
                    title = "正在读取 2FA 条目…",
                    detail = "验证码由网关在本机计算，页面不接触密钥原文。",
                )
            }
            is TotpUiState.Failed -> item {
                if (state.code == "gateway_not_running") {
                    DeepStateCard(
                        title = "网关未启动",
                        detail = "启动本地网关后即可管理 2FA 条目。",
                        actionLabel = "前往概览",
                        onAction = onOpenOverview,
                    )
                } else {
                    DeepStateCard(
                        title = "读取失败",
                        detail = "${state.message}（错误码：${state.code}）",
                        tone = DeepTone.Danger,
                        actionLabel = "重试",
                        onAction = onRefresh,
                    )
                }
            }
            is TotpUiState.Ready -> {
                item { TotpBatchBar(count = state.entries.size, canAdd = canAdd) { showAdd = true } }
                if (state.entries.isEmpty()) {
                    item {
                        DeepStateCard(
                            title = "还没有 2FA 条目",
                            detail = "把平台的 TOTP 密钥加进来，验证码会在这里自动刷新。",
                            actionLabel = "添加第一个",
                            onAction = { showAdd = true },
                        )
                    }
                } else {
                    val codesById = state.codes.associateBy { it.id }
                    items(state.entries.size) { index ->
                        val entry = state.entries[index]
                        TotpEntryCard(
                            entry = entry,
                            code = codesById[entry.id],
                            deleting = deleting == entry.id,
                            onCopy = { code -> copyToClipboard(context, code) },
                            onDelete = { pendingDelete = entry },
                        )
                    }
                }
            }
        }
        actionError?.let { message ->
            item { DeepNotice(text = message, isError = true, onDismiss = onDismissActionError) }
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
            onConfirm = { request ->
                addBaseline = (state as? TotpUiState.Ready)?.sampledAtMs ?: 0L
                onAdd(request)
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
                    text = "将删除「${entry.issuer.ifBlank { "未命名" }}」这条。只删本机记录，平台上的 2FA 不受影响。",
                    style = MaterialTheme.typography.bodySmall,
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

/** 桌面顶部条（twofa.js:54-58）：条目数 + 本机计算说明 + 「添加 2FA」。 */
@Composable
private fun TotpBatchBar(count: Int, canAdd: Boolean, onAdd: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        McaChip(
            selected = false,
            onClick = {},
            label = {
                Text(
                    text = "$count 个条目",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            },
        )
        Spacer(Modifier.width(8.dp))
        Text(
            text = "验证码只在本机计算；列表接口不回传密钥。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
        Spacer(Modifier.width(8.dp))
        McaButton(
            onClick = onAdd,
            style = McaButtonStyle.Primary,
            size = McaButtonSize.CardAction,
            enabled = canAdd,
        ) { Text("添加 2FA") }
    }
}

/** 条目卡：桌面 `entryCard`（twofa.js:69-100）的 1:1 字段。 */
@Composable
private fun TotpEntryCard(
    entry: TotpEntry,
    code: TotpCode?,
    deleting: Boolean,
    onCopy: (String) -> Unit,
    onDelete: () -> Unit,
) {
    val title = entry.issuer.ifBlank { entry.account.ifBlank { "未命名平台" } }
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            McaIconPlate(
                size = McaPlateSize.Medium,
                shape = McaShape.Badge,
                containerColor = MaterialTheme.colorScheme.primaryContainer,
            ) {
                Text(
                    text = title.take(1),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.onPrimaryContainer,
                )
            }
            Spacer(Modifier.width(10.dp))
            Column(Modifier.weight(1f)) {
                Text(
                    text = title,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                if (entry.account.isNotBlank()) {
                    Text(
                        text = entry.account,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            Spacer(Modifier.width(8.dp))
            Text(
                text = totpAlgoPeriodLabel(entry),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(10.dp))
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
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = formatTotpCode(code.code),
                    style = MaterialTheme.typography.headlineSmall.copy(letterSpacing = 4.sp),
                    fontFamily = FontFamily.Monospace,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.onSurface,
                    modifier = Modifier.weight(1f),
                )
                Spacer(Modifier.width(8.dp))
                McaButton(
                    onClick = { onCopy(code.code) },
                    style = McaButtonStyle.Outlined,
                    size = McaButtonSize.CardAction,
                    enabled = code.code.isNotBlank(),
                ) { Text("复制") }
            }
            Spacer(Modifier.height(8.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                LinearProgressIndicator(
                    progress = { totpRemainingFraction(code.remaining, entry.period) },
                    modifier = Modifier.weight(1f).height(4.dp),
                )
                Spacer(Modifier.width(8.dp))
                Text(
                    text = "${code.remaining}s",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (entry.note.isNotBlank()) {
            Spacer(Modifier.height(8.dp))
            Text(
                text = entry.note,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(12.dp))
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "密钥尾号 ${entry.secretTail.ifBlank { "••••" }}",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(8.dp))
            McaButton(
                onClick = onDelete,
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
                enabled = !deleting,
                contentColor = MaterialTheme.colorScheme.error,
            ) { Text(if (deleting) "删除中…" else "删除") }
        }
    }
}

/** 算法·周期 chip 文案：`SHA1 · 30s`（桌面 twofa.js:78）。 */
internal fun totpAlgoPeriodLabel(entry: TotpEntry): String =
    "${entry.algorithm.ifBlank { "SHA1" }} · ${entry.period}s"

@Composable
private fun TotpAddDialog(
    adding: Boolean,
    error: String?,
    onDismiss: () -> Unit,
    onConfirm: (TotpAddRequest) -> Unit,
) {
    var issuer by remember { mutableStateOf("") }
    var account by remember { mutableStateOf("") }
    var note by remember { mutableStateOf("") }
    var secret by remember { mutableStateOf("") }
    var digits by remember { mutableStateOf(6) }
    var period by remember { mutableStateOf(30) }
    var algorithm by remember { mutableStateOf("SHA1") }
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("添加 2FA", fontWeight = FontWeight.Bold) },
        text = {
            Column {
                Text(
                    text = "粘贴服务商提供的 Base32 密钥。密钥只随这次请求提交，不会留在页面上。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(12.dp))
                // 桌面字段顺序（twofa.js:112-117）：平台 → 账号 → 密钥* → [位数/周期/算法] → 备注。
                TotpField("平台（例如 GitHub）", issuer) { issuer = it }
                TotpField("账号（可选）", account) { account = it }
                TotpField("密钥 *（Base32）", secret, password = true) { secret = it }
                TotpChoiceRow("位数", TOTP_DIGIT_OPTIONS.map { it.toString() }, digits.toString()) {
                    digits = it.toIntOrNull() ?: 6
                }
                TotpChoiceRow("周期", TOTP_PERIOD_OPTIONS.map { it.toString() }, period.toString()) {
                    period = it.toIntOrNull() ?: 30
                }
                TotpChoiceRow("算法", TOTP_ALGORITHM_OPTIONS, algorithm) { algorithm = it }
                TotpField("备注（可选）", note) { note = it }
                Spacer(Modifier.height(6.dp))
                Text(
                    text = "密钥只在本机加密保存；支持 base32，可带空格。",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                error?.let {
                    Spacer(Modifier.height(6.dp))
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
                onClick = {
                    onConfirm(
                        TotpAddRequest(
                            issuer = issuer,
                            account = account,
                            note = note,
                            secret = secret,
                            digits = digits,
                            period = period,
                            algorithm = algorithm,
                        ),
                    )
                },
                style = McaButtonStyle.Primary,
                enabled = !adding && secret.isNotBlank(),
            ) { Text(if (adding) "保存中…" else "保存") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("取消") }
        },
    )
}

/** 桌面 `select` 的移动端形态：一行可点的 chip。 */
@Composable
private fun TotpChoiceRow(
    label: String,
    options: List<String>,
    selected: String,
    onSelect: (String) -> Unit,
) {
    Column(Modifier.padding(vertical = 4.dp)) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(6.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            options.forEach { option ->
                McaChip(
                    selected = option == selected,
                    onClick = { onSelect(option) },
                    label = {
                        Text(
                            text = if (label == "周期") "${option}s" else option,
                            style = MaterialTheme.typography.labelSmall,
                        )
                    },
                )
            }
        }
    }
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

/** 复制验证码：桌面用 copyText；Android 侧走系统剪贴板，不落任何应用内存储。 */
private fun copyToClipboard(context: Context, text: String) {
    if (text.isBlank()) {
        return
    }
    val manager = context.getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager ?: return
    manager.setPrimaryClip(ClipData.newPlainText("TOTP", text))
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
