package util

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

// GenerateRunID 生成唯一的 run ID，使用 crypto/rand 避免高并发下的纳秒时间戳碰撞。
func GenerateRunID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "run-" + hex.EncodeToString(b)
}

// ResolveNumbatPath 解析 ~/.numbat/ 下的路径，返回绝对路径。
// 若设置了 NUMBAT_HOME，则以 <NUMBAT_HOME>/.numbat 为根（便于沙箱/测试环境重定向数据目录）。
// 否则依次尝试 os.UserHomeDir()、HOME 环境变量、USERPROFILE（Windows）。
func ResolveNumbatPath(parts ...string) string {
	if home := os.Getenv("NUMBAT_HOME"); home != "" {
		return filepath.Join(append([]string{home, ".numbat"}, parts...)...)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		home = "~"
	}
	return filepath.Join(append([]string{home, ".numbat"}, parts...)...)
}
