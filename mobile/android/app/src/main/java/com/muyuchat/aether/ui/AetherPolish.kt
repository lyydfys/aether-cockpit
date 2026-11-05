package com.muyuchat.aether.ui

import androidx.annotation.DrawableRes
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.waitForUpOrCancellation
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import kotlin.math.abs
import kotlin.math.max

/* ============================================================================
 * Aether Flagship Micro-Craftsmanship & Material System
 *
 * 核心设计准则：
 * 1. 4 级材质纵深：AetherBackdrop 微光环境背板 -> Specular Rim 顶边高光卡片 ->
 *    无边框同心内嵌底托 (AetherSubSurface) -> 浮动胶囊控制层 (AetherSegmentedPill)。
 * 2. 8 色低饱和身份底盘 (AetherIdentityTone)：告别千篇一律的灰框线图，提供柔和莫兰迪色盘。
 * 3. 低饱和语义状态色 (AetherColors)：告别刺眼运维面板红绿，采用温润宝石绿/琥珀/深绯红。
 * 4. 光学墨迹对齐 (AetherChevron) 与同心胶囊几何 (R_outer = R_inner + Inset)。
 * ========================================================================== */

@Composable
internal fun isAetherDarkTheme(): Boolean =
    MaterialTheme.colorScheme.background.luminance() < 0.5f

/**
 * 8 组低饱和身份色盘，
 * 浅色模式为柔和粉彩底 + 深邃墨色，深色模式为半透明深色底 + 明亮微光色。
 */
internal enum class AetherIdentityTone {
    BLUE,
    PURPLE,
    GREEN,
    AMBER,
    PINK,
    CYAN,
    INDIGO,
    SLATE,
}

@Immutable
internal data class AetherTonePair(
    val container: Color,
    val content: Color,
    val border: Color,
) {
    val foreground: Color get() = content
}

@Composable
internal fun AetherIdentityTone.foreground(): Color = AetherColors.identityPair(this).content

internal object AetherColors {

    /** 第三级字色（元数据、时间戳、辅助计数），保证 WCAG AA 对比度同时拉开主次层级。 */
    @Composable
    fun textTertiary(): Color = if (isAetherDarkTheme()) {
        Color(154, 158, 169)
    } else {
        Color(100, 105, 116)
    }

    /** 低饱和度成功态（在线 / 就绪 / 充足）。 */
    @Composable
    fun success(): Color = if (isAetherDarkTheme()) {
        Color(101, 193, 140)
    } else {
        Color(51, 114, 80)
    }

    @Composable
    fun successContainer(): Color = if (isAetherDarkTheme()) {
        Color(22, 48, 35)
    } else {
        Color(226, 243, 233)
    }

    /** 低饱和度警告态（额度偏低 / 缓慢 / 待注意）。 */
    @Composable
    fun warning(): Color = if (isAetherDarkTheme()) {
        Color(229, 173, 76)
    } else {
        Color(147, 97, 35)
    }

    @Composable
    fun warningContainer(): Color = if (isAetherDarkTheme()) {
        Color(56, 40, 16)
    } else {
        Color(250, 238, 216)
    }

    /** 低饱和度危险态（异常 / 耗尽 / 失败）。 */
    @Composable
    fun danger(): Color = if (isAetherDarkTheme()) {
        Color(236, 123, 118)
    } else {
        Color(165, 71, 67)
    }

    @Composable
    fun dangerContainer(): Color = if (isAetherDarkTheme()) {
        Color(58, 24, 24)
    } else {
        Color(251, 229, 228)
    }

    /** 卡片实体填充色：浅色下使用纯净白与冷调背板拉开层次，深色下使用微亮石墨色。 */
    @Composable
    fun cardSurface(): Color = if (isAetherDarkTheme()) {
        Color(26, 29, 36)
    } else {
        Color(255, 255, 255)
    }

    /** 卡片内嵌子容器底色（替代盒中盒 0.5dp 描边）。 */
    @Composable
    fun subSurface(): Color = if (isAetherDarkTheme()) {
        Color(18, 20, 26)
    } else {
        Color(243, 245, 250)
    }

    /** 浮动药丸 / 轨道槽底色。 */
    @Composable
    fun pillTrack(): Color = if (isAetherDarkTheme()) {
        Color(32, 36, 45)
    } else {
        Color(236, 240, 248)
    }

    /** 浮动药丸内滑块底色。 */
    @Composable
    fun pillThumb(): Color = if (isAetherDarkTheme()) {
        Color(48, 54, 67)
    } else {
        Color(255, 255, 255)
    }

    @Composable
    fun identityPair(tone: AetherIdentityTone): AetherTonePair {
        val dark = isAetherDarkTheme()
        return when (tone) {
            AetherIdentityTone.BLUE -> if (dark) {
                AetherTonePair(Color(23, 43, 77), Color(133, 184, 255), Color(45, 78, 138))
            } else {
                AetherTonePair(Color(222, 234, 253), Color(35, 86, 168), Color(192, 214, 248))
            }
            AetherIdentityTone.PURPLE -> if (dark) {
                AetherTonePair(Color(43, 30, 74), Color(188, 156, 255), Color(74, 52, 124))
            } else {
                AetherTonePair(Color(237, 228, 253), Color(94, 53, 177), Color(216, 199, 248))
            }
            AetherIdentityTone.GREEN -> if (dark) {
                AetherTonePair(Color(20, 49, 37), Color(110, 207, 151), Color(38, 86, 64))
            } else {
                AetherTonePair(Color(221, 243, 229), Color(33, 110, 70), Color(189, 229, 204))
            }
            AetherIdentityTone.AMBER -> if (dark) {
                AetherTonePair(Color(56, 39, 16), Color(242, 186, 82), Color(96, 68, 28))
            } else {
                AetherTonePair(Color(253, 238, 212), Color(148, 92, 20), Color(244, 215, 168))
            }
            AetherIdentityTone.PINK -> if (dark) {
                AetherTonePair(Color(58, 25, 44), Color(244, 146, 192), Color(98, 44, 74))
            } else {
                AetherTonePair(Color(252, 226, 238), Color(166, 46, 104), Color(242, 194, 216))
            }
            AetherIdentityTone.CYAN -> if (dark) {
                AetherTonePair(Color(18, 48, 58), Color(104, 209, 232), Color(34, 84, 102))
            } else {
                AetherTonePair(Color(218, 242, 248), Color(22, 108, 130), Color(182, 226, 238))
            }
            AetherIdentityTone.INDIGO -> if (dark) {
                AetherTonePair(Color(28, 34, 74), Color(148, 162, 255), Color(52, 62, 128))
            } else {
                AetherTonePair(Color(226, 231, 253), Color(54, 68, 168), Color(196, 206, 246))
            }
            AetherIdentityTone.SLATE -> if (dark) {
                AetherTonePair(Color(36, 41, 51), Color(176, 184, 198), Color(62, 70, 86))
            } else {
                AetherTonePair(Color(232, 236, 242), Color(71, 80, 96), Color(206, 213, 224))
            }
        }
    }

    fun toneForKey(key: String?): AetherIdentityTone {
        if (key.isNullOrBlank()) return AetherIdentityTone.SLATE
        val tones = AetherIdentityTone.entries
        val idx = abs(key.trim().lowercase().hashCode()) % tones.size
        return tones[idx]
    }
}

/**
 * 全局环境微光背板：
 * 消除纯平死白/死黑底色，用极克制的垂直冷暖渐变 + 两处大半径环境光晕，
 * 为上层卡片提供真实的材质衬托。
 */
@Composable
internal fun AetherBackdrop(
    modifier: Modifier = Modifier,
    content: @Composable BoxScope.() -> Unit,
) {
    val dark = isAetherDarkTheme()
    val topBg = if (dark) Color(13, 15, 20) else Color(239, 243, 251)
    val bottomBg = if (dark) Color(18, 21, 28) else Color(246, 248, 252)
    val glowTop = if (dark) {
        Color(56, 114, 224).copy(alpha = 0.08f)
    } else {
        Color(51, 102, 204).copy(alpha = 0.055f)
    }
    val glowBottom = if (dark) {
        Color(112, 84, 214).copy(alpha = 0.05f)
    } else {
        Color(86, 132, 226).copy(alpha = 0.04f)
    }

    Box(
        modifier = modifier
            .fillMaxSize()
            .drawWithCache {
                val verticalBrush = Brush.verticalGradient(listOf(topBg, bottomBg))
                val r1 = max(size.width, size.height) * 0.65f
                val r2 = max(size.width, size.height) * 0.58f
                val radialTop = Brush.radialGradient(
                    colors = listOf(glowTop, Color.Transparent),
                    center = Offset(size.width * 0.15f, size.height * 0.08f),
                    radius = r1.coerceAtLeast(1f),
                )
                val radialBottom = Brush.radialGradient(
                    colors = listOf(glowBottom, Color.Transparent),
                    center = Offset(size.width * 0.88f, size.height * 0.86f),
                    radius = r2.coerceAtLeast(1f),
                )
                onDrawBehind {
                    drawRect(brush = verticalBrush)
                    drawRect(brush = radialTop)
                    drawRect(brush = radialBottom)
                }
            },
        content = content,
    )
}

/**
 * 卡片顶边高光微描边：
 * 顶部受光边缘更亮，向下平滑衰减为细腻环境轮廓线，赋予卡片真实的玻璃/金属倒角厚度感。
 */
@Composable
internal fun Modifier.aetherSpecularRim(
    cornerRadius: Dp = 20.dp,
    tone: DeepTone = DeepTone.Neutral,
): Modifier {
    val dark = isAetherDarkTheme()
    val primary = MaterialTheme.colorScheme.primary
    val error = MaterialTheme.colorScheme.error
    val warning = AetherColors.warning()
    val success = AetherColors.success()
    return this.drawWithCache {
        val radiusPx = cornerRadius.toPx()
        val strokePx = 0.85.dp.toPx()
        val topColor = when (tone) {
            DeepTone.Neutral -> if (dark) {
                Color.White.copy(alpha = 0.13f)
            } else {
                Color.White.copy(alpha = 0.92f)
            }
            DeepTone.Info -> primary.copy(alpha = if (dark) 0.38f else 0.26f)
            DeepTone.Danger -> error.copy(alpha = if (dark) 0.42f else 0.30f)
            DeepTone.Warning -> warning.copy(alpha = if (dark) 0.42f else 0.30f)
            DeepTone.Success -> success.copy(alpha = if (dark) 0.42f else 0.30f)
        }
        val midColor = when (tone) {
            DeepTone.Neutral -> if (dark) {
                Color.White.copy(alpha = 0.05f)
            } else {
                Color(205, 214, 228).copy(alpha = 0.65f)
            }
            DeepTone.Info -> primary.copy(alpha = 0.14f)
            DeepTone.Danger -> error.copy(alpha = 0.16f)
            DeepTone.Warning -> warning.copy(alpha = 0.16f)
            DeepTone.Success -> success.copy(alpha = 0.16f)
        }
        val bottomColor = when (tone) {
            DeepTone.Neutral -> if (dark) {
                Color.White.copy(alpha = 0.03f)
            } else {
                Color(192, 202, 218).copy(alpha = 0.50f)
            }
            DeepTone.Info -> primary.copy(alpha = 0.10f)
            DeepTone.Danger -> error.copy(alpha = 0.12f)
            DeepTone.Warning -> warning.copy(alpha = 0.12f)
            DeepTone.Success -> success.copy(alpha = 0.12f)
        }
        val rimBrush = Brush.verticalGradient(
            0.0f to topColor,
            0.38f to midColor,
            1.0f to bottomColor,
        )
        onDrawWithContent {
            drawContent()
            drawRoundRect(
                brush = rimBrush,
                topLeft = Offset(strokePx * 0.5f, strokePx * 0.5f),
                size = size.copy(
                    width = (size.width - strokePx).coerceAtLeast(0f),
                    height = (size.height - strokePx).coerceAtLeast(0f),
                ),
                cornerRadius = CornerRadius(radiusPx, radiusPx),
                style = Stroke(width = strokePx),
            )
        }
    }
}

@Composable
internal fun Modifier.aetherSpecularRim(
    shape: Shape,
    tone: DeepTone = DeepTone.Neutral,
): Modifier = aetherSpecularRim(cornerRadius = 20.dp, tone = tone)

/**
 * 物理弹簧按压修饰符：
 * 带有柔和的 0.975x 物理阻尼回弹与微触觉反馈。
 * 支持直接传入 `onClick` 或仅作为视觉按压层配合下游 `.clickable(...)`。
 */
@Composable
internal fun Modifier.aetherPressable(
    enabled: Boolean = true,
    scaleDown: Float = 0.975f,
    haptic: Boolean = true,
    onClick: (() -> Unit)? = null,
): Modifier {
    val haptics = LocalHapticFeedback.current
    if (onClick != null) {
        val interactionSource = remember { MutableInteractionSource() }
        val pressed by interactionSource.collectIsPressedAsState()
        val scale by animateFloatAsState(
            targetValue = if (pressed && enabled) scaleDown else 1f,
            animationSpec = spring(
                dampingRatio = 0.78f,
                stiffness = Spring.StiffnessMediumLow,
            ),
            label = "aetherPressScale",
        )
        return this
            .graphicsLayer {
                scaleX = scale
                scaleY = scale
            }
            .clickable(
                interactionSource = interactionSource,
                indication = null,
                enabled = enabled,
                onClick = {
                    if (haptic) {
                        haptics.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                    }
                    onClick()
                },
            )
    }

    var pressed by remember { mutableStateOf(false) }
    val scale by animateFloatAsState(
        targetValue = if (pressed && enabled) scaleDown else 1f,
        animationSpec = spring(
            dampingRatio = 0.78f,
            stiffness = Spring.StiffnessMediumLow,
        ),
        label = "aetherPressScalePassive",
    )
    return this
        .graphicsLayer {
            scaleX = scale
            scaleY = scale
        }
        .pointerInput(enabled) {
            if (!enabled) return@pointerInput
            awaitEachGesture {
                awaitFirstDown(requireUnconsumed = false)
                pressed = true
                waitForUpOrCancellation()
                pressed = false
            }
        }
}

/**
 * 卡片内嵌同心子容器（圆角 12dp 配合外层卡片 20dp + 16dp 内边距）：
 * 消除原本卡片内部再画一层 0.5dp 灰线的“线框套娃”。
 */
@Composable
internal fun AetherSubSurface(
    modifier: Modifier = Modifier,
    shape: Shape = RoundedCornerShape(12.dp),
    color: Color = AetherColors.subSurface(),
    containerColor: Color = color,
    padding: PaddingValues = PaddingValues(12.dp),
    contentPadding: PaddingValues = padding,
    content: @Composable BoxScope.() -> Unit,
) {
    Box(
        modifier = modifier
            .clip(shape)
            .background(containerColor, shape)
            .padding(contentPadding),
        content = content,
    )
}

/**
 * 光学墨迹右对齐指示箭头：
 * 24x24 图标的右箭头墨迹右缘在 15.3/24 处，右侧留白约 2.5dp；
 * 通过 graphicsLayer 向右补偿 +2.5dp，使箭头真实墨迹与上下行的文字/开关/按钮严格落在同一条垂直线上。
 */
@Composable
internal fun AetherChevron(
    modifier: Modifier = Modifier,
    tint: Color = AetherColors.textTertiary(),
    size: Dp = 18.dp,
) {
    Icon(
        painter = painterResource(R.drawable.ic_chevron_right),
        contentDescription = null,
        tint = tint,
        modifier = modifier
            .size(size)
            .graphicsLayer {
                translationX = 2.5.dp.toPx()
            },
    )
}

/**
 * 32dp 圆形/超椭圆柔和身份图标底盘：
 * 采用 8 组低饱和莫兰迪色系 + 18dp 居中图标 + 极淡同色系微光边。
 */
@Composable
internal fun AetherIdentityDisc(
    @DrawableRes iconRes: Int,
    tone: AetherIdentityTone,
    modifier: Modifier = Modifier,
    size: Dp = 34.dp,
    glyphSize: Dp = 18.dp,
    shape: Shape = RoundedCornerShape(10.dp),
) {
    val pair = AetherColors.identityPair(tone)
    Box(
        modifier = modifier
            .size(size)
            .clip(shape)
            .background(pair.container, shape)
            .drawWithCache {
                val strokePx = 0.6.dp.toPx()
                val r = 10.dp.toPx()
                onDrawWithContent {
                    drawContent()
                    drawRoundRect(
                        color = pair.border.copy(alpha = 0.55f),
                        topLeft = Offset(strokePx * 0.5f, strokePx * 0.5f),
                        size = this.size.copy(
                            width = (this.size.width - strokePx).coerceAtLeast(0f),
                            height = (this.size.height - strokePx).coerceAtLeast(0f),
                        ),
                        cornerRadius = CornerRadius(r, r),
                        style = Stroke(width = strokePx),
                    )
                }
            },
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            painter = painterResource(iconRes),
            contentDescription = null,
            tint = pair.content,
            modifier = Modifier.size(glyphSize),
        )
    }
}

/**
 * 首字母几何徽章底盘：
 * 当渠道为 antigravity / azure / workbuddy 或自定义渠道时，不再千篇一律画一把细钥匙，
 * 而是生成专属的 8 色柔和底盘 + 居中几何首字母。
 */
@Composable
internal fun AetherMonogramPlate(
    label: String = "",
    text: String = label,
    toneKey: String = text.ifBlank { label },
    modifier: Modifier = Modifier,
    size: Dp = 34.dp,
    shape: Shape = RoundedCornerShape(10.dp),
) {
    val resolved = text.ifBlank { label }
    val pair = AetherColors.identityPair(AetherColors.toneForKey(toneKey.ifBlank { resolved }))
    val initial = remember(resolved) {
        val cleaned = resolved.trim()
        if (cleaned.isEmpty()) "A" else cleaned.first().uppercaseChar().toString()
    }
    Box(
        modifier = modifier
            .size(size)
            .clip(shape)
            .background(pair.container, shape),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = initial,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Bold,
            color = pair.content,
            maxLines = 1,
        )
    }
}

/**
 * 呼吸状态点：
 * 运行/在线态带有柔和的呼吸外环光晕，离线/停用态为克制实心圆点。
 */
@Composable
internal fun AetherStatusDot(
    color: Color,
    modifier: Modifier = Modifier,
    pulsing: Boolean = false,
    pulse: Boolean = pulsing,
    dotSize: Dp = 8.dp,
    size: Dp = dotSize,
) {
    val isPulsing = pulse || pulsing
    val resolvedSize = size
    if (!isPulsing) {
        Box(
            modifier = modifier
                .size(resolvedSize)
                .clip(CircleShape)
                .background(color, CircleShape),
        )
        return
    }
    val transition = rememberInfiniteTransition(label = "aetherStatusDot")
    val haloScale by transition.animateFloat(
        initialValue = 1.0f,
        targetValue = 1.85f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 1600, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "haloScale",
    )
    val haloAlpha by transition.animateFloat(
        initialValue = 0.32f,
        targetValue = 0.06f,
        animationSpec = infiniteRepeatable(
            animation = tween(durationMillis = 1600, easing = FastOutSlowInEasing),
            repeatMode = RepeatMode.Reverse,
        ),
        label = "haloAlpha",
    )
    Box(
        modifier = modifier.size(resolvedSize * 2f),
        contentAlignment = Alignment.Center,
    ) {
        Canvas(modifier = Modifier.fillMaxSize()) {
            val baseRadius = (resolvedSize.toPx() * 0.5f)
            drawCircle(
                color = color.copy(alpha = haloAlpha),
                radius = baseRadius * haloScale,
            )
            drawCircle(
                color = color,
                radius = baseRadius,
            )
        }
    }
}

/**
 * 低饱和度胶囊状态徽章：
 * 取代原本刺眼的纯色块或带 0.5dp 灰线的生硬方框。
 */
internal enum class AetherChipKind { NEUTRAL, ACCENT, SUCCESS, WARNING, DANGER }

@Composable
internal fun AetherStatusChip(
    text: String,
    modifier: Modifier = Modifier,
    kind: AetherChipKind = AetherChipKind.NEUTRAL,
    tone: DeepTone? = null,
    containerColor: Color? = null,
    contentColor: Color? = null,
    showDot: Boolean = false,
    pulsingDot: Boolean = false,
) {
    val effectiveKind = when (tone) {
        DeepTone.Info -> AetherChipKind.ACCENT
        DeepTone.Danger -> AetherChipKind.DANGER
        DeepTone.Warning -> AetherChipKind.WARNING
        DeepTone.Success -> AetherChipKind.SUCCESS
        DeepTone.Neutral -> AetherChipKind.NEUTRAL
        null -> kind
    }
    val resolvedContainer = containerColor ?: when (effectiveKind) {
        AetherChipKind.NEUTRAL -> AetherColors.pillTrack()
        AetherChipKind.ACCENT -> MaterialTheme.colorScheme.primaryContainer.copy(alpha = if (isAetherDarkTheme()) 0.45f else 0.75f)
        AetherChipKind.SUCCESS -> AetherColors.successContainer()
        AetherChipKind.WARNING -> AetherColors.warningContainer()
        AetherChipKind.DANGER -> AetherColors.dangerContainer()
    }
    val resolvedContent = contentColor ?: when (effectiveKind) {
        AetherChipKind.NEUTRAL -> MaterialTheme.colorScheme.onSurfaceVariant
        AetherChipKind.ACCENT -> MaterialTheme.colorScheme.primary
        AetherChipKind.SUCCESS -> AetherColors.success()
        AetherChipKind.WARNING -> AetherColors.warning()
        AetherChipKind.DANGER -> AetherColors.danger()
    }
    Row(
        modifier = modifier
            .clip(CircleShape)
            .background(resolvedContainer, CircleShape)
            .padding(horizontal = 9.dp, vertical = 3.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(5.dp),
    ) {
        if (showDot) {
            AetherStatusDot(
                color = resolvedContent,
                pulsing = pulsingDot,
                dotSize = 6.dp,
            )
        }
        Text(
            text = text,
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.Medium,
            color = resolvedContent,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * 同心胶囊滑动分段控件：
 * 外层跑道形胶囊轨道 + 内缩 3dp 的物理弹簧滑块 + 顶边微光 + 触觉反馈。
 * 彻底取代原本满屏散装的 `Primary + Outlined` 蓝色线框按钮群。
 */
@Composable
internal fun <T> AetherSegmentedPill(
    items: List<T>,
    selected: T,
    onSelect: (T) -> Unit,
    labelMapper: (T) -> String,
    modifier: Modifier = Modifier,
    height: Dp = 36.dp,
) {
    if (items.isEmpty()) return
    val selectedIndex = items.indexOf(selected).coerceAtLeast(0)
    val inset = 3.dp
    val trackColor = AetherColors.pillTrack()
    val thumbColor = AetherColors.pillThumb()
    val dark = isAetherDarkTheme()
    val haptics = LocalHapticFeedback.current

    BoxWithConstraints(
        modifier = modifier
            .height(height)
            .clip(CircleShape)
            .background(trackColor, CircleShape)
            .padding(inset),
    ) {
        val segmentCount = items.size
        val segmentWidth = maxWidth / segmentCount
        val thumbOffset by animateDpAsState(
            targetValue = segmentWidth * selectedIndex,
            animationSpec = spring(
                dampingRatio = 0.82f,
                stiffness = 480f,
            ),
            label = "segmentedPillThumb",
        )

        // 滑动的同心胶囊底托（带轻微高光描边）
        Box(
            modifier = Modifier
                .offset(x = thumbOffset)
                .width(segmentWidth)
                .fillMaxHeight()
                .clip(CircleShape)
                .background(thumbColor, CircleShape)
                .drawWithCache {
                    val strokePx = 0.6.dp.toPx()
                    val r = size.height * 0.5f
                    val rimColor = if (dark) {
                        Color.White.copy(alpha = 0.14f)
                    } else {
                        Color(195, 205, 222).copy(alpha = 0.55f)
                    }
                    onDrawBehind {
                        drawRoundRect(
                            color = rimColor,
                            topLeft = Offset(strokePx * 0.5f, strokePx * 0.5f),
                            size = size.copy(
                                width = (size.width - strokePx).coerceAtLeast(0f),
                                height = (size.height - strokePx).coerceAtLeast(0f),
                            ),
                            cornerRadius = CornerRadius(r, r),
                            style = Stroke(width = strokePx),
                        )
                    }
                },
        )

        Row(modifier = Modifier.fillMaxSize()) {
            items.forEachIndexed { index, item ->
                val isSelected = index == selectedIndex
                val textColor by animateColorAsState(
                    targetValue = if (isSelected) {
                        MaterialTheme.colorScheme.onSurface
                    } else {
                        MaterialTheme.colorScheme.onSurfaceVariant
                    },
                    animationSpec = tween(durationMillis = 150),
                    label = "segmentedPillText",
                )
                val label = labelMapper(item)
                Box(
                    modifier = Modifier
                        .weight(1f)
                        .fillMaxHeight()
                        .clip(CircleShape)
                        .clickable(
                            interactionSource = remember { MutableInteractionSource() },
                            indication = null,
                        ) {
                            if (!isSelected) {
                                haptics.performHapticFeedback(HapticFeedbackType.TextHandleMove)
                                onSelect(item)
                            }
                        }
                        .semantics {
                            role = Role.Tab
                            this.selected = isSelected
                            contentDescription = label
                        },
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = label,
                        style = MaterialTheme.typography.labelMedium,
                        fontWeight = if (isSelected) FontWeight.Bold else FontWeight.Medium,
                        color = textColor,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.padding(horizontal = 6.dp),
                    )
                }
            }
        }
    }
}

@Composable
internal fun <T> AetherSegmentedPill(
    options: List<T>,
    selected: T,
    onSelect: (T) -> Unit,
    modifier: Modifier = Modifier,
    height: Dp = 36.dp,
    label: (T) -> String,
) {
    AetherSegmentedPill(
        items = options,
        selected = selected,
        onSelect = onSelect,
        labelMapper = label,
        modifier = modifier,
        height = height,
    )
}

@Composable
@JvmName("AetherSegmentedPillPairs")
internal fun <T> AetherSegmentedPill(
    options: List<Pair<T, String>>,
    selected: T,
    onSelect: (T) -> Unit,
    modifier: Modifier = Modifier,
    height: Dp = 36.dp,
) {
    val selectedPair = options.firstOrNull { it.first == selected } ?: options.firstOrNull() ?: return
    AetherSegmentedPill(
        items = options,
        selected = selectedPair,
        onSelect = { onSelect(it.first) },
        labelMapper = { it.second },
        modifier = modifier,
        height = height,
    )
}

/**
 * 顶栏/操作区圆形微光按键：
 * 修复原本 `McaIconButton` 20dp 容器强行钳制 22dp 图标及外部硬编码 `tint` 导致禁用态不置灰的全局 Bug。
 * 视觉上呈现为 36dp 圆润胶囊底托 + 20dp 光学居中图标，具备完整的 48dp 无障碍触控热区。
 */
@Composable
internal fun AetherHeaderIconButton(
    @DrawableRes iconRes: Int,
    contentDescription: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    active: Boolean = false,
    tint: Color? = null,
) {
    val dark = isAetherDarkTheme()
    val containerColor = when {
        !enabled -> AetherColors.pillTrack().copy(alpha = 0.45f)
        active -> MaterialTheme.colorScheme.primaryContainer
        else -> AetherColors.pillTrack()
    }
    val contentColor by animateColorAsState(
        targetValue = when {
            !enabled -> (tint ?: MaterialTheme.colorScheme.onSurfaceVariant).copy(alpha = 0.36f)
            tint != null -> tint
            active -> MaterialTheme.colorScheme.primary
            else -> MaterialTheme.colorScheme.onSurfaceVariant
        },
        animationSpec = tween(durationMillis = 140),
        label = "headerIconTint",
    )

    Box(
        modifier = modifier
            .size(40.dp)
            .aetherPressable(enabled = enabled, scaleDown = 0.92f, onClick = onClick)
            .semantics {
                role = Role.Button
                this.contentDescription = contentDescription
            },
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(36.dp)
                .clip(CircleShape)
                .background(containerColor, CircleShape)
                .drawWithCache {
                    val strokePx = 0.6.dp.toPx()
                    val rimColor = if (dark) {
                        Color.White.copy(alpha = if (enabled) 0.10f else 0.04f)
                    } else {
                        Color(200, 210, 226).copy(alpha = if (enabled) 0.55f else 0.25f)
                    }
                    onDrawBehind {
                        drawCircle(
                            color = rimColor,
                            radius = (size.minDimension - strokePx) * 0.5f,
                            style = Stroke(width = strokePx),
                        )
                    }
                },
            contentAlignment = Alignment.Center,
        ) {
            CompositionLocalProvider(LocalContentColor provides contentColor) {
                Icon(
                    painter = painterResource(iconRes),
                    contentDescription = null,
                    tint = LocalContentColor.current,
                    modifier = Modifier.size(19.dp),
                )
            }
        }
    }
}

/**
 * 支持 ImageVector（如 `McaIcons.*`）的顶栏圆形微光按键。
 */
@Composable
internal fun AetherHeaderVectorButton(
    imageVector: ImageVector,
    contentDescription: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    active: Boolean = false,
) {
    val dark = isAetherDarkTheme()
    val containerColor = when {
        !enabled -> AetherColors.pillTrack().copy(alpha = 0.45f)
        active -> MaterialTheme.colorScheme.primaryContainer
        else -> AetherColors.pillTrack()
    }
    val contentColor by animateColorAsState(
        targetValue = when {
            !enabled -> MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.36f)
            active -> MaterialTheme.colorScheme.primary
            else -> MaterialTheme.colorScheme.onSurfaceVariant
        },
        animationSpec = tween(durationMillis = 140),
        label = "headerVectorTint",
    )

    Box(
        modifier = modifier
            .size(40.dp)
            .aetherPressable(enabled = enabled, scaleDown = 0.92f, onClick = onClick)
            .semantics {
                role = Role.Button
                this.contentDescription = contentDescription
            },
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(36.dp)
                .clip(CircleShape)
                .background(containerColor, CircleShape)
                .drawWithCache {
                    val strokePx = 0.6.dp.toPx()
                    val rimColor = if (dark) {
                        Color.White.copy(alpha = if (enabled) 0.10f else 0.04f)
                    } else {
                        Color(200, 210, 226).copy(alpha = if (enabled) 0.55f else 0.25f)
                    }
                    onDrawBehind {
                        drawCircle(
                            color = rimColor,
                            radius = (size.minDimension - strokePx) * 0.5f,
                            style = Stroke(width = strokePx),
                        )
                    }
                },
            contentAlignment = Alignment.Center,
        ) {
            CompositionLocalProvider(LocalContentColor provides contentColor) {
                Icon(
                    imageVector = imageVector,
                    contentDescription = null,
                    tint = LocalContentColor.current,
                    modifier = Modifier.size(19.dp),
                )
            }
        }
    }
}

/**
 * 紧凑型行内图标按键（28dp 圆形软底托 + 15dp 图标）：
 * 用于搜索框清除按钮、探针折叠箭头、行内复制按键，彻底解决原本塞入 48dp `McaIconButton` 撑大行高的问题。
 */
@Composable
internal fun AetherCompactIconButton(
    @DrawableRes iconRes: Int,
    contentDescription: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    discSize: Dp = 26.dp,
    glyphSize: Dp = 14.dp,
) {
    val contentColor by animateColorAsState(
        targetValue = if (enabled) {
            MaterialTheme.colorScheme.onSurfaceVariant
        } else {
            MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.36f)
        },
        animationSpec = tween(durationMillis = 120),
        label = "compactIconTint",
    )
    Box(
        modifier = modifier
            .size(32.dp)
            .aetherPressable(enabled = enabled, scaleDown = 0.90f, onClick = onClick)
            .semantics {
                role = Role.Button
                this.contentDescription = contentDescription
            },
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(discSize)
                .clip(CircleShape)
                .background(AetherColors.pillTrack(), CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                painter = painterResource(iconRes),
                contentDescription = null,
                tint = contentColor,
                modifier = Modifier.size(glyphSize),
            )
        }
    }
}

/**
 * 柔和胶囊次要动作按钮（Tonal Action Pill）：
 * 取代原本卡片内部泛滥的 `McaButtonStyle.Outlined` 蓝字细框按钮，
 * 采用无描边低饱和底托 + 中性/主题墨色 + 弹簧按压，视觉清爽克制。
 */
@Composable
internal fun AetherPillButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    accent: Boolean = false,
    primary: Boolean = false,
    selected: Boolean = false,
    @DrawableRes leadingIconRes: Int? = null,
    @DrawableRes iconRes: Int? = null,
) {
    val isAccent = accent || primary || selected
    val resolvedIcon = leadingIconRes ?: iconRes
    val container = when {
        !enabled -> AetherColors.pillTrack().copy(alpha = 0.5f)
        isAccent -> MaterialTheme.colorScheme.primaryContainer
        else -> AetherColors.pillTrack()
    }
    val content = when {
        !enabled -> MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.38f)
        isAccent -> MaterialTheme.colorScheme.primary
        else -> MaterialTheme.colorScheme.onSurface
    }
    Row(
        modifier = modifier
            .height(34.dp)
            .clip(CircleShape)
            .background(container, CircleShape)
            .aetherPressable(enabled = enabled, scaleDown = 0.95f, onClick = onClick)
            .padding(horizontal = 14.dp)
            .semantics {
                role = Role.Button
                contentDescription = text
            },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.Center,
    ) {
        if (resolvedIcon != null) {
            Icon(
                painter = painterResource(resolvedIcon),
                contentDescription = null,
                tint = content,
                modifier = Modifier.size(15.dp),
            )
            Spacer(Modifier.width(6.dp))
        }
        Text(
            text = text,
            style = MaterialTheme.typography.labelMedium,
            fontWeight = FontWeight.SemiBold,
            color = content,
            maxLines = 1,
        )
    }
}
