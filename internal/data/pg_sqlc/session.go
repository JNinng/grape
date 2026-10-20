package pg_sqlc

import (
	"context"
	"fmt"

	"grape/internal/data/pg_sqlc/gen"

	"github.com/jackc/pgx/v5"
)

// AuthContext 是服务端认证上下文中的两个身份事实：TenantID 以事务级
// set_config 注入会话变量 app.tenant_id（RLS 靠它隔离租户）；UserID
// 传给权限过滤/判断函数算可见范围，并落入审计列。用户有什么角色、能
// 看到什么，全部由数据库推导，应用层不携带任何权限参数。Scope 只从
// 服务端认证上下文注入，永不受前端请求影响。
type AuthContext struct {
	TenantID string
	UserID   int64
}

func (a AuthContext) validate() error {
	if a.TenantID == "" {
		return fmt.Errorf("pg_sqlc: auth tenant id is empty")
	}
	if a.UserID <= 0 {
		return fmt.Errorf("pg_sqlc: auth user id must be positive, got %d", a.UserID)
	}
	return nil
}

// Session 是绑定身份的事务级会话：app.tenant_id 以 local=true 注入，
// 事务结束自动失效，连接归还池后不残留上一请求的租户上下文。所有经
// 普通数据库角色（grape_u）建立的连接都必须经 Session 访问数据库，
// 禁止绕过租户注入直连。
type Session struct {
	tx   pgx.Tx
	auth AuthContext
	q    *gen.Queries
}

// Q 返回绑定本事务的命名查询集（权限判断、写入等 *Queries 方法）。
// 注意与动态查询的风格区别：动态查询（ParseXxx/ListXxx）是 gen 包
// 顶层函数，DBTX 经 Tx() 显式传入。
func (s *Session) Q() *gen.Queries {
	if s.q == nil {
		s.q = gen.New(s.tx)
	}
	return s.q
}

// Tx 暴露底层事务；生成代码的 List/Count 接受任意 DBTX（含 pgx.Tx）。
func (s *Session) Tx() pgx.Tx { return s.tx }

// Auth 返回本会话的身份事实：审计列（created_by/updated_by）与写前
// 判断函数的操作者入参从这取。
func (s *Session) Auth() AuthContext { return s.auth }

// BasicScope 为 @scope 表 basic 构造守卫参数载体。三个过滤维度的
// 标识符参数统一是当前用户 ID，可见范围由库内权限过滤函数推导；
// Scope 不注入时生成代码 fail-closed 报错，绝不变异成全量可见。
func (s *Session) BasicScope() *gen.BasicScope { return &gen.BasicScope{UserID: s.auth.UserID} }

// SysUserScope 为 @scope 表 sys_user 构造守卫参数载体（同 BasicScope）。
func (s *Session) SysUserScope() *gen.SysUserScope { return &gen.SysUserScope{UserID: s.auth.UserID} }

// Session 开启事务并注入租户会话变量，fn 内经会话访问数据库：fn 返回
// nil 则提交，返回 error 则整体回滚。fn 之外不应触碰数据库。
func (d *Data) Session(ctx context.Context, auth AuthContext, fn func(*Session) error) error {
	if err := auth.validate(); err != nil {
		return err
	}
	tx, err := d.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pg_sqlc: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", auth.TenantID); err != nil {
		return fmt.Errorf("pg_sqlc: set app.tenant_id: %w", err)
	}
	if err := fn(&Session{tx: tx, auth: auth}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
