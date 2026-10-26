package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"grape/internal/biz"
	"grape/internal/conf"
	"grape/internal/ops"

	"github.com/go-kratos/kratos/v3/middleware"
)

// routeTestFacility 返回既定样本，隔离挂载测试对真实设施的依赖。
type routeTestFacility struct {
	samples []biz.MetricSample
}

func (*routeTestFacility) Facility() string { return "postgres" }

func (f *routeTestFacility) Pressure(context.Context) ([]biz.MetricSample, error) {
	return f.samples, nil
}

// TestMetricsRouteBypassesBusinessMiddleware 验证 /metrics 直挂的原始
// 路由不经业务中间件链：侦察中间件对任何进入链内的请求记账，若
// /metrics 走了链，记账必非零且出口样本无法免认证返回。
func TestMetricsRouteBypassesBusinessMiddleware(t *testing.T) {
	spyCalls := 0
	spy := func(h middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			spyCalls++
			return h(ctx, req)
		}
	}
	h := ops.NewMetricsHandler([]biz.FacilityDiagnostics{
		&routeTestFacility{samples: []biz.MetricSample{
			{
				Name: "grape_pg_pool_connections_max", Help: "Maximum number of connections the pool may open.",
				Type: biz.MetricGauge, Value: 4,
			},
		}},
	})
	// user/session/agent/web 服务传 nil：本测试不触达业务与静态路由，
	// 注册只需路由存在。trace 位传空实现：/metrics 直挂链外，链上中间件
	// 不会被调用；超时位传直通中间件，普通请求超时不属于本测试关注点。
	noop := func(h middleware.Handler) middleware.Handler { return h }
	// telemetry 位传 nil：本测试不涉 OTel（nil 接收者按未启用处理）。
	srv, err := NewHTTPServer(&conf.Server{Http: &conf.Server_HTTP{}}, nil, noop, noop, spy, h, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build http server: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "grape_pg_pool_connections_max 4\n") {
		t.Fatalf("metrics body missing sample:\n%s", rec.Body.String())
	}
	if spyCalls != 0 {
		t.Fatalf("business middleware invoked %d times on /metrics, want 0", spyCalls)
	}
}
