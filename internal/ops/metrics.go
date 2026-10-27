package ops

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"grape/internal/biz"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// diagnosticsTimeout 是单个设施一次 Pressure 采集的超时：诊断面只跑
// 常规只读查询，超时说明设施异常，按采集失败计数处理，绝不拖住整次
// 抓取或其他设施。
const diagnosticsTimeout = 5 * time.Second

// scrapeErrorsName 是设施采集失败计数器的全名，与诊断样本同一出口输出，
// 让"哪个设施采不上来"本身可观测、可告警。
const scrapeErrorsName = "grape_diagnostics_scrape_errors_total"

// facilityCollector 在每次抓取时逐设施带超时调用 Pressure，把样本按
// 出口契约原样映射为 Prometheus const metric；出口不理解样本语义，
// 不做任何二次计算。
type facilityCollector struct {
	facilities []biz.FacilityDiagnostics
	// errDesc 是失败计数器的描述；累计值自管而非用 CounterVec——
	// CounterVec 的子项在失败瞬间才创建，与 Gather 的读取时序无保证。
	errDesc *prometheus.Desc
	// failures 按设施累计自进程起的采集失败次数，mu 保护。
	mu       sync.Mutex
	failures map[string]int64
}

// newFacilityCollector 构造采集器并预置失败计数。
func newFacilityCollector(facilities []biz.FacilityDiagnostics) *facilityCollector {
	return &facilityCollector{
		facilities: facilities,
		errDesc: prometheus.NewDesc(scrapeErrorsName,
			"Facility diagnostics collection failures since process start.",
			[]string{"facility"}, nil),
		failures: make(map[string]int64, len(facilities)),
	}
}

// onFailure 给设施记 n 次采集失败，返回累计值。
func (c *facilityCollector) onFailure(facility string, n int) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures[facility] += int64(n)
	return float64(c.failures[facility])
}

// Describe 不声明任何固定描述：样本集合是动态的（unchecked collector），
// 契约把正确性交给采集实现。
func (*facilityCollector) Describe(chan<- *prometheus.Desc) {}

// Collect 并发采集各设施。单设施失败只递增其错误计数并输出计数器，
// 其余设施样本照常输出；单条样本映射失败同样计为该设施的采集失败。
func (c *facilityCollector) Collect(ch chan<- prometheus.Metric) {
	var wg sync.WaitGroup
	for _, f := range c.facilities {
		wg.Add(1)
		go func(f biz.FacilityDiagnostics) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), diagnosticsTimeout)
			defer cancel()
			samples, err := f.Pressure(ctx)
			if err != nil {
				ch <- c.scrapeError(f.Facility(), 1)
				return
			}
			bad := 0
			for _, s := range samples {
				m, err := constMetric(s)
				if err != nil {
					bad++
					continue
				}
				ch <- m
			}
			if bad > 0 {
				ch <- c.scrapeError(f.Facility(), bad)
			}
		}(f)
	}
	wg.Wait()
}

// scrapeError 输出该设施的累计失败计数（含本次新增）。
func (c *facilityCollector) scrapeError(facility string, n int) prometheus.Metric {
	return prometheus.MustNewConstMetric(c.errDesc, prometheus.CounterValue,
		c.onFailure(facility, n), facility)
}

// constMetric 把设施无关样本映射为 const metric。标签名按字典序固定，
// 保证 desc 与取值顺序一致。
func constMetric(s biz.MetricSample) (prometheus.Metric, error) {
	names := make([]string, 0, len(s.Labels))
	for name := range s.Labels {
		names = append(names, name)
	}
	sort.Strings(names)
	values := make([]string, len(names))
	for i, name := range names {
		values[i] = s.Labels[name]
	}
	return prometheus.NewConstMetric(
		prometheus.NewDesc(s.Name, s.Help, names, nil),
		promValueType(s.Type), s.Value, values...,
	)
}

// promValueType 把契约指标类别映射为 Prometheus 值类型。
func promValueType(t biz.MetricType) prometheus.ValueType {
	if t == biz.MetricCounter {
		return prometheus.CounterValue
	}
	return prometheus.GaugeValue
}

// NewMetricsHandler 构造标准指标出口：独立 registry 承载设施诊断指标
// （各设施原始样本 + 采集失败计数）与 agent 运行面指标（RuntimeMetrics，
// nil 时不注册——仅设施形态的测试装配），经 promhttp 输出文本格式。
func NewMetricsHandler(facilities []biz.FacilityDiagnostics, runtime *RuntimeMetrics) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(newFacilityCollector(facilities))
	if runtime != nil {
		reg.MustRegister(runtime)
	}
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
