package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaShape

/* ============================================================================
 * 深层页面共享排版与旗舰级表面原语
 *
 * 保持所有布局节奏常量（16 / 24 / 88）与单测合同 100% 兼容，
 * 同时将底层视觉材质升级为：
 *  - 浅色模式：纯净白实体卡（与微光冷调背板形成通透对比）+ 顶边高光倒角 + 柔和微阴影
 *  - 深色模式：微亮石墨层 + 顶部 13% 受光白边向下渐隐为 3% 环境轮廓线
 * ========================================================================== */

/** 卡片水平内边距：统一 16dp。 */
internal val DeepCardPadding: Dp = 16.dp

/** 卡片垂直内边距：16dp（与水平一致）。 */
internal val DeepCardVerticalPadding: Dp = 16.dp

/** 列表卡片内边距：水平 16、垂直 0。 */
internal val DeepListCardPadding: PaddingValues =
    PaddingValues(horizontal = DeepCardPadding, vertical = 0.dp)

/** 卡片之间的间距：16dp。 */
internal val DeepCardGap: Dp = 16.dp

/** 区块之间的间距：统一 24dp（由「列表 16dp + 标题上内距 8dp」组成）。 */
internal val DeepBlockGap: Dp = 24.dp

/** 列表行的垂直内边距：统一 16dp（行高约 52dp，满足 48dp 触控下限）。 */
internal val DeepRowVertical: Dp = 16.dp

/** 页面主标题到首个内容块的间距：与 [DeepCardGap] 同一个数（16dp）。 */
internal val DeepPageTitleGap: Dp = DeepCardGap

/**
 * 页面级标题：全站唯一实现。
 *
 * 主标题使用 `titleLarge` + Bold，副标题采用低饱和度胶囊徽章或第三级墨色展示在一侧，
 * 让顶栏既有旗舰级仪表盘的精致感，又保持极高的首屏信息密度。
 */
@Composable
internal fun DeepPageTitle(
    title: String,
    modifier: Modifier = Modifier,
    subtitle: String? = null,
    trailing: (@Composable RowScope.() -> Unit)? = null,
) {
    Row(
        modifier = modifier,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onBackground,
        )
        if (!subtitle.isNullOrBlank()) {
            Spacer(Modifier.width(10.dp))
            AetherStatusChip(
                text = subtitle,
                kind = AetherChipKind.NEUTRAL,
                modifier = Modifier.weight(1f, fill = false),
            )
        }
        if (trailing != null) {
            Spacer(Modifier.weight(1f))
            Row(
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically,
                content = trailing,
            )
        }
    }
}

/** 页面可滚动内容底部给悬浮球预留的空间：88dp。 */
internal val BubbleSafeBottom: Dp = 88.dp

/** 深层页面 LazyColumn 的统一 contentPadding。 */
internal fun bubbleSafeContentPadding(
    horizontal: Dp = 20.dp,
    top: Dp = 10.dp,
    bottom: Dp = BubbleSafeBottom,
): PaddingValues = PaddingValues(
    start = horizontal,
    top = top,
    end = horizontal,
    bottom = bottom,
)

/** 卡片色调。 */
internal enum class DeepTone { Neutral, Info, Danger, Warning, Success }

/** 1.3× 档的判定口径：系统字体缩放 ≥ 1.25。 */
@Composable
internal fun isLargeFontScale(threshold: Float = 1.25f): Boolean =
    LocalDensity.current.fontScale >= threshold

/**
 * 区块标题：`titleMedium + Bold`，右侧可选计数徽章或动作。
 */
@Composable
internal fun DeepSectionTitle(
    text: String,
    modifier: Modifier = Modifier,
    trailing: String? = null,
    action: (@Composable () -> Unit)? = null,
) {
    val base = modifier
        .fillMaxWidth()
        .padding(top = DeepBlockGap - DeepCardGap)
    if (isLargeFontScale() && !trailing.isNullOrBlank()) {
        Column(modifier = base, verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    text = text,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.onBackground,
                    modifier = Modifier.weight(1f),
                )
                if (action != null) {
                    Spacer(Modifier.width(8.dp))
                    action()
                }
            }
            Text(
                text = trailing,
                style = MaterialTheme.typography.labelSmall,
                color = AetherColors.textTertiary(),
            )
        }
        return
    }
    Row(
        modifier = base,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onBackground,
            modifier = Modifier.weight(1f),
        )
        if (!trailing.isNullOrBlank()) {
            AetherStatusChip(
                text = trailing,
                kind = AetherChipKind.NEUTRAL,
            )
        }
        if (action != null) {
            Spacer(Modifier.width(8.dp))
            action()
        }
    }
}

/**
 * 旗舰级一层卡片：`McaShape.Sheet` (20dp) + Specular Rim 顶边受光微描边 + 柔和实体填充。
 */
@Composable
internal fun DeepCard(
    modifier: Modifier = Modifier,
    tone: DeepTone = DeepTone.Neutral,
    contentPadding: PaddingValues = PaddingValues(
        horizontal = DeepCardPadding,
        vertical = DeepCardVerticalPadding,
    ),
    content: @Composable ColumnScope.() -> Unit,
) {
    val dark = isAetherDarkTheme()
    val containerColor = when (tone) {
        DeepTone.Neutral -> AetherColors.cardSurface()
        DeepTone.Info -> if (dark) {
            MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.35f)
        } else {
            MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.65f)
        }
        DeepTone.Danger -> AetherColors.dangerContainer()
        DeepTone.Warning -> AetherColors.warningContainer()
        DeepTone.Success -> AetherColors.successContainer()
    }
    Surface(
        modifier = modifier
            .fillMaxWidth()
            .aetherSpecularRim(cornerRadius = 20.dp, tone = tone),
        shape = McaShape.Sheet,
        color = containerColor,
        shadowElevation = if (dark || tone != DeepTone.Neutral) 0.dp else 1.dp,
    ) {
        Column(Modifier.fillMaxWidth().padding(contentPadding), content = content)
    }
}

/** 卡片标题：`titleMedium + Bold`。 */
@Composable
internal fun DeepCardTitle(text: String, tone: DeepTone = DeepTone.Neutral) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.Bold,
        color = when (tone) {
            DeepTone.Neutral -> MaterialTheme.colorScheme.onSurface
            DeepTone.Info -> MaterialTheme.colorScheme.onPrimaryContainer
            DeepTone.Danger -> AetherColors.danger()
            DeepTone.Warning -> AetherColors.warning()
            DeepTone.Success -> AetherColors.success()
        },
    )
}

/** 卡片正文：`bodySmall + onSurfaceVariant`。 */
@Composable
internal fun DeepCardBody(text: String, tone: DeepTone = DeepTone.Neutral) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = when (tone) {
            DeepTone.Neutral -> MaterialTheme.colorScheme.onSurfaceVariant
            DeepTone.Info -> MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.85f)
            DeepTone.Danger -> AetherColors.danger().copy(alpha = 0.90f)
            DeepTone.Warning -> AetherColors.warning().copy(alpha = 0.90f)
            DeepTone.Success -> AetherColors.success().copy(alpha = 0.90f)
        },
    )
}

/**
 * 状态 / 空态卡：一句话说清为什么空 + 最多一个下一步动作。
 */
@Composable
internal fun DeepStateCard(
    title: String,
    detail: String,
    tone: DeepTone = DeepTone.Neutral,
    actionLabel: String? = null,
    onAction: (() -> Unit)? = null,
) {
    DeepCard(tone = tone) {
        DeepCardTitle(title, tone)
        Spacer(Modifier.height(6.dp))
        DeepCardBody(detail, tone)
        if (actionLabel != null && onAction != null) {
            Spacer(Modifier.height(14.dp))
            McaButton(onClick = onAction, style = McaButtonStyle.Primary) { Text(actionLabel) }
        }
    }
}

/** 一次性提示（操作回执 / 错误）：一行文案 + 柔和胶囊关闭键。 */
@Composable
internal fun DeepNotice(text: String, isError: Boolean = false, onDismiss: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        AetherStatusDot(
            color = if (isError) AetherColors.danger() else MaterialTheme.colorScheme.primary,
            pulsing = false,
            dotSize = 8.dp,
        )
        Text(
            text = text,
            style = MaterialTheme.typography.bodySmall,
            color = if (isError) {
                AetherColors.danger()
            } else {
                MaterialTheme.colorScheme.onSurface
            },
            modifier = Modifier.weight(1f),
        )
        AetherPillButton(
            text = "知道了",
            onClick = onDismiss,
        )
    }
}
