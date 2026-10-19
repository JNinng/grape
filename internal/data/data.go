package data

import (
	"context"
	"errors"
	"log/slog"

	"grape/internal/conf"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/google/wire"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// ProviderSet 是 data 层的 Wire 依赖注入集合。
var ProviderSet = wire.NewSet(NewData, NewUserRepo, NewOrderRepo)

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

// mapStorageError 将驱动层错误映射到 biz 的类型化错误：
// ErrNoRows → notFound；dynq 协议错误 → invalidArg（保留 detail）；
// SQLSTATE 23505 → alreadyExists；其余原样返回。
func mapStorageError(err error, notFound, invalidArg, alreadyExists error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return notFound
	case isDynqError(err):
		if be, ok := invalidArg.(*kratoserrors.Error); ok {
			return kratoserrors.New(int(be.Code), be.Reason, dynq.AsError(err).Reason)
		}
		return invalidArg
	default:
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcodeUniqueViolation {
			return alreadyExists
		}
		return err
	}
}

// isDynqError 报告 err 是否由动态查询协议产生。不能直接用
// dynq.AsError(err) != nil 判断：该函数对任意错误都会返回兜底 Error，
// 会把驱动错误误判为请求错误。
func isDynqError(err error) bool {
	var e *dynq.Error
	return errors.As(err, &e)
}

// pgerrcodeUniqueViolation 是 PostgreSQL 唯一约束冲突的 SQLSTATE。
const pgerrcodeUniqueViolation = "23505"
