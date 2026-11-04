package com.muyuchat.aether.ui

/**
 * 删除调用方密钥的确认判定。
 *
 * 规则（与页面一致）：
 * - 目标名称留空时，界面显示的是 id，因此必须输入 **id**；
 * - 判定为**精确匹配**（仅容忍首尾空格），大小写敏感；
 * - 目标为空时永远不通过——避免"空对空"把删除按钮点亮。
 *
 * 抽成纯函数是为了能被单测覆盖：判定逻辑留在 Composable 里就测不到。
 */
internal fun callerKeyDeleteConfirmed(input: String, name: String, id: String): Boolean {
    val target = name.trim().ifBlank { id.trim() }
    if (target.isEmpty()) {
        return false
    }
    return input.trim() == target
}
