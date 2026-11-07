package com.muyuchat.aether.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R

/**
 * 输入框右侧的「一键清空」按钮。
 *
 * 为什么不是直接塞 [McaIconButton]（48dp 视觉圆）：
 * 那一版会把单行输入撑高、图标比例失衡（`AetherPolish.kt` 的
 * [AetherCompactIconButton] 注释里记过这个坑）。这里做的是
 * **48dp 触摸区 + 26dp 软底托视觉**：手指够得着，行高不被撑爆。
 *
 * 只在 `value` 非空时由调用方渲染（空值不显示 ×，避免视觉噪音）。
 */
@Composable
internal fun AetherFieldClearButton(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    contentDescription: String = "清除",
    discSize: Dp = 26.dp,
    glyphSize: Dp = 14.dp,
) {
    val tint = if (enabled) {
        MaterialTheme.colorScheme.onSurfaceVariant
    } else {
        MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.36f)
    }
    Box(
        modifier = modifier
            .size(48.dp)
            .semantics {
                role = Role.Button
                this.contentDescription = contentDescription
            }
            .aetherPressable(enabled = enabled, scaleDown = 0.90f, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(discSize)
                .clip(CircleShape)
                .background(AetherColors.pillTrack(), CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                painter = painterResource(R.drawable.ic_close),
                contentDescription = null,
                tint = tint,
                modifier = Modifier.size(glyphSize),
            )
        }
    }
}

/**
 * 输入框 + 右侧清除按钮的统一骨架。
 *
 * - `value` 为空时不显示 ×（没有可清的东西就不占位）；
 * - 点 × 先回调 `onClear()`，再把焦点交还输入框（`FocusRequester` 由骨架持有，
 *   内容通过 `content(modifier, focusRequester)` 把它接到 `BasicTextField` 上）；
 * - `trailing` 与清除按钮**共存**：清除排在前面，密码可见性等业务按钮仍排最后。
 *
 * 视觉外壳（`Surface` + 描边）由调用方保留，骨架只负责横向排布。
 */
@Composable
internal fun ClearableFieldRow(
    value: String,
    onClear: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    showClear: Boolean = true,
    /** 多行输入用 `Alignment.Top`：× 跟着第一行走，不悬在整块中间。 */
    verticalAlignment: Alignment.Vertical = Alignment.CenterVertically,
    trailing: (@Composable () -> Unit)? = null,
    content: @Composable (Modifier, FocusRequester) -> Unit,
) {
    val focusRequester = remember { FocusRequester() }
    Row(
        modifier = modifier.fillMaxWidth(),
        verticalAlignment = verticalAlignment,
    ) {
        Box(Modifier.weight(1f)) {
            content(Modifier.fillMaxWidth(), focusRequester)
        }
        if (showClear && enabled && value.isNotEmpty()) {
            AetherFieldClearButton(
                onClick = {
                    onClear()
                    // 焦点留在输入框：清空后可以直接接着打字，不用再点一次。
                    focusRequester.requestFocus()
                },
            )
        }
        trailing?.invoke()
    }
}

/**
 * 通用可清除输入框（单行/多行）。
 *
 * 直接套在调用方既有的 `Surface` 里即可，不引入新视觉：
 * ```kotlin
 * Surface(...) { ClearableTextField(value, onValueChange, placeholder = "…") }
 * ```
 *
 * `trailing` 用于业务附加控件（密码可见性切换等），与 × 同时显示：
 * × 排在业务按钮之前，业务按钮的点击区域优先。
 */
@Composable
internal fun ClearableTextField(
    value: String,
    onValueChange: (String) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    singleLine: Boolean = true,
    placeholder: String = "",
    mono: Boolean = false,
    minHeight: Dp = 48.dp,
    visualTransformation: VisualTransformation = VisualTransformation.None,
    contentDescription: String = "",
    trailing: (@Composable () -> Unit)? = null,
    textStyle: TextStyle? = null,
) {
    ClearableFieldRow(
        value = value,
        onClear = { onValueChange("") },
        modifier = modifier,
        enabled = enabled,
        verticalAlignment = if (singleLine) Alignment.CenterVertically else Alignment.Top,
        trailing = trailing,
    ) { fieldModifier, focusRequester ->
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            enabled = enabled,
            singleLine = singleLine,
            textStyle = textStyle ?: MaterialTheme.typography.bodyMedium.copy(
                color = MaterialTheme.colorScheme.onSurface,
                fontFamily = if (mono) FontFamily.Monospace else FontFamily.Default,
            ),
            visualTransformation = visualTransformation,
            cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
            modifier = fieldModifier
                .heightIn(min = minHeight)
                .focusRequester(focusRequester)
                .padding(horizontal = 12.dp, vertical = 10.dp)
                .then(
                    if (contentDescription.isBlank()) {
                        Modifier
                    } else {
                        Modifier.semantics { this.contentDescription = contentDescription }
                    },
                ),
            decorationBox = { inner ->
                Box {
                    if (value.isEmpty() && placeholder.isNotEmpty()) {
                        Text(
                            text = placeholder,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    inner()
                }
            },
        )
    }
}
