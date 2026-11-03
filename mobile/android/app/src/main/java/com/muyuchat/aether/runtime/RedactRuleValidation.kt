package com.muyuchat.aether.runtime

/**
 * 自定义脱敏规则的本地预校验。
 *
 * 这是 Go `internal/core/redact.SetCustomRules` 的**提交前镜像**，不是替代，也不是完整复刻：
 * 「整表替换」是一条不合法就整次 400，用户在弹窗里刚打的字会丢，所以只对
 * **能精确镜像的约束**（空内容、各字段 rune 上限、条数上限）做本地拦截。
 *
 * 故意**不**在本地判断「正则能否编译」：服务端用 Go RE2，客户端只能用 java.util.regex，
 * 两者接受集双向不等——`(a)\1`、`(?!x)`、`\Z` 在 Java 编译通过但 RE2 拒绝；
 * `(?P<name>re)` 在 RE2 合法但 Java 抛异常。本地判错会把合法规则拦死，
 * 判漏又会制造「本地放行、服务端失败」的假象。这类错误交给服务端 400 原文回显
 * （`SettingsApiClient.requestJson` → `ManagementApiException`），弹窗保持打开。
 */
private const val MAX_KIND_CHARS = 40
private const val MAX_PREFIX_CHARS = 100
private const val MAX_REGEX_CHARS = 500
private const val MAX_RULES = 100

/** 与 Go `redact.NormalizeKind` 等价的规范化：大写、空白/点/横线换下划线、其余符号丢弃。 */
internal fun normalizeRedactKind(raw: String): String {
    val out = StringBuilder()
    raw.trim().uppercase().forEach { ch ->
        when {
            ch in 'A'..'Z' || ch in '0'..'9' || ch == '_' -> out.append(ch)
            ch == ' ' || ch == '.' || ch == '-' -> out.append('_')
        }
    }
    return out.toString().trim('_').ifEmpty { "CUSTOM" }
}

/** 返回第一条人类可读的问题；null 表示这条规则可以提交。 */
internal fun validateRedactCustomRule(rule: RedactCustomRule): String? {
    val kind = rule.kind.trim()
    val prefix = rule.prefix.trim()
    val regex = rule.regex.trim()
    if (kind.isEmpty()) {
        return "请填写规则类型"
    }
    if (prefix.isEmpty() && regex.isEmpty()) {
        return "请填写前缀或正则"
    }
    // 长度按 code point 计，与 Go 的 []rune 对齐。
    val normalizedKind = normalizeRedactKind(kind)
    if (normalizedKind.codePointCount(0, normalizedKind.length) > MAX_KIND_CHARS) {
        return "规则类型最多 $MAX_KIND_CHARS 个字符"
    }
    if (prefix.isNotEmpty() && prefix.codePointCount(0, prefix.length) > MAX_PREFIX_CHARS) {
        return "前缀最多 $MAX_PREFIX_CHARS 个字符"
    }
    // prefix 非空时服务端按前缀保存，正则既不判长也不编译，这里同样不判。
    if (prefix.isEmpty() && regex.codePointCount(0, regex.length) > MAX_REGEX_CHARS) {
        return "正则最多 $MAX_REGEX_CHARS 个字符"
    }
    return null
}

/** 整表替换前的校验；错误带上 1 起的序号，和 Go 的「第 N 条…」措辞一致。 */
internal fun validateRedactRules(rules: List<RedactCustomRule>): String? {
    if (rules.size > MAX_RULES) {
        return "规则最多 $MAX_RULES 条"
    }
    rules.forEachIndexed { index, rule ->
        val problem = validateRedactCustomRule(rule) ?: return@forEachIndexed
        return "第 ${index + 1} 条：$problem"
    }
    return null
}
