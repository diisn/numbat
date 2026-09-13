// Package webui 内嵌前端构建产物。
//
// 产物由 webui/ 下的 Vite 工程生成（npm run build → internal/webui/dist），
// 由 numbat-core 网关挂在 /app/* 下提供，使前端具备独立交付形态（PRD F4）。
package webui

import (
	"embed"
	"io/fs"
)

// all: 前缀确保 .gitkeep 等点文件也被嵌入，使未构建时目录依然存在。
//
//go:embed all:dist
var distFS embed.FS

// Dist 返回前端产物文件系统（对应 dist 目录的内容）。
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// Built 报告产物中是否包含 index.html。
// false 表示前端尚未执行 npm run build（此时网关仍可提供 /ws 等接口）。
func Built() bool {
	_, err := fs.Stat(distFS, "dist/index.html")
	return err == nil
}
