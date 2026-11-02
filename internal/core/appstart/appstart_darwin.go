//go:build darwin

package appstart

import (
	"errors"
	"html"
	"os"
	"path/filepath"
	"strings"
)

const launchAgentLabel = "com.aether.cockpit"

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), nil
}

func launchAgentPlist(exe string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchAgentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + html.EscapeString(exe) + `</string>
	</array>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`
}

func readLaunchAgent() (string, error) {
	path, err := launchAgentPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func writeLaunchAgent(content string) error {
	path, err := launchAgentPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aether-launchagent-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Get 返回当前登录自启状态。LaunchAgent 文件存在即视为已开启。
func Get() Status {
	st := Status{Supported: true}
	content, err := readLaunchAgent()
	if err != nil || strings.TrimSpace(content) == "" {
		return st
	}
	st.Enabled = true
	if cmd, err := CurrentCommand(); err == nil {
		st.Command = cmd
	}
	return st
}

// Set 写入/删除 LaunchAgent。写入后下一次登录生效；不主动 bootstrap，
// 避免用户点“开启”时又拉起一个实例。
func Set(on bool) error {
	if !on {
		path, err := launchAgentPath()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	return writeLaunchAgent(launchAgentPlist(exe))
}

// EnsureCurrent 在应用被移动/升级后修正 ProgramArguments。
func EnsureCurrent() error {
	st := Get()
	if !st.Enabled {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	want := launchAgentPlist(exe)
	got, err := readLaunchAgent()
	if err != nil {
		return err
	}
	if got == want {
		return nil
	}
	return writeLaunchAgent(want)
}
