package com.muyuchat.aether.runtime

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RoutingResolveTest {
    @Test
    fun parsesRealResolvePayload() {
        val preview = parseResolve(
            """
            {
              "model": "auto/DeepSeek-V4-Flash",
              "bindings": [
                {"provider":"qoder-cn","name":"Qoder CN","upstream_model":"DeepSeek-V4-Flash",
                 "ready":true,"accounts":2,"available":2},
                {"provider":"trae-cn","name":"Trae CN","upstream_model":"DeepSeek-V4-Flash",
                 "ready":false,"accounts":1,"available":0}
              ]
            }
            """.trimIndent(),
        )
        assertEquals("auto/DeepSeek-V4-Flash", preview.model)
        assertEquals(2, preview.bindings.size)
        assertEquals("Qoder CN", preview.bindings[0].name)
        assertEquals("DeepSeek-V4-Flash", preview.bindings[0].upstreamModel)
        assertTrue(preview.bindings[0].ready)
        assertEquals(2, preview.bindings[0].available)
        assertFalse(preview.bindings[1].ready)
        assertEquals(1, preview.bindings[1].accounts)
    }

    @Test
    fun emptyBindingsAreValidAndMalformedRejected() {
        val empty = parseResolve("""{"model":"x","bindings":[]}""")
        assertTrue(empty.bindings.isEmpty())

        for (body in listOf("not json", "{}", """{"bindings":"oops"}""")) {
            val error = runCatching { parseResolve(body) }.exceptionOrNull()
            assertTrue("body=$body", error is ManagementApiException)
        }
    }

    @Test
    fun resolveUrlEncodesGroupId() {
        assertEquals(
            "http://127.0.0.1:1/api/models/resolve?model=auto%2FDeepSeek-V4-Flash",
            resolveUrl("127.0.0.1:1", "auto/DeepSeek-V4-Flash"),
        )
    }
}
