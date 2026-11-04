package com.muyuchat.aether.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.drawable.Icon
import android.os.Build
import android.os.IBinder
import com.muyuchat.aether.MainActivity
import com.muyuchat.aether.R
import com.muyuchat.aether.runtime.GatewayServiceController
import com.muyuchat.aether.runtime.GatewayState
import com.muyuchat.aether.runtime.GatewayStatus
import com.muyuchat.aether.runtime.notificationText
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.launch

/**
 * Foreground owner of the local gateway.
 *
 * Android may stop this service (Doze, OEM battery policy, user force-stop,
 * notification permission changes). The UI always renders the real
 * [GatewayServiceController] state; this service never promises permanence.
 */
class AetherGatewayService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private lateinit var notifications: NotificationManager
    private var foreground = false

    override fun onCreate() {
        super.onCreate()
        notifications = getSystemService(NotificationManager::class.java)
        notifications.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, "网关运行状态", NotificationManager.IMPORTANCE_LOW).apply {
                description = "Aether 本地网关的运行状态与停止操作"
                setShowBadge(false)
            },
        )
        scope.launch {
            GatewayServiceController.status.collectLatest { status ->
                if (foreground) {
                    notifications.notify(NOTIFICATION_ID, buildNotification(status))
                }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                GatewayOverlayController.hide()
                if (!foreground) {
                    stopSelf()
                    return START_NOT_STICKY
                }
                notifications.notify(
                    NOTIFICATION_ID,
                    buildNotification(GatewayServiceController.status.value.copy(state = GatewayState.STOPPING)),
                )
                scope.launch {
                    GatewayServiceController.stop()
                    foreground = false
                    stopForeground(STOP_FOREGROUND_REMOVE)
                    stopSelf()
                }
                return START_NOT_STICKY
            }
            ACTION_SHOW_OVERLAY -> {
                if (!GatewayServiceController.status.value.running) {
                    GatewayOverlayController.hide()
                    stopSelf()
                    return START_NOT_STICKY
                }
                if (!foreground) {
                    if (!tryStartAsForeground(GatewayServiceController.status.value)) {
                        stopSelf()
                        return START_NOT_STICKY
                    }
                }
                val colors = OverlayColors.from(intent)
                val shown = colors != null && GatewayOverlayController.show(this, colors)
                notifications.notify(
                    NOTIFICATION_ID,
                    if (shown) {
                        buildNotification(GatewayServiceController.status.value)
                    } else {
                        buildNotification(
                            GatewayServiceController.status.value,
                            overlayNote = "系统悬浮窗未显示：请检查「显示在其他应用上层」权限",
                        )
                    },
                )
                return START_STICKY
            }
            ACTION_HIDE_OVERLAY -> {
                GatewayOverlayController.hide()
                if (foreground) {
                    notifications.notify(
                        NOTIFICATION_ID,
                        buildNotification(GatewayServiceController.status.value),
                    )
                }
                return START_STICKY
            }
            else -> {
                if (!tryStartAsForeground(
                        GatewayServiceController.status.value.copy(
                            state = if (GatewayServiceController.status.value.running) {
                                GatewayState.RUNNING
                            } else {
                                GatewayState.STARTING
                            },
                        ),
                    )
                ) {
                    stopSelf()
                    return START_NOT_STICKY
                }
                scope.launch {
                    val result = GatewayServiceController.start(applicationContext)
                    if (result.state == GatewayState.FAILED) {
                        foreground = false
                        stopForeground(STOP_FOREGROUND_REMOVE)
                        stopSelf()
                    }
                }
            }
        }
        // 网关服务是用户主动开启的前台服务：被系统回收后尽力恢复；不承诺不会被杀。
        return START_STICKY
    }

    private fun startAsForeground(status: GatewayStatus) {
        val notification = buildNotification(status)
        // Manifest: android:foregroundServiceType="specialUse". The runtime argument must match it.
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }
        foreground = true
    }

    /**
     * 被系统以 START_STICKY 拉起时，Android 12+ 可能拒绝后台启动 FGS。
     * 失败就停服务，不让异常穿透 onStartCommand 造成崩溃；如实放弃而不是假装常驻。
     */
    private fun tryStartAsForeground(status: GatewayStatus): Boolean =
        runCatching { startAsForeground(status) }.isSuccess

    private fun buildNotification(status: GatewayStatus, overlayNote: String? = null): Notification {
        val open = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val stop = PendingIntent.getService(
            this,
            1,
            Intent(this, AetherGatewayService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val builder = Notification.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_stat_aether)
            .setContentTitle("Aether 网关")
            .setContentText(overlayNote ?: notificationText(status))
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setCategory(Notification.CATEGORY_SERVICE)
            .addAction(
                Notification.Action.Builder(
                    Icon.createWithResource(this, R.drawable.ic_stat_aether),
                    "停止",
                    stop,
                ).build(),
            )
        if (Build.VERSION.SDK_INT >= 31) {
            builder.setForegroundServiceBehavior(Notification.FOREGROUND_SERVICE_IMMEDIATE)
        }
        return builder.build()
    }

    override fun onDestroy() {
        GatewayOverlayController.hide()
        scope.cancel()
        // Best effort only: the process may die before this finishes, and the UI
        // reports stopped after a restart because the Go runtime died with it.
        if (GatewayServiceController.status.value.state == GatewayState.RUNNING ||
            GatewayServiceController.status.value.state == GatewayState.STARTING
        ) {
            CoroutineScope(SupervisorJob() + Dispatchers.IO).launch {
                GatewayServiceController.stop()
            }
        }
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    companion object {
        const val ACTION_START = "com.muyuchat.aether.action.START_GATEWAY"
        const val ACTION_STOP = "com.muyuchat.aether.action.STOP_GATEWAY"

        /** 显示/关闭系统级悬浮窗（不是应用内浮球；需要 SYSTEM_ALERT_WINDOW）。 */
        const val ACTION_SHOW_OVERLAY = "com.muyuchat.aether.action.SHOW_OVERLAY"
        const val ACTION_HIDE_OVERLAY = "com.muyuchat.aether.action.HIDE_OVERLAY"
        const val CHANNEL_ID = "aether_gateway"
        const val NOTIFICATION_ID = 1001
    }
}
