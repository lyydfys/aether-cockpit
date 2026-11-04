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
import androidx.compose.runtime.setValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.AccountSummary
import com.muyuchat.aether.runtime.CheckinSummary
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.WakeDraft
import com.muyuchat.aether.runtime.WakeDraftProblems
import com.muyuchat.aether.runtime.checkinSummaryLine
import com.muyuchat.aether.runtime.WakeTask
import com.muyuchat.aether.runtime.schedulerSummaryLine
import com.muyuchat.aether.runtime.wakeEnabledLabel
import com.muyuchat.aether.runtime.wakeIntervalLabel
import com.muyuchat.aether.runtime.wakeLastRunLabel
import com.muyuchat.aether.runtime.wakeNextRunLabel
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize

/**
 * 唤醒队列页（自动化里的那一小块）。
 *
 * 只做「看状态 + 手动触发一次」：手机端的调度是 Go 进程内的定时器，系统省电策略随时会停掉它，
 * 所以页面上**不承诺定时执行**，`enabled` 的文案固定是「已启用（尽力而为）」。
 * 新建任务要求先选账号，属于桌面端的活；这里如实说明而不是放一个点了没用的按钮。
 */
@Composable
internal fun WakePage(
    status: GatewayStatus,
    state: WakeUiState,
    triggering: String?,
    notice: String?,
    actionError: String?,
    scheduler: SchedulerUiState,
    checkinRunning: Boolean,
    checkinNote: String?,
    onRefresh: () -> Unit,
    onRun: (String) -> Unit,
    onDismissNotice: () -> Unit,
    onDismissActionError: () -> Unit,
    onRefreshScheduler: () -> Unit,
    onRunCheckin: () -> Unit,
    onDismissCheckinNote: () -> Unit,
    checkinSummary: CheckinSummary? = null,
    accounts: List<AccountSummary> = emptyList(),
    creating: Boolean = false,
    createProblems: WakeDraftProblems? = null,
    onCreateWake: (WakeDraft, (Boolean) -> Unit) -> Unit = { _, done -> done(false) },
    onClearCreateProblems: () -> Unit = {},
    onDeleteWake: (String) -> Unit = {},
    onRefreshAccounts: () -> Unit = {},
    onBack: () -> Unit,
    onOpenOverview: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var showCreate by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        onRefresh()
        onRefreshScheduler()
        // 新建唤醒任务要选账号，顺带把账号列表与签到汇总读一次。
        onRefreshAccounts()
    }

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
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
                    Text(
                        text = "唤醒队列",
                        style = MaterialTheme.typography.headlineMedium,
                        fontWeight = FontWeight.Bold,
                        color = MaterialTheme.colorScheme.onBackground,
                    )
                    Text(
                        text = "查看 / 新建 / 删除 · 手动触发",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                McaIconButton(
                    contentDescription = "刷新唤醒队列",
                    onClick = onRefresh,
                    enabled = state !is WakeUiState.Loading,
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
        // 这条说明是页面的核心口径，放在最上面而不是底部小字。
        item { DeepSectionTitle("手机端只能尽力而为") }
        item {
            DeepCard {
                DeepCardBody(
                    "唤醒任务的定时器跑在网关进程里：应用退到后台、系统省电策略或进程被回收都会让它停下，" +
                        "所以「已启用」不等于「一定按时执行」。需要准点执行请用桌面端；" +
                        "这里可以查看当前状态，并对某条任务点一次「立即唤醒」。",
                )
            }
        }
        notice?.let { message ->
            item { DeepNotice(message, onDismiss = onDismissNotice) }
        }
        actionError?.let { message ->
            item { DeepNotice(message, isError = true, onDismiss = onDismissActionError) }
        }
        item {
            DeepSectionTitle(
                text = "唤醒任务",
                trailing = if (state is WakeUiState.Ready) wakeTaskCountLabel(state.tasks.size) else null,
                action = {
                    McaButton(
                        onClick = { showCreate = true },
                        style = McaButtonStyle.Outlined,
                        enabled = status.running,
                    ) { Text("新建") }
                },
            )
        }
        when (state) {
            WakeUiState.Idle, WakeUiState.Loading -> item {
                DeepCardBody("正在读取唤醒队列…")
            }
            is WakeUiState.Failed -> item {
                DeepStateCard(
                    title = if (state.code == "gateway_not_running") "网关未启动" else "读取失败",
                    detail = if (state.code == "gateway_not_running") {
                        "启动本地网关后即可查看唤醒队列。"
                    } else {
                        "${state.message}（错误码：${state.code}）"
                    },
                    tone = if (state.code == "gateway_not_running") DeepTone.Info else DeepTone.Danger,
                    actionLabel = if (state.code == "gateway_not_running") "前往概览" else "重试",
                    onAction = if (state.code == "gateway_not_running") onOpenOverview else onRefresh,
                )
            }
            is WakeUiState.Ready -> {
                if (state.tasks.isEmpty()) {
                    item {
                        DeepStateCard(
                            title = "还没有唤醒任务",
                            detail = "选一个本机账号就能建一条；任务跑在网关进程里，手机端只是尽力而为。",
                            actionLabel = "新建任务",
                            onAction = {
                                if (status.running) {
                                    showCreate = true
                                }
                            },
                        )
                    }
                } else {
                    items(state.tasks.size) { index ->
                        WakeTaskCard(
                            task = state.tasks[index],
                            now = state.serverNow,
                            triggering = triggering == state.tasks[index].id,
                            onRun = { onRun(state.tasks[index].id) },
                            onDelete = { onDeleteWake(state.tasks[index].id) },
                        )
                    }
                }
            }
        }
        // —— 签到调度：与唤醒任务分开的一节 ——
        // 放在同一页而不是新增第 18 个工作区入口：WorkspaceTest 锁着 17 条的批准设计。
        item { DeepSectionTitle("签到调度") }
        item {
            SchedulerSection(
                scheduler = scheduler,
                checkinRunning = checkinRunning,
                checkinNote = checkinNote,
                checkinSummary = checkinSummary,
                onRunCheckin = onRunCheckin,
                onDismissCheckinNote = onDismissCheckinNote,
                onRefresh = onRefreshScheduler,
            )
        }
    }

    if (showCreate) {
        WakeCreateDialog(
            accounts = accounts,
            creating = creating,
            problems = createProblems,
            error = actionError,
            onDismiss = {
                showCreate = false
                onClearCreateProblems()
            },
            onSave = { draft ->
                onCreateWake(draft) { ok ->
                    if (ok) {
                        showCreate = false
                        onClearCreateProblems()
                    }
                }
            },
        )
    }
}

/** 列表计数文案。 */
internal fun wakeTaskCountLabel(count: Int): String = "$count 条任务"

/**
 * 任务副标题：间隔 · 下次执行 · 执行中 · 连续失败。
 * 缺失字段（`every_minutes`/`next_run` 为 0）交给 runtime 的两个标签函数处理，不在这里编造。
 */
internal fun wakeTaskMetaLine(task: WakeTask, now: Long): String = buildString {
    append(wakeIntervalLabel(task.everyMinutes))
    append(" · ")
    append(wakeNextRunLabel(task.nextRun, now))
    if (task.running) {
        append(" · 正在执行")
    }
    if (task.failures > 0) {
        append(" · 连续失败 ${task.failures} 次")
    }
}

/**
 * 签到调度区块。
 *
 * 三条如实口径：
 *  1. 调度器没接线时显示服务端给的 reason（「调度器未启动」），而不是空任务列表；
 *  2. 手动触发是**入队**，文案照服务端的 note 走，不改成"已完成"；
 *  3. 手机端的定时能力是尽力而为，系统省电策略会停掉网关进程。
 */
@Composable
private fun SchedulerSection(
    scheduler: SchedulerUiState,
    checkinRunning: Boolean,
    checkinNote: String?,
    checkinSummary: CheckinSummary?,
    onRunCheckin: () -> Unit,
    onDismissCheckinNote: () -> Unit,
    onRefresh: () -> Unit,
) {
    DeepCard {
        when (scheduler) {
            SchedulerUiState.Idle -> DeepCardBody("正在读取签到状态…")
            is SchedulerUiState.Failed -> DeepCardBody("${scheduler.message}（错误码：${scheduler.code}）")
            is SchedulerUiState.Ready -> {
                DeepCardBody(schedulerSummaryLine(scheduler.state))
                if (scheduler.state.enabled) {
                    // 只列渠道与今日进度，不展示 cron/凭证之类的内部信息。
                    scheduler.state.jobs.take(6).forEach { job ->
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text = "· ${job.title}：今日 ${job.todayDone}/${job.todayTotal}" +
                                if (job.pending > 0) "（队列 ${job.pending}）" else "",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    if (scheduler.state.jobs.size > 6) {
                        Text(
                            text = "…另有 ${scheduler.state.jobs.size - 6} 条任务",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
        Spacer(Modifier.height(12.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            McaButton(
                onClick = onRunCheckin,
                style = McaButtonStyle.Primary,
                enabled = !checkinRunning && (scheduler as? SchedulerUiState.Ready)?.state?.enabled == true,
            ) { Text(if (checkinRunning) "提交中…" else "立即签到") }
            McaButton(onClick = onRefresh, style = McaButtonStyle.Outlined) { Text("刷新状态") }
        }
        checkinNote?.let { note ->
            Spacer(Modifier.height(8.dp))
            Text(
                text = note,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(8.dp))
            McaButton(onClick = onDismissCheckinNote, style = McaButtonStyle.Outlined) { Text("知道了") }
        }
        Spacer(Modifier.height(8.dp))
        Text(
            text = "签到任务已入队后按渠道串行执行，不会立即完成；手机端调度是尽力而为，" +
                "系统省电策略可能停掉网关进程，需要准点执行请用桌面端。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        // 汇总是"本次一键签到"的口径（since 到今天 0 点），不是"总计"。
        checkinSummary?.let { summary ->
            Spacer(Modifier.height(8.dp))
            Text(
                text = "本次签到结果",
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
            )
            Text(
                text = checkinSummaryLine(summary),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            summary.items.filter { it.failures.isNotEmpty() }.take(3).forEach { item ->
                item.failures.take(2).forEach { failure ->
                    Text(
                        text = "· ${item.providerName.ifBlank { item.providerId }} / ${failure.label.ifBlank { failure.accountId }}：" +
                            failure.message.take(60),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
            }
        }
    }
}

@Composable
private fun WakeTaskCard(
    task: WakeTask,
    now: Long,
    triggering: Boolean,
    onRun: () -> Unit,
    onDelete: () -> Unit = {},
) {
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(
                    text = task.label.ifBlank { task.accountId.ifBlank { task.id } },
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                if (task.model.isNotBlank()) {
                    Text(
                        text = task.model,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            Spacer(Modifier.width(8.dp))
            Text(
                text = wakeEnabledLabel(task.enabled),
                style = MaterialTheme.typography.labelSmall,
                color = if (task.enabled) {
                    MaterialTheme.colorScheme.primary
                } else {
                    MaterialTheme.colorScheme.onSurfaceVariant
                },
            )
        }
        Spacer(Modifier.height(8.dp))
        Text(
            text = wakeTaskMetaLine(task, now),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(4.dp))
        Text(
            text = wakeLastRunLabel(task.lastRun, task.lastOk, task.lastResult),
            style = MaterialTheme.typography.labelSmall,
            color = if (task.lastRun > 0L && !task.lastOk) {
                MaterialTheme.colorScheme.error
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
        )
        Spacer(Modifier.height(12.dp))
        Row(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            modifier = Modifier.fillMaxWidth(),
        ) {
            McaButton(
                onClick = onRun,
                style = McaButtonStyle.Primary,
                enabled = !triggering && !task.running,
            ) { Text(if (triggering) "提交中…" else "立即唤醒") }
            McaButton(
                onClick = onDelete,
                style = McaButtonStyle.Outlined,
                // 危险动作只染文字，容器保持中性——整块染红会跟"运行中/失败"的状态色打架。
                contentColor = MaterialTheme.colorScheme.error,
            ) { Text("删除") }
        }
    }
}
