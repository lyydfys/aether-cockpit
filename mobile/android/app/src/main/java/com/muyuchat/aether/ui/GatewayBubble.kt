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
import androidx.compose.ui.draw.scale
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
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
import com.muyuchat.core.designsystem.McaIconPlate
import com.muyuchat.core.designsystem.McaShape
import kotlin.math.hypot
import kotlin.math.roundToInt

/** 悬浮球直径；页面球和系统 overlay 共用。 */
internal const val BubbleMarkDp = 64f

/** 页面列表底部预留区：球高 + 默认边距，默认位置不会压住最后一张卡。 */
internal val BubbleReservedBottom: Dp = 80.dp

/** 页面球拖动时的活动区（root 坐标），由内层 BoxWithConstraints 上报。 */
private data class BubbleMetrics(
    val minX: Int,
    val minY: Int,
    val travelX: Int,
    val travelY: Int,
)

/** 未运行时永远没有光晕；运行中才使用动画值。 */
internal fun gatewayHaloAlpha(running: Boolean, animatedAlpha: Float): Float =
    if (running) animatedAlpha.coerceIn(0f, 1f) else 0f

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

/** 运行中才组合呼吸动画；停止态是单独的静态灰，不创建 InfiniteTransition。 */
@Composable
private fun GatewayOrb(running: Boolean) {
    if (running) {
        val accent = MaterialTheme.colorScheme.primary
        val transition = rememberInfiniteTransition(label = "gatewayBreath")
        val haloAlpha by transition.animateFloat(
            initialValue = 0.18f,
            targetValue = 0.55f,
            animationSpec = infiniteRepeatable(tween(1400), RepeatMode.Reverse),
            label = "gatewayBreathAlpha",
        )
        val haloScale by transition.animateFloat(
            initialValue = 0.92f,
            targetValue = 1.08f,
            animationSpec = infiniteRepeatable(tween(1400), RepeatMode.Reverse),
            label = "gatewayBreathScale",
        )
        OrbPlate(
            running = true,
            accent = accent,
            haloAlpha = haloAlpha,
            haloScale = haloScale,
        )
    } else {
        OrbPlate(
            running = false,
            accent = MaterialTheme.colorScheme.onSurfaceVariant,
            haloAlpha = 0f,
            haloScale = 1f,
        )
    }
}

@Composable
private fun OrbPlate(
    running: Boolean,
    accent: Color,
    haloAlpha: Float,
    haloScale: Float,
) {
    val container = if (running) {
        MaterialTheme.colorScheme.primaryContainer
    } else {
        MaterialTheme.colorScheme.surfaceVariant
    }
    val border = if (running) McaHairline.selected(accent) else McaHairline.default()
    Box(modifier = Modifier.size(BubbleMarkDp.dp), contentAlignment = Alignment.Center) {
        if (running) {
            Box(
                Modifier
                    .size(BubbleMarkDp.dp)
                    .scale(haloScale)
                    .background(accent.copy(alpha = gatewayHaloAlpha(running, haloAlpha)), CircleShape),
            )
        }
        McaIconPlate(
            size = 48.dp,
            shape = CircleShape,
            selected = running,
            accent = accent,
            containerColor = container,
            borderColor = border,
        ) {
            Image(
                painter = painterResource(R.drawable.ic_brand_avatar),
                contentDescription = null,
                colorFilter = if (running) {
                    null
                } else {
                    ColorFilter.tint(MaterialTheme.colorScheme.onSurfaceVariant)
                },
                modifier = Modifier.size(28.dp),
            )
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
    if (running) {
        val transition = rememberInfiniteTransition(label = "gatewayOverlayBreath")
        val haloAlpha by transition.animateFloat(
            initialValue = 0.18f,
            targetValue = 0.55f,
            animationSpec = infiniteRepeatable(tween(1400), RepeatMode.Reverse),
            label = "gatewayOverlayBreathAlpha",
        )
        val haloScale by transition.animateFloat(
            initialValue = 0.92f,
            targetValue = 1.08f,
            animationSpec = infiniteRepeatable(tween(1400), RepeatMode.Reverse),
            label = "gatewayOverlayBreathScale",
        )
        SystemOrb(
            running = true,
            primary = primary,
            surface = surface,
            outline = outline,
            haloAlpha = haloAlpha,
            haloScale = haloScale,
            onClick = onClick,
        )
    } else {
        SystemOrb(
            running = false,
            primary = primary,
            surface = surface,
            outline = outline,
            haloAlpha = 0f,
            haloScale = 1f,
            onClick = onClick,
        )
    }
}

@Composable
private fun SystemOrb(
    running: Boolean,
    primary: Color,
    surface: Color,
    outline: Color,
    haloAlpha: Float,
    haloScale: Float,
    onClick: () -> Unit,
) {
    Box(
        modifier = Modifier
            .size(BubbleMarkDp.dp)
            .semantics {
                contentDescription = if (running) "Aether 网关运行中" else "Aether 网关已停止"
                role = Role.Button
                onClick(label = "打开 Aether Cockpit") {
                    onClick()
                    true
                }
            },
        contentAlignment = Alignment.Center,
    ) {
        if (running) {
            Box(
                Modifier
                    .size(BubbleMarkDp.dp)
                    .scale(haloScale)
                    .background(primary.copy(alpha = gatewayHaloAlpha(running, haloAlpha)), CircleShape),
            )
        }
        Box(
            modifier = Modifier
                .size(48.dp)
                .clip(CircleShape)
                .background(surface)
                .border(
                    McaHairline.Width,
                    if (running) primary.copy(alpha = 0.38f) else outline,
                    CircleShape,
                ),
            contentAlignment = Alignment.Center,
        ) {
            Image(
                painter = painterResource(R.drawable.ic_brand_avatar),
                contentDescription = null,
                colorFilter = if (running) null else ColorFilter.tint(outline),
                modifier = Modifier.size(28.dp),
            )
        }
    }
}

internal fun overlayPermissionIntent(context: Context): Intent =
    Intent(
        Settings.ACTION_MANAGE_OVERLAY_PERMISSION,
        Uri.parse("package:${context.packageName}"),
    )
