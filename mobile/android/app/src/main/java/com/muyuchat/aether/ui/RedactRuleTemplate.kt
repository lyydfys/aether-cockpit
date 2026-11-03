package com.muyuchat.aether.ui

import com.muyuchat.aether.runtime.RedactCustomRule

/**
 * 静态脱敏规则模板。
 *
 * 模板只包含公开的默认值，不读取账号、联系人、剪贴板或任何用户数据；
 * 点击后仅预填规则编辑器，最终保存仍需用户确认。
 */
data class RedactRuleTemplate(
    val label: String,
    val kind: String,
    val prefix: String = "",
    val regex: String = "",
) {
    fun toRule(): RedactCustomRule = RedactCustomRule(
        kind = kind,
        prefix = prefix,
        regex = regex,
    )

    companion object {
        /** 手机号脱敏：按 +86 前缀匹配，具体脱敏动作由 Go 网关执行。 */
        val PhoneRule = RedactRuleTemplate(
            label = "手机号脱敏",
            kind = "phone",
            prefix = "+86",
        )
    }
}
