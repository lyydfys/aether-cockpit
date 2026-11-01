//go:build windows

package appstart

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// getValue 读取 HKCU\Run 下的值；不存在返回空串。
func getValue(name string) (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// setValue 写入/覆盖 HKCU\Run 下的值。
func setValue(name, command string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, command)
}

// deleteValue 删除 HKCU\Run 下的值；不存在不算错误。
func deleteValue(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// Get 返回当前开机自启状态。
func Get() Status {
	st := Status{Supported: true}
	cmd, err := getValue(RunValueName)
	if err != nil {
		return st
	}
	if cmd != "" {
		st.Enabled = true
		st.Command = cmd
	}
	return st
}

// Set 打开或关闭开机自启。
func Set(on bool) error {
	if !on {
		return deleteValue(RunValueName)
	}
	cmd, err := CurrentCommand()
	if err != nil {
		return err
	}
	return setValue(RunValueName, cmd)
}

// EnsureCurrent 在已开启自启但路径已变化（升级/移动安装目录）时修正指向。
func EnsureCurrent() error {
	st := Get()
	if !st.Enabled {
		return nil
	}
	cur, err := CurrentCommand()
	if err != nil {
		return err
	}
	if sameCommand(st.Command, cur) {
		return nil
	}
	return setValue(RunValueName, cur)
}
