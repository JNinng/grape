package pg_sqlc

import (
	"context"
	"log/slog"

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
	t.logger.DebugContext(ctx, "sql query", "sql", data.SQL, "args", data.Args)
	return ctx
}

func (t *queryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
