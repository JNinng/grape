package server

import (
	"bytes"
	"io"
	"io/fs"
	nethttp "net/http"
	"path"
	"strings"
	"time"

	"grape/internal/service"
	"grape/web"
)

// assetsPathPrefix 是前端构建产物的内容哈希目录：文件名不可变，可长缓存。
const assetsPathPrefix = "/assets/"

// indexName 是 SPA 入口文件在 dist 内的路径。
const indexName = "index.html"

// WebHandler 是前端静态托管 Handler 的注入类型。NewHTTPServer 另有一个
// nethttp.Handler 形参（metrics），命名类型避免 wire 按参数类型装配时
// 二义——与 RouteTimeoutMiddleware 同一手法。
type WebHandler nethttp.Handler

// NewWebHandler 构造前端静态托管 Handler：嵌入 dist 产物 + API 保留前缀
// 名单。agent 非 nil 时其 AG-UI / ack 前缀进入名单；测试装配传 nil 时
// 名单退化为内置前缀。
func NewWebHandler(agent *service.AgentService) WebHandler {
	return WebHandler(newWebFileHandler(web.DistFS(), ReservedAPIPrefixes(agent)))
}

// ReservedAPIPrefixes 列出绝不落入 SPA fallback 的 API 路径前缀：生成契约
// 路由（/v1）、运维出口（/metrics）与 AG-UI / ack 前缀。前缀判定按边界
// 匹配（等于前缀或以其子路径开头），/v1x 之类的相邻路径不受影响——与
// 路由级超时中间件的跳过判定同一纪律。
func ReservedAPIPrefixes(agent *service.AgentService) []string {
	prefixes := make([]string, 0, 3)
	prefixes = append(prefixes, "/v1", "/metrics")
	if agent != nil {
		prefixes = append(prefixes, agent.BasePath())
	}
	return prefixes
}

// newWebFileHandler 构造静态文件 Handler：命中文件直出（assets 长缓存），
// 未命中一律回退 index.html（SPA 客户端路由刷新不 404）；保留前缀内的
// 未知路径按 API 语义返回 404，绝不返回页面壳。挂载点与免认证的取舍见
// internal/server/http.go 与 README。
type webFileHandler struct {
	files    fs.FS
	reserved []string
}

func newWebFileHandler(files fs.FS, reserved []string) nethttp.Handler {
	return &webFileHandler{files: files, reserved: reserved}
}

func (h *webFileHandler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	// 保留前缀判定先于方法检查：API 路径语义优先，未知 API 路径无论
	// 方法一律 404，绝不落页面壳或静态的 405。
	if isReservedPath(r.URL.Path, h.reserved) {
		nethttp.Error(w, "not found", nethttp.StatusNotFound)
		return
	}
	// Kratos 对 /metrics、静态路径这类原始 Handler 不套方法约束，方法
	// 分发归本 Handler：静态资源只有读语义。
	if r.Method != nethttp.MethodGet && r.Method != nethttp.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		nethttp.Error(w, "method not allowed", nethttp.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" {
		if serveFile(w, r, h.files, name) {
			return
		}
	}
	serveIndex(w, r, h.files)
}

// serveFile 直出命中文件；未命中、目录与不可 Seek 的实现交给调用方回退。
func serveFile(w nethttp.ResponseWriter, r *nethttp.Request, files fs.FS, name string) bool {
	f, err := files.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		return false
	}
	if strings.HasPrefix(r.URL.Path, assetsPathPrefix) {
		// Vite 产物带内容哈希，文件名与内容一一对应，可immutable长缓存。
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	if seeker, ok := f.(io.ReadSeeker); ok {
		nethttp.ServeContent(w, r, stat.Name(), stat.ModTime(), seeker)
		return true
	}
	// fs.FS 实现不保证 io.ReadSeeker（fs.FS 契约只有 ReadCloser）：整体
	// 读入后经内存副本提供 Seek 能力。
	data, err := io.ReadAll(f)
	if err != nil {
		nethttp.Error(w, "internal server error", nethttp.StatusInternalServerError)
		return true
	}
	nethttp.ServeContent(w, r, stat.Name(), stat.ModTime(), bytes.NewReader(data))
	return true
}

// serveIndex 回退 SPA 入口：Cache-Control 禁缓存，保证发布新版本后客户端
// 总能取到引用最新哈希资源的页面壳。
func serveIndex(w nethttp.ResponseWriter, r *nethttp.Request, files fs.FS) {
	data, err := fs.ReadFile(files, indexName)
	if err != nil {
		// dist 缺 index.html 属异常构建（占位页兜底后不应发生）。
		nethttp.Error(w, "index not found", nethttp.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	nethttp.ServeContent(w, r, indexName, time.Time{}, bytes.NewReader(data))
}

// isReservedPath 按边界匹配判定路径是否落在保留前缀内：等于前缀或以其
// 子路径形式开头。
func isReservedPath(p string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
