package com.muyuchat.aether.ui

import android.os.Build
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.runtime.GatewayStatus

/** 仓库根 LICENSE 核到的是 Apache License 2.0；不要在页面里另写别的许可名。 */
internal const val AppLicenseName: String = "Apache License 2.0"

/** 更新通道没有运行时接口，按产品事实写；不编造应用商店链接或自动更新。 */
internal const val AppUpdateChannelCopy: String = "随应用商店 / APK 更新"

/**
 * 关于页字段（版本/运行环境/网关版本）的纯格式化，单测不依赖 Android 运行时。
 *
 * `version` / `model` / `abis` 都可能因系统裁剪为空，空值统一显示「—」，不抛异常。
 */
internal fun aboutVersionLabel(versionName: String?, versionCode: Long?): String {
    val name = versionName?.trim().orEmpty().ifBlank { "—" }
    val code = versionCode?.takeIf { it >= 0 }?.toString() ?: "—"
    return "$name ($code)"
}

internal fun aboutRuntimeLabel(release: String?, sdkInt: Int?, model: String?): String {
    val releaseText = release?.trim().orEmpty().ifBlank { "—" }
    val sdkText = sdkInt?.takeIf { it > 0 }?.toString() ?: "—"
    val modelText = model?.trim().orEmpty().ifBlank { "—" }
    return "Android $releaseText (API $sdkText) · $modelText"
}

internal fun aboutAbiLabel(abis: List<String>?): String =
    abis?.map { it.trim() }?.filter { it.isNotBlank() }?.joinToString(", ")
        ?.ifBlank { null }
        ?: "—"

/** GatewayStatus 目前没有版本字段；有值才显示，没有就如实写「—」。 */
internal fun aboutGatewayVersionLabel(version: String?): String =
    version?.trim().orEmpty().ifBlank { "—" }

/** 网关版本为空时，页面必须说明为什么是「—」，不能留一个像加载失败的符号。 */
internal fun aboutGatewayVersionNote(version: String?): String? =
    if (version.isNullOrBlank()) "网关未启动或版本未上报" else null

/**
 * 「关于」设置区块。调用方把它挂在设置页列表里即可；不重复实现诊断包导出。
 *
 * 版本来自 PackageManager（本模块未开启 buildConfigFeature，不用 BuildConfig）；
 * 运行环境来自 Build；网关版本来自现有 [GatewayStatus]（没有字段时显示「—」）。
 */
@Suppress("DEPRECATION")
@Composable
internal fun SettingsAboutSection(
    status: GatewayStatus,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val packageInfo = remember(context) {
        runCatching {
            context.packageManager.getPackageInfo(context.packageName, 0)
        }.getOrNull()
    }
    val versionName = packageInfo?.versionName
    val versionCode = packageInfo?.let {
        if (Build.VERSION.SDK_INT >= 28) it.longVersionCode else it.versionCode.toLong()
    }
    val gatewayVersion = gatewayVersionOf(status)

    DeepCard(modifier = modifier) {
        DeepCardTitle("关于")
        Spacer(Modifier.height(8.dp))
        AboutRow("版本", aboutVersionLabel(versionName, versionCode))
        AboutRow("更新通道", AppUpdateChannelCopy)
        AboutRow("开源许可", AppLicenseName)
        AboutRow(
            "运行环境",
            aboutRuntimeLabel(Build.VERSION.RELEASE, Build.VERSION.SDK_INT, Build.MODEL),
        )
        AboutRow("ABI", aboutAbiLabel(Build.SUPPORTED_ABIS?.toList()))
        AboutRow("网关版本", aboutGatewayVersionLabel(gatewayVersion))
        aboutGatewayVersionNote(gatewayVersion)?.let { note ->
            Text(
                text = note,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.fillMaxWidth().padding(top = 2.dp),
            )
        }
        Spacer(Modifier.height(12.dp))
        Text(
            text = "诊断包：设置页的「自检摘要」区块已经可以生成并分享纯文本报告；" +
                "本区不重复实现导出，请到该区块操作。",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/**
 * 网关版本是可选能力：当前 [GatewayStatus] 没有版本字段，统一返回 null，
 * 页面显示「—」并注明原因。以后网关上报版本时只改这一个函数。
 */
@Suppress("UNUSED_PARAMETER")
internal fun gatewayVersionOf(status: GatewayStatus): String? = null

@Composable
private fun AboutRow(label: String, value: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = DeepRowVertical),
        verticalAlignment = Alignment.Top,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.width(96.dp),
        )
        Spacer(Modifier.width(8.dp))
        Text(
            text = value,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurface,
            textAlign = TextAlign.End,
            modifier = Modifier.weight(1f),
        )
    }
}
