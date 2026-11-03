package com.muyuchat.aether.runtime

import android.content.Context
import android.content.Intent
import com.muyuchat.aether.service.AetherGatewayService
import kotlinx.coroutines.flow.StateFlow

/**
 * Narrow platform adapter used by the overview ViewModel. Composables never
 * touch the Service directly; this repository only forwards lifecycle intents
 * and exposes the single controller-owned status flow.
 */
class GatewayRuntimeRepository(context: Context) {
    private val appContext = context.applicationContext

    val status: StateFlow<GatewayStatus> = GatewayServiceController.status

    fun start() {
        appContext.startForegroundService(
            Intent(appContext, AetherGatewayService::class.java)
                .setAction(AetherGatewayService.ACTION_START),
        )
    }

    fun stop() {
        appContext.startService(
            Intent(appContext, AetherGatewayService::class.java)
                .setAction(AetherGatewayService.ACTION_STOP),
        )
    }
}
