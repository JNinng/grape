package ops

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"grape/internal/biz"
)

// fakeFacility 返回既定样本或错误，隔离测试出口的映射与失败计数。
type fakeFacility struct {
	name    string
	samples []biz.MetricSample
	err     error
}

func (f *fakeFacility) Facility() string { return f.name }

func (f *fakeFacility) Pressure(context.Context) ([]biz.MetricSample, error) {
	return f.samples, f.err
}

// scrape 对出口执行一次 GET 抓取，返回正文。
func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestMetricsHandlerMapsSamplesVerbatim 验证出口对样本只做格式映射：
// TYPE/HELP 与标签值逐字出现，无语义加工。
func TestMetricsHandlerMapsSamplesVerbatim(t *testing.T) {
	h := NewMetricsHandler([]biz.FacilityDiagnostics{
		&fakeFacility{name: "postgres", samples: []biz.MetricSample{
			{
				Name: "grape_pg_pool_connections", Help: "Current connections in the pool by state (total, idle, acquired).",
				Type: biz.MetricGauge, Labels: map[string]string{"state": "total"}, Value: 3,
			},
			{
				Name: "grape_pg_pool_connections", Help: "Current connections in the pool by state (total, idle, acquired).",
				Type: biz.MetricGauge, Labels: map[string]string{"state": "idle"}, Value: 1,
			},
			{
				Name: "grape_pg_scrape_errors_total", Help: "Cumulative collection failures.",
				Type: biz.MetricCounter, Value: 7,
			},
		}},
	})
	body := scrape(t, h)

	for _, want := range []string{
		"# HELP grape_pg_pool_connections Current connections in the pool by state (total, idle, acquired).\n",
		"# TYPE grape_pg_pool_connections gauge\n",
		"grape_pg_pool_connections{state=\"total\"} 3\n",
		"grape_pg_pool_connections{state=\"idle\"} 1\n",
		"# HELP grape_pg_scrape_errors_total Cumulative collection failures.\n",
		"# TYPE grape_pg_scrape_errors_total counter\n",
		"grape_pg_scrape_errors_total 7\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("scrape output missing %q\ngot:\n%s", want, body)
		}
	}
	if strings.Contains(body, "grape_diagnostics_scrape_errors_total") {
		t.Fatalf("healthy scrape must not report collection errors:\n%s", body)
	}
}

// TestMetricsHandlerCountsFailuresPerFacility 验证单设施采集失败不中断
// 整体：其余设施样本照常输出，失败按设施计数且跨抓取递增。
func TestMetricsHandlerCountsFailuresPerFacility(t *testing.T) {
	h := NewMetricsHandler([]biz.FacilityDiagnostics{
		&fakeFacility{name: "bad", err: errors.New("diagnostics unavailable")},
		&fakeFacility{name: "postgres", samples: []biz.MetricSample{
			{
				Name: "grape_pg_pool_connections_max", Help: "Maximum number of connections the pool may open.",
				Type: biz.MetricGauge, Value: 4,
			},
		}},
	})

	body := scrape(t, h)
	if !strings.Contains(body, "grape_pg_pool_connections_max 4\n") {
		t.Fatalf("healthy facility samples missing on mixed scrape:\n%s", body)
	}
	if !strings.Contains(body, "grape_diagnostics_scrape_errors_total{facility=\"bad\"} 1\n") {
		t.Fatalf("first failure must count 1 for facility bad:\n%s", body)
	}
	if strings.Contains(body, `facility="postgres"`) {
		t.Fatalf("healthy facility must not be counted:\n%s", body)
	}

	// 第二次抓取在同一计数器上累加：错误计数随抓取递增而非重置。
	if body := scrape(t, h); !strings.Contains(body, "grape_diagnostics_scrape_errors_total{facility=\"bad\"} 2\n") {
		t.Fatalf("failure count must accumulate across scrapes:\n%s", body)
	}
}
