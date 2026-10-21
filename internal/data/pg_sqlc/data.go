package pg_sqlc

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"grape/internal/conf"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Data 持有各仓储共享的长生命周期存储客户端。
type Data struct {
	db *pgxpool.Pool
}

// NewData 建立数据库连接池，返回客户端与清理函数。
func NewData(c *conf.Data) (*Data, func(), error) {
	cfg, err := pgxpool.ParseConfig(c.GetDatabase().GetSource())
	if err != nil {
		return nil, nil, err
	}
	if c.GetDatabase().GetDebug() {
		cfg.ConnConfig.Tracer = &queryTracer{logger: slog.Default()}
	}
	db, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := db.Ping(context.Background()); err != nil {
		db.Close()
		return nil, nil, err
	}
	cleanup := func() {
		slog.Info("closing the data resources")
		db.Close()
	}
	return &Data{db: db}, cleanup, nil
}

// queryTracer 逐条记录执行的语句与参数。仅开发便利，
// 经 data.database.debug 开启。
type queryTracer struct {
	logger *slog.Logger
}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	t.logger.DebugContext(ctx, "sql query", "sql", data.SQL, "args", redactArgs(data.SQL, data.Args))
	return ctx
}

func (t *queryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// redactArgs 对 set_config 注入的令牌哈希做脱敏：app.session_* 的值是
// 凭据（令牌哈希），debug 日志也不落盘。GUC 名与值成对出现在
// set_config($1, $2, true) 的前两个参数；租户注入
// set_config('app.tenant_id', $1, true) 的名字在 SQL 文本里，租户 ID
// 非凭据，保留可见。
func redactArgs(sql string, args []any) []any {
	if len(args) < 2 || !strings.Contains(sql, "set_config") {
		return args
	}
	name, ok := args[0].(string)
	if !ok || !strings.HasPrefix(name, "app.session_") {
		return args
	}
	redacted := slices.Clone(args)
	redacted[1] = "[redacted]"
	return redacted
}
