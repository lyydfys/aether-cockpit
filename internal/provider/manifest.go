package provider

import "aether-cockpit/internal/core/ir"

// AuthMethod 声明式登录方式。
//
// 前端不按任意 JSON Schema 渲染表单，而是按这里的内置流程枚举匹配通用组件：
// 这是"新增渠道前端零改动"能以 20% 成本覆盖 95% 场景的关键。
type AuthMethod string

const (
	// AuthLocalImport 一键扫描/导入本机已登录客户端的凭证（最省事，优先实现）。
	AuthLocalImport AuthMethod = "local_import"
	// AuthOAuthPKCE 本地回环监听 + PKCE 授权码。
	AuthOAuthPKCE AuthMethod = "oauth_pkce"
	// AuthDeviceCode OIDC 设备码轮询（如 AWS Builder ID）。
	AuthDeviceCode AuthMethod = "device_code"
	// AuthManualInput 手动粘贴 Token / Cookie。
	AuthManualInput AuthMethod = "manual_input"
	// AuthWebViewLogin 内置浏览器拦截登录回调。
	AuthWebViewLogin AuthMethod = "webview_login"
)

// NetworkPolicy 声明该渠道必须走哪种网络出口。
//
// 这个字段直接决定 transport 层用哪个 Dialer，是"国内外渠道混跑不互相污染"的唯一开关。
type NetworkPolicy string

const (
	// NetPolicyDirectForce 强制直连：绕过系统代理与软件全局代理。CN 渠道必须如此，
	// 否则流量绕道海外 IP 会触发厂商异地登录风控。
	NetPolicyDirectForce NetworkPolicy = "direct_force"
	// NetPolicyProxyFirst 优先走配置的代理池。国际渠道必须如此。
	NetPolicyProxyFirst NetworkPolicy = "proxy_first"
	// NetPolicySystem 跟随系统代理。
	NetPolicySystem NetworkPolicy = "system"
)

// FormFieldSchema 手动登录表单的单个字段。
type FormFieldSchema struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // text | password | textarea | select
	Placeholder string   `json:"placeholder,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Help        string   `json:"help,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// OAuthConfig 描述一个渠道的 OAuth2 参数。
//
// 内核据此代跑授权流程：PKCE（本地回环回调）或设备码（轮询）。
// 渠道插件不需要自己实现 HTTP 监听与轮询，只声明参数即可。
type OAuthConfig struct {
	// AuthorizeURL 授权页地址（PKCE 用）。
	AuthorizeURL string `json:"authorize_url,omitempty"`
	// TokenURL 换取 token 的地址。
	TokenURL string `json:"token_url,omitempty"`
	// DeviceCodeURL 设备码申请地址（设备码流程用）。
	DeviceCodeURL string `json:"device_code_url,omitempty"`
	// ClientID 公开客户端 ID。
	ClientID string `json:"client_id"`
	// ClientSecret 机密客户端才需要；桌面端一般不用。
	ClientSecret string `json:"client_secret,omitempty"`
	// Scopes 申请的权限范围。
	Scopes []string `json:"scopes,omitempty"`
	// RedirectPath 本地回调路径，默认 /oauth/callback。
	RedirectPath string `json:"redirect_path,omitempty"`
	// ExtraAuthParams 额外的授权查询参数（如 prompt=consent）。
	ExtraAuthParams map[string]string `json:"extra_auth_params,omitempty"`
	// TokenField 把 access_token 存到 credentials 的哪个 key，默认 access_token。
	TokenField string `json:"token_field,omitempty"`
	// RefreshField 把 refresh_token 存到哪个 key，默认 refresh_token。
	RefreshField string `json:"refresh_field,omitempty"`
	// ExpiresField 把过期时间存到哪个 key，默认 expires_at。
	ExpiresField string `json:"expires_field,omitempty"`
	// TokenFormat 是换 token 的请求体格式：form（默认）或 json。
	TokenFormat string `json:"token_format,omitempty"`
	// RedirectHost 回调地址里的主机名，默认 127.0.0.1；
	// 预注册客户端要求 localhost 时显式声明。
	RedirectHost string `json:"redirect_host,omitempty"`
	// RedirectPort 固定回调端口；0 表示随机。端口被占用会直接报错，
	// 因为预注册客户端的 redirect_uri 不能变。
	RedirectPort int `json:"redirect_port,omitempty"`
	// ExtraTokenParams 换 token 时附带的额外字段。
	ExtraTokenParams map[string]string `json:"extra_token_params,omitempty"`
	// ExtraCredentials 落库时额外写入的凭证字段（如 oauth_client_id）。
	ExtraCredentials map[string]string `json:"extra_credentials,omitempty"`
	// IDTokenClaims 声明从 id_token 提取哪些声明写入凭证：key 为凭证字段，
	// value 为声明路径，`#` 前是顶层键、`#` 后是点分嵌套路径，
	// 例如 `https://api.openai.com/auth#chatgpt_account_id`。
	IDTokenClaims map[string]string `json:"id_token_claims,omitempty"`
}

// TaskDefinition 一个可执行的签到/积分任务。
type TaskDefinition struct {
	TaskID      string `json:"task_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// DependsOn 声明前置任务。用有序数组 + 依赖列表即可表达签到链，
	// 无需 DAG 调度器（实际依赖深度从不超过 3）。
	DependsOn []string `json:"depends_on,omitempty"`
	// CronDefault 默认执行周期。
	CronDefault string `json:"cron_default,omitempty"`
	// Optional 表示任务失败不阻塞后续任务（如"猫猫旅行"这类玩法随时可能下线）。
	Optional bool `json:"optional,omitempty"`
}

// ProviderManifest 是渠道对外的完整自描述。
//
// 前端只认这个结构：Tab、卡片、导入弹窗、签到开关、模型列表全部由它渲染。
// 新增渠道时前端代码零改动。
type ProviderManifest struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	// Family 用于把同一产品的 CN/Intl 两个变体在 UI 上归到一组。
	Family      string `json:"family,omitempty"`
	RegionTag   string `json:"region_tag,omitempty"` // CN | GLOBAL
	IconSVG     string `json:"icon_svg,omitempty"`
	BrandColor  string `json:"brand_color,omitempty"`
	Description string `json:"description,omitempty"`

	// NetworkPolicy 供 transport 层选择 Dialer。
	NetworkPolicy NetworkPolicy `json:"network_policy"`

	// BaseURL 是渠道的默认上游地址（账号没填 base_url 时生效）。
	//
	// 单独暴露出来是为了让"连接预热 / 网络诊断"这类通用能力知道往哪里打
	// 探针——否则每个渠道都要再实现一遍同样的地址解析逻辑。
	BaseURL string `json:"base_url,omitempty"`

	SupportedAuths  []AuthMethod      `json:"supported_auths"`
	AuthFormSchema  []FormFieldSchema `json:"auth_form_schema,omitempty"`
	WebViewLoginURL string            `json:"webview_login_url,omitempty"`
	// OAuth 是 OAuth2 类登录的参数。声明了它，内核才能代跑 PKCE / 设备码流程。
	OAuth *OAuthConfig `json:"oauth,omitempty"`

	Tasks  []TaskDefinition `json:"tasks,omitempty"`
	Models []ir.ModelMeta   `json:"models"`

	// RequiredMiddlewares 声明需要挂载的通用补偿中间件。
	// 例：不支持原生 tools 的渠道挂 "xml_tool_call"；有状态上游挂 "stateful_session"。
	RequiredMiddlewares []string `json:"required_middlewares,omitempty"`

	// NativeResponses 声明该渠道上游原生就是 OpenAI Responses 协议。
	// 为 true 且渠道实现了 NativeResponsesCapability 时，
	// 下游同样是 /v1/responses 的请求会走 Fast-Path 零损直通。
	NativeResponses bool `json:"native_responses,omitempty"`

	// Capabilities 声明该渠道实现了哪些可选能力接口，供内核跳过类型断言。
	Capabilities CapabilitySet `json:"capabilities"`
}

// CapabilitySet 渠道能力位图。
type CapabilitySet struct {
	Chat        bool `json:"chat"`
	CheckIn     bool `json:"check_in"`
	Quota       bool `json:"quota"`
	LocalImport bool `json:"local_import"`
	// LocalInject 是否支持一键切号：把账号写回本机 IDE 的登录态。
	LocalInject bool `json:"local_inject"`
	// OfficialFirst 选号时是否优先官方订阅账号（配合 OfficialAccountCapability）。
	OfficialFirst bool `json:"official_first"`
	// OfficialBinding 是否支持解析官方绑定信息（配合 OfficialBindingCapability）。
	OfficialBinding bool `json:"official_binding"`
	Refresh         bool `json:"refresh"`
	// ResponsivePassthrough 是否支持 Responses 协议直通。
	ResponsivePassthrough bool `json:"responses_passthrough"`
	// ResponsesPassthroughNonStream 是否允许非流式 Responses 也走直通。
	// 默认 false：保留"非流式走 IR"的既有行为；原生 Responses 渠道可显式开启。
	ResponsesPassthroughNonStream bool `json:"responses_passthrough_non_stream"`
}

// HasTask 判断渠道是否声明了某个任务。
func (m ProviderManifest) HasTask(taskID string) bool {
	for _, t := range m.Tasks {
		if t.TaskID == taskID {
			return true
		}
	}
	return false
}

// HasMiddleware 判断渠道是否要求挂载某个中间件。
func (m ProviderManifest) HasMiddleware(name string) bool {
	for _, n := range m.RequiredMiddlewares {
		if n == name {
			return true
		}
	}
	return false
}
