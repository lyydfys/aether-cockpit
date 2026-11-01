package provider

import "testing"

func TestServesModelAcceptsPrefixedWhitelist(t *testing.T) {
	env := NewAccountEnv("a", "mock-native")
	env.SetAllowedModels([]string{"mock-native/echo-pro", "other-model"})
	if !env.ServesModel("echo-pro") {
		t.Error("prefixed whitelist entry should serve upstream name")
	}
	if !env.ServesModel("other-model") {
		t.Error("bare whitelist entry should serve")
	}
	if env.ServesModel("echo-mini") {
		t.Error("unlisted model must not be served")
	}
	env.SetAllowedModels(nil)
	if !env.ServesModel("anything") {
		t.Error("empty whitelist means all models")
	}
}
