//go:build !windows

package appstart

// 非 Windows 平台不支持该功能；如实返回 Unsupported。
func Get() Status { return Status{Supported: false} }

func Set(on bool) error { return nil }

func EnsureCurrent() error { return nil }
