package antigravity

// 官方 Antigravity 客户端的公开 OAuth 参数；与 cockpit-tools / magpie 使用的一致。
// 这只是刷新令牌的默认兜底：账号凭据里的 oauth_client_id/oauth_client_secret
// 以及 AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID/SECRET 都优先；未来客户端轮换参数时
// 仍会回退到从本机 language_server 运行时发现。
var referenceOAuthClientPair = oauthClientPair{
	ID:     "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com",
	Secret: "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",
}
