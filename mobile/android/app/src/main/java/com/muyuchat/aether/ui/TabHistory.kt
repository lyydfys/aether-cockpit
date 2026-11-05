package com.muyuchat.aether.ui

/**
 * 根页返回策略（用户最终口径：**概览是唯一根部**，不做 tab 历史栈）。
 *
 * - 概览：**不接管**，交给系统播放退出动画（这才是"退出 App"的正确观感）；
 * - 其它四个 tab（额度 / 用量 / 请求 / 更多）：**一律回概览**，不退出 App；
 * - 覆盖层（设置/探针/备份…）优先：它们打开时先关它们，与本策略无关。
 */
internal object RootBackPolicy {
    /** 这次返回是否由 App 接管。 */
    fun shouldHandleBack(anySubpageOpen: Boolean, selectedTab: MobileTab): Boolean =
        anySubpageOpen || selectedTab != MobileTab.OVERVIEW

    /**
     * 接管的根页返回要落到哪个 tab：
     * - 非概览 → 概览；
     * - 概览 → `null`（不该接管，返回 null 让调用方交系统）。
     */
    fun targetTab(selectedTab: MobileTab): MobileTab? =
        if (selectedTab == MobileTab.OVERVIEW) null else MobileTab.OVERVIEW
}
