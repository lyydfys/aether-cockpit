package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// vertexDraft 是编辑器为 Vertex AI 提交的字段（magpie 同名）。
type vertexDraft struct {
	Project     string `json:"project"`
	Location    string `json:"location"`
	Credentials string `json:"credentials"`
	Impersonate string `json:"impersonate"`
}

type adcFile struct {
	Type         string `json:"type"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	TokenURI     string `json:"token_uri"`
}

// vertexCredentials 把编辑器草稿转成账号凭证：project / location / ADC 内容。
// 凭据文件留空时读 gcloud 默认 ADC；文件不存在不算错（更新账号时沿用旧凭据），
// 新账号会在保存处再校验必须有 adc_json。
func vertexCredentials(d *providerDraft) (map[string]string, string) {
	v := d.Vertex
	if v == nil {
		return nil, "缺少 Vertex 配置（project / location / 凭据）"
	}
	project := strings.TrimSpace(v.Project)
	if project == "" {
		return nil, "Vertex 需要 Google Cloud 项目 ID"
	}
	if strings.TrimSpace(v.Impersonate) != "" {
		return nil, "Vertex 服务账号模拟（impersonate）暂不支持；请直接提供可用的 ADC 凭据文件"
	}
	location := strings.TrimSpace(v.Location)
	if location == "" {
		location = "global"
	}
	creds := map[string]string{"project": project, "location": location}
	if base := trimEndpoint(d.Chat); base != "" {
		// 自建 Vertex 兼容端点 / 测试假上游。
		creds["base_url"] = base
	}

	path := strings.TrimSpace(v.Credentials)
	resolved := path
	if resolved == "" {
		if home, err := os.UserHomeDir(); err == nil {
			resolved = filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
		}
	}
	if resolved == "" {
		return creds, ""
	}
	if strings.HasPrefix(resolved, "~/") || strings.HasPrefix(resolved, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			resolved = filepath.Join(home, resolved[2:])
		}
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		if path == "" {
			// 默认 ADC 不存在：允许更新账号时沿用已存凭据。
			return creds, ""
		}
		return nil, "读取 ADC 凭据失败: " + err.Error()
	}
	if len(raw) > 1<<20 {
		return nil, "ADC 凭据文件过大"
	}
	var adc adcFile
	if err := json.Unmarshal(raw, &adc); err != nil {
		return nil, "ADC 凭据不是合法 JSON: " + err.Error()
	}
	if strings.TrimSpace(adc.Type) != "authorized_user" || adc.RefreshToken == "" || adc.ClientID == "" || adc.ClientSecret == "" {
		return nil, "仅支持 gcloud application_default_credentials.json（authorized_user）；service account 暂不支持"
	}
	creds["adc_json"] = string(raw)
	creds["credentials_path"] = resolved
	return creds, ""
}
