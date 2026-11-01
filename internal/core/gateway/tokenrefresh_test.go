package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

func TestParseUnixValue(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"1700000000", 1700000000},
		{"1700000000000", 1700000000},
		{"2030-01-02T03:04:05Z", 1893553445},
	}
	for _, c := range cases {
		if got := parseUnixValue(c.in); got != c.want {
			t.Errorf("parseUnixValue(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// refreshStub 是只实现刷新能力的测试渠道。
type refreshStub struct {
	id    string
	calls int
	fail  bool
}

func (p *refreshStub) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:            p.id,
		DisplayName:   "Refresh Stub",
		NetworkPolicy: provider.NetPolicySystem,
		Capabilities:  provider.CapabilitySet{Refresh: true},
	}
}

func (p *refreshStub) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	p.calls++
	if p.fail {
		return nil, errors.New("refresh failed")
	}
	expires := time.Now().Add(2 * time.Hour).Unix()
	return &provider.CredentialDraft{
		Credentials: map[string]string{
			"access_token": "new-token",
			"expires_at":   time.Unix(expires, 0).Format(time.RFC3339),
		},
		ExpiresAt: expires,
	}, nil
}

func TestRefreshExpiringTokens(t *testing.T) {
	spec.SetDefault(spec.NewStore())
	t.Cleanup(func() { spec.SetDefault(spec.NewStore()) })

	stub := &refreshStub{id: "test-refresh-ok"}
	provider.Register(stub)

	g := New(Config{Addr: "127.0.0.1:0"})
	env := provider.NewAccountEnv("test-refresh-account", "test-refresh-ok")
	env.Credentials = map[string]string{
		"access_token": "old-token",
		"expires_at":   time.Now().Add(5 * time.Minute).Format(time.RFC3339),
	}
	g.Pool().Add(env)

	g.refreshExpiringTokens(context.Background(), 30*time.Minute)

	if stub.calls != 1 {
		t.Fatalf("刷新调用次数 = %d", stub.calls)
	}
	if got := env.GetCredential("access_token"); got != "new-token" {
		t.Fatalf("access_token = %q", got)
	}
	if st := env.State(); st != provider.StateReady {
		t.Fatalf("state = %s", st)
	}
}

func TestRefreshFailureRestoresState(t *testing.T) {
	spec.SetDefault(spec.NewStore())
	t.Cleanup(func() { spec.SetDefault(spec.NewStore()) })

	stub := &refreshStub{id: "test-refresh-fail-provider", fail: true}
	provider.Register(stub)

	g := New(Config{Addr: "127.0.0.1:0"})
	env := provider.NewAccountEnv("test-refresh-fail", "test-refresh-fail-provider")
	env.Credentials = map[string]string{
		"access_token": "old-token",
		"expires_at":   time.Now().Add(5 * time.Minute).Format(time.RFC3339),
	}
	g.Pool().Add(env)

	g.refreshExpiringTokens(context.Background(), 30*time.Minute)

	if st := env.State(); st != provider.StateReady {
		t.Fatalf("临时失败不应改变原状态，state = %s", st)
	}
}
