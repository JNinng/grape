package pg_sqlc

import (
	"errors"
	"testing"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// TestMapStorageErrorClassifiesDriverErrors 验证驱动错误按语义分类，
// 而不是被 dynq 的兜底错误吞成 invalidArg。
func TestMapStorageErrorClassifiesDriverErrors(t *testing.T) {
	notFound := kratoserrors.NotFound("not_found", "not found")
	invalidArg := kratoserrors.BadRequest("invalid_argument", "invalid argument")
	alreadyExists := kratoserrors.Conflict("already_exists", "already exists")

	t.Run("no rows", func(t *testing.T) {
		if got := mapStorageError(pgx.ErrNoRows, notFound, invalidArg, alreadyExists); got != notFound {
			t.Fatalf("want notFound, got %v", got)
		}
	})

	t.Run("unique violation", func(t *testing.T) {
		err := &pgconn.PgError{Code: pgerrcodeUniqueViolation}
		if got := mapStorageError(err, notFound, invalidArg, alreadyExists); got != alreadyExists {
			t.Fatalf("want alreadyExists, got %v", got)
		}
	})

	t.Run("dynq error keeps reason", func(t *testing.T) {
		err := dynq.NewErr(dynq.KindWhere, "age", "op not allowed", 0)
		got := mapStorageError(err, notFound, invalidArg, alreadyExists)
		var ke *kratoserrors.Error
		if !errors.As(got, &ke) {
			t.Fatalf("want kratos error, got %T", got)
		}
		if ke.Code != invalidArg.Code || ke.Reason != invalidArg.Reason {
			t.Fatalf("unexpected code/reason: %+v", ke)
		}
		if ke.Message != "op not allowed" {
			t.Fatalf("want dynq reason as message, got %q", ke.Message)
		}
	})

	t.Run("generic error passes through", func(t *testing.T) {
		err := errors.New("connection reset")
		if got := mapStorageError(err, notFound, invalidArg, alreadyExists); got != err {
			t.Fatalf("want original error, got %v", got)
		}
	})
}
