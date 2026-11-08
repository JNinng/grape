// Package web 承载前端（Vite + React）构建产物的 go:embed 导出。
//
// go:embed 不能引用父目录，故本包必须位于 web/、与 dist 同级；server 是
// 唯一消费方，只导入导出的 fs.FS，不感知前端工程细节（见 internal/README.md）。
// 仓库内恒有极简 dist/index.html 占位页，保证 go build 始终可通过；npm 构建
// 后产物覆盖 dist（不入库），重新编译即嵌入真实前端。
package web

import (
	"embed"
	"io/fs"
)

// distFS 是前端构建产物根。all: 前缀让以 . 与 _ 开头的文件也被嵌入
// （默认 embed 目录会跳过它们）。
//
//go:embed all:dist
var distFS embed.FS

// DistFS 返回以 dist 目录为根的前端静态资源文件系统。embed 保证 dist
// 存在（占位页兜底），fs.Sub 不可能失败。
func DistFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("web: dist must be embedded: " + err.Error())
	}
	return sub
}
