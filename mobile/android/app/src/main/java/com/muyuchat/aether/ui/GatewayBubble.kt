package com.muyuchat.aether.ui

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInRoot
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Popup
import androidx.compose.ui.window.PopupProperties
import androidx.compose.ui.zIndex
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaDialog
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import kotlin.math.hypot
import kotlin.math.roundToInt
import androidx.compose.animation.core.LinearEasing
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.drawscope.Stroke

/** 悬浮球直径；页面球和系统 overlay 共用。 */
internal const val BubbleMarkDp = 64f

/** 页面球拖动时的活动区（root 坐标），由内层 BoxWithConstraints 上报。 */
private data class BubbleMetrics(
    val minX: Int,
    val minY: Int,
    val travelX: Int,
    val travelY: Int,
)

/** 系统外显的前置条件；顺序就是用户实际会遇到的顺序（先有网关，再要权限）。 */
internal enum class OverlayAvailability { Available, NeedGateway, NeedPermission }

internal fun overlayAvailability(running: Boolean, permissionGranted: Boolean): OverlayAvailability = when {
    !running -> OverlayAvailability.NeedGateway
    !permissionGranted -> OverlayAvailability.NeedPermission
    else -> OverlayAvailability.Available
}

internal fun overlayActionLabel(visible: Boolean): String =
    if (visible) "关闭 app 外显" else "app 外显"

/** 权限未授予时的原话：说清为什么、去哪授权，不假装已经显示。 */
internal fun overlayPermissionRequestCopy(): String =
    "系统级悬浮窗需要「显示在其他应用上层」权限。没有这项权限，退出本 app 后悬浮球不会留在桌面上。" +
        "授权后回到本页，再点一次「app 外显」即可。"

/**
 * 后台常驻的诚实说法。
 *
 * 前台服务能显著降低被回收的概率，但 Android/厂商省电策略、用户强制停止、
 * 通知权限变化都可能终止它；不写"永不"或"保证"。
 */
internal fun gatewayBackgroundNote(): String =
    "网关由前台服务托管，退出 app 后通常仍会运行；但系统省电策略、强制停止或通知权限变化都可能终止它，" +
        "停止后悬浮球会变灰。"

/**
 * 页面内悬浮球宿主。
 *
 * 关键：手势层是**覆盖全屏、位于内容之上的父节点**，节点本身不移动，所以 pointer 坐标稳定；
 * 在 `awaitFirstDown` 时用球的 root 矩形 hit-test，命中才 consume 并拖动，未命中直接返回，
 * 事件继续交给下层页面（父节点不消费，子节点照常收到）。
 *
 * 拖动全程只用像素（dragDx/dragDy + offset lambda），松手才换算比例并吸边。
 * 运行中=primaryContainer/品牌色/呼吸灯；停止=surfaceVariant/onSurfaceVariant/无光晕。
 */
@Composable
internal fun GatewayBubble(
    status: GatewayStatus,
    overlayVisible: Boolean,
    copied: Boolean,
    onPrimaryAction: () -> Unit,
    onCopy: () -> Unit,
    onToggleOverlay: () -> Unit,
    modifier: Modifier = Modifier,
    contentInsets: PaddingValues = PaddingValues(0.dp),
    content: @Composable () -> Unit = {},
) {
    val context = LocalContext.current
    val density = LocalDensity.current
    var expanded by remember { mutableStateOf(false) }
    var permissionDialog by remember { mutableStateOf(false) }
    var permissionGranted by remember { mutableStateOf(Settings.canDrawOverlays(context)) }
    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) {
        permissionGranted = Settings.canDrawOverlays(context)
        if (permissionGranted && status.running) {
            // 用户是点了「app 外显 → 去授权」才离开的，回来后直接尝试开启；失败仍不伪装成功。
            onToggleOverlay()
        }
    }

    LaunchedEffect(expanded) {
        if (expanded) {
            permissionGranted = Settings.canDrawOverlays(context)
        }
    }

    var xFraction by rememberSaveable { mutableStateOf(1f) }
    var yFraction by rememberSaveable { mutableStateOf(1f) }
    var dragDx by remember { mutableIntStateOf(0) }
    var dragDy by remember { mutableIntStateOf(0) }
    var bubbleRect by remember { mutableStateOf(Rect.Zero) }
    var hostOrigin by remember { mutableStateOf(Offset.Zero) }
    var metrics by remember { mutableStateOf(BubbleMetrics(0, 0, 0, 0)) }

    BoxWithConstraints(
        modifier = modifier
            .fillMaxSize()
            .onGloballyPositioned { hostOrigin = it.positionInRoot() }
            .pointerInput(Unit) {
                val dragModeThresholdPx = 4.dp.toPx()
                awaitEachGesture {
                    val down = awaitFirstDown(
                        requireUnconsumed = false,
                        pass = PointerEventPass.Initial,
                    )
                    val downInRoot = down.position + hostOrigin
                    // 未命中小球：不消费、不拦事件，页面照常滚动/点击。
                    if (!OverlayGeometry.hitTest(
                            pointerX = downInRoot.x,
                            pointerY = downInRoot.y,
                            left = bubbleRect.left,
                            top = bubbleRect.top,
                            sizePx = bubbleRect.width,
                        )
                    ) {
                        return@awaitEachGesture
                    }
                    down.consume()

                    val startPointerX = down.position.x
                    val startPointerY = down.position.y
                    val m = metrics
                    val startAbsX = m.minX + OverlayGeometry.offsetPx(xFraction, m.travelX)
                    val startAbsY = m.minY + OverlayGeometry.offsetPx(yFraction, m.travelY)
                    val maxAbsX = m.minX + m.travelX
                    val maxAbsY = m.minY + m.travelY
                    var dragging = false

                    while (true) {
                        // Initial pass：父节点先于页面子节点收到，命中后 consume 才能阻止页面滚动。
                        val event = awaitPointerEvent(PointerEventPass.Initial)
                        val change = event.changes.firstOrNull { it.id == down.id } ?: break
                        if (!change.pressed) {
                            if (dragging) {
                                val finalX = OverlayGeometry.dragOffsetPx(
                                    startOffsetPx = startAbsX,
                                    startPointerPx = startPointerX,
                                    currentPointerPx = change.position.x,
                                    minPx = m.minX,
                                    maxPx = maxAbsX,
                                )
                                val finalY = OverlayGeometry.dragOffsetPx(
                                    startOffsetPx = startAbsY,
                                    startPointerPx = startPointerY,
                                    currentPointerPx = change.position.y,
                                    minPx = m.minY,
                                    maxPx = maxAbsY,
                                )
                                xFraction = OverlayGeometry.snapFraction(
                                    OverlayGeometry.fractionForOffset(finalX - m.minX, m.travelX),
                                )
                                yFraction = OverlayGeometry.fractionForOffset(finalY - m.minY, m.travelY)
                            }
                            dragDx = 0
                            dragDy = 0
                            if (!dragging) {
                                expanded = !expanded
                            }
                            return@awaitEachGesture
                        }

                        val dx = change.position.x - startPointerX
                        val dy = change.position.y - startPointerY
                        val distance = hypot(dx.toDouble(), dy.toDouble()).toFloat()
                        if (!dragging && distance > dragModeThresholdPx) {
                            dragging = true
                            expanded = false
                        }
                        // 第一帧就跟随；两轴各自独立累加，不存在斜拖时一轴被抵消的问题。
                        val absX = OverlayGeometry.dragOffsetPx(
                            startOffsetPx = startAbsX,
                            startPointerPx = startPointerX,
                            currentPointerPx = change.position.x,
                            minPx = m.minX,
                            maxPx = maxAbsX,
                        )
                        val absY = OverlayGeometry.dragOffsetPx(
                            startOffsetPx = startAbsY,
                            startPointerPx = startPointerY,
                            currentPointerPx = change.position.y,
                            minPx = m.minY,
                            maxPx = maxAbsY,
                        )
                        dragDx = absX - startAbsX
                        dragDy = absY - startAbsY
                        change.consume()
                    }
                    dragDx = 0
                    dragDy = 0
                }
            },
    ) {
        content()

        BoxWithConstraints(
            modifier = Modifier
                .fillMaxSize()
                .padding(contentInsets)
                .imePadding()
                .zIndex(2f),
        ) {
            val markPx = with(density) { BubbleMarkDp.dp.toPx() }
            val edgePx = with(density) { OverlayGeometry.EDGE_DP.dp.toPx() }.roundToInt()
            val widthPx = with(density) { maxWidth.toPx() }
            val heightPx = with(density) { maxHeight.toPx() }
            val travelX = OverlayGeometry.travelPx(widthPx.roundToInt(), markPx.roundToInt(), edgePx)
            val travelY = OverlayGeometry.travelPx(heightPx.roundToInt(), markPx.roundToInt(), edgePx)
            val minX = edgePx
            val minY = edgePx
            val maxX = edgePx + travelX
            val maxY = edgePx + travelY
            SideEffect {
                metrics = BubbleMetrics(minX = minX, minY = minY, travelX = travelX, travelY = travelY)
            }

            val onRight = xFraction >= 0.5f
            val menuAbove = yFraction >= 0.5f

            Box(
                modifier = Modifier
                    .offset {
                        val baseX = minX + OverlayGeometry.offsetPx(xFraction, travelX)
                        val baseY = minY + OverlayGeometry.offsetPx(yFraction, travelY)
                        IntOffset(
                            x = (baseX + dragDx).coerceIn(minX, maxX),
                            y = (baseY + dragDy).coerceIn(minY, maxY),
                        )
                    }
                    .zIndex(2f)
                    .size(BubbleMarkDp.dp)
                    .onGloballyPositioned { bubbleRect = it.boundsInRoot() }
                    .semantics {
                        contentDescription = if (status.running) {
                            "网关运行中，打开网关操作"
                        } else {
                            "网关已停止，打开网关操作"
                        }
                        role = Role.Button
                        onClick(label = "打开网关操作") {
                            expanded = !expanded
                            true
                        }
                    },
            ) {
                GatewayOrb(running = status.running)

                if (expanded) {
                    val gapPx = with(density) { 12.dp.roundToPx() }
                    Popup(
                        alignment = when {
                            onRight && menuAbove -> Alignment.BottomEnd
                            onRight -> Alignment.TopEnd
                            menuAbove -> Alignment.BottomStart
                            else -> Alignment.TopStart
                        },
                        offset = IntOffset(
                            x = 0,
                            y = if (menuAbove) {
                                -(markPx.roundToInt() + gapPx)
                            } else {
                                markPx.roundToInt() + gapPx
                            },
                        ),
                        onDismissRequest = { expanded = false },
                        properties = PopupProperties(
                            focusable = true,
                            dismissOnBackPress = true,
                            dismissOnClickOutside = true,
                        ),
                    ) {
                        BubbleMenu(
                            status = status,
                            overlayVisible = overlayVisible,
                            copied = copied,
                            permissionGranted = permissionGranted,
                            onPrimaryAction = onPrimaryAction,
                            onCopy = onCopy,
                            onOverlayClick = {
                                if (overlayVisible) {
                                    onToggleOverlay()
                                } else {
                                    when (overlayAvailability(status.running, permissionGranted)) {
                                        OverlayAvailability.NeedGateway -> Unit
                                        OverlayAvailability.NeedPermission -> {
                                            expanded = false
                                            permissionDialog = true
                                        }
                                        OverlayAvailability.Available -> onToggleOverlay()
                                    }
                                }
                            },
                        )
                    }
                }
            }
        }
    }

    if (permissionDialog) {
        McaDialog(
            onDismissRequest = { permissionDialog = false },
            title = {
                Text(
                    text = "需要系统悬浮窗权限",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                )
            },
            text = {
                Text(
                    text = overlayPermissionRequestCopy(),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            },
            confirmButton = {
                McaButton(
                    onClick = {
                        permissionDialog = false
                        runCatching {
                            permissionLauncher.launch(overlayPermissionIntent(context))
                        }
                    },
                    style = McaButtonStyle.Primary,
                ) { Text("去授权") }
            },
            dismissButton = {
                McaButton(
                    onClick = { permissionDialog = false },
                    style = McaButtonStyle.Outlined,
                ) { Text("取消") }
            },
        )
    }
}

@Composable
private fun BubbleMenu(
    status: GatewayStatus,
    overlayVisible: Boolean,
    copied: Boolean,
    permissionGranted: Boolean,
    onPrimaryAction: () -> Unit,
    onCopy: () -> Unit,
    onOverlayClick: () -> Unit,
) {
    Surface(
        modifier = Modifier.width(264.dp),
        shape = McaShape.Sheet,
        color = MaterialTheme.colorScheme.surface,
        border = McaHairline.stroke(),
    ) {
        Column(
            modifier = Modifier.padding(12.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = gatewayStateHeadline(status),
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.Bold,
                    color = statusAccent(status),
                    modifier = Modifier.weight(1f),
                )
                Text(
                    text = if (status.running) "运行中" else "已停止",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Text(
                text = gatewayBackgroundNote(),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                McaButton(
                    onClick = onPrimaryAction,
                    style = McaButtonStyle.Primary,
                    modifier = Modifier.weight(1f),
                    enabled = !status.busy,
                ) { Text(gatewayActionLabel(status)) }
                McaButton(
                    onClick = onCopy,
                    style = McaButtonStyle.Outlined,
                    modifier = Modifier.weight(1f),
                    enabled = status.running && status.addr.isNotBlank(),
                ) { Text(if (copied) "已复制" else "复制地址") }
            }
            McaButton(
                onClick = onOverlayClick,
                style = McaButtonStyle.Outlined,
                modifier = Modifier.fillMaxWidth(),
                enabled = status.running,
            ) { Text(overlayActionLabel(overlayVisible)) }
            if (status.running && !permissionGranted) {
                Text(
                    text = "系统外显需要「显示在其他应用上层」权限；未授权时不会显示。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            } else if (!status.running) {
                Text(
                    text = "启动网关后才能开启系统外显。",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

/** 球内品牌图标的显示尺寸：48dp 圆里 32dp（圆不放大，图标占比 67%）。 */
private val BrandOrbGlyphDp = 32.dp

/** 圆形直径：运行态与停止态**完全相同**——灯光只在圆内，不许把圆撑大。 */
internal val OrbDiameter = 48.dp

/** 圆内呼吸环：宽度、透明度区间、周期（用户规格：1.5–2dp / 0.35↔0.85 / 1.6s 一个来回）。 */
internal val OrbRingWidth = 1.75.dp
internal const val OrbRingAlphaLow = 0.35f
internal const val OrbRingAlphaHigh = 0.85f
internal const val OrbRingPeriodMs = 1600

/** 圆内极淡内发光的上限（别用外发光：会溢出圆形、显廉价）。 */
internal const val OrbGlowAlphaMax = 0.10f

/**
 * 呼吸环透明度：**停止态恒 0**（没有灯），运行态在 [OrbRingAlphaLow] ↔ [OrbRingAlphaHigh] 之间按 phase 插值。
 * phase 由 InfiniteTransition 在 0..1 之间来回驱动。
 */
internal fun orbRingAlpha(running: Boolean, phase: Float): Float {
    if (!running) {
        return 0f
    }
    val p = phase.coerceIn(0f, 1f)
    return OrbRingAlphaLow + (OrbRingAlphaHigh - OrbRingAlphaLow) * p
}

/** 内发光强度：停止态恒 0；运行态与环反相（环最亮时内发光最淡），上限 [OrbGlowAlphaMax]。 */
internal fun orbGlowAlpha(running: Boolean, phase: Float): Float {
    if (!running) {
        return 0f
    }
    val p = phase.coerceIn(0f, 1f)
    return OrbGlowAlphaMax * (1f - p)
}

/** 圆形直径在任何状态下都一样（回归测试用它锁"不许扩大圆形"）。 */
internal fun orbDiameter(running: Boolean): Dp = OrbDiameter

/** 图标永远用品牌本色：单色 tint 会把气泡/星芒涂平（上一轮线上事故的原因）。 */
internal fun orbTintsGlyph(): Boolean = false

/**
 * 球内的品牌图标。
 *
 * 用球内专用的 [R.drawable.ic_brand_orb]：气泡 + 星芒 + **放大 1.7× 的 channel-route 徽标**。
 * 不做任何 tint、不参与动画——灯光由外层圆环/内发光承担，图标本身保持品牌本色。
 * （`ic_brand_mark` 只有气泡 + 星芒，和 MCA 的图标撞脸；`ic_brand_avatar` 带背景矩形，小尺寸不能用。）
 */
@Composable
private fun BrandOrbIcon() {
    Image(
        painter = painterResource(R.drawable.ic_brand_orb),
        contentDescription = null,
        modifier = Modifier.size(BrandOrbGlyphDp),
    )
}

/** 运行中才组合呼吸动画；停止态是纯静态（不创建 InfiniteTransition，没有灯光）。 */
@Composable
private fun GatewayOrb(running: Boolean) {
    val accent = MaterialTheme.colorScheme.primary
    var ringAlpha = 0f
    var glowAlpha = 0f
    if (running) {
        val transition = rememberInfiniteTransition(label = "orbBreath")
        val phase by transition.animateFloat(
            initialValue = 0f,
            targetValue = 1f,
            animationSpec = infiniteRepeatable(
                tween(OrbRingPeriodMs, easing = LinearEasing),
                RepeatMode.Reverse,
            ),
            label = "orbBreathPhase",
        )
        ringAlpha = orbRingAlpha(running = true, phase = phase)
        glowAlpha = orbGlowAlpha(running = true, phase = phase)
    }
    OrbPlate(
        running = running,
        accent = accent,
        ringAlpha = ringAlpha,
        glowAlpha = glowAlpha,
        container = MaterialTheme.colorScheme.surfaceVariant,
        border = McaHairline.default(),
    )
}

/**
 * 圆形球体：**一个圆 + 品牌标识**。
 *
 * 停止态：浅灰圆 + 品牌本色图标，没有灯光、没有动画。
 * 运行态：同一个圆（尺寸不变）内部加一圈呼吸环 + 一层极淡内发光；两者都只画在圆内。
 */
@Composable
private fun OrbPlate(
    running: Boolean,
    accent: Color,
    ringAlpha: Float,
    glowAlpha: Float,
    container: Color,
    border: Color,
    semantics: Modifier = Modifier,
) {
    // 外层仍是 BubbleMarkDp(64dp) 的布局外壳：拖动/贴边的几何按它算，不能改。
    // 圆本身固定 48dp，运行态也不放大——灯光只画在圆内。
    Box(modifier = Modifier.size(BubbleMarkDp.dp), contentAlignment = Alignment.Center) {
        Box(
            modifier = semantics
                .size(OrbDiameter)
                .clip(CircleShape)
                .background(container)
                .border(McaHairline.Width, border, CircleShape)
                // 内发光：在底板之上、图标之下；radialGradient 半径 = 圆半径，天然不外溢。
                .drawBehind {
                    if (glowAlpha > 0f) {
                        drawCircle(
                            brush = Brush.radialGradient(
                                colors = listOf(accent.copy(alpha = glowAlpha), Color.Transparent),
                                center = center,
                                radius = size.minDimension / 2f,
                            ),
                            radius = size.minDimension / 2f,
                        )
                    }
                },
            contentAlignment = Alignment.Center,
        ) {
            BrandOrbIcon()
            // 呼吸环：贴着内缘画，环心半径 = (直径 - 环宽) / 2，不出圆。
            if (ringAlpha > 0f) {
                Canvas(Modifier.matchParentSize()) {
                    val stroke = OrbRingWidth.toPx()
                    drawCircle(
                        color = accent.copy(alpha = ringAlpha),
                        radius = (size.minDimension - stroke) / 2f,
                        style = Stroke(width = stroke),
                    )
                }
            }
        }
    }
}

/**
 * 系统级悬浮窗里的球（WindowManager 的 ComposeView 使用）。
 *
 * 这里**不套 McaTheme**：McaTheme 会写 Activity window，而 overlay 的
 * context 是 Service（`view.context as Activity` 会崩）。颜色由页面从
 * `MaterialTheme.colorScheme` 取好后经 Intent 传进来，不新增颜色常量。
 *
 * 触摸/拖动由 Service 里的 OnTouchListener 处理；这里只保留无障碍点击语义。
 */
@Composable
internal fun GatewaySystemOverlayBubble(
    running: Boolean,
    primary: Color,
    surface: Color,
    outline: Color,
    onClick: () -> Unit,
) {
    // 与 app 内那个球同一套规格：同一个圆、停止态无灯、运行态圆内呼吸环 + 内发光。
    var ringAlpha = 0f
    var glowAlpha = 0f
    if (running) {
        val transition = rememberInfiniteTransition(label = "orbOverlayBreath")
        val phase by transition.animateFloat(
            initialValue = 0f,
            targetValue = 1f,
            animationSpec = infiniteRepeatable(
                tween(OrbRingPeriodMs, easing = LinearEasing),
                RepeatMode.Reverse,
            ),
            label = "orbOverlayBreathPhase",
        )
        ringAlpha = orbRingAlpha(running = true, phase = phase)
        glowAlpha = orbGlowAlpha(running = true, phase = phase)
    }
    OrbPlate(
        running = running,
        accent = primary,
        ringAlpha = ringAlpha,
        glowAlpha = glowAlpha,
        container = surface,
        border = if (running) primary.copy(alpha = 0.38f) else outline,
        semantics = Modifier.semantics {
            contentDescription = if (running) "Aether 网关运行中" else "Aether 网关已停止"
            role = Role.Button
            onClick(label = "打开 Aether Cockpit") {
                onClick()
                true
            }
        },
    )
}

internal fun overlayPermissionIntent(context: Context): Intent =
    Intent(
        Settings.ACTION_MANAGE_OVERLAY_PERMISSION,
        Uri.parse("package:${context.packageName}"),
    )
