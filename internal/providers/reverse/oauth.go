package reverse

import (
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// oauthFromSpec 把 spec 里的 OAuth 参数转成内核认证中心认识的契约。
func oauthFromSpec(o *spec.OAuthSpec) *provider.OAuthConfig {
	if o == nil {
		return nil
	}
	cfg := &provider.OAuthConfig{
		AuthorizeURL:    o.AuthorizeURL,
		TokenURL:        o.TokenURL,
		DeviceCodeURL:   o.DeviceCodeURL,
		ClientID:        o.ClientID,
		ClientSecret:    o.ClientSecret,
		Scopes:          append([]string(nil), o.Scopes...),
		RedirectPath:    o.RedirectPath,
		ExtraAuthParams: make(map[string]string, len(o.ExtraAuthParams)),
		TokenField:      o.TokenField,
		RefreshField:    o.RefreshField,
		ExpiresField:    o.ExpiresField,
	}
	for k, v := range o.ExtraAuthParams {
		cfg.ExtraAuthParams[k] = v
	}
	if cfg.RedirectPath == "" {
		cfg.RedirectPath = "/oauth/callback"
	}
	return cfg
}
