//go:build !windows && !darwin

package appstart

// 未接入登录自启的平台如实返回 Unsupported。
func Get() Status { return Status{Supported: false} }

func Set(on bool) error { return nil }

func EnsureCurrent() error { return nil }
