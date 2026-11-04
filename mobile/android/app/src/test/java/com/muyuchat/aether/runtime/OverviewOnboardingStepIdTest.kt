package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * 三步接入的 step id 与按钮文案（桌面 `overview.js:183-185`）。
 *
 * 关键：**不要按 index 映射**跳转目标——将来在中间插一步会静默错位，
 * 所以每步自带 `id`，按钮文案与目标都从 id 推。
 */
class OverviewOnboardingStepIdTest {
    private fun steps() = overviewOnboardingSteps(
        running = true,
        linksLoaded = true,
        accountsKnown = true,
        accountCount = 0,
        usageKnown = true,
        calls = 0L,
    )

    @Test
    fun stepsCarryStableIdsInDesktopOrder() {
        assertEquals(
            listOf(
                ONBOARDING_STEP_SETTINGS,
                ONBOARDING_STEP_ONBOARD,
                ONBOARDING_STEP_PROBE,
            ),
            steps().map { it.id },
        )
        // id 必须唯一
        assertEquals(3, steps().map { it.id }.toSet().size)
    }

    @Test
    fun buttonLabelsMatchDesktopWording() {
        assertEquals("去设置", onboardingStepActionLabel(ONBOARDING_STEP_SETTINGS))
        assertEquals("去接入", onboardingStepActionLabel(ONBOARDING_STEP_ONBOARD))
        assertEquals("去网关页", onboardingStepActionLabel(ONBOARDING_STEP_PROBE))
    }

    @Test
    fun unknownIdFallsBackToTheLastStepWording() {
        // 新增了步骤但没配文案时，宁可显示「去网关页」也不要崩或空白
        assertEquals("去网关页", onboardingStepActionLabel("something-new"))
        assertEquals("去网关页", onboardingStepActionLabel(""))
    }

    @Test
    fun everyStepHasNonBlankTitleAndStatus() {
        for (step in steps()) {
            assertTrue(step.id, step.title.isNotBlank())
            assertTrue(step.id, step.description.isNotBlank())
            assertTrue(step.id, step.status.isNotBlank())
        }
    }
}
