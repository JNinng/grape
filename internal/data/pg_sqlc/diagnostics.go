package pg_sqlc

import (
	"context"
	"fmt"

	"grape/internal/biz"

	"github.com/jackc/pgx/v5/pgxpool"
)

// metricPrefix 是 PostgreSQL 诊断指标的全名前缀，设施间不撞名，
// 出口按前缀即可归属指标来源。
const metricPrefix = "grape_pg_"

// Diagnostics 是 PostgreSQL 诊断面（biz.FacilityDiagnostics）：输出
// 连接池与库内活动的原始压力指标。可见性边界：应用角色未授予 pg_monitor，
// pg_stat_activity 只能看见自身后端的行——应用级诊断只见自身查询属预期，
// 不视为配置缺陷；全库视角归监控侧的超级用户查询。
type Diagnostics struct {
	pool *pgxpool.Pool
}

// NewDiagnostics 构造 PostgreSQL 诊断面。
func NewDiagnostics(d *Data) biz.FacilityDiagnostics {
	return &Diagnostics{pool: d.db}
}

// Facility 返回设施名。
func (*Diagnostics) Facility() string { return "postgres" }

// Pressure 采集一轮原始压力指标：从池取一条连接跑完全部 SQL，不开事务、
// 不写任何数据。指标全为介质给出的原始值，不做比率、不做聚合——阈值
// 与告警归监控配置层。
func (d *Diagnostics) Pressure(ctx context.Context) ([]biz.MetricSample, error) {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("pg_sqlc: acquire connection for diagnostics: %w", err)
	}
	defer conn.Release()

	// 池统计直接读自本次采集使用的池，无 SQL。
	samples := poolSamples(d.pool.Stat())
	if samples, err = appendActivitySamples(ctx, conn, samples); err != nil {
		return nil, err
	}
	if samples, err = appendTableSamples(ctx, conn, samples); err != nil {
		return nil, err
	}
	return appendIndexSamples(ctx, conn, samples)
}

// poolSamples 把连接池自带统计转为样本。state 三个取值与 pgxpool.Stat
// 的字段一一对应，max 单列以便告警直接比对池上限。
func poolSamples(s *pgxpool.Stat) []biz.MetricSample {
	poolConnections := metricPrefix + "pool_connections"
	return []biz.MetricSample{
		{
			Name: poolConnections, Help: "Current connections in the pool by state (total, idle, acquired).",
			Type: biz.MetricGauge, Labels: map[string]string{"state": "total"}, Value: float64(s.TotalConns()),
		},
		{
			Name: poolConnections, Help: "Current connections in the pool by state (total, idle, acquired).",
			Type: biz.MetricGauge, Labels: map[string]string{"state": "idle"}, Value: float64(s.IdleConns()),
		},
		{
			Name: poolConnections, Help: "Current connections in the pool by state (total, idle, acquired).",
			Type: biz.MetricGauge, Labels: map[string]string{"state": "acquired"}, Value: float64(s.AcquiredConns()),
		},
		{
			Name: metricPrefix + "pool_connections_max", Help: "Maximum number of connections the pool may open.",
			Type: biz.MetricGauge, Value: float64(s.MaxConns()),
		},
	}
}

// appendActivitySamples 采集 pg_stat_activity 可见后端的会话与事务压力：
// 会话按 state 分组计数，另取最长事务时长与超 1 秒的活跃查询数。
func appendActivitySamples(ctx context.Context, conn *pgxpool.Conn, samples []biz.MetricSample) ([]biz.MetricSample, error) {
	rows, err := conn.Query(ctx,
		`SELECT coalesce(state, 'unknown') AS state, count(*)
		 FROM pg_stat_activity
		 GROUP BY state`)
	if err != nil {
		return nil, fmt.Errorf("pg_sqlc: query pg_stat_activity sessions: %w", err)
	}
	for rows.Next() {
		var (
			state string
			count int64
		)
		if err := rows.Scan(&state, &count); err != nil {
			rows.Close()
			return nil, fmt.Errorf("pg_sqlc: scan activity session row: %w", err)
		}
		samples = append(samples, biz.MetricSample{
			Name:   metricPrefix + "activity_sessions",
			Help:   "Backend sessions visible to the application role, grouped by pg_stat_activity state.",
			Type:   biz.MetricGauge,
			Labels: map[string]string{"state": state},
			Value:  float64(count),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg_sqlc: iterate activity session rows: %w", err)
	}

	var longest float64
	// clock_timestamp() 取真实当前时刻：now() 冻结在事务起点，不适合
	// 表达"最长已运行多久"。
	if err := conn.QueryRow(ctx,
		`SELECT coalesce(max(extract(epoch FROM clock_timestamp() - xact_start)), 0)::float8
		 FROM pg_stat_activity
		 WHERE xact_start IS NOT NULL`).Scan(&longest); err != nil {
		return nil, fmt.Errorf("pg_sqlc: query longest transaction: %w", err)
	}
	samples = append(samples, biz.MetricSample{
		Name:  metricPrefix + "transaction_longest_seconds",
		Help:  "Age in seconds of the longest running transaction visible to the application role.",
		Type:  biz.MetricGauge,
		Value: longest,
	})

	var slow int64
	if err := conn.QueryRow(ctx,
		`SELECT count(*)
		 FROM pg_stat_activity
		 WHERE state = 'active' AND clock_timestamp() - query_start > interval '1 second'`).Scan(&slow); err != nil {
		return nil, fmt.Errorf("pg_sqlc: query queries over 1s: %w", err)
	}
	samples = append(samples, biz.MetricSample{
		Name:  metricPrefix + "queries_over_1s",
		Help:  "Active queries running longer than one second visible to the application role.",
		Type:  biz.MetricGauge,
		Value: float64(slow),
	})
	return samples, nil
}

// appendTableSamples 采集租户表的元组与体积统计：pg_stat_user_tables
// 覆盖全部用户表（未访问的表计数为零），行统计与表/索引体积合并为一次
// 查询取回。本仓部署约定所有表都在 public（schema 文件以 public. 建表）。
func appendTableSamples(ctx context.Context, conn *pgxpool.Conn, samples []biz.MetricSample) ([]biz.MetricSample, error) {
	rows, err := conn.Query(ctx,
		`SELECT relname, n_live_tup, n_dead_tup,
		        pg_relation_size(relid), pg_indexes_size(relid)
		 FROM pg_stat_user_tables
		 WHERE schemaname = 'public'`)
	if err != nil {
		return nil, fmt.Errorf("pg_sqlc: query pg_stat_user_tables: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			table           string
			live, dead      int64
			size, indexSize int64
		)
		if err := rows.Scan(&table, &live, &dead, &size, &indexSize); err != nil {
			return nil, fmt.Errorf("pg_sqlc: scan table stat row: %w", err)
		}
		labels := map[string]string{"table": table}
		samples = append(samples,
			biz.MetricSample{
				Name:   metricPrefix + "table_live_tuples",
				Help:   "Estimated live tuple count per user table.",
				Type:   biz.MetricGauge,
				Labels: labels,
				Value:  float64(live),
			},
			biz.MetricSample{
				Name:   metricPrefix + "table_dead_tuples",
				Help:   "Estimated dead tuple count per user table.",
				Type:   biz.MetricGauge,
				Labels: labels,
				Value:  float64(dead),
			},
			biz.MetricSample{
				Name:   metricPrefix + "table_size_bytes",
				Help:   "Disk size of the table main fork per user table.",
				Type:   biz.MetricGauge,
				Labels: labels,
				Value:  float64(size),
			},
			biz.MetricSample{
				Name:   metricPrefix + "table_index_size_bytes",
				Help:   "Total disk size of all indexes attached to each user table.",
				Type:   biz.MetricGauge,
				Labels: labels,
				Value:  float64(indexSize),
			},
		)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg_sqlc: iterate table stat rows: %w", err)
	}
	return samples, nil
}

// appendIndexSamples 采集逐索引体积：pg_stat_user_indexes 提供表到索引
// 的归属，体积经 pg_relation_size(indexrelid) 取得（比 PG16 才有的
// idx_size 列兼容更老版本）。
func appendIndexSamples(ctx context.Context, conn *pgxpool.Conn, samples []biz.MetricSample) ([]biz.MetricSample, error) {
	rows, err := conn.Query(ctx,
		`SELECT t.relname AS table, i.indexrelname AS index,
		        pg_relation_size(i.indexrelid) AS size
		 FROM pg_stat_user_tables t
		 JOIN pg_stat_user_indexes i USING (relid)
		 WHERE t.schemaname = 'public'`)
	if err != nil {
		return nil, fmt.Errorf("pg_sqlc: query pg_stat_user_indexes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			table string
			index string
			size  int64
		)
		if err := rows.Scan(&table, &index, &size); err != nil {
			return nil, fmt.Errorf("pg_sqlc: scan index size row: %w", err)
		}
		samples = append(samples, biz.MetricSample{
			Name:   metricPrefix + "index_size_bytes",
			Help:   "Disk size of each index on user tables.",
			Type:   biz.MetricGauge,
			Labels: map[string]string{"table": table, "index": index},
			Value:  float64(size),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg_sqlc: iterate index size rows: %w", err)
	}
	return samples, nil
}
