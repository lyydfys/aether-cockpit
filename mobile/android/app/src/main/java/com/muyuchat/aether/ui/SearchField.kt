package com.muyuchat.aether.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.muyuchat.aether.R

/**
 * 旗舰级胶囊搜索栏：
 * - 采用同心跑道胶囊 (`CircleShape`) + Specular Rim 顶边微光；
 * - 右侧清除按钮采用同心 26dp 软底托 ([AetherCompactIconButton])，
 *   彻底解决原本塞入 48dp `McaIconButton` 导致图标过小、外框臃肿的比例失衡问题。
 */
@Composable
internal fun SearchField(
    query: String,
    onQueryChange: (String) -> Unit,
    placeholder: String = "搜索功能名称",
) {
    val dark = isAetherDarkTheme()
    // 清空后焦点回到搜索框：否则用户要继续搜还得再点一次。
    val focusRequester = remember { FocusRequester() }
    Surface(
        modifier = Modifier
            .fillMaxWidth()
            .aetherSpecularRim(cornerRadius = 24.dp),
        shape = CircleShape,
        color = AetherColors.cardSurface(),
        shadowElevation = if (dark) 0.dp else 0.5.dp,
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = 46.dp)
                .padding(start = 16.dp, end = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                painter = painterResource(R.drawable.ic_search),
                contentDescription = null,
                tint = AetherColors.textTertiary(),
                modifier = Modifier.size(18.dp),
            )
            Spacer(Modifier.width(10.dp))
            BasicTextField(
                value = query,
                onValueChange = onQueryChange,
                modifier = Modifier
                    .weight(1f)
                    .fillMaxHeight()
                    .wrapContentHeight(Alignment.CenterVertically)
                    .focusRequester(focusRequester)
                    .semantics { contentDescription = placeholder },
                singleLine = true,
                textStyle = MaterialTheme.typography.bodyMedium.copy(
                    color = MaterialTheme.colorScheme.onSurface,
                ),
                cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                decorationBox = { inner ->
                    Box {
                        if (query.isEmpty()) {
                            Text(
                                text = placeholder,
                                style = MaterialTheme.typography.bodyMedium,
                                color = AetherColors.textTertiary(),
                            )
                        }
                        inner()
                    }
                },
            )
            if (query.isNotEmpty()) {
                AetherCompactIconButton(
                    iconRes = R.drawable.ic_close,
                    contentDescription = "清除搜索",
                    onClick = {
                        onQueryChange("")
                        focusRequester.requestFocus()
                    },
                )
            }
        }
    }
}
