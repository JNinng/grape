package pg_sqlc

import (
	"errors"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

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
