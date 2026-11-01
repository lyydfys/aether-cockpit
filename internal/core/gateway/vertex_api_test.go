package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVertexCredentialsReadsADC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "adc.json")
	raw := `{"type":"authorized_user","client_id":"cid","client_secret":"cs","refresh_token":"rt"}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	creds, problem := vertexCredentials(&providerDraft{
		ID:     "vertex",
		Vertex: &vertexDraft{Project: "proj", Credentials: path},
	})
	if problem != "" {
		t.Fatalf("problem = %q", problem)
	}
	if creds["project"] != "proj" || creds["location"] != "global" || creds["adc_json"] == "" {
		t.Errorf("creds = %+v", creds)
	}
	if creds["credentials_path"] != path {
		t.Errorf("credentials_path = %q", creds["credentials_path"])
	}

	if _, msg := vertexCredentials(&providerDraft{Vertex: &vertexDraft{Credentials: path}}); !strings.Contains(msg, "项目") {
		t.Errorf("missing project msg = %q", msg)
	}
	if _, msg := vertexCredentials(&providerDraft{Vertex: &vertexDraft{Project: "p", Credentials: path, Impersonate: "sa@p.iam"}}); !strings.Contains(msg, "暂不支持") {
		t.Errorf("impersonate msg = %q", msg)
	}

	sa := filepath.Join(dir, "sa.json")
	_ = os.WriteFile(sa, []byte(`{"type":"service_account"}`), 0o600)
	if _, msg := vertexCredentials(&providerDraft{Vertex: &vertexDraft{Project: "p", Credentials: sa}}); !strings.Contains(msg, "authorized_user") {
		t.Errorf("service account msg = %q", msg)
	}
}
