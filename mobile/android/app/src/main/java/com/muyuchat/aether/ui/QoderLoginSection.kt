package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.CircularProgressIndicator
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
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R
import com.muyuchat.core.designsystem.McaButton
import com.muyuchat.core.designsystem.McaButtonStyle
import com.muyuchat.core.designsystem.McaHairline
import com.muyuchat.core.designsystem.McaIconSize
import com.muyuchat.core.designsystem.McaShape
import com.muyuchat.core.designsystem.McaTonal

/** Qoder's browser login; the login URL is opened by the system browser only. */
@Composable
internal fun QoderLoginSection(
    state: QoderLoginUiState,
    onStart: () -> Unit,
    onOpenUrl: (String) -> Boolean,
    onCancel: () -> Unit,
    onDismiss: () -> Unit,
) {
    var openFailed by remember { mutableStateOf(false) }
    LaunchedEffect(state) { openFailed = false }
    Surface(
        shape = McaShape.Card,
        color = McaTonal.fill(),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(18.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Surface(
                    modifier = Modifier.size(30.dp),
                    shape = McaShape.Badge,
                    color = MaterialTheme.colorScheme.primary.copy(alpha = 0.10f),
                    border = McaHairline.stroke(),
                ) {
                    Box(contentAlignment = Alignment.Center) {
                        Icon(
                            painter = painterResource(R.drawable.ic_ws_channels),
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary,
                            modifier = Modifier.size(McaIconSize.Xs),
                        )
                    }
                }
                Spacer(Modifier.width(10.dp))
                Column {
                    Text(
                        text = "Qoder CN 登录",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Text(
                        text = "订阅登录 · 浏览器授权",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Spacer(Modifier.height(12.dp))
            when (state) {
                QoderLoginUiState.Idle -> {
                    Text(
                        text = "在系统浏览器完成 Qoder 登录，网关会自动把账号写入本机；请勿把登录链接转发给他人。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(12.dp))
                    McaButton(onClick = onStart, style = McaButtonStyle.Primary) {
                        Text("开始登录")
                    }
                }
                QoderLoginUiState.Starting -> Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(20.dp),
                        strokeWidth = 2.dp,
                        color = MaterialTheme.colorScheme.primary,
                    )
                    Spacer(Modifier.width(10.dp))
                    Text(
                        text = "正在创建登录会话…",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                is QoderLoginUiState.Waiting -> {
                    Text(
                        text = "等待浏览器授权",
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = "打开登录页并在 qoder.com 完成登录，完成后账号会自动写入本机。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    if (state.expiresIn > 0) {
                        Spacer(Modifier.height(2.dp))
                        Text(
                            text = "登录会话约 ${state.expiresIn / 60} 分钟内有效",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    if (openFailed) {
                        Spacer(Modifier.height(6.dp))
                        Text(
                            text = "无法打开浏览器，请检查系统浏览器后重试。",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.error,
                        )
                    }
                    Spacer(Modifier.height(12.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        McaButton(onClick = onCancel, style = McaButtonStyle.Outlined) {
                            Text("取消")
                        }
                        McaButton(
                            onClick = { openFailed = !onOpenUrl(state.loginUrl) },
                            style = McaButtonStyle.Primary,
                        ) {
                            Text("打开登录页")
                        }
                    }
                }
                is QoderLoginUiState.Done -> {
                    Text(
                        text = "登录成功",
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.primary,
                    )
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = "账号已写入本机账号池。",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(12.dp))
                    McaButton(onClick = onDismiss, style = McaButtonStyle.Primary) {
                        Text("完成")
                    }
                }
                is QoderLoginUiState.Failed -> {
                    Text(
                        text = "登录未完成",
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium,
                        color = MaterialTheme.colorScheme.error,
                    )
                    Spacer(Modifier.height(2.dp))
                    Text(
                        text = state.message,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurface,
                    )
                    Text(
                        text = "错误码：${state.code}",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(12.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        McaButton(onClick = onDismiss, style = McaButtonStyle.Outlined) {
                            Text("关闭")
                        }
                        McaButton(onClick = onStart, style = McaButtonStyle.Primary) {
                            Text("重新开始")
                        }
                    }
                }
            }
        }
    }
}
