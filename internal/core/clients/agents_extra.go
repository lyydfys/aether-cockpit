package clients

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/tidwall/jsonc"
)

// 本文件补两类单文件 JSON 客户端（schema 取自 aether 的实现，2026-10 核实）：
//
//   - Qwen Code（Gemini CLI fork）：~/.qwen/settings.json（$QWEN_HOME 覆盖）
//     modelProviders.openai[] 里每项 {id,name,baseUrl,envKey}，密钥放在
//     env.AETHER_QWEN_API_KEY，security.auth.selectedType=openai，
//     model.{name,baseUrl} 指定默认模型。
//   - Droid（Factory）：~/.factory/settings.json（$FACTORY_HOME_OVERRIDE 覆盖家目录）
//     customModels[] 里每项 {model,id,displayName,baseUrl,apiKey,provider,...}，
//     id 前缀 custom:，sessionDefaultSettings.model 指定默认模型。

const qwenEnvKey = "AETHER_QWEN_API_KEY"

const droidModelIDPrefix = "custom:aether/"

func init() {
	defs = append(defs,
		clientDef{
			id:      "qwen_code",
			name:    "Qwen Code",
			kind:    KindJSON,
			relPath: filepath.Join(".qwen", "settings.json"),
			note:    "写入 ~/.qwen/settings.json（$QWEN_HOME 优先）：modelProviders.openai 增加 aether 条目、env.AETHER_QWEN_API_KEY、security.auth.selectedType=openai；应用后需重启 qwen。带注释的 JSONC 会拒绝改写。",
			apply:   applyQwen,
			managed: managedQwen,
			pathFn: func(home string) string {
				dir := strings.TrimSpace(os.Getenv("QWEN_HOME"))
				if dir == "" {
					dir = filepath.Join(home, ".qwen")
				}
				return filepath.Join(dir, "settings.json")
			},
		},
		clientDef{
			id:      "droid",
			name:    "Droid",
			kind:    KindJSON,
			relPath: filepath.Join(".factory", "settings.json"),
			note:    "写入 ~/.factory/settings.json（$FACTORY_HOME_OVERRIDE 优先）：customModels 增加 aether 自定义模型并把 sessionDefaultSettings.model 指过去；应用后需重启 droid。带注释的 JSONC 会拒绝改写。",
			apply:   applyDroid,
			managed: managedDroid,
			pathFn: func(home string) string {
				root := strings.TrimSpace(os.Getenv("FACTORY_HOME_OVERRIDE"))
				if root == "" {
					root = home
				}
				return filepath.Join(root, ".factory", "settings.json")
			},
		},
	)
}

// modelRows 把 ApplyParams 折成 (id, label) 列表：优先 ModelOptions，
// Model 非空时保证它也在列表里。
func modelRows(p ApplyParams) [][2]string {
	out := make([][2]string, 0, len(p.ModelOptions)+1)
	seen := map[string]bool{}
	for _, o := range p.ModelOptions {
		v := strings.TrimSpace(o.Value)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		label := strings.TrimSpace(o.Label)
		if label == "" {
			label = v
		}
		out = append(out, [2]string{v, label})
	}
	if m := strings.TrimSpace(p.Model); m != "" && !seen[m] {
		out = append(out, [2]string{m, m})
	}
	return out
}

func applyQwen(content []byte, p ApplyParams) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(content))) > 0 {
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, fmt.Errorf("现有 qwen settings.json 不是合法 JSON（可能含注释，JSONC 无法安全合并），拒绝改写: %w", err)
		}
	}
	env, _ := root["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env[qwenEnvKey] = p.APIKey
	root["env"] = env

	providers, _ := root["modelProviders"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	list, _ := providers["openai"].([]any)
	kept := make([]any, 0, len(list)+len(p.ModelOptions)+1)
	for _, e := range list {
		m, _ := e.(map[string]any)
		if m != nil {
			if key, _ := m["envKey"].(string); key == qwenEnvKey {
				continue // 上一次注入的自己
			}
		}
		kept = append(kept, e)
	}
	rows := modelRows(p)
	base := p.BaseURL + "/v1"
	for _, r := range rows {
		kept = append(kept, map[string]any{"id": r[0], "name": r[1], "baseUrl": base, "envKey": qwenEnvKey})
	}
	providers["openai"] = kept
	root["modelProviders"] = providers

	sec, _ := root["security"].(map[string]any)
	if sec == nil {
		sec = map[string]any{}
	}
	auth, _ := sec["auth"].(map[string]any)
	if auth == nil {
		auth = map[string]any{}
	}
	auth["selectedType"] = "openai"
	sec["auth"] = auth
	root["security"] = sec

	if m := strings.TrimSpace(p.Model); m != "" {
		root["model"] = map[string]any{"name": m, "baseUrl": base}
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func managedQwen(data []byte) *ManagedInfo {
	var root struct {
		ModelProviders struct {
			OpenAI []struct {
				ID     string `json:"id"`
				Base   string `json:"baseUrl"`
				EnvKey string `json:"envKey"`
			} `json:"openai"`
		} `json:"modelProviders"`
		Model struct {
			Name string `json:"name"`
		} `json:"model"`
	}
	if json.Unmarshal(data, &root) != nil {
		return nil
	}
	for _, e := range root.ModelProviders.OpenAI {
		if e.EnvKey == qwenEnvKey {
			return &ManagedInfo{BaseURL: e.Base, Model: root.Model.Name}
		}
	}
	return nil
}

func applyDroid(content []byte, p ApplyParams) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(content))) > 0 {
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, fmt.Errorf("现有 droid settings.json 不是合法 JSON（可能含注释，JSONC 无法安全合并），拒绝改写: %w", err)
		}
	}
	list, _ := root["customModels"].([]any)
	kept := make([]any, 0, len(list)+len(p.ModelOptions)+1)
	for _, e := range list {
		m, _ := e.(map[string]any)
		if m != nil {
			if id, _ := m["id"].(string); strings.HasPrefix(id, droidModelIDPrefix) {
				continue
			}
		}
		kept = append(kept, e)
	}
	rows := modelRows(p)
	base := p.BaseURL + "/v1"
	for _, r := range rows {
		kept = append(kept, map[string]any{
			"model":          r[0],
			"id":             droidModelIDPrefix + r[0],
			"displayName":    r[1],
			"baseUrl":        base,
			"apiKey":         p.APIKey,
			"provider":       "generic-chat-completion-api",
			"noImageSupport": true,
		})
	}
	root["customModels"] = kept

	if m := strings.TrimSpace(p.Model); m != "" {
		sess, _ := root["sessionDefaultSettings"].(map[string]any)
		if sess == nil {
			sess = map[string]any{}
		}
		sess["model"] = droidModelIDPrefix + m
		root["sessionDefaultSettings"] = sess
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func managedDroid(data []byte) *ManagedInfo {
	var root struct {
		CustomModels []struct {
			Model string `json:"model"`
			ID    string `json:"id"`
			Base  string `json:"baseUrl"`
		} `json:"customModels"`
		SessionDefaultSettings struct {
			Model string `json:"model"`
		} `json:"sessionDefaultSettings"`
	}
	if json.Unmarshal(data, &root) != nil {
		return nil
	}
	for _, e := range root.CustomModels {
		if strings.HasPrefix(e.ID, droidModelIDPrefix) {
			model := strings.TrimPrefix(root.SessionDefaultSettings.Model, droidModelIDPrefix)
			return &ManagedInfo{BaseURL: e.Base, Model: model}
		}
	}
	return nil
}

// Kimi Code 的配置是 config.toml，官方客户端会读 [providers.<name>] 与
// [models."<key>"] 两张表，default_model 指向 model 表的键。这里做行级
// 表替换（保留用户的注释与其它表），不用 TOML 库重排整个文件。
const (
	kimiProviderTable = "[providers.aether]"
	kimiModelPrefix   = `[models."aether/`
	kimiContext       = 128000
)

func kimiConfigPath(home string) string {
	if d := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME")); d != "" {
		return filepath.Join(d, "config.toml")
	}
	code := filepath.Join(home, ".kimi-code")
	if st, err := os.Stat(code); err == nil && st.IsDir() {
		return filepath.Join(code, "config.toml")
	}
	if d := strings.TrimSpace(os.Getenv("KIMI_SHARE_DIR")); d != "" {
		return filepath.Join(d, "config.toml")
	}
	legacy := filepath.Join(home, ".kimi")
	if st, err := os.Stat(legacy); err == nil && st.IsDir() {
		return filepath.Join(legacy, "config.toml")
	}
	return filepath.Join(code, "config.toml")
}

func init() {
	defs = append(defs, clientDef{
		id:      "kimi_code",
		name:    "Kimi Code",
		kind:    KindTOML,
		relPath: filepath.Join(".kimi-code", "config.toml"),
		note:    "写入 config.toml 的 [providers.aether] 与 [models.\"aether/*\"] 表，并把 default_model 指过去；$KIMI_CODE_HOME / $KIMI_SHARE_DIR / ~/.kimi 依次探测。应用后需重启 kimi。",
		apply:   applyKimi,
		managed: managedKimi,
		pathFn:  kimiConfigPath,
	})
}

// applyKimi 行级替换托管表：删除旧的 [providers.aether] 与所有
// [models."aether/…"] 表，再追加新表；顶层 default_model 一并替换。
func applyKimi(content []byte, p ApplyParams) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines)+16)
	skip := false
	seenSection := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		isHeader := strings.HasPrefix(t, "[")
		if isHeader {
			seenSection = true
			skip = t == kimiProviderTable || strings.HasPrefix(t, kimiModelPrefix)
		}
		if skip {
			continue
		}
		if !seenSection && p.Model != "" && strings.HasPrefix(t, "default_model") {
			continue
		}
		out = append(out, line)
	}
	// 去掉尾部空行，稍后统一补一个空行。
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}

	base := strings.TrimRight(p.BaseURL, "/")
	rows := modelRows(p)
	if len(rows) == 0 && p.Model != "" {
		rows = [][2]string{{p.Model, p.Model}}
	}
	if p.Model != "" {
		// 顶层键必须放在第一个表头之前，否则会被 TOML 归进上面那张表。
		out = append([]string{"default_model = " + tomlQuote("aether/" + p.Model), ""}, out...)
	}
	out = append(out,
		"",
		kimiProviderTable,
		`type = "kimi"`,
		"base_url = "+tomlQuote(base),
		"api_key = "+tomlQuote(p.APIKey),
	)
	for _, r := range rows {
		out = append(out,
			"",
			`[models."aether/`+r[0]+`"]`,
			`provider = "aether"`,
			"model = "+tomlQuote(r[0]),
			"max_context_size = "+strconv.Itoa(kimiContext),
			`capabilities = ["tool_use"]`,
		)
	}
	res := strings.Join(out, "\n")
	if !strings.HasSuffix(res, "\n") {
		res += "\n"
	}
	return []byte(res), nil
}

func tomlQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// managedKimi 回显 [providers.aether] 的 base_url 与 default_model。
func managedKimi(data []byte) *ManagedInfo {
	var info ManagedInfo
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(t, "[") {
			section = t
			continue
		}
		if section == "" && strings.HasPrefix(t, "default_model") {
			if i := strings.Index(t, "="); i > 0 {
				info.Model = strings.Trim(strings.TrimSpace(t[i+1:]), `"`)
			}
		}
		if section == kimiProviderTable && strings.HasPrefix(t, "base_url") {
			if i := strings.Index(t, "="); i > 0 {
				info.BaseURL = strings.Trim(strings.TrimSpace(t[i+1:]), `"`)
			}
		}
	}
	if info.BaseURL == "" {
		return nil
	}
	return &info
}

// Cline：providers.json 是 CLI/新版扩展读的配置；已安装扩展时还有
// globalState.json（选择器状态）与 secrets.json（Key 密文存储）。
// 三者都写，各自留备份；扩展文件不存在时只写 providers.json。
const clineSlot = "openai-compatible"

func clineDataDir(home string) string {
	dir := strings.TrimSpace(os.Getenv("CLINE_DIR"))
	if dir == "" {
		dir = filepath.Join(home, ".cline")
	}
	if d := strings.TrimSpace(os.Getenv("CLINE_DATA_DIR")); d != "" {
		return d
	}
	return filepath.Join(dir, "data")
}

func init() {
	defs = append(defs, clientDef{
		id:      "cline",
		name:    "Cline",
		kind:    KindJSON,
		relPath: filepath.Join(".cline", "data", "settings", "providers.json"),
		note:    "写入 providers.json 的 aether（openai-compatible）条目；装了扩展时一并更新 globalState.json / secrets.json（各自备份）。应用后重载窗口或重启 cline。",
		apply:   applyClineProviders,
		managed: managedCline,
		pathFn: func(home string) string {
			return filepath.Join(clineDataDir(home), "settings", "providers.json")
		},
		aux: []auxFile{
			{
				onlyIfExists: true,
				pathFn: func(home string) string {
					return filepath.Join(clineDataDir(home), "globalState.json")
				},
				render: applyClineState,
			},
			{
				onlyIfExists: true,
				pathFn: func(home string) string {
					return filepath.Join(clineDataDir(home), "secrets.json")
				},
				render: applyClineSecrets,
			},
		},
	})
}

// applyClineProviders 合并写 providers.json：只改 aether 槽位与 lastUsedProvider。
func applyClineProviders(content []byte, p ApplyParams) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(content))) > 0 {
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, fmt.Errorf("现有 Cline providers.json 不是合法 JSON: %w", err)
		}
	}
	providers, _ := root["providers"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	entry, _ := providers[clineSlot].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	settings, _ := entry["settings"].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	settings["apiKey"] = p.APIKey
	settings["baseUrl"] = p.BaseURL + "/v1"
	if m := strings.TrimSpace(p.Model); m != "" {
		settings["model"] = m
	}
	entry["settings"] = settings
	providers[clineSlot] = entry
	root["providers"] = providers
	root["lastUsedProvider"] = clineSlot
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// applyClineState 同步扩展状态（存在才写）：provider/openAiBaseUrl/模型 id。
func applyClineState(content []byte, p ApplyParams) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(content))) > 0 {
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, fmt.Errorf("现有 Cline globalState.json 不是合法 JSON: %w", err)
		}
	}
	root["actModeApiProvider"] = "openai"
	root["planModeApiProvider"] = "openai"
	root["openAiBaseUrl"] = p.BaseURL + "/v1"
	root["openAiHeaders"] = map[string]any{"User-Agent": "cline"}
	if m := strings.TrimSpace(p.Model); m != "" {
		root["actModeOpenAiModelId"] = m
		root["planModeOpenAiModelId"] = m
	}
	delete(root, "actModeOpenAiModelInfo")
	delete(root, "planModeOpenAiModelInfo")
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// applyClineSecrets 把 Key 写进扩展的 secrets.json（存在才写）。
func applyClineSecrets(content []byte, p ApplyParams) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(content))) > 0 {
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, fmt.Errorf("现有 Cline secrets.json 不是合法 JSON: %w", err)
		}
	}
	root["openAiApiKey"] = p.APIKey
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func managedCline(data []byte) *ManagedInfo {
	var root struct {
		Providers map[string]struct {
			Settings struct {
				BaseURL string `json:"baseUrl"`
				Model   string `json:"model"`
			} `json:"settings"`
		} `json:"providers"`
	}
	if json.Unmarshal(data, &root) != nil {
		return nil
	}
	entry, ok := root.Providers[clineSlot]
	if !ok || entry.Settings.BaseURL == "" {
		return nil
	}
	return &ManagedInfo{BaseURL: entry.Settings.BaseURL, Model: entry.Settings.Model}
}

// Goose：自定义 provider 是 JSON（custom_providers/aether.json），
// 当前选择在 config.yaml（active_provider + providers.aether）。YAML 用
// 行级编辑（保留注释与其它 provider），不引入新依赖。
const gooseProviderID = "aether"

func gooseDir(home string) string {
	if d := strings.TrimSpace(os.Getenv("GOOSE_CONFIG_DIR")); d != "" {
		return d
	}
	return filepath.Join(home, ".config", "goose")
}

func init() {
	defs = append(defs, clientDef{
		id:      "goose",
		name:    "Goose",
		kind:    KindJSON,
		relPath: filepath.Join(".config", "goose", "custom_providers", gooseProviderID+".json"),
		note:    "写入 custom_providers/aether.json（engine=openai）并更新 config.yaml 的 active_provider/providers.aether；其它 provider 与注释保留。应用后重启 goose。",
		apply:   applyGooseProvider,
		managed: managedGoose,
		pathFn: func(home string) string {
			return filepath.Join(gooseDir(home), "custom_providers", gooseProviderID+".json")
		},
		aux: []auxFile{{
			pathFn: func(home string) string { return filepath.Join(gooseDir(home), "config.yaml") },
			render: applyGooseConfig,
		}},
	})
}

// applyGooseProvider 生成 Goose 的自定义 provider 文件。
func applyGooseProvider(_ []byte, p ApplyParams) ([]byte, error) {
	models := make([]any, 0, len(p.ModelOptions)+1)
	seen := map[string]bool{}
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		models = append(models, map[string]any{"name": id, "reasoning": false})
	}
	for _, o := range p.ModelOptions {
		add(strings.TrimSpace(o.Value))
	}
	add(strings.TrimSpace(p.Model))
	doc := map[string]any{
		"name":                     gooseProviderID,
		"engine":                   "openai",
		"display_name":             "Aether Cockpit",
		"description":              "Every model Aether Cockpit has, through its local gateway",
		"api_key_env":              "",
		"base_url":                 p.BaseURL + "/v1",
		"headers":                  map[string]any{"Authorization": "Bearer " + p.APIKey},
		"requires_auth":            false,
		"skip_canonical_filtering": true,
		"models":                   models,
	}
	if len(models) > 0 {
		doc["dynamic_models"] = false
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// applyGooseConfig 行级更新 config.yaml：
//   - 顶层 active_provider 换成 aether；
//   - providers.aether 子块替换为 enabled/model/configured；
//   - 其余内容（注释、其它 provider）原样保留。
func applyGooseConfig(content []byte, p ApplyParams) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines)+8)
	hasActive := false
	inProviders := false
	skipAether := false
	insertedAether := false
	providersIndent := 0

	var aetherBlock []string
	aetherBlock = append(aetherBlock, "  "+gooseProviderID+":")
	if m := strings.TrimSpace(p.Model); m != "" {
		aetherBlock = append(aetherBlock, "    model: "+yamlScalar(m))
	}
	aetherBlock = append(aetherBlock, "    enabled: true", "    configured: true")

	flushAether := func() {
		if inProviders && !insertedAether {
			out = append(out, aetherBlock...)
			insertedAether = true
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if skipAether {
			// 跳过对 aether 子块内部的缩进行，直到回到同级或更浅。
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || indent > providersIndent+2 {
				continue
			}
			skipAether = false
		}
		if inProviders && indent <= providersIndent && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			flushAether()
			inProviders = false
		}
		if strings.HasPrefix(trimmed, "active_provider:") && indent == 0 {
			out = append(out, "active_provider: "+yamlScalar(gooseProviderID))
			hasActive = true
			continue
		}
		if strings.HasPrefix(trimmed, "providers:") && indent == 0 {
			inProviders = true
			providersIndent = indent
			out = append(out, line)
			continue
		}
		if inProviders && indent == 2 && strings.HasPrefix(trimmed, gooseProviderID+":") {
			skipAether = true
			continue
		}
		out = append(out, line)
	}
	flushAether()
	if !hasActive {
		out = append([]string{"active_provider: " + yamlScalar(gooseProviderID)}, out...)
	}
	if !insertedAether {
		out = append(out, "", "providers:", "  "+gooseProviderID+":")
		if m := strings.TrimSpace(p.Model); m != "" {
			out = append(out, "    model: "+yamlScalar(m))
		}
		out = append(out, "    enabled: true", "    configured: true")
	}
	res := strings.Join(out, "\n")
	if !strings.HasSuffix(res, "\n") {
		res += "\n"
	}
	return []byte(res), nil
}

func yamlScalar(s string) string {
	need := s == ""
	if !need && strings.ContainsAny(s, " 	:#{}[],&*?|<>=!%@`\"'") {
		need = true
	}
	if !need && len(s) > 0 && strings.ContainsRune("-?:", rune(s[0])) {
		need = true
	}
	if need {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

func managedGoose(data []byte) *ManagedInfo {
	var doc struct {
		BaseURL string `json:"base_url"`
		Models  []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &doc) != nil || doc.BaseURL == "" {
		return nil
	}
	info := &ManagedInfo{BaseURL: doc.BaseURL}
	if len(doc.Models) > 0 {
		info.Model = doc.Models[0].Name
	}
	return info
}

// CodeBuddy Code（腾讯 CLI，codebuddy/cbc；IDE 同读）支持"自定义模型"：
// ~/.codebuddy/models.json（$CODEBUDDY_CONFIG_DIR 覆盖）里的条目带
// url + apiKey，CLI 直接按该 URL 发 OpenAI Chat 请求、且热加载。
// 把网关模型写进去，CodeBuddy 的对话就落在本网关上，无需逆向它的私有协议。
const codebuddyVendor = "aether"

func codebuddyDir(home string) string {
	if d := strings.TrimSpace(os.Getenv("CODEBUDDY_CONFIG_DIR")); d != "" {
		return d
	}
	return filepath.Join(home, ".codebuddy")
}

func init() {
	defs = append(defs, clientDef{
		id:      "codebuddy",
		name:    "CodeBuddy",
		kind:    KindJSON,
		relPath: filepath.Join(".codebuddy", "models.json"),
		note:    "写入 ~/.codebuddy/models.json（$CODEBUDDY_CONFIG_DIR 优先）的自定义模型：CodeBuddy CLI/IDE 会按 url 直接请求本网关并热加载；存在 settings.json 时把默认 model 指过去。",
		apply:   applyCodebuddyModels,
		managed: managedCodebuddy,
		pathFn: func(home string) string {
			return filepath.Join(codebuddyDir(home), "models.json")
		},
		aux: []auxFile{{
			onlyIfExists: true,
			pathFn: func(home string) string {
				return filepath.Join(codebuddyDir(home), "settings.json")
			},
			render: applyCodebuddySettings,
		}},
	})
}

// codebuddyModel 是 models.json 里的一条自定义模型。
func codebuddyModels(p ApplyParams) []any {
	rows := modelRows(p)
	if len(rows) == 0 && p.Model != "" {
		rows = [][2]string{{p.Model, p.Model}}
	}
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"id":              r[0],
			"name":            r[1],
			"vendor":          codebuddyVendor,
			"apiKey":          p.APIKey,
			"url":             p.BaseURL + "/v1/chat/completions",
			"supportsToolCall": true,
		})
	}
	return out
}

// applyCodebuddyModels 合并写 models.json：支持顶层数组与 {models,…} 两种形态，
// 只替换 vendor=aether 的条目，其余原样保留。
func applyCodebuddyModels(content []byte, p ApplyParams) ([]byte, error) {
	ours := codebuddyModels(p)
	trimmed := strings.TrimSpace(string(content))
	var root map[string]any
	var list []any
	isArray := false
	if trimmed == "" {
		root = map[string]any{}
	} else if strings.HasPrefix(trimmed, "[") {
		isArray = true
		if err := json.Unmarshal(content, &list); err != nil {
			return nil, fmt.Errorf("现有 CodeBuddy models.json 不是合法 JSON: %w", err)
		}
	} else if err := json.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("现有 CodeBuddy models.json 不是合法 JSON: %w", err)
	}
	if !isArray {
		if raw, ok := root["models"].([]any); ok {
			list = raw
		}
	}
	kept := make([]any, 0, len(list)+len(ours))
	ids := make([]string, 0, len(ours))
	for _, e := range list {
		m, _ := e.(map[string]any)
		if m != nil {
			if v, _ := m["vendor"].(string); v == codebuddyVendor {
				continue
			}
		}
		kept = append(kept, e)
	}
	for _, e := range ours {
		kept = append(kept, e)
		if m, _ := e.(map[string]any); m != nil {
			if id, _ := m["id"].(string); id != "" {
				ids = append(ids, id)
			}
		}
	}
	var out any
	if isArray {
		out = kept
	} else {
		root["models"] = kept
		if raw, ok := root["availableModels"].([]any); ok {
			avail := make([]any, 0, len(raw)+len(ids))
			for _, v := range raw {
				if s, _ := v.(string); s != "" && strings.HasPrefix(s, "aether") {
					continue
				}
				avail = append(avail, v)
			}
			for _, id := range ids {
				avail = append(avail, id)
			}
			root["availableModels"] = avail
		}
		out = root
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// applyCodebuddySettings 把 CLI 的默认 model 指向我们的第一个模型（存在才写）。
func applyCodebuddySettings(content []byte, p ApplyParams) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(content))) > 0 {
		if err := json.Unmarshal(content, &root); err != nil {
			return nil, fmt.Errorf("现有 CodeBuddy settings.json 不是合法 JSON: %w", err)
		}
	}
	rows := modelRows(p)
	if len(rows) == 0 && p.Model != "" {
		rows = [][2]string{{p.Model, p.Model}}
	}
	if len(rows) > 0 {
		root["model"] = rows[0][0]
	}
	raw, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func managedCodebuddy(data []byte) *ManagedInfo {
	var list []struct {
		ID     string `json:"id"`
		Vendor string `json:"vendor"`
		URL    string `json:"url"`
	}
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		var wrapper struct {
			Models []struct {
				ID     string `json:"id"`
				Vendor string `json:"vendor"`
				URL    string `json:"url"`
			} `json:"models"`
		}
		if json.Unmarshal(data, &wrapper) != nil {
			return nil
		}
		list = wrapper.Models
	} else if json.Unmarshal(data, &list) != nil {
		return nil
	}
	for _, m := range list {
		if m.Vendor == codebuddyVendor && m.URL != "" {
			return &ManagedInfo{BaseURL: strings.TrimSuffix(m.URL, "/chat/completions"), Model: m.ID}
		}
	}
	return nil
}

// WorkBuddy 桌面端（CN `~/.workbuddy`、Intl `~/.workbuddy-ai`，可用
// WORKBUDDY_CONFIG_DIR 覆盖）与 CodeBuddy 共用 models.json 的自定义模型格式：
// 条目带 url + apiKey，客户端按该 URL 发 OpenAI Chat 请求，模型在任务窗口里选。
// 写的文件与 CodeBuddy 同构，直接复用其合并逻辑。
func workbuddyModelsPath(home, folder string) string {
	if d := strings.TrimSpace(os.Getenv("WORKBUDDY_CONFIG_DIR")); d != "" {
		return filepath.Join(d, "models.json")
	}
	return filepath.Join(home, folder, "models.json")
}

func init() {
	defs = append(defs,
		clientDef{
			id:      "workbuddy",
			name:    "WorkBuddy",
			kind:    KindJSON,
			relPath: filepath.Join(".workbuddy", "models.json"),
			note:    "写入 ~/.workbuddy/models.json 的自定义模型（与 CodeBuddy 同格式）：WorkBuddy 会按 url 请求本网关，模型在任务窗口里选。",
			apply:   applyCodebuddyModels,
			managed: managedCodebuddy,
			pathFn:  func(home string) string { return workbuddyModelsPath(home, ".workbuddy") },
		},
		clientDef{
			id:      "workbuddy-ai",
			name:    "WorkBuddy AI",
			kind:    KindJSON,
			relPath: filepath.Join(".workbuddy-ai", "models.json"),
			note:    "写入 ~/.workbuddy-ai/models.json（国际版）的自定义模型；格式与 WorkBuddy/CodeBuddy 相同。",
			apply:   applyCodebuddyModels,
			managed: managedCodebuddy,
			pathFn:  func(home string) string { return workbuddyModelsPath(home, ".workbuddy-ai") },
		},
	)
}

// Grok Build（xAI 的 grok CLI）：~/.grok/config.toml（$GROK_HOME 覆盖）。
// 用户/目录模型写在 [model."<id>"] 表；新会话默认模型是 [models] default。
// 另有一个必须处理的点：登录后 xAI 会下发 campaign 补丁覆盖 models.default，
// 所以设置默认模型时要同时写 [features] campaigns = false，否则新的默认值
// 会被 campaign 顶掉（aether 的 grok.go 记录了 2026-09 的 launch campaign）。
const grokModelTablePrefix = `[model."aether/`

func init() {
	defs = append(defs, clientDef{
		id:      "grok",
		name:    "Grok CLI",
		kind:    KindTOML,
		relPath: filepath.Join(".grok", "config.toml"),
		note:    "写入 [model.\"aether/*\"] 与 [models] default，并关闭 xAI 的 campaign 默认模型覆盖；其余表与注释保留。应用后重启 grok。",
		apply:   applyGrok,
		managed: managedGrok,
		pathFn: func(home string) string {
			dir := strings.TrimSpace(os.Getenv("GROK_HOME"))
			if dir == "" {
				dir = filepath.Join(home, ".grok")
			}
			return filepath.Join(dir, "config.toml")
		},
	})
}

// tomlUpsertTableKey 在指定表里设置 key=value；表不存在则追加。
func tomlUpsertTableKey(lines []string, table, key, value string) []string {
	header := "[" + table + "]"
	section := ""
	insertAt := -1
	done := false
	out := make([]string, 0, len(lines)+4)
	for _, line := range lines {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(t, "[") {
			if !done && insertAt == len(out) {
				out = append(out, key+" = "+value)
				done = true
			}
			section = t
		}
		if !done && section == header {
			if eq := strings.Index(t, "="); eq > 0 && strings.TrimSpace(t[:eq]) == key {
				out = append(out, key+" = "+value)
				done = true
				continue
			}
			insertAt = len(out) + 1 // 表头之后、下一行之前
		}
		out = append(out, line)
	}
	if !done {
		if insertAt >= 0 && insertAt <= len(out) {
			res := make([]string, 0, len(out)+1)
			res = append(res, out[:insertAt]...)
			res = append(res, key+" = "+value)
			res = append(res, out[insertAt:]...)
			return res
		}
		out = append(out, "", header, key+" = "+value)
	}
	return out
}

// applyGrok 行级替换托管表，再更新 [models]/[features]。
func applyGrok(content []byte, p ApplyParams) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines)+16)
	section := ""
	skipSection := false
	for _, line := range lines {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(t, "[") {
			section = t
			skipSection = strings.HasPrefix(t, grokModelTablePrefix)
			if skipSection {
				continue
			}
		}
		if skipSection {
			continue
		}
		// 只替换托管的两个键；[models]/[features] 里用户的其它键原样保留。
		if eq := strings.Index(t, "="); eq > 0 {
			key := strings.TrimSpace(t[:eq])
			if (section == "[models]" && key == "default") || (section == "[features]" && key == "campaigns") {
				continue
			}
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}

	base := strings.TrimRight(p.BaseURL, "/")
	rows := modelRows(p)
	if len(rows) == 0 && p.Model != "" {
		rows = [][2]string{{p.Model, p.Model}}
	}
	if len(rows) > 0 {
		out = tomlUpsertTableKey(out, "models", "default", tomlQuote("aether/"+rows[0][0]))
	}
	out = tomlUpsertTableKey(out, "features", "campaigns", "false")
	for _, r := range rows {
		out = append(out,
			"",
			`[model."aether/`+r[0]+`"]`,
			"model = "+tomlQuote(r[0]),
			"name = "+tomlQuote(r[1]),
			"base_url = "+tomlQuote(base+"/v1"),
			"api_key = "+tomlQuote(p.APIKey),
			"context_window = 128000",
		)
	}
	res := strings.Join(out, "\n")
	if !strings.HasSuffix(res, "\n") {
		res += "\n"
	}
	return []byte(res), nil
}

func managedGrok(data []byte) *ManagedInfo {
	var info ManagedInfo
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if strings.HasPrefix(t, "[") {
			section = t
			continue
		}
		eq := strings.Index(t, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(t[:eq])
		val := strings.Trim(strings.TrimSpace(t[eq+1:]), `"`)
		if section == "[models]" && key == "default" {
			info.Model = val
		}
		if section == `[model."aether/`+strings.TrimPrefix(info.Model, "aether/")+`"]` && key == "base_url" {
			info.BaseURL = val
		}
	}
	if info.BaseURL == "" {
		return nil
	}
	return &info
}

// VS Code（内置 Copilot Chat）自 1.122 起支持 Custom Endpoint：模型组写在
// settings.json 旁的 chatLanguageModels.json（JSONC 数组，热加载）。
// 组里的 apiKey 不能写文件（VS Code 只认自己的 secret storage），所以每个
// 模型用 requestHeaders.Authorization 带上网关 Key。这里只写这个文件，
// 不动 settings.json（避免破坏用户的 JSONC 注释）；默认模型在 Chat 的
// 模型选择器里选，VS Code 自己记在 storage 里。
const vscodeGroupName = "aether"

func vscodeUserDir(home string) string {
	if d := strings.TrimSpace(os.Getenv("VSCODE_USER_DIR")); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		base := strings.TrimSpace(os.Getenv("APPDATA"))
		if base == "" {
			base = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(base, "Code", "User")
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Code", "User")
	}
	return filepath.Join(home, ".config", "Code", "User")
}

func init() {
	defs = append(defs,
		clientDef{
			id:      "vscode",
			name:    "VS Code",
			kind:    KindJSON,
			relPath: filepath.Join("Code", "User", "chatLanguageModels.json"),
			note:    "写入 VS Code 内置 Copilot Chat 的 Custom Endpoint 模型组（chatLanguageModels.json，无需 GitHub 登录/套餐）；在 Chat 的模型选择器里选 aether 模型。文件以标准 JSON 重写（注释会丢）。",
			apply:   applyVSCode,
			managed: managedVSCode,
			pathFn: func(home string) string {
				return filepath.Join(vscodeUserDir(home), "chatLanguageModels.json")
			},
		},
		clientDef{
			id:      "copilot-jetbrains",
			name:    "Copilot (JetBrains)",
			kind:    KindJSON,
			relPath: filepath.Join(".config", "github-copilot", "byok.json"),
			note:    "写入 Copilot 语言服务器的 byok.json（JetBrains/Xcode/Eclipse 共用）：新增 aether provider 与模型；需要 IDE 已用 Copilot 账号登录并重启 IDE。",
			apply:   applyCopilotJetBrains,
			managed: managedCopilotJetBrains,
			pathFn:  copilotByokPath,
		},
	)
}

// applyVSCode 合并写 chatLanguageModels.json：只替换 name=aether 的组。
func applyVSCode(content []byte, p ApplyParams) ([]byte, error) {
	normalized := content
	if len(strings.TrimSpace(string(content))) > 0 {
		parsed, err := jsoncToJSON(content)
		if err != nil {
			return nil, fmt.Errorf("现有 chatLanguageModels.json 不是合法 JSON/JSONC: %w", err)
		}
		normalized = parsed
	}
	var groups []any
	if len(strings.TrimSpace(string(normalized))) > 0 {
		if err := json.Unmarshal(normalized, &groups); err != nil {
			return nil, fmt.Errorf("chatLanguageModels.json 顶层不是数组: %w", err)
		}
	}
	rows := modelRows(p)
	if len(rows) == 0 && p.Model != "" {
		rows = [][2]string{{p.Model, p.Model}}
	}
	models := make([]any, 0, len(rows))
	for _, r := range rows {
		models = append(models, map[string]any{
			"id": r[0], "name": r[1],
			"url": p.BaseURL + "/v1/chat/completions",
			"toolCalling": true, "vision": true,
			"contextWindow": 128000, "maxOutputTokens": 8192,
			"requestHeaders": map[string]string{"Authorization": "Bearer " + p.APIKey},
		})
	}
	kept := make([]any, 0, len(groups)+1)
	for _, g := range groups {
		if m, _ := g.(map[string]any); m != nil {
			if name, _ := m["name"].(string); name == vscodeGroupName {
				continue
			}
		}
		kept = append(kept, g)
	}
	kept = append(kept, map[string]any{
		"name": vscodeGroupName, "vendor": "customendpoint",
		"apiType": "chat-completions", "models": models,
	})
	out, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func managedVSCode(data []byte) *ManagedInfo {
	var groups []struct {
		Name   string `json:"name"`
		Models []struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &groups) != nil {
		return nil
	}
	for _, g := range groups {
		if g.Name != vscodeGroupName || len(g.Models) == 0 {
			continue
		}
		return &ManagedInfo{
			BaseURL: strings.TrimSuffix(g.Models[0].URL, "/chat/completions"),
			Model:   g.Models[0].ID,
		}
	}
	return nil
}

// copilotByokPath：$XDG_CONFIG_HOME/github-copilot（绝对值时）→ Windows
// %LOCALAPPDATA%/github-copilot → ~/.config/github-copilot。
func copilotByokPath(home string) string {
	if d := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "github-copilot", "byok.json")
	}
	if runtime.GOOS == "windows" {
		base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if base == "" {
			base = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(base, "github-copilot", "byok.json")
	}
	return filepath.Join(home, ".config", "github-copilot", "byok.json")
}

// applyCopilotJetBrains 在 byok.json 里新增/替换 aether provider 的三个键。
func applyCopilotJetBrains(content []byte, p ApplyParams) ([]byte, error) {
	root := map[string]any{}
	if len(strings.TrimSpace(string(content))) > 0 {
		parsed, err := jsoncToJSON(content)
		if err != nil {
			return nil, fmt.Errorf("现有 byok.json 不是合法 JSON: %w", err)
		}
		if err := json.Unmarshal(parsed, &root); err != nil {
			return nil, fmt.Errorf("byok.json 顶层不是对象: %w", err)
		}
	}
	rows := modelRows(p)
	if len(rows) == 0 && p.Model != "" {
		rows = [][2]string{{p.Model, p.Model}}
	}
	models := map[string]any{}
	for _, r := range rows {
		models[r[0]] = map[string]any{
			"deploymentUrl":  p.BaseURL + "/v1/chat/completions",
			"isRegistered":   true,
			"isCustomModel":  true,
			"modelCapabilities": map[string]any{
				"name": r[1], "maxInputTokens": 128000, "maxOutputTokens": 8192,
				"toolCalling": true, "vision": true,
			},
		}
	}
	root[vscodeGroupName+"-provider-config"] = map[string]any{"groupName": vscodeGroupName, "apiType": "chatCompletions"}
	root[vscodeGroupName+"-api-key"] = p.APIKey
	root[vscodeGroupName+"-models-config"] = models
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func managedCopilotJetBrains(data []byte) *ManagedInfo {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return nil
	}
	var models map[string]struct {
		DeploymentURL string `json:"deploymentUrl"`
	}
	if json.Unmarshal(root[vscodeGroupName+"-models-config"], &models) != nil {
		return nil
	}
	for id, m := range models {
		return &ManagedInfo{BaseURL: strings.TrimSuffix(m.DeploymentURL, "/chat/completions"), Model: id}
	}
	return nil
}

// jsoncToJSON 容忍注释与尾逗号（VS Code 的 JSONC 由它自己写）。
func jsoncToJSON(raw []byte) ([]byte, error) {
	return jsonc.ToJSON(raw), nil
}
