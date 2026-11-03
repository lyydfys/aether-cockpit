package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * 回归：规则编辑器**不得**在发请求之前就关闭，也不得因为漏看一帧而卡住。
 *
 * 原实现是「点保存 → 调 onSave → 立刻 editor = null」，服务端 400/409 或网络失败时
 * 用户刚打的字已经没了，错误只能显示在已经关掉的父对话框底部。
 */
class RedactEditorOutcomeTest {
    @Test
    fun idleBeforeTheUserSubmits() {
        assertEquals(
            RedactEditorOutcome.Idle,
            redactEditorOutcome(submitting = false, baselineVersion = 3, currentVersion = 3, error = null),
        )
        // 还没点保存时，历史错误与计数都无权决定关闭。
        assertEquals(
            RedactEditorOutcome.Idle,
            redactEditorOutcome(submitting = false, baselineVersion = 3, currentVersion = 9, error = "x"),
        )
    }

    @Test
    fun staysOpenUntilTheWriteActuallyCompletes() {
        assertEquals(
            RedactEditorOutcome.Wait,
            redactEditorOutcome(submitting = true, baselineVersion = 3, currentVersion = 3, error = null),
        )
    }

    @Test
    fun closesOnlyAfterASuccessfulRoundTrip() {
        assertEquals(
            RedactEditorOutcome.Close,
            redactEditorOutcome(submitting = true, baselineVersion = 3, currentVersion = 4, error = null),
        )
    }

    /**
     * 关键回归：只用 `saving` 的中间态推断成功会在「一帧都没观察到 saving=true」时卡死，
     * 所以判定必须看写完计数，而不是看请求是否正在飞。
     */
    @Test
    fun successfulWriteClosesEvenIfTheInFlightFrameWasNeverObserved() {
        assertEquals(
            RedactEditorOutcome.Close,
            redactEditorOutcome(submitting = true, baselineVersion = 0, currentVersion = 1, error = null),
        )
    }

    @Test
    fun keepsTheEditorAndItsInputWhenTheServerRejects() {
        assertEquals(
            RedactEditorOutcome.KeepOpen,
            redactEditorOutcome(
                submitting = true,
                baselineVersion = 3,
                currentVersion = 3,
                error = "规则最多 100 条",
            ),
        )
    }

    @Test
    fun keepsTheEditorOnConflictSoTheUserCanRefreshInsteadOfRetyping() {
        assertEquals(
            RedactEditorOutcome.KeepOpen,
            redactEditorOutcome(
                submitting = true,
                baselineVersion = 3,
                currentVersion = 3,
                error = "规则已被改动，请刷新后重试",
            ),
        )
    }
}
