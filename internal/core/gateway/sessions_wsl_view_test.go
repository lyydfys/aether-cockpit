package gateway

import (
	"testing"

	"aether-cockpit/internal/core/sessions"
)

func TestSessionViewCarriesWSL(t *testing.T) {
	g, _, _ := agentApplyTestGateway(t)
	v := g.sessionViewOf(sessions.Session{Agent: "claude-code", ID: "x", Title: "t", WSL: "Ubuntu-22.04"}, nil, false)
	if v.WSL != "Ubuntu-22.04" {
		t.Fatalf("wsl = %q", v.WSL)
	}
	plain := g.sessionViewOf(sessions.Session{Agent: "codex", ID: "y", Title: "t"}, nil, false)
	if plain.WSL != "" {
		t.Fatalf("本机会话不应带 wsl: %q", plain.WSL)
	}
}
