package com.muyuchat.aether.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
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
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.AccountQuotaRow
import com.muyuchat.aether.runtime.CUSTOM_PROVIDER_FORM_PROTOCOLS
import com.muyuchat.aether.runtime.CustomProviderDraft
import com.muyuchat.aether.runtime.CustomProviderProtocol
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.ProviderSummary
import com.muyuchat.aether.runtime.onboardDeviceKind
import com.muyuchat.aether.runtime.onboardMethodBlockedBy
import com.muyuchat.aether.runtime.onboardMethodLabel
import com.muyuchat.aether.runtime.onboardMethods
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonSize
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaChip
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import kotlinx.coroutines.launch

/**
 * 「接入渠道」页：1:1 对标桌面 `internal/web/ui2/js/views/onboard.js`。
 *
 * - 标题/副标题照抄桌面（`onboard.js:40-41`）；
 * - 渠道列表照抄桌面卡片（`onboard.js:181-196`）；搜索过滤对齐桌面 `paintGrid`
 *   的 `display_name / family / id / description`（`onboard.js:172-178`）；
 * - 手机端新增：A–Z 首字母索引（桌面是 grid-3 全量铺，手机单列需要索引）；
 * - **接入方式全部在手机本机完成**：浏览器授权（`/api/auth` 系列端点，回环回调 127.0.0.1）、
 *   设备码（`/api/auth/start {device_code}` 与 `/api/provider/{kind}/device`）、
 *   手动凭据（`POST /api/accounts`）、Token / JSON（同端点）。
 *   账号包只是桌面迁移的次要通道，**不是接入前置**。
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
    viewModel: OnboardViewModel = viewModel(),
) {
    LaunchedRefresh(status, onRefresh)

    val query by viewModel.query.collectAsStateWithLifecycle()
    val filters by viewModel.filters.collectAsStateWithLifecycle()
    val draft by viewModel.draft.collectAsStateWithLifecycle()
    val saving by viewModel.saving.collectAsStateWithLifecycle()
    val formError by viewModel.formError.collectAsStateWithLifecycle()
    val modelsFetch by viewModel.modelsFetch.collectAsStateWithLifecycle()
    val notice by viewModel.notice.collectAsStateWithLifecycle()
    val connect by viewModel.connect.state.collectAsStateWithLifecycle()
    val listState = rememberLazyListState()
    // 侧边字母索引拖动时，屏幕中央浮出的大字母。
    var indexBubble by remember { mutableStateOf<String?>(null) }
    // 字母索引跳转请求：**只保留最新目标**。
    // 拖动一次会连续经过 20+ 个字母，如果每次都 `scope.launch { animateScrollToItem }`，
    // 多个动画会互相打断，最后停在哪个位置不确定（真机表现为"拖动不灵/不动"）。
    // 这里用一个状态 + LaunchedEffect 串行化：新请求会取消旧协程，永远只跑最新的那个。
    var scrollRequest by remember { mutableStateOf<OnboardScrollRequest?>(null) }
    LaunchedEffect(scrollRequest) {
        val request = scrollRequest ?: return@LaunchedEffect
        if (request.animate) {
            listState.animateScrollToItem(request.index)
        } else {
            // 拖动中要跟手：瞬时定位，不要动画堆叠。
            listState.scrollToItem(request.index)
        }
        scrollRequest = null
    }

    val filtered = applyOnboardQuery(providers, accounts, query, filters)
    // 名称排序按字母分组（目录顺序）；筛选/排序入口已按用户要求移除，默认即此形态。
    val grouped = filters.sort == OnboardSort.NAME
    val groups = if (grouped) groupOnboardProviders(filtered) else emptyList()
    val rows: List<OnboardListRow> = if (grouped) {
        buildList {
            groups.forEach { group ->
                add(OnboardListRow.Letter(group.letter))
                group.providers.forEach { add(OnboardListRow.Provider(it)) }
            }
        }
    } else {
        filtered.map { OnboardListRow.Provider(it) }
    }
    // 字母索引点击跳转：前 5 个 item 固定（MobileHeader / 页头 / 搜索 / 自定义入口 / 区块标题）。
    // 筛选工具行与横排字母条已移除，所以基数是 5（原 7）。
    val rowIndexByLetter: Map<String, Int> = rows.withIndex()
        .filter { it.value is OnboardListRow.Letter }
        .associate { (rows[it.index] as OnboardListRow.Letter).letter to (OnboardRowsBase + it.index) }
    // 有渠道的字母（其余字母在侧边栏里置灰但仍显示）。
    val lettersWithData: Set<String> = groups.map { it.letter }.toSet()

    Box(modifier = modifier.fillMaxSize()) {
    LazyColumn(
        state = listState,
        modifier = Modifier.fillMaxSize(),
        // 底部必须留够悬浮球安全区（BubbleSafeBottom=88dp），否则最后几张卡会被球压住；
        // 右侧 28dp 是给字母索引条让位，不能丢。
        contentPadding = PaddingValues(
            start = 20.dp,
            end = 28.dp,
            top = 8.dp,
            bottom = BubbleSafeBottom,
        ),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item { MobileHeader(status) }
        item {
            Row(verticalAlignment = Alignment.CenterVertically) {
                AetherHeaderIconButton(
                    iconRes = R.drawable.ic_back,
                    contentDescription = "返回",
                    onClick = onBack,
                    tint = MaterialTheme.colorScheme.onBackground,
                )
                Spacer(Modifier.width(8.dp))
                Box(Modifier.weight(1f)) {
                    // 页面标题统一走 DeepPageTitle（文案照抄桌面 onboard.js:40-41）
                    DeepPageTitle(
                        title = "接入渠道",
                        subtitle = "多平台登录：本机导入、浏览器授权、设备码、手动凭据",
                    )
                }
                AetherHeaderIconButton(
                    iconRes = R.drawable.ic_refresh,
                    contentDescription = "刷新渠道",
                    onClick = onRefresh,
                    enabled = status.running,
                )
            }
        }
        item {
            SearchField(
                query = query,
                onQueryChange = viewModel::setQuery,
                placeholder = "搜索渠道名或描述",
            )
        }
        item {
            OnboardCustomProviderCard(
                notice = notice,
                canAdd = status.running,
                onAdd = viewModel::openForm,
                onDismissNotice = viewModel::dismissNotice,
            )
        }
        item {
            DeepSectionTitle(
                text = "按渠道接入",
                trailing = onboardCountLabel(
                    visible = filtered.size,
                    total = providers.size,
                    searching = query.isNotBlank() || filters.filterCount > 0,
                ),
            )
        }
        if (providers.isEmpty()) {
            item {
                DeepStateCard(
                    title = "还没有可接入的渠道",
                    detail = "网关启动后会带回可用渠道清单；如果是刚启动，点右上角刷新一次。",
                    actionLabel = "刷新渠道",
                    onAction = onRefresh,
                )
            }
        } else if (filtered.isEmpty()) {
            item {
                DeepStateCard(
                    title = "没有匹配的渠道",
                    detail = "搜索只匹配渠道名、描述与 id。清掉搜索词就能看到全部渠道。",
                    actionLabel = "清除搜索",
                    onAction = {
                        viewModel.setQuery("")
                    },
                )
            }
        } else {
            items(
                count = rows.size,
                key = { index ->
                    when (val row = rows[index]) {
                        is OnboardListRow.Letter -> "letter_${row.letter}"
                        is OnboardListRow.Provider -> "provider_${row.provider.id}"
                    }
                },
            ) { index ->
                when (val row = rows[index]) {
                    is OnboardListRow.Letter -> OnboardLetterHeader(row.letter)
                    is OnboardListRow.Provider -> OnboardProviderCard(
                        provider = row.provider,
                        accountCount = accounts.count { it.providerId == row.provider.id },
                        onConnect = { viewModel.connect.open(row.provider) },
                    )
                }
            }
        }

        // ——— 账号包：**不是接入前置**，只做桌面迁移的次要通道 ———
        item { DeepSectionTitle(text = "账号包（次要：桌面迁移用）") }
        item {
            DeepCard {
                DeepCardBody(
                    "接入渠道**不需要**账号包：上面每个渠道卡片里都能直接用浏览器授权、设备码、" +
                        "手动凭据或 Token 接入。这里只保留桌面↔手机的迁移通道。",
                )
                Spacer(Modifier.height(8.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(onClick = onImportBundle, style = McaButtonStyle.Outlined) {
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

    // 右侧竖排字母索引：完整 A–Z + #，无数据的字母置灰；支持点击与拖动连续跳转。
    if (grouped && rows.isNotEmpty()) {
        OnboardLetterIndex(
            available = lettersWithData,
            onJump = { letter, animate ->
                rowIndexByLetter[letter]?.let { target ->
                    // 覆盖式赋值：拖动时只有最后一次请求会被真正执行。
                    scrollRequest = OnboardScrollRequest(target, animate)
                }
            },
            onBubbleChange = { indexBubble = it },
            modifier = Modifier
                .align(Alignment.CenterEnd)
                .padding(end = 2.dp),
        )
    }
    // 拖动时屏幕中央的大字母气泡。
    indexBubble?.let { letter ->
        Surface(
            modifier = Modifier
                .align(Alignment.Center)
                .aetherSpecularRim(McaShape.Card),
            shape = McaShape.Card,
            color = MaterialTheme.colorScheme.surface,
            border = McaHairline.stroke(),
        ) {
            Box(
                modifier = Modifier.size(72.dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = letter,
                    style = MaterialTheme.typography.headlineSmall,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.primary,
                    maxLines = 1,
                )
            }
        }
    }
    }

    connect?.let { current ->
        OnboardConnectDialog(
            state = current,
            qoderLoginState = qoderLoginState,
            onStartQoderLogin = onStartQoderLogin,
            onOpenQoderLogin = onOpenQoderLogin,
            onCancelQoderLogin = onCancelQoderLogin,
            onDismissQoderLogin = onDismissQoderLogin,
            onSelectMethod = viewModel.connect::selectMethod,
            onLabel = viewModel.connect::setLabel,
            onCallbackInput = viewModel.connect::setCallbackInput,
            onTokenInput = viewModel.connect::setTokenInput,
            onManualValue = viewModel.connect::setManualValue,
            onWindsurfEmail = viewModel.connect::setWindsurfEmail,
            onWindsurfPassword = viewModel.connect::setWindsurfPassword,
            onDismissError = viewModel.connect::dismissError,
            onStartAuth = { viewModel.connect.startAuth(onRefresh) },
            onSubmitCallback = { viewModel.connect.submitCallback() },
            onCommitAuth = { viewModel.connect.commitAuth(onRefresh) },
            onCancelAuth = viewModel.connect::cancelAuth,
            onStartDevice = { viewModel.connect.startDevice(onRefresh) },
            onCancelDevice = viewModel.connect::cancelDevice,
            onSubmitTokenJson = { viewModel.connect.submitTokenJson(onRefresh) },
            onSubmitManual = { viewModel.connect.submitManual(onRefresh) },
            onSubmitWindsurf = { viewModel.connect.submitWindsurf(onRefresh) },
            onOpenCustomForm = {
                viewModel.connect.close()
                onDismissQoderLogin()
                viewModel.openForm()
            },
            onOpenUrl = onOpenQoderLogin,
            onNudgeAuthPoll = viewModel.connect::nudgeAuthPoll,
            onDismiss = {
                viewModel.connect.close()
                onDismissQoderLogin()
            },
        )
    }
    draft?.let { current ->
        OnboardCustomProviderDialog(
            draft = current,
            saving = saving,
            error = formError,
            modelsFetch = modelsFetch,
            onNameChange = viewModel::updateName,
            onBaseUrlChange = viewModel::updateBaseUrl,
            onApiKeyChange = viewModel::updateApiKey,
            onModelsChange = viewModel::updateModels,
            onFetchModels = viewModel::fetchModels,
            onProtocolChange = viewModel::updateProtocol,
            onDismiss = viewModel::dismissForm,
            onSave = {
                viewModel.save(
                    existingIds = providers.map { it.id }.toSet(),
                    onSaved = onRefresh,
                )
            },
        )
    }
    // 口令弹窗（预览/导入共用一份逻辑，见 BundleImportHost.kt）
    bundleHost()
}

/** 固定 item 数：MobileHeader / 页头 / 搜索 / 自定义入口 / 区块标题。
 *  筛选工具行与横排字母条已按用户要求移除（原值 7 → 5）。 */
private const val OnboardRowsBase = 5

/** 列表行：字母分组头 或 渠道卡。 */
internal sealed interface OnboardListRow {
    data class Letter(val letter: String) : OnboardListRow
    data class Provider(val provider: ProviderSummary) : OnboardListRow
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
    val displayName = provider.name.ifBlank { provider.id }
    DeepCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            ProviderBrandPlate(
                providerId = provider.id,
                name = displayName,
                size = 40.dp,
                online = if (accountCount > 0) true else null,
            )
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(
                    text = displayName,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
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
                // 没有对话转发能力的渠道（`chat == false`，如 cursor / windsurf / zed）：
                // 在描述下方补一句预期说明。**按钮仍然可点**——接入后能看账号与额度，是有意义的。
                if (!provider.chat) {
                    Text(
                        text = onboardChatUnsupportedHint(),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Spacer(Modifier.height(4.dp))
                Text(
                    text = onboardProviderMeta(provider, accountCount),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.width(8.dp))
            AetherPillButton(
                text = if (accountCount > 0) "再加一个" else "接入",
                onClick = onConnect,
                selected = accountCount == 0,
            )
        }
    }
}

/** 分组头：字母本身 + 该组条数；空组不会进列表（`groupOnboardProviders` 已滤掉）。 */
@Composable
private fun OnboardLetterHeader(letter: String) {
    Text(
        text = letter,
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.Bold,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 4.dp, bottom = 0.dp),
        maxLines = 1,
    )
}

/** 侧边索引的字母表：完整 A–Z + `#`（无数据的字母置灰，但仍参与拖动定位）。 */
internal val OnboardIndexLetters: List<String> = ('A'..'Z').map { it.toString() } + "#"

/**
 * 一次字母跳转请求。
 *
 * [animate] = true 表示点击（值得放动画）；false 表示拖动跟手（必须瞬时定位，
 * 否则连续 20+ 次动画会互相打断）。请求是**覆盖式**的：拖动期间只有最新一条会执行。
 */
internal data class OnboardScrollRequest(val index: Int, val animate: Boolean)

/**
 * 右侧竖排字母索引（通讯录式）。
 *
 * - 完整 A–Z + `#`：没数据的字母**置灰而不是隐藏**，避免条子长度随搜索结果跳动；
 * - 支持**点击**与**按住上下滑动**连续跳转；拖动时由调用方在中央浮出大字母；
 * - 每格高度固定 [OnboardIndexCellHeight]，索引总高 ≈ 27×16dp，一屏放得下；
 *   1.3× 字号下只压缩行高，不换行、不出现单字竖排。
 */
private val OnboardIndexCellHeight = 16.dp
/** 索引条宽度：24dp 的字母格太窄、手指很难精确落上去（真机上「拖动不灵」的主因之一），
 *  这里把**可触摸宽度**放宽到 40dp，字母仍贴右缘绘制；列表右侧按 40dp 留空。 */
private val OnboardIndexWidth = 40.dp

@Composable
private fun OnboardLetterIndex(
    available: Set<String>,
    onJump: (String, Boolean) -> Unit,
    onBubbleChange: (String?) -> Unit,
    modifier: Modifier = Modifier,
) {
    val letters = OnboardIndexLetters
    var barHeightPx by remember { mutableStateOf(0f) }
    var current by remember { mutableStateOf<String?>(null) }

    // 按 y 比例换算成字母下标：不依赖每格的实际像素高度，1.3× 下也准。
    fun letterAt(y: Float): String {
        if (barHeightPx <= 0f) return letters.first()
        val ratio = (y / barHeightPx).coerceIn(0f, 0.9999f)
        return letters[(ratio * letters.size).toInt().coerceIn(0, letters.lastIndex)]
    }

    // 指针进入新字母：更新气泡；有数据就跳转。同一字母不重复触发。
    // animate：按下那一刻（tap 或拖动起点）给动画，进入滑动后关掉——跟手必须瞬时。
    fun follow(letter: String, animate: Boolean) {
        if (current == letter) return
        current = letter
        onBubbleChange(letter)
        if (letter in available) onJump(letter, animate)
    }

    fun release() {
        if (current == null) return
        current = null
        onBubbleChange(null)
    }

    Column(
        modifier = modifier
            .width(OnboardIndexWidth)
            .onSizeChanged { barHeightPx = it.height.toFloat() }
            // 整套手势自己处理（按下即跟随、滑动连续切换、松手收起气泡）。
            // 单元格上**不再挂 clickable**：子节点的 clickable 会先消费 down 事件，
            // detectVerticalDragGestures 因此收不到拖动——这正是「拖动不灵 + 气泡不消失」
            // 的根因（点击路径设置了气泡却没有任何清除入口）。
            .pointerInput(available) {
                awaitEachGesture {
                    val down = awaitFirstDown(requireUnconsumed = false)
                    // 按下的那一格：给一次动画跳转（点击语义）。
                    follow(letterAt(down.position.y), animate = true)
                    down.consume()
                    try {
                        while (true) {
                            val event = awaitPointerEvent()
                            val change = event.changes.firstOrNull { it.id == down.id } ?: break
                            if (!change.pressed) {
                                change.consume()
                                break
                            }
                            // 一旦有移动就是拖动语义：后续全部瞬时定位，避免动画排队。
                            follow(letterAt(change.position.y), animate = false)
                            change.consume()
                        }
                    } finally {
                        release()
                    }
                }
            },
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        letters.forEach { letter ->
            val hasData = letter in available
            Box(
                modifier = Modifier
                    .width(OnboardIndexWidth)
                    .height(OnboardIndexCellHeight),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = letter,
                    style = MaterialTheme.typography.labelSmall,
                    color = if (hasData) {
                        MaterialTheme.colorScheme.primary
                    } else {
                        MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.35f)
                    },
                    maxLines = 1,
                )
            }
        }
    }
}

/**
 * 筛选 / 排序工具行。
 *
 * 形态照 MCA 模型推荐页的工具行（`feature/modelhub/RecommendationFilterSheet.kt:64-160`）：
 * 左侧「筛选 · N」（有条件时高亮），右侧显示当前排序；一行放不下就各自成 chip 按钮，
 * 不加第三个下拉。条件已生效时额外给一个「清除」，与 MCA 的「全部」入口同义。
 */
@Composable
private fun OnboardFilterToolbar(
    state: OnboardQuery,
    onOpenPanel: () -> Unit,
    onClearFilters: () -> Unit,
) {
    Row(
        // 1.3× 下三个按钮放不下同一行：整行横向滚动（允许的「横向滚动 ↔ 换行」差异）。
        // 注意：滚动容器里不能用 weight，所以「清除」紧跟排序，不做右对齐。
        modifier = Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        McaButton(
            onClick = onOpenPanel,
            style = if (state.filterCount > 0) McaButtonStyle.Primary else McaButtonStyle.Outlined,
            size = McaButtonSize.CardAction,
        ) { Text(onboardFilterChipLabel(state)) }
        McaButton(
            onClick = onOpenPanel,
            style = if (state.sort != OnboardSort.NAME) McaButtonStyle.Primary else McaButtonStyle.Outlined,
            size = McaButtonSize.CardAction,
        ) { Text(if (state.sort == OnboardSort.NAME) "排序" else state.sort.label) }
        if (!state.isPristine) {
            McaButton(onClick = onClearFilters, style = McaButtonStyle.Text, size = McaButtonSize.CardAction) {
                Text("清除")
            }
        }
    }
}

/**
 * 「筛选与排序」面板。
 *
 * 照 MCA `RecommendationFilterSheet.kt:174-360` 的事务口径：面板里改的是**草稿**，
 * 只有点「应用」才回写给页面；「重置」只清筛选、保留排序（MCA 同款）。
 * 面板内是两栏（筛选 / 排序）而不是两个弹窗，一次提交。
 */
@Composable
private fun OnboardFilterPanel(
    applied: OnboardQuery,
    previewCount: (OnboardQuery) -> Int,
    onApply: (OnboardQuery) -> Unit,
    onDismiss: () -> Unit,
) {
    var draft by remember(applied) { mutableStateOf(applied) }
    var tab by remember { mutableStateOf(0) }
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("筛选与排序", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) },
        text = {
            Column(
                modifier = Modifier.heightIn(max = 420.dp).verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    McaButton(
                        onClick = { tab = 0 },
                        style = if (tab == 0) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                    ) { Text("筛选") }
                    McaButton(
                        onClick = { tab = 1 },
                        style = if (tab == 1) McaButtonStyle.Primary else McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                    ) { Text("排序") }
                }
                if (tab == 0) {
                    Text(
                        text = "接入方式",
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Row(
                        modifier = Modifier.horizontalScroll(rememberScrollState()),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        OnboardMethodFilter.entries.forEach { option ->
                            McaChip(
                                selected = draft.method == option,
                                onClick = { draft = draft.copy(method = option) },
                                label = { Text(option.label) },
                            )
                        }
                    }
                    Text(
                        text = "接入状态",
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Row(
                        modifier = Modifier.horizontalScroll(rememberScrollState()),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        OnboardStatusFilter.entries.forEach { option ->
                            McaChip(
                                selected = draft.status == option,
                                onClick = { draft = draft.copy(status = option) },
                                label = { Text(option.label) },
                            )
                        }
                    }
                    Text(
                        text = "「仅桌面端」= 该渠道声明的方式在手机上全部不可用（典型是只支持读桌面登录态文件）。",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                } else {
                    Text(
                        text = "排序",
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Row(
                        modifier = Modifier.horizontalScroll(rememberScrollState()),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        OnboardSort.entries.forEach { option ->
                            McaChip(
                                selected = draft.sort == option,
                                onClick = { draft = draft.copy(sort = option) },
                                label = { Text(option.label) },
                            )
                        }
                    }
                    Text(
                        text = onboardSortSummaryLine(draft.sort, previewCount(draft)),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Text(
                    text = "当前条件会命中 ${previewCount(draft)} 个渠道。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                McaButton(
                    onClick = { draft = OnboardQuery(sort = draft.sort) },
                    style = McaButtonStyle.Text,
                    size = McaButtonSize.CardAction,
                ) { Text("重置筛选") }
            }
        },
        confirmButton = {
            McaButton(onClick = { onApply(draft) }, style = McaButtonStyle.Primary) { Text("应用") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("取消") }
        },
    )
}

/** 自定义供应商入口卡：桌面对应「手动凭据」，保存走 `POST /api/provider/save`。 */
@Composable
private fun OnboardCustomProviderCard(
    notice: String?,
    canAdd: Boolean,
    onAdd: () -> Unit,
    onDismissNotice: () -> Unit,
) {
    DeepCard {
        DeepCardTitle("自定义供应商（走内置协议）")
        Spacer(Modifier.height(4.dp))
        DeepCardBody(
            "桌面对应渠道抽屉里的「手动凭据」：按渠道填写 名称 / 接口类型 / Base URL / Key / 模型（选填）。" +
                "账号会挂到内置协议渠道（openai-compat / claude-sub），不会新增渠道行。" +
                "想注册一个新渠道行，请用「账号与额度」页的「自定义渠道」。",
        )
        if (notice != null) {
            Spacer(Modifier.height(8.dp))
            Text(
                text = notice,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.primary,
            )
            Spacer(Modifier.height(4.dp))
            McaButton(onClick = onDismissNotice, style = McaButtonStyle.Outlined, size = McaButtonSize.CardAction) {
                Text("知道了")
            }
        }
        Spacer(Modifier.height(10.dp))
        McaButton(
            onClick = onAdd,
            style = McaButtonStyle.Primary,
            enabled = canAdd,
        ) { Text("手动凭据 · 添加账号") }
    }
}

/**
 * 自定义**供应商**表单：名称 → 接口类型 → Base URL → API Key → 模型 → 保存/取消。
 *
 * 与「自定义渠道（新渠道行）」的区别：这里保存后账号挂在内置 core provider 上，
 * 不会新增渠道；要注册新渠道行请到「账号与额度」页。
 *
 * 字段以服务端为准（`providerDraft`，provider_api.go:23-92）：只有 `name` / `key` /
 * `chat` 或 `anthropic` / 可选 `models`；**没有** `type`、`apiKey`、`baseURL`、`note`。
 */
@Composable
private fun OnboardCustomProviderDialog(
    draft: CustomProviderDraft,
    saving: Boolean,
    error: String?,
    modelsFetch: ModelsFetchUiState,
    onNameChange: (String) -> Unit,
    onBaseUrlChange: (String) -> Unit,
    onApiKeyChange: (String) -> Unit,
    onModelsChange: (String) -> Unit,
    onFetchModels: () -> Unit,
    onProtocolChange: (CustomProviderProtocol) -> Unit,
    onDismiss: () -> Unit,
    onSave: () -> Unit,
) {
    var showKey by remember { mutableStateOf(false) }
    McaDialog(
        onDismissRequest = onDismiss,
        title = { Text("自定义供应商（走内置协议）", fontWeight = FontWeight.Bold) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState())) {
                Text(
                    text = "这里是「自定义供应商」：保存后账号会挂到内置协议渠道（openai-compat / claude-sub），不会新增渠道行。" +
                        "想注册一个新渠道行，请用「账号与额度」页的「自定义渠道」。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(6.dp))
                OnboardField("名称（必填）", draft.name, "例如 我的中转") { onNameChange(it) }
                Spacer(Modifier.height(6.dp))
                Text(
                    text = "接口类型",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(6.dp))
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .horizontalScroll(rememberScrollState()),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    CUSTOM_PROVIDER_FORM_PROTOCOLS.forEach { protocol ->
                        McaChip(
                            selected = draft.protocol == protocol,
                            onClick = { onProtocolChange(protocol) },
                            label = {
                                Text(protocol.label, style = MaterialTheme.typography.labelSmall, maxLines = 1)
                            },
                        )
                    }
                }
                OnboardField(
                    label = "Base URL（必填）",
                    value = draft.baseUrl,
                    placeholder = "https://api.example.com/v1",
                ) { onBaseUrlChange(it) }
                OnboardField(
                    label = "API Key（必填）",
                    value = draft.apiKey,
                    placeholder = "sk-…",
                    password = !showKey,
                    trailing = {
                        McaButton(
                            onClick = { showKey = !showKey },
                            style = McaButtonStyle.Outlined,
                            size = McaButtonSize.CardAction,
                        ) { Text(if (showKey) "隐藏" else "显示") }
                    },
                ) { onApiKeyChange(it) }
                OnboardField(
                    label = "模型（选填，逗号分隔）",
                    value = draft.modelsText,
                    placeholder = "gpt-4o-mini, claude-sonnet-4",
                ) { onModelsChange(it) }
                // 「获取模型」：拿 Base URL + Key 直接问端点（`POST /api/provider/models`），
                // 不用先保存；成功只**补**模型，不覆盖手填的内容。
                val fetchHint = draftModelsFetchHint(draft.baseUrl, draft.apiKey)
                val fetching = modelsFetch is ModelsFetchUiState.Loading
                Row(verticalAlignment = Alignment.CenterVertically) {
                    McaButton(
                        onClick = onFetchModels,
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                        enabled = fetchHint == null && !fetching,
                    ) { Text(if (fetching) "获取中…" else "获取模型") }
                    Spacer(Modifier.width(8.dp))
                    Text(
                        text = when (modelsFetch) {
                            ModelsFetchUiState.Idle -> fetchHint.orEmpty()
                            ModelsFetchUiState.Loading -> "正在向该端点要模型清单…"
                            is ModelsFetchUiState.Ready -> "已获取 ${modelsFetch.count} 个模型"
                            is ModelsFetchUiState.Failed -> modelsFetch.message
                        },
                        style = MaterialTheme.typography.labelSmall,
                        color = if (modelsFetch is ModelsFetchUiState.Failed) {
                            MaterialTheme.colorScheme.error
                        } else {
                            MaterialTheme.colorScheme.onSurfaceVariant
                        },
                    )
                }
                Spacer(Modifier.height(6.dp))
                Text(
                    text = "备注：服务端 providerDraft 没有备注字段，这里不提交（也不伪造成 name 或其它字段）。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "实际提交：name / key / " + draft.protocol.jsonField + "（Base URL）" +
                        (if (draft.modelsText.isBlank()) "" else " / models") +
                        "；不含 type、apiKey、baseURL。账号落到 " + draft.protocol.coreProvider + "。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                error?.let {
                    Spacer(Modifier.height(8.dp))
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
                onClick = onSave,
                style = McaButtonStyle.Primary,
                enabled = !saving,
            ) { Text(if (saving) "保存中…" else "保存") }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("取消") }
        },
    )
}

@Composable
private fun OnboardField(
    label: String,
    value: String,
    placeholder: String = "",
    password: Boolean = false,
    trailing: (@Composable () -> Unit)? = null,
    onValueChange: (String) -> Unit,
) {
    Column(Modifier.padding(vertical = 4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = label,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (trailing != null) {
                Spacer(Modifier.width(8.dp))
                trailing()
            }
        }
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
                textStyle = MaterialTheme.typography.bodySmall.copy(color = MaterialTheme.colorScheme.onSurface),
                cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                visualTransformation = if (password) PasswordVisualTransformation() else VisualTransformation.None,
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(min = 48.dp)
                    .wrapContentHeight(Alignment.CenterVertically)
                    .padding(horizontal = 12.dp)
                    .semantics { contentDescription = label },
                decorationBox = { inner ->
                    Box {
                        if (value.isEmpty() && placeholder.isNotEmpty() && !password) {
                            Text(
                                text = placeholder,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        inner()
                    }
                },
            )
        }
    }
}

/**
 * 接入向导：**按该渠道真实支持的方式**列按钮，点开后直接在本机完成，不依赖桌面。
 *
 * 方式判定与桌面同源（`onboardMethods` = `onboard.js:207-216`）；手机端做不到的方式
 * 置灰并在下方逐条写明原因（`onboardMethodBlockedBy`），不给假按钮。
 */
@Composable
private fun OnboardConnectDialog(
    state: OnboardConnectState,
    qoderLoginState: QoderLoginUiState,
    onStartQoderLogin: () -> Unit,
    onOpenQoderLogin: (String) -> Boolean,
    onCancelQoderLogin: () -> Unit,
    onDismissQoderLogin: () -> Unit,
    onSelectMethod: (String) -> Unit,
    onLabel: (String) -> Unit,
    onCallbackInput: (String) -> Unit,
    onTokenInput: (String) -> Unit,
    onManualValue: (String, String) -> Unit,
    onWindsurfEmail: (String) -> Unit,
    onWindsurfPassword: (String) -> Unit,
    onDismissError: () -> Unit,
    onStartAuth: () -> Unit,
    onSubmitCallback: () -> Unit,
    onCommitAuth: () -> Unit,
    onCancelAuth: () -> Unit,
    onStartDevice: () -> Unit,
    onCancelDevice: () -> Unit,
    onSubmitTokenJson: () -> Unit,
    onSubmitManual: () -> Unit,
    onSubmitWindsurf: () -> Unit,
    onOpenCustomForm: () -> Unit,
    onOpenUrl: (String) -> Boolean,
    onNudgeAuthPoll: () -> Unit,
    onDismiss: () -> Unit,
) {
    val clipboard = androidx.compose.ui.platform.LocalClipboardManager.current
    val blocked = state.methods.filter { !it.enabled }
    // 应用内登录窗：只有 PKCE 的 authorize_url 走 WebView，其余（设备码 / 验证页）仍是系统浏览器。
    var webLoginUrl by remember(state.providerId) { mutableStateOf<String?>(null) }
    // 已经打开过一次应用内登录窗 → 按钮文案从「在应用内登录」变「重新打开登录页」。
    var webLoginOpened by remember(state.providerId) { mutableStateOf(false) }
    // 主提交动作统一放 footer（confirmButton）：内容区是滚动的，长表单会把按钮滚出屏幕，
    // 用户就只看到「关闭」——这正是线上那个「找不到确定按钮」的问题。
    val primary = onboardPrimaryAction(state)
    val onPrimary: () -> Unit = when (primary.kind) {
        OnboardPrimaryKind.StartAuth -> onStartAuth
        OnboardPrimaryKind.CommitAuth -> onCommitAuth
        OnboardPrimaryKind.SubmitCallback -> onSubmitCallback
        OnboardPrimaryKind.StartDevice -> onStartDevice
        OnboardPrimaryKind.SubmitTokenJson -> onSubmitTokenJson
        OnboardPrimaryKind.SubmitManual -> onSubmitManual
        OnboardPrimaryKind.SubmitWindsurf -> onSubmitWindsurf
        OnboardPrimaryKind.None -> onDismiss
    }
    McaDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                text = "接入 · ${state.providerName}",
                fontWeight = FontWeight.Bold,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        },
        text = {
            Column(
                Modifier
                    .heightIn(max = 460.dp)
                    .verticalScroll(rememberScrollState()),
            ) {
                if (state.providerDescription.isNotBlank()) {
                    Text(
                        text = state.providerDescription,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(8.dp))
                }
                // Trae 四渠道：网页授权在手机端必然失败（授权码要桌面客户端回传），
                // 一进来就把原因和唯一可行路径写清楚。
                if (onboardLoginDesktopRequired(state.providerId)) {
                    Text(
                        text = onboardTraeDesktopLoginNote(),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(8.dp))
                }
                OnboardField("账号备注（可选）", state.label, "例如 主力号") { onLabel(it) }
                Spacer(Modifier.height(8.dp))
                Text(
                    text = "接入方式",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(6.dp))
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .horizontalScroll(rememberScrollState()),
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                ) {
                    state.methods.forEach { method ->
                        McaButton(
                            onClick = { if (method.enabled) onSelectMethod(method.id) },
                            style = if (state.active == method.id) {
                                McaButtonStyle.Primary
                            } else {
                                McaButtonStyle.Outlined
                            },
                            size = McaButtonSize.CardAction,
                            enabled = method.enabled,
                        ) {
                            Text(method.label, maxLines = 1)
                        }
                    }
                }
                Spacer(Modifier.height(10.dp))
                when {
                    state.active == "oauth_pkce" || state.active == "device_code" -> OnboardAuthMethodBody(
                        state = state,
                        onStart = onStartAuth,
                        // 用户要求：浏览器授权不要跳到外部浏览器。PKCE 的 authorize_url 用应用内 WebView 打开；
                        // 设备码的 verification_uri 仍是系统浏览器（那是给用户在别处登录确认的）。
                        onOpenUrl = { target ->
                            if (state.active == "oauth_pkce" && authUrlHost(target) != null) {
                                webLoginUrl = target
                                webLoginOpened = true
                                true
                            } else {
                                onOpenUrl(target)
                            }
                        },
                        onCopy = { clipboard.setText(AnnotatedString(it)) },
                        onCallbackInput = onCallbackInput,
                        onSubmitCallback = onSubmitCallback,
                        onCancel = onCancelAuth,
                        onCommit = onCommitAuth,
                        webLoginOpened = webLoginOpened,
                    )
                    state.active == "qoder_device" -> QoderLoginSection(
                        state = qoderLoginState,
                        onStart = onStartQoderLogin,
                        onOpenUrl = onOpenQoderLogin,
                        onCancel = onCancelQoderLogin,
                        onDismiss = onDismissQoderLogin,
                    )
                    onboardDeviceKind(state.active) != null -> OnboardDeviceMethodBody(
                        state = state,
                        onStart = onStartDevice,
                        onOpenUrl = onOpenUrl,
                        onCopy = { clipboard.setText(AnnotatedString(it)) },
                        onCancel = onCancelDevice,
                    )
                    state.active == "windsurf_login" -> OnboardWindsurfBody(
                        state = state,
                        onEmail = onWindsurfEmail,
                        onPassword = onWindsurfPassword,
                        onSubmit = onSubmitWindsurf,
                    )
                    state.active == "token_json" -> OnboardTokenJsonBody(
                        state = state,
                        onTokenInput = onTokenInput,
                        onSubmit = onSubmitTokenJson,
                    )
                    state.active == "manual_input" -> OnboardManualBody(
                        state = state,
                        onManualValue = onManualValue,
                        onSubmit = onSubmitManual,
                        onOpenCustomForm = onOpenCustomForm,
                    )
                    else -> Text(
                        text = state.currentMethod?.blockedReason ?: "该方式在手机端未实现。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                state.statusLine.takeIf { it.isNotBlank() }?.let {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.primary,
                    )
                }
                state.error?.let {
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = it,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.height(4.dp))
                    McaButton(
                        onClick = onDismissError,
                        style = McaButtonStyle.Outlined,
                        size = McaButtonSize.CardAction,
                    ) { Text("知道了") }
                }
                if (blocked.isNotEmpty()) {
                    Spacer(Modifier.height(10.dp))
                    Text(
                        text = "手机端做不到的方式",
                        style = MaterialTheme.typography.labelSmall,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    blocked.forEach { method ->
                        Spacer(Modifier.height(4.dp))
                        // 会被桌面端拦下的登录方式：不再给"点了会失败"的按钮，直接写清只能桌面端做。
                        val reason = method.blockedReason.orEmpty()
                        val desktopOnly = reason.contains("桌面")
                        Text(
                            text = when {
                                // Trae 的长说明已经在顶部显示过一次，这里不重复整段。
                                reason == onboardTraeDesktopLoginNote() ->
                                    "${method.label}：${onboardAuthButtonLabel(OnboardAuthActionKind.DesktopOnly)}"
                                desktopOnly ->
                                    "${method.label}：${onboardAuthButtonLabel(OnboardAuthActionKind.DesktopOnly)}（$reason）"
                                else -> "${method.label}：$reason"
                            },
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                // 主按钮被置灰时，说清"还差什么"，别让用户以为按钮坏了。
                primary.hint?.takeIf { it.isNotBlank() }?.let { hint ->
                    Spacer(Modifier.height(8.dp))
                    Text(
                        text = hint,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        },
        confirmButton = {
            // 主提交永远在 footer，不随内容滚动；没有主动作的阶段只留「关闭」。
            if (primary.visible) {
                McaButton(onClick = onPrimary, style = McaButtonStyle.Primary, enabled = primary.enabled) {
                    Text(primary.label)
                }
            }
        },
        dismissButton = {
            McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("关闭") }
        },
    )

    // 应用内 OAuth 登录窗（用户要求不跳外部浏览器）。白名单只放行授权域名与 127.0.0.1 回环。
    val loginUrl = webLoginUrl
    if (loginUrl != null) {
        val authHost = authUrlHost(loginUrl)
        OnboardWebLoginDialog(
            url = loginUrl,
            providerHosts = if (authHost == null) emptySet() else setOf(authHost),
            onCancel = {
                webLoginUrl = null
                // 与「取消授权」同一语义：关窗 + 通知服务端取消会话。
                onCancelAuth()
            },
            onCallbackReached = {
                // WebView 已把回调请求打到本机网关：关窗，并立刻催一次轮询（不等下一个周期）。
                webLoginUrl = null
                onNudgeAuthPoll()
            },
            onFinish = {
                // 兜底：回调页没自动跳回时，用户点「完成」也会催一次轮询。
                webLoginUrl = null
                onNudgeAuthPoll()
            },
        )
    }
}

/** 浏览器授权 / 通用设备码：`/api/auth` 三段式（start → 轮询 → commit）。 */
@Composable
private fun OnboardAuthMethodBody(
    state: OnboardConnectState,
    onStart: () -> Unit,
    onOpenUrl: (String) -> Boolean,
    onCopy: (String) -> Unit,
    onCallbackInput: (String) -> Unit,
    onSubmitCallback: () -> Unit,
    onCancel: () -> Unit,
    onCommit: () -> Unit,
    webLoginOpened: Boolean = false,
) {
    val auth = state.auth
    if (auth == null) {
        Text(
            text = "1. 打开授权页　2. 在浏览器完成登录　3. 回到这里自动完成",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        // 「开始授权」在弹窗 footer（`confirmButton`）。
        return
    }
    if (auth.isSuccess) {
        Text(
            text = "授权成功。确认备注名后点「完成接入」把它存成账号。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.primary,
        )
        // 「完成接入」在弹窗 footer（`confirmButton`）。
        return
    }
    if (auth.userCode.isNotBlank()) {
        Text(
            text = "在浏览器里确认这段设备码：",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(4.dp))
        Text(
            text = auth.userCode,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onSurface,
        )
        Spacer(Modifier.height(6.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            McaButton(
                onClick = { onCopy(auth.userCode) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text("复制设备码") }
            if (auth.verificationUri.isNotBlank()) {
                // 设备码确认页是第三方站点：只能外部浏览器，点之前先说明。
                McaButton(
                    onClick = { onOpenUrl(auth.verificationUri) },
                    style = McaButtonStyle.Primary,
                    size = McaButtonSize.CardAction,
                ) { Text(onboardAuthButtonLabel(OnboardAuthActionKind.ExternalBrowser)) }
            }
        }
        onboardAuthButtonHint(OnboardAuthActionKind.ExternalBrowser)?.let { hint ->
            Text(
                text = hint,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    } else if (auth.authorizeUrl.isNotBlank()) {
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            // PKCE：真的在应用内 WebView 打开（不跳外部浏览器）。
            val inAppKind = onboardAuthActionKind(
                method = state.active,
                reopened = webLoginOpened,
            )
            McaButton(
                onClick = { onOpenUrl(auth.authorizeUrl) },
                style = McaButtonStyle.Primary,
                size = McaButtonSize.CardAction,
            ) { Text(onboardAuthButtonLabel(inAppKind)) }
            McaButton(
                onClick = { onCopy(auth.authorizeUrl) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text("复制链接") }
        }
        Spacer(Modifier.height(10.dp))
        // 浏览器多半会落到 127.0.0.1:<回调端口> 的本地回环页面：能自动完成；
        // 万一浏览器没有把请求交给网关，就允许把地址栏内容贴回来。
        OnboardField(
            "回调地址（自动完成不了时）",
            state.callbackInput,
            "粘贴完整回调地址，或只贴 code",
        ) { onCallbackInput(it) }
        // 「我已授权，继续」在弹窗 footer（`confirmButton`）。
    }
    Spacer(Modifier.height(8.dp))
    McaButton(onClick = onCancel, style = McaButtonStyle.Outlined, size = McaButtonSize.CardAction) {
        Text("取消授权")
    }
}

/** 渠道各自的登录会话（trae / kiro / zed / codebuddy / workbuddy / minimax）。 */
@Composable
private fun OnboardDeviceMethodBody(
    state: OnboardConnectState,
    onStart: () -> Unit,
    onOpenUrl: (String) -> Boolean,
    onCopy: (String) -> Unit,
    onCancel: () -> Unit,
) {
    val device = state.device
    if (device == null) {
        Text(
            text = "打开登录页并完成登录，网关会自动把账号入库。",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        // 「开始登录」在弹窗 footer（`confirmButton`）。
        return
    }
    if (device.hasUserCode) {
        Text(
            text = "在浏览器里确认这段代码：",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(4.dp))
        Text(
            text = device.userCode,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onSurface,
        )
        Spacer(Modifier.height(6.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            McaButton(
                onClick = { onCopy(device.userCode) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text("复制设备码") }
            if (device.verificationUri.isNotBlank()) {
                McaButton(
                    onClick = { onOpenUrl(device.verificationUri) },
                    style = McaButtonStyle.Primary,
                    size = McaButtonSize.CardAction,
                ) { Text(onboardAuthButtonLabel(OnboardAuthActionKind.ExternalBrowser)) }
            }
        }
        onboardAuthButtonHint(OnboardAuthActionKind.ExternalBrowser)?.let { hint ->
            Text(
                text = hint,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    } else if (device.loginUrl.isNotBlank()) {
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            // 渠道私有登录页（trae / kiro / zed …）：第三方站点，只能外部浏览器。
            McaButton(
                onClick = { onOpenUrl(device.loginUrl) },
                style = McaButtonStyle.Primary,
                size = McaButtonSize.CardAction,
            ) { Text(onboardAuthButtonLabel(OnboardAuthActionKind.ExternalBrowser)) }
            McaButton(
                onClick = { onCopy(device.loginUrl) },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text("复制链接") }
        }
        onboardAuthButtonHint(OnboardAuthActionKind.ExternalBrowser)?.let { hint ->
            Text(
                text = hint,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    Spacer(Modifier.height(8.dp))
    McaButton(onClick = onCancel, style = McaButtonStyle.Outlined, size = McaButtonSize.CardAction) {
        Text("取消登录")
    }
}

/** Token / JSON：纯 token 或完整 JSON 都收，逐项落 `POST /api/accounts`。 */
@Composable
private fun OnboardTokenJsonBody(
    state: OnboardConnectState,
    onTokenInput: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    Text(
        text = "粘贴 access token / API Key，或完整 JSON（例如 Codex auth.json）；" +
            "支持 {tokens:{...}} 包装，也支持一次贴多份（数组）。",
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Spacer(Modifier.height(6.dp))
    OnboardTextArea(
        value = state.tokenInput,
        placeholder = "sk-… 或 {\"access_token\":\"…\"}",
        onValueChange = onTokenInput,
    )
    // 主按钮在弹窗 footer（`confirmButton`），这里不再放第二颗「导入并接入」。
}

/** 手动凭据：按渠道 `auth_form_schema` 生成表单（桌面 `onboard.js:297-354`）。 */
@Composable
private fun OnboardManualBody(
    state: OnboardConnectState,
    onManualValue: (String, String) -> Unit,
    onSubmit: () -> Unit,
    onOpenCustomForm: () -> Unit,
) {
    state.manualSchema.forEach { field ->
        OnboardField(
            label = field.label + if (field.required) "（必填）" else "（选填）",
            value = state.manualValues[field.key].orEmpty(),
            placeholder = field.placeholder,
            password = field.type == "password",
        ) { onManualValue(field.key, it) }
        if (field.help.isNotBlank()) {
            Text(
                text = field.help,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
    Spacer(Modifier.height(8.dp))
    // 「接入」在 footer；这里只留次要动作（自定义供应商）。
    Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        McaButton(onClick = onOpenCustomForm, style = McaButtonStyle.Outlined, size = McaButtonSize.CardAction) {
            Text("自定义供应商（走内置协议）")
        }
    }
    Spacer(Modifier.height(6.dp))
    Text(
        text = "Base URL 型网关用「自定义供应商」：账号挂到内置协议渠道，名称 / 接口类型 / Base URL / Key 都按渠道填写；" +
            "想注册一个新渠道行，请用「账号与额度」页的「自定义渠道」。",
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
}

/**
 * Windsurf 邮箱 + 密码登录（桌面 `onboard.js:561-582`，服务端 `POST /api/provider/windsurf/login`）。
 *
 * 密码是凭据：密码样式默认遮蔽、可临时显示；不进剪贴板、不打印、不落盘，
 * 只随一次请求体提交（见 `OnboardAuthApiClient.loginWindsurf`）。
 */
@Composable
private fun OnboardWindsurfBody(
    state: OnboardConnectState,
    onEmail: (String) -> Unit,
    onPassword: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    var showPassword by remember { mutableStateOf(false) }
    Text(
        text = "使用 Windsurf / Devin 账号邮箱密码登录；密码只用于本次请求，不保存、不写日志、不进剪贴板。",
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Spacer(Modifier.height(6.dp))
    OnboardField("邮箱（必填）", state.windsurfEmail, "dev@example.com") { onEmail(it) }
    OnboardField(
        label = "密码（必填）",
        value = state.windsurfPassword,
        placeholder = "••••••",
        password = !showPassword,
        trailing = {
            McaButton(
                onClick = { showPassword = !showPassword },
                style = McaButtonStyle.Outlined,
                size = McaButtonSize.CardAction,
            ) { Text(if (showPassword) "隐藏" else "显示") }
        },
    ) { onPassword(it) }
    // 「登录并接入」在弹窗 footer（`confirmButton`）。
}

/** 多行输入框（Token / JSON 用）；48dp 最小高、字号放大不裁字。 */
@Composable
private fun OnboardTextArea(value: String, placeholder: String, onValueChange: (String) -> Unit) {
    Surface(
        shape = McaShape.CardAction,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            textStyle = MaterialTheme.typography.bodySmall.copy(color = MaterialTheme.colorScheme.onSurface),
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = 96.dp)
                .padding(12.dp),
            decorationBox = { inner ->
                Box {
                    if (value.isEmpty()) {
                        Text(
                            text = placeholder,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    inner()
                }
            },
        )
    }
}

/** 桌面 `onboard.js:209`：`qoder-` 前缀的渠道额外支持设备码登录。 */
internal fun hasQoderDeviceLogin(providerId: String): Boolean =
    providerId.trim().lowercase().startsWith("qoder-")

/** 渠道卡片副标题：账号数 + 该渠道在**手机端**真正可用的接入方式。 */
internal fun onboardProviderMeta(provider: ProviderSummary, accountCount: Int): String {
    val usable = onboardMethods(provider).filter { onboardMethodBlockedBy(it, provider) == null }
    val labels = usable.map { onboardMethodLabel(it) } + "Token / JSON"
    return "已有 $accountCount 个账号 · 可用：" + labels.joinToString(" / ")
}

/**
 * `chat == false` 渠道的预期说明（cursor / windsurf / zed：只有账号 / 额度 / 登录能力）。
 *
 * 放在渠道卡描述下方、**不禁用接入按钮**——接入后能看账号与额度，本身是有意义的；
 * 这句话只是把"不会转发对话"提前讲清楚，别等用户发请求才撞 503/400。
 */
internal fun onboardChatUnsupportedHint(): String =
    "该渠道目前仅支持账号与额度查看，暂不支持对话转发"

/**
 * 搜索过滤：桌面 `paintGrid` 用 `display_name / family / id / description` 做子串匹配
 * （`onboard.js:172-178`）；手机端没有 family 字段，口径取 name / id / description。
 * 空白搜索返回原列表（保持服务端顺序）。
 */
internal fun filterOnboardProviders(providers: List<ProviderSummary>, query: String): List<ProviderSummary> {
    val needle = query.trim()
    if (needle.isEmpty()) {
        return providers
    }
    return providers.filter { provider ->
        provider.name.contains(needle, ignoreCase = true) ||
            provider.id.contains(needle, ignoreCase = true) ||
            provider.description.contains(needle, ignoreCase = true)
    }
}

/** 首字母：A–Z 归对应字母，其余（中文、数字、符号）统一进「#」。 */
internal fun onboardProviderLetter(provider: ProviderSummary): String {
    val source = provider.name.trim().ifBlank { provider.id.trim() }
    val first = source.firstOrNull() ?: return "#"
    val upper = first.uppercaseChar()
    return if (upper in 'A'..'Z') upper.toString() else "#"
}

internal data class OnboardProviderGroup(
    val letter: String,
    val providers: List<ProviderSummary>,
)

/**
 * 按首字母分组：A–Z 升序、「#」排最后；**空组不出现**。
 * 组内保持服务端顺序（不打乱用户刚看过的渠道）。
 */
internal fun groupOnboardProviders(providers: List<ProviderSummary>): List<OnboardProviderGroup> {
    val byLetter = LinkedHashMap<String, MutableList<ProviderSummary>>()
    providers.forEach { provider ->
        val letter = onboardProviderLetter(provider)
        byLetter.getOrPut(letter) { mutableListOf() }.add(provider)
    }
    val orderedLetters = byLetter.keys.sortedWith(compareBy({ it == "#" }, { it }))
    return orderedLetters.map { letter -> OnboardProviderGroup(letter, byLetter.getValue(letter)) }
}

/* ============================================================================
 * 筛选 / 排序（对标 MCA 模型推荐页：`feature/modelhub/RecommendationQuery.kt` +
 * `RecommendationFilterSheet.kt` 的「筛选 · N」工具行 + 一个面板两栏）
 *
 * 与 MCA 的对应关系：
 *  - `RecommendationSort`  → [OnboardSort]（单一维度枚举 + 中文 label + 摘要句）
 *  - `filterCount` / `筛选 · N` → [OnboardQuery.filterCount] / [onboardFilterChipLabel]
 *  - `isPristine` + 「重置」只清筛选、保留排序 → [OnboardQuery.isPristine] / [OnboardViewModel.clearFilters]
 *  - 目录顺序分组、显式排序平铺 → 排序 = 名称时按字母分组，其它排序平铺（见页面 rows 构建）
 * ========================================================================== */

/** 手机端**真正可用**的接入方式（`local_import` 与缺 OAuth 参数的方式已被过滤掉）。 */
internal fun onboardUsableMethods(provider: ProviderSummary): List<String> =
    onboardMethods(provider).filter { onboardMethodBlockedBy(it, provider) == null }

/** 一键授权类方式：不需要用户手抄 Key 就能拿到账号。 */
private val ONBOARD_AUTO_METHODS: Set<String> = setOf(
    "oauth_pkce",
    "device_code",
    "minimax_device",
    "qoder_device",
    "workbuddy_device",
    "codebuddy_device",
    "zed_device",
    "kiro_device",
    "trae_device",
    "windsurf_login",
)

/** 「接入方式」筛选：三档互斥，覆盖手机端所有可能。 */
internal enum class OnboardMethodFilter(val label: String) {
    ALL("全部"),
    AUTO("可自动授权"),
    MANUAL("需手动凭据"),
    DESKTOP_ONLY("仅桌面端"),
}

/** 「接入状态」筛选。 */
internal enum class OnboardStatusFilter(val label: String) {
    ALL("全部"),
    CONNECTED("已接入"),
    NOT_CONNECTED("未接入"),
}

/** 排序：默认名称 A–Z（与字母条同一顺序）。 */
internal enum class OnboardSort(val label: String) {
    NAME("名称 A–Z"),
    ACCOUNTS("账号数"),
    RECENT("最近使用"),
}

/** 一次已应用的筛选 + 排序。 */
internal data class OnboardQuery(
    val method: OnboardMethodFilter = OnboardMethodFilter.ALL,
    val status: OnboardStatusFilter = OnboardStatusFilter.ALL,
    val sort: OnboardSort = OnboardSort.NAME,
) {
    /** 已应用的筛选条件数（排序不计入，与 MCA `filterCount` 同口径）。 */
    val filterCount: Int
        get() = (if (method != OnboardMethodFilter.ALL) 1 else 0) +
            (if (status != OnboardStatusFilter.ALL) 1 else 0)

    /** 无筛选且默认排序 = 未改动原始目录。 */
    val isPristine: Boolean
        get() = filterCount == 0 && sort == OnboardSort.NAME
}

/**
 * 渠道归类：有可用的一键方式 → 可自动授权；否则有手动方式 → 需手动凭据；
 * 一个可用方式都没有（声明的方式全被平台门禁挡住，典型是只支持本机导入）→ 仅桌面端。
 */
internal fun onboardMethodCategory(provider: ProviderSummary): OnboardMethodFilter {
    val usable = onboardUsableMethods(provider)
    if (usable.any { it in ONBOARD_AUTO_METHODS }) {
        return OnboardMethodFilter.AUTO
    }
    if (usable.isNotEmpty()) {
        return OnboardMethodFilter.MANUAL
    }
    return OnboardMethodFilter.DESKTOP_ONLY
}

/** 某渠道在给定账号列表里的账号数。 */
internal fun onboardAccountCount(provider: ProviderSummary, accounts: List<AccountQuotaRow>): Int =
    accounts.count { it.providerId == provider.id }

/** 某渠道最近一次使用（取该渠道下账号 `lastUsed` 的最大值；0 = 从未使用）。 */
internal fun onboardLastUsed(provider: ProviderSummary, accounts: List<AccountQuotaRow>): Long =
    accounts.filter { it.providerId == provider.id }.maxOfOrNull { it.lastUsed } ?: 0L

/**
 * 搜索 + 筛选 + 排序一次做完（顺序与 MCA 一致：先筛后排；搜索与两者叠加）。
 * 空搜索 / 全部筛选时行为与旧实现完全一致。
 */
internal fun applyOnboardQuery(
    providers: List<ProviderSummary>,
    accounts: List<AccountQuotaRow>,
    query: String,
    state: OnboardQuery,
): List<ProviderSummary> {
    val searched = filterOnboardProviders(providers, query)
    val filtered = searched.filter { provider ->
        val methodOk = state.method == OnboardMethodFilter.ALL ||
            onboardMethodCategory(provider) == state.method
        if (!methodOk) {
            return@filter false
        }
        val count = onboardAccountCount(provider, accounts)
        when (state.status) {
            OnboardStatusFilter.ALL -> true
            OnboardStatusFilter.CONNECTED -> count > 0
            OnboardStatusFilter.NOT_CONNECTED -> count == 0
        }
    }
    return sortOnboardProviders(filtered, accounts, state.sort)
}

/**
 * 排序。`NAME` 用显示名不区分大小写；`ACCOUNTS` 账号多在前；`RECENT` 最近用过的在前。
 * 三者都以名称做稳定的次级键，避免同值时顺序抖动。
 */
internal fun sortOnboardProviders(
    providers: List<ProviderSummary>,
    accounts: List<AccountQuotaRow>,
    sort: OnboardSort,
): List<ProviderSummary> {
    val byName = compareBy<ProviderSummary>(
        { it.name.ifBlank { it.id }.lowercase() },
        { it.id },
    )
    return when (sort) {
        OnboardSort.NAME -> providers.sortedWith(byName)
        OnboardSort.ACCOUNTS -> providers.sortedWith(
            compareByDescending<ProviderSummary> { onboardAccountCount(it, accounts) }.then(byName),
        )
        OnboardSort.RECENT -> providers.sortedWith(
            compareByDescending<ProviderSummary> { onboardLastUsed(it, accounts) }.then(byName),
        )
    }
}

/** 工具行左侧按钮文案（MCA `recommendationFilterChipLabel` 同形）。 */
internal fun onboardFilterChipLabel(state: OnboardQuery): String =
    if (state.filterCount > 0) "筛选 · ${state.filterCount}" else "筛选"

/** 面板里排序那一栏的摘要句（MCA `recommendationSortSummaryLine` 同形）。 */
internal fun onboardSortSummaryLine(sort: OnboardSort, count: Int): String = when (sort) {
    OnboardSort.NAME -> "按名称 A–Z 显示 $count 个渠道，字母条可用"
    OnboardSort.ACCOUNTS -> "账号多的排前面；未接入的排后面（共 $count 个）"
    OnboardSort.RECENT -> "最近用过的排前面；从未使用的按名称排（共 $count 个）"
}

/** 渠道列表标题右侧的计数：有筛选/搜索时显示 `命中 / 总数`。 */
internal fun onboardCountLabel(visible: Int, total: Int, searching: Boolean): String =
    if (searching && total > 0) "$visible / $total 个渠道" else "$visible 个渠道"
