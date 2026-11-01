package gateway

import "testing"

func TestExpandHeaderMap(t *testing.T) {
	t.Setenv("MCP_HEADER_TEST", "tok")
	h, missing := expandHeaderMap(map[string]any{
		"Authorization": "Bearer ${MCP_HEADER_TEST}",
		"X-Other":       "plain",
	})
	if len(missing) != 0 || h["Authorization"] != "Bearer tok" || h["X-Other"] != "plain" {
		t.Fatalf("h=%v missing=%v", h, missing)
	}
	_, missing = expandHeaderMap(map[string]any{
		"Authorization": "Bearer ${MCP_MISSING_HEADER_VAR}",
	})
	if len(missing) != 1 || missing[0] != "MCP_MISSING_HEADER_VAR" {
		t.Fatalf("missing = %v", missing)
	}
}
