package geminicompat

// Gemini CLI 官方公开 OAuth 客户端参数；magpie 的 geminiApp 使用同一组。
// 账号凭据中的 oauth_client_id/oauth_client_secret 与环境变量优先覆盖。
// 这只是公开客户端信息，不是用户账号凭证。
const (
	referenceGeminiOAuthClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	referenceGeminiOAuthClientSecret = "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"
)
