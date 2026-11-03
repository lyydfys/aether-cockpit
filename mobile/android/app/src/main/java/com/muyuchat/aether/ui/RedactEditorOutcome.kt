package com.muyuchat.aether.ui

/**
 * 规则编辑器提交后应该怎么处置弹窗。
 *
 * 单列成纯函数是因为这条判定决定了「用户输入会不会丢」：服务端 400/409 时弹窗必须留着。
 *
 * 判定只看**权威信号**：ViewModel 每成功完成一次写入就把它自己的写完计数 +1。
 * 不用 `saving` 的中间态推断成功——Compose 完全可能一次都没观察到 `saving=true`
 * 的那一帧（请求太快或状态跳变），那样推断就会把成功误判成「还在保存」而把弹窗卡死。
 */
enum class RedactEditorOutcome {
    /** 用户还没点保存。 */
    Idle,

    /** 请求在飞，弹窗保持现状并禁用重复提交。 */
    Wait,

    /** 服务端已回读成功（写完计数前进），可以关闭。 */
    Close,

    /** 服务端拒绝或网络失败：保留弹窗与输入，错误就地显示。 */
    KeepOpen,
}

/**
 * @param submitting 本轮是否已经点过保存
 * @param baselineVersion 点保存那一刻的写完计数
 * @param currentVersion ViewModel 当前的写完计数；前进即代表这次写入成功了
 * @param error ViewModel 的 redactError；非空表示最近一次写入失败
 */
internal fun redactEditorOutcome(
    submitting: Boolean,
    baselineVersion: Int,
    currentVersion: Int,
    error: String?,
): RedactEditorOutcome = when {
    !submitting -> RedactEditorOutcome.Idle
    currentVersion > baselineVersion -> RedactEditorOutcome.Close
    error != null -> RedactEditorOutcome.KeepOpen
    else -> RedactEditorOutcome.Wait
}
