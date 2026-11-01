package antigravity

import (
	"errors"
	"strings"
	"testing"
)

func TestRefreshHint(t *testing.T) {
	for _, in := range []string{
		`Post "https://oauth2.googleapis.com/token": EOF`,
		"dial tcp: i/o timeout",
		"no such host",
	} {
		if !strings.Contains(refreshHint(errors.New(in)), "全局代理") {
			t.Errorf("%q 应带代理提示", in)
		}
	}
	if got := refreshHint(errors.New("invalid_grant")); got != "" {
		t.Errorf("非网络错误不应加提示: %q", got)
	}
}
