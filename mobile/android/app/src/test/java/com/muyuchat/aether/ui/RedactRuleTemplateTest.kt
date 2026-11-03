package com.muyuchat.aether.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class RedactRuleTemplateTest {
    @Test
    fun phoneTemplateMapsToExpectedCustomRule() {
        val rule = RedactRuleTemplate.PhoneRule.toRule()

        assertEquals("phone", rule.kind)
        assertEquals("+86", rule.prefix)
        assertEquals("", rule.regex)
    }

    @Test
    fun phoneTemplateHasUserFacingLabel() {
        assertEquals("手机号脱敏", RedactRuleTemplate.PhoneRule.label)
        assertTrue(RedactRuleTemplate.PhoneRule.regex.isBlank())
    }
}
