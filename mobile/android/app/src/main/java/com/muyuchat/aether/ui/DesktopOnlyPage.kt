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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.UnsupportedCapability
import com.muyuchat.aether.runtime.byDesignNotSupported
import com.muyuchat.aether.runtime.desktopOnlyCapabilities
import com.muyuchat.core.designsystem.McaIconButton
import com.muyuchat.core.designsystem.McaIconSize

/** 区块标题与卡片之间的视觉间距（标题上内距 12 + 这个 8 + 行内 16 ≈ 标题到首行 24dp）。 */
private val TitleToRows = 8.dp

/**
 * 「仅 Windows」说明页。
 *
 * 这一页**只解释**，不放按钮：把这些能力写成"即将支持"或给个灰按钮，都是在制造假希望。
 * 每条都要回答"为什么手机上做不到"（权限 / 物理限制 / 安全边界）。
 *
 * 排版（UI_REFRESH_SPEC §1）：说明卡 + 两个区块（需要桌面端 11 项 / 按设计不做 1 项），
 * 每个区块是**一张** DeepCard，条目是卡里的行（16dp 垂直节奏），不再一条能力套一张卡。
 * 行的三处位置固定：标题左缘、正文左缘都是卡片内容左缘；「仅 Windows / 按设计不做」尾标
 * 贴卡片内容右缘。正文不在标题那一行里（标题行只有「标题 weight(1f) + 尾标」），
 * 所以 360dp 窄屏与 1.3× 字号下正文折行也不会压到尾标——结构上就压不到。
 */
@Composable
internal fun DesktopOnlyPage(
    status: GatewayStatus,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val desktopOnly = desktopOnlyCapabilities()
    val byDesign = byDesignNotSupported()

    LazyColumn(
        modifier = modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(DeepCardGap),
    ) {
        item {
            Column(verticalArrangement = Arrangement.spacedBy(DeepCardGap)) {
                MobileHeader(status)
                PageHeadline(onBack = onBack)
            }
        }
        item {
            DeepCard {
                DeepCardTitle("为什么会有这一页")
                Spacer(Modifier.height(4.dp))
                DeepCardBody(desktopOnlyIntroLine(desktopOnly.size))
            }
        }
        item {
            Column {
                DeepSectionTitle(desktopOnlySectionTitle(desktopOnly.size))
                Spacer(Modifier.height(TitleToRows))
                DeepCard(contentPadding = PaddingValues(horizontal = DeepCardPadding)) {
                    desktopOnly.forEach { item -> CapabilityRow(item) }
                }
            }
        }
        item {
            Column {
                DeepSectionTitle(byDesignSectionTitle(byDesignCount(byDesign)))
                Spacer(Modifier.height(TitleToRows))
                DeepCard(contentPadding = PaddingValues(horizontal = DeepCardPadding)) {
                    CapabilityRow(byDesign)
                }
            }
        }
    }
}

@Composable
private fun PageHeadline(onBack: () -> Unit) {
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
                text = "仅 Windows",
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onBackground,
            )
            Text(
                text = "手机端做不到的能力与原因",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/** 尾标：按设计不做 ≠ 仅 Windows，两者不能混写。 */
internal fun capabilityTailLabel(item: UnsupportedCapability): String =
    if (item.isByDesign) "按设计不做" else "仅 Windows"

/** 区块标题里的计数来自实际列表，不写死 11 / 1。 */
internal fun desktopOnlySectionTitle(count: Int): String = "需要桌面端（$count 项）"

internal fun byDesignSectionTitle(count: Int): String = "按设计不做（$count 项）"

/** 按设计不做的条目恒为一条；仍然从数据算，避免以后加条目时标题说谎。 */
internal fun byDesignCount(item: UnsupportedCapability): Int = if (item.isByDesign) 1 else 0

internal fun desktopOnlyIntroLine(count: Int): String =
    "手机是一个独立沙箱：它只能看到自己的数据，不能驱动 PC 的进程、窗口或文件。" +
        "下面 $count 项需要桌面端环境，所以手机上不提供按钮——不是还没做，是做不了。"

/**
 * 一条能力：标题行（标题 + 尾标）+ 正文。
 *
 * 正文单独占一整行，永远在尾标下方，既不与尾标抢宽度也不会重叠；
 * 标题 `weight(1f)` 让尾标保持自身宽度，长标题换行而不是把尾标挤出去。
 */
@Composable
private fun CapabilityRow(item: UnsupportedCapability) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = DeepRowVertical),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = item.title,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Medium,
                color = MaterialTheme.colorScheme.onSurface,
                modifier = Modifier.weight(1f),
            )
            Spacer(Modifier.width(8.dp))
            Text(
                text = capabilityTailLabel(item),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(4.dp))
        Text(
            text = item.reason,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
