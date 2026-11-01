package cursor

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCursorAuthFromDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"auth0|user_abc123","exp":1999999999}`))
	token := "eyJhbGciOiJub25lIn0." + payload + ".sig"
	for k, v := range map[string]string{
		"cursorAuth/accessToken":          token,
		"cursorAuth/refreshToken":         "rt-1",
		"cursorAuth/cachedEmail":          "dev@example.com",
		"cursorAuth/stripeMembershipType": "pro",
	} {
		if _, err := db.Exec("INSERT INTO ItemTable(key,value) VALUES(?,?)", k, v); err != nil {
			t.Fatalf("insert %s: %v", k, err)
		}
	}
	db.Close()

	d, err := readCursorAuth(path)
	if err != nil {
		t.Fatalf("readCursorAuth: %v", err)
	}
	if d.Credentials["access_token"] != token || d.Credentials["refresh_token"] != "rt-1" {
		t.Fatalf("凭证不对: %+v", d.Credentials)
	}
	if d.Credentials["email"] != "dev@example.com" || d.Credentials["membership"] != "pro" {
		t.Fatalf("资料不对: %+v", d.Credentials)
	}
	if d.Credentials["auth_id"] != "user_abc123" {
		t.Fatalf("auth_id 应取 JWT sub: %+v", d.Credentials["auth_id"])
	}
	if !strings.Contains(d.Label, "dev@example.com") {
		t.Fatalf("label = %q", d.Label)
	}
}

func TestQuotaFromUsage(t *testing.T) {
	root := map[string]any{
		"membershipType": "pro",
		"plan": map[string]any{
			"used": 37.0, "limit": 100.0,
			"totalPercentUsed": 37.0, "autoPercentUsed": 12.0, "apiPercentUsed": 3.0,
		},
	}
	q := quotaFromUsage(root)
	if q.Plan != "pro" || q.Total != 100 || q.Used != 37 || q.Remaining != 63 {
		t.Fatalf("总额度不对: %+v", q)
	}
	if len(q.Windows) != 3 || q.Windows[0].Name != "Total Usage" || q.Windows[1].Used != 12 {
		t.Fatalf("窗口不对: %+v", q.Windows)
	}
}

func TestQuotaFromNestedUsage(t *testing.T) {
	root := map[string]any{
		"individualUsage": map[string]any{
			"plan": map[string]any{"used": "3", "limit": "10"},
		},
	}
	q := quotaFromUsage(root)
	if len(q.Windows) != 1 || q.Windows[0].Used != 30 {
		t.Fatalf("嵌套用量不对: %+v", q)
	}
}

func TestSessionCookieAndJWT(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"sub": "auth0|user_xyz"})
	token := "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s"
	if got := jwtSubject(token); got != "user_xyz" {
		t.Fatalf("sub = %q", got)
	}
	cookie := sessionCookie(token)
	if !strings.Contains(cookie, "WorkosCursorSessionToken=user_xyz%3A%3A") {
		t.Fatalf("cookie = %q", cookie)
	}
	if jwtSubject("not-a-jwt") != "" {
		t.Fatal("非法 JWT 应返回空")
	}
}
