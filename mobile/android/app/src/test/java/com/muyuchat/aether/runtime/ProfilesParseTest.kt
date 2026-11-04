package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** `/api/profiles` 真实回包是对象 `{"profiles":[...],"active":...}`，不是裸数组。 */
class ProfilesParseTest {
    @Test
    fun parsesObjectEnvelope() {
        val rows = parseProfiles(
            """{"profiles":[{"id":"p-1","name":"家里","updated_at":1700000000,
                 "clients":[{"client_id":"claude_code","model":"claude-x","origin":"http://127.0.0.1"}]}],
                "active":"p-1"}""",
        )
        assertEquals(1, rows.size)
        assertEquals("家里", rows.single().name)
        assertEquals("claude_code · claude-x · http://127.0.0.1", profileClientLabel(rows.single().clients.single()))
    }

    @Test
    fun stillAcceptsBareArrayForCompatibility() {
        val rows = parseProfiles("""[{"id":"p-2","name":"办公室","clients":[]}]""")
        assertEquals("办公室", rows.single().name)
    }

    @Test
    fun missingOrNullProfilesKeyIsMalformed() {
        for (body in listOf("{}", """{"profiles":null}""", """{"profiles":"x"}""")) {
            val error = runCatching { parseProfiles(body) }.exceptionOrNull()
            assertTrue(
                "body=<$body> 缺 profiles/类型不对必须抛 invalid_response，实际=${error?.javaClass?.name ?: "没有抛"}",
                error is ManagementApiException && error.code == "invalid_response",
            )
        }
    }

    @Test
    fun explicitEmptyProfilesArrayIsValidEmpty() {
        assertTrue(parseProfiles("""{"profiles":[],"active":""}""").isEmpty())
    }
}
