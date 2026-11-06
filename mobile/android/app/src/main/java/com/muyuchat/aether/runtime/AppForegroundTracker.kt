package com.muyuchat.aether.runtime

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * 进程内「App 是否在前台」的唯一事实源。
 *
 * 系统悬浮窗的宿主是 [com.muyuchat.aether.service.AetherGatewayService]，没有 Activity 可查；
 * 而功能栏里的那颗按钮要按宿主切换语义（应用内 = 关闭 app 外显，应用外 = 返回 app），
 * 所以需要一个不依赖 Activity 实例的前台状态。MainActivity 的 onStart/onStop 是这个
 * 进程里唯一的 Activity 入口，直接记账即可。
 *
 * 用计数而不是布尔：将来若出现临时 Activity，`onStop` 先于另一个 `onStart` 到达时
 * 布尔会把状态写反；计数只关心"还有没有可见 Activity"。
 *
 * 只反映"本进程有没有可见 Activity"：Service 被系统单独拉起（START_STICKY，没有界面）时
 * 是 false——那正是应该显示「返回 app」的情况。
 */
internal object AppForegroundTracker {
    private val _inForeground = MutableStateFlow(false)

    /** 当前进程是否有可见 Activity；系统悬浮窗的按钮文案读它。 */
    val inForeground: StateFlow<Boolean> = _inForeground.asStateFlow()

    private var startedActivities: Int = 0

    fun onActivityStarted() {
        startedActivities += 1
        _inForeground.value = true
    }

    fun onActivityStopped() {
        startedActivities = (startedActivities - 1).coerceAtLeast(0)
        _inForeground.value = startedActivities > 0
    }
}
