package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * 保存前的本地校验必须和 Go `internal/core/redact.SetCustomRules` 同口径**仅限可精确镜像的约束**：
 * 空内容、各字段 rune 上限、条数上限。正则能否编译不在本地判断——RE2 与 java.util.regex
 * 接受集双向不等，判错会拦死合法规则或制造假放行，这类错误由服务端 400 原文回显。
 */
class RedactRuleValidationTest {
    @Test
    fun blankKindIsRejectedBeforeTheRoundTrip() {
        val problem = validateRedactCustomRule(RedactCustomRule("", prefix = "sk-"))
        assertEquals("请填写规则类型", problem)
    }

    @Test
    fun kindWithOnlyDroppedSymbolsBecomesCustomLikeTheServer() {
        // Go: NormalizeKind 丢掉非 [A-Z0-9_] 且非空格的字符；全被丢掉时回 CUSTOM。
        assertEquals("CUSTOM", normalizeRedactKind("中文"))
        assertEquals("API_KEY", normalizeRedactKind(" api-key "))
        assertEquals("CUSTOM", normalizeRedactKind("   "))
        assertNull(validateRedactCustomRule(RedactCustomRule("中文", prefix = "sk-")))
    }

    @Test
    fun ruleNeedsEitherPrefixOrRegex() {
        assertEquals("请填写前缀或正则", validateRedactCustomRule(RedactCustomRule("API_KEY")))
        assertEquals(
            "请填写前缀或正则",
            validateRedactCustomRule(RedactCustomRule("API_KEY", prefix = "   ", regex = "  ")),
        )
    }

    @Test
    fun lengthsMatchTheServerRuneLimits() {
        assertEquals(
            "规则类型最多 40 个字符",
            validateRedactCustomRule(RedactCustomRule("K".repeat(41), prefix = "sk-")),
        )
        assertEquals(
            "前缀最多 100 个字符",
            validateRedactCustomRule(RedactCustomRule("API_KEY", prefix = "p".repeat(101))),
        )
        assertEquals(
            "正则最多 500 个字符",
            validateRedactCustomRule(RedactCustomRule("API_KEY", regex = "r".repeat(501))),
        )
        // 多字节字符按 code point 计，和 Go 的 []rune 一致。
        assertNull(validateRedactCustomRule(RedactCustomRule("K".repeat(40), prefix = "前".repeat(100))))
    }

    /**
     * 回归：本地**不得**因为 java.util.regex 编译失败而拦下规则。
     * `(a)\1` 在 Java 里合法、在 RE2 里非法，本地拦了就会把服务端能接受/拒绝的判定抢走；
     * 反过来 `(?P<name>re)` 在 RE2 里合法、在 Java 里非法，本地拦了就会拦死合法规则。
     * 两种都由服务端 400 原文回显，弹窗保持打开。
     */
    @Test
    fun regexCompilationIsNeverDecidedLocally() {
        assertNull(validateRedactCustomRule(RedactCustomRule("API_KEY", regex = "*abc")))
        assertNull(validateRedactCustomRule(RedactCustomRule("API_KEY", regex = "(a)\\1")))
        assertNull(validateRedactCustomRule(RedactCustomRule("API_KEY", regex = "(?P<tok>oc_[a-z]+)")))
        assertNull(validateRedactCustomRule(RedactCustomRule("API_KEY", regex = "sk-(?!test)")))
    }

    @Test
    fun validRuleAndEmptyListPass() {
        assertNull(validateRedactCustomRule(RedactCustomRule("API_KEY", prefix = "oc_sk_")))
        assertNull(validateRedactCustomRule(RedactCustomRule("email", regex = "[a-z]+@example\\.com")))
        assertNull(validateRedactRules(emptyList()))
    }

    @Test
    fun ruleCountCeilingMatchesTheServer() {
        val full = List(100) { RedactCustomRule("R$it", prefix = "p$it") }
        assertNull(validateRedactRules(full))
        assertEquals("规则最多 100 条", validateRedactRules(full + RedactCustomRule("X", prefix = "x_")))
    }

    @Test
    fun listValidationReportsTheOffendingIndexOneBased() {
        val rules = listOf(
            RedactCustomRule("OK", prefix = "ok_"),
            RedactCustomRule("", prefix = "bad_"),
        )
        assertEquals("第 2 条：请填写规则类型", validateRedactRules(rules))
    }
}
