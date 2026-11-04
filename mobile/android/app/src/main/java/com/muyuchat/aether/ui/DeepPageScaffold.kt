package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

/* ============================================================================
 * 深层页面共享排版原语：全站唯一定义处
 *
 * 这些原语原先是寄居在 RequestsPage.kt 顶部的（当时写集不允许新建文件），
 * 结果 Requests / Settings / Library / Sessions / Wake / Plugins / More /
 * GatewayProbe / Routing / Clients / DesktopOnly / Diagnose / Drift 十多个页面
 * 都反向依赖"请求页"这个语义上无关的文件。现在单独成文件，谁都不欠谁。
 *
 * 改签名时要一次改完所有调用点；**不要**在页面里就地复制一份。
 *
 * 规格（UI_REFRESH_SPEC §1.2，第二轮密度收敛）：卡片水平内边距 16、垂直 20（上下各 +4，内容不贴边）；
 * 卡片之间 16；区块之间 24（标题上内距 = 24 - 16 = 8）；
 * 区块标题 titleMedium + Bold（**Bold 只允许出现在标题上**，正文级文字不加 Bold）；
 * 列表行垂直 16dp（触控下限）；一个卡片内最多一条分隔线。
 * ========================================================================== */

/** 卡片水平内边距：统一 16dp。 */
internal val DeepCardPadding: Dp = 16.dp

/** 卡片垂直内边距：16 + 上下各 4，内容不贴边。 */
internal val DeepCardVerticalPadding: Dp = 20.dp

/** 列表卡片内边距：水平 16，垂直只留 4（行自己带 16 的垂直内边距，外缘合计 20）。 */
internal val DeepListCardPadding: PaddingValues =
    PaddingValues(horizontal = DeepCardPadding, vertical = 4.dp)

/** 卡片之间的间距：统一 16dp。 */
internal val DeepCardGap: Dp = 16.dp

/** 区块之间的间距：统一 24dp（由「列表 16dp + 标题上内距 8dp」组成）。 */
internal val DeepBlockGap: Dp = 24.dp

/** 列表行的垂直内边距：统一 16dp（行高约 52dp，顺带满足 48dp 触控下限）。 */
internal val DeepRowVertical: Dp = 16.dp

/**
 * 页面主标题到首个内容块的间距：16dp（标题层瘦身后从 24 降为 16）。
 *
 * 页面级 LazyColumn 用 `spacedBy(DeepCardGap)` 时天然就是 16；只有少数手写标题块
 * 需要显式引用这个常量，保持全站一致。
 */
internal val DeepPageTitleGap: Dp = 16.dp

/**
 * 页面级标题：**全站唯一实现**，不要在页面里自己写 `Text(style = headlineXxx)`。
 *
 * 字阶：`titleLarge`（22sp）+ Bold —— 比原来的 headlineSmall/headlineMedium 再降一档，
 * 因为 28sp 标题会把首屏吃掉约 1/3 屏。
 * 副标题：`labelSmall` + onSurfaceVariant，**放在标题同一行右侧**而不是单独一行，
 * 这样每页少一行（约 20dp）；太长会自动省略号，不会把标题挤走。
 *
 * 卡片内标题请用 [DeepCardTitle]，本组件只用于页面级。
 */
@Composable
internal fun DeepPageTitle(
    title: String,
    modifier: Modifier = Modifier,
    subtitle: String? = null,
) {
    Row(modifier = modifier, verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onBackground,
        )
        if (!subtitle.isNullOrBlank()) {
            Spacer(Modifier.width(8.dp))
            Text(
                text = subtitle,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
        }
    }
}

/**
 * 页面可滚动内容底部给悬浮球预留的空间：球 64 + 边距 8 + 余量 16 = 88。
 *
 * 只作为 LazyColumn 的 `contentPadding` 使用；不要给页面根容器加常驻 padding，
 * 否则每页底部会永久出现一条空白带。
 */
internal val BubbleSafeBottom: Dp = 88.dp

/**
 * 深层页面 LazyColumn 的统一 contentPadding：底部滚出悬浮球的安全区。
 *
 * 页面正常滚动时看不到这块空间，只有滚到末尾才多出 88dp，最后一张卡不会被球压住。
 */
internal fun bubbleSafeContentPadding(
    horizontal: Dp = 20.dp,
    top: Dp = 8.dp,
    bottom: Dp = BubbleSafeBottom,
): PaddingValues = PaddingValues(
    start = horizontal,
    top = top,
    end = horizontal,
    bottom = bottom,
)

/** 卡片色调。只在这三档里选，避免每页各发明一种底色。 */
internal enum class DeepTone { Neutral, Info, Danger }

/**
 * 区块标题：`titleMedium + Bold`，右侧可选计数或动作。
 *
 * 上内距 = 24 - 16，配合 LazyColumn 的 16dp 列表间距凑出「区块间 24dp」；
 * 因此页面不要自己再给标题加 Spacer。
 */
@Composable
internal fun DeepSectionTitle(
    text: String,
    modifier: Modifier = Modifier,
    trailing: String? = null,
    action: (@Composable () -> Unit)? = null,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(top = DeepBlockGap - DeepCardGap),
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
            Text(
                text = trailing,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (action != null) {
            Spacer(Modifier.width(8.dp))
            action()
        }
    }
}

/**
 * 一层卡片：`McaShape.Sheet` + `McaHairline` + 水平 16 / 垂直 20 内边距。不嵌套第二层卡片。
 *
 * 列表卡片传 `contentPadding = DeepListCardPadding`（水平 16 + 垂直 4），
 * 由行自己提供 16dp 垂直内边距，这样首行/末行的外缘也正好是 20dp。
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
    Surface(
        modifier = modifier.fillMaxWidth(),
        shape = McaShape.Sheet,
        color = when (tone) {
            DeepTone.Neutral -> McaTonal.fill()
            DeepTone.Info -> MaterialTheme.colorScheme.primaryContainer
            DeepTone.Danger -> MaterialTheme.colorScheme.errorContainer
        },
        border = McaHairline.stroke(),
    ) {
        Column(Modifier.fillMaxWidth().padding(contentPadding), content = content)
    }
}

/** 卡片标题：`titleMedium + Bold`；正文 `bodySmall + onSurfaceVariant`。 */
@Composable
internal fun DeepCardTitle(text: String, tone: DeepTone = DeepTone.Neutral) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.Bold,
        color = when (tone) {
            DeepTone.Neutral -> MaterialTheme.colorScheme.onSurface
            DeepTone.Info -> MaterialTheme.colorScheme.onPrimaryContainer
            DeepTone.Danger -> MaterialTheme.colorScheme.onErrorContainer
        },
    )
}

/** 卡片正文：`bodySmall + onSurfaceVariant`（Info/Danger 卡自动跟随容器前景色）。 */
@Composable
internal fun DeepCardBody(text: String, tone: DeepTone = DeepTone.Neutral) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = when (tone) {
            DeepTone.Neutral -> MaterialTheme.colorScheme.onSurfaceVariant
            DeepTone.Info -> MaterialTheme.colorScheme.onPrimaryContainer.copy(alpha = 0.82f)
            DeepTone.Danger -> MaterialTheme.colorScheme.onErrorContainer.copy(alpha = 0.85f)
        },
    )
}

/**
 * 状态 / 空态卡：**一句话说清为什么空** + 最多一个下一步动作。
 *
 * 全站只有这一个形状：不许写成「暂无数据」，也不许堆两个以上按钮。
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
            Spacer(Modifier.height(12.dp))
            McaButton(onClick = onAction, style = McaButtonStyle.Primary) { Text(actionLabel) }
        }
    }
}

/** 一次性提示（操作回执 / 错误）：一行文案 + 一个「知道了」。 */
@Composable
internal fun DeepNotice(text: String, isError: Boolean = false, onDismiss: () -> Unit) {
    Column(Modifier.fillMaxWidth()) {
        Text(
            text = text,
            style = MaterialTheme.typography.bodySmall,
            color = if (isError) {
                MaterialTheme.colorScheme.error
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
        )
        Spacer(Modifier.height(12.dp))
        McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) { Text("知道了") }
    }
}
