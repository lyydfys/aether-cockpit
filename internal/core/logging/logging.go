// Package logging 提供最小可用的滚动日志：单文件上限 5 MB，保留一份历史。
//
// 产品形态（双击启动、无控制台）需要可回看的日志；不引入第三方日志库，
// 只把标准库 log 的输出同时写到控制台与文件。
package logging

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"aether-cockpit/internal/core/apppaths"
)

// MaxSize 是单个日志文件的滚动阈值（测试可调）。
var MaxSize int64 = 5 << 20

var mu sync.Mutex

// Setup 打开日志文件（自动建目录、必要时滚动），返回恢复函数。
// path 为空时是空操作。
func Setup(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	mu.Lock()
	defer mu.Unlock()
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	rotate(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	prev := log.Writer()
	prevFlags := log.Flags()
	log.SetFlags(prevFlags)
	log.SetOutput(io.MultiWriter(prev, f))
	var once sync.Once
	return func() {
		once.Do(func() {
			log.SetOutput(prev)
			_ = f.Close()
		})
	}, nil
}

// rotate 在文件超过阈值时改名为 .1（覆盖旧历史）。
func rotate(path string) {
	st, err := os.Stat(path)
	if err != nil || st.Size() < MaxSize {
		return
	}
	_ = os.Remove(path + ".1")
	_ = os.Rename(path, path+".1")
}

// DefaultPath 返回 data/logs/aether.log；产品默认日志位置。
func DefaultPath() string {
	return apppaths.Data("logs", "aether.log")
}

// RotatedAt 用于诊断：返回当前日志文件的最后写入时间（不存在时为零值）。
func RotatedAt(path string) time.Time {
	if st, err := os.Stat(path); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}
