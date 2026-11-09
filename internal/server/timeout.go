package server

import (
	"context"
	"strings"

	knowledgev1 "grape/api/knowledge/v1"
	"grape/internal/conf"
	"grape/internal/service"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
)

// embeddingOperations 是同步调用嵌入端点的路由：条目正文变更即逐块摄取，
// 检索先向量化查询。耗时随切片数与嵌入端点（含本地模型冷启动加载）而定，
// 按 embedding_request_timeout 限时。新增同步调用 Embedder 的端点须登记。
var embeddingOperations = map[string]struct{}{
	knowledgev1.OperationDocumentServiceCreateDocument:  {},
	knowledgev1.OperationDocumentServiceUpdateDocument:  {},
	knowledgev1.OperationDocumentServiceReindexDocument: {},
	knowledgev1.OperationDocumentServiceSearchDocuments: {},
}

// RouteTimeoutMiddleware 是路由级请求超时中间件的注入类型。以命名类型
// 承载，避免 Wire 按参数类型装配时与普通 middleware.Middleware 混淆。
type RouteTimeoutMiddleware middleware.Middleware

// NewRouteTimeoutMiddleware 构造路由级请求超时中间件：server.http.timeout
// 归零后，普通路由按 server.http.request_timeout 恢复限时，向量化路由按
// embedding_request_timeout（非正回落 request_timeout）；agent 前缀
// （SSE 长流）跳过。跳过按操作名边界匹配（等于前缀或子路径），防止
// /aguiX 类相邻操作被误跳过；request_timeout 非正时退化为直通。
func NewRouteTimeoutMiddleware(c *conf.Server, agent *service.AgentService) RouteTimeoutMiddleware {
	d := c.GetHttp().GetRequestTimeout().AsDuration()
	embedding := c.GetHttp().GetEmbeddingRequestTimeout().AsDuration()
	if embedding <= 0 {
		embedding = d
	}
	basePath := ""
	if agent != nil {
		basePath = agent.BasePath()
	}
	return RouteTimeoutMiddleware(func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if d <= 0 {
				return handler(ctx, req)
			}
			limit := d
			if tr, ok := transport.FromServerContext(ctx); ok {
				op := tr.Operation()
				if basePath != "" && (op == basePath || strings.HasPrefix(op, basePath+"/")) {
					return handler(ctx, req)
				}
				if _, ok := embeddingOperations[op]; ok {
					limit = embedding
				}
			}
			tctx, cancel := context.WithTimeout(ctx, limit)
			defer cancel()
			return handler(tctx, req)
		}
	})
}
