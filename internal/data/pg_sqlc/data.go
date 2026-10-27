package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"grape/internal/conf"
	"grape/pkg/redact"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Data 持有各仓储共享的长生命周期存储客户端。
type Data struct {
	db *pgxpool.Pool
}

// SystemData 是系统库句柄，平台级设施（将来全局配置/任务状态等）专用。
// 底层与业务主库句柄 *Data 同构，以命名类型承载是为了在 wire 装配层与
// *Data 区分——wire 按参数类型装配，同型双参无法区分（同 TraceMiddleware
// 的处理）；按用途注入对应句柄，将来两库拆分时消费方零改动。
type SystemData struct {
	*Data
}

// NewData 建立业务主库（data.database.master）连接池，返回客户端与清理
// 函数。装配期一并校验 system 槽：系统库句柄（NewSystemData）暂无消费者，
// wire 不实例化、不建池，其配置错误仍须在启动期暴露，fail-fast 不带病
// 启动。过渡期两槽指向同一实例，拆分时只改配置。
func NewData(c *conf.Data) (*Data, func(), error) {
	if err := validateSlots(c); err != nil {
		return nil, nil, err
	}
	return newData(c.GetDatabase().GetMaster())
}

// NewSystemData 建立系统库（data.database.system）连接池，返回客户端与
// 清理函数。当前无消费方，wire 装配不实例化；两槽校验已由 NewData 在
// 装配期统一完成，此处仅对本槽解析建池。
func NewSystemData(c *conf.Data) (*SystemData, func(), error) {
	d, cleanup, err := newData(c.GetDatabase().GetSystem())
	if err != nil {
		return nil, nil, err
	}
	return &SystemData{Data: d}, cleanup, nil
}

// validateSlots 装配期校验全部槽位：各槽 source 必填且须可解析为连接
// 配置，任一缺失或非法即返回错误并指名槽位，不带病启动。系统库槽位
// 暂无建池消费方，其校验收在本函数、由 NewData 统一触发。
func validateSlots(c *conf.Data) error {
	for _, pair := range []struct {
		name string
		slot *conf.Data_Database_Slot
	}{
		{"master", c.GetDatabase().GetMaster()},
		{"system", c.GetDatabase().GetSystem()},
	} {
		if _, err := parseSlot(pair.slot); err != nil {
			return fmt.Errorf("database slot %s: %w", pair.name, err)
		}
	}
	return nil
}

// newData 是各槽位建池的共享核心：解析槽位配置、建立连接池并 Ping 验证，
// 返回客户端与清理函数。
func newData(slot *conf.Data_Database_Slot) (*Data, func(), error) {
	cfg, err := parseSlot(slot)
	if err != nil {
		return nil, nil, err
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

// parseSlot 把单一槽位解析为连接池配置：source 必填（空串会被 pgx 视为
// 未设置而回落默认连接目标，须显式拒绝）、DSN 非法即报错；debug 开启时
// 挂 queryTracer（脱敏注册表随 tracer 注入）。不触库、不建池，装配期
// 校验与建池共用，可独立单测。
func parseSlot(slot *conf.Data_Database_Slot) (*pgxpool.Config, error) {
	if slot.GetSource() == "" {
		return nil, errors.New("source is empty")
	}
	cfg, err := pgxpool.ParseConfig(slot.GetSource())
	if err != nil {
		return nil, err
	}
	if slot.GetDebug() {
		cfg.ConnConfig.Tracer = &queryTracer{logger: slog.Default(), registry: redact.NewStandardRegistry()}
	}
	return cfg, nil
}

// queryTracer 逐条记录执行的语句与参数。仅开发便利，
// 经 data.database.<槽位>.debug 开启；参数脱敏经 pkg/redact 注册表
// （与审计表、复现视图共用同一脱敏器）。
type queryTracer struct {
	logger   *slog.Logger
	registry *redact.Registry
}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	t.logger.DebugContext(ctx, "sql query", "sql", data.SQL, "args", redactArgs(t.registry, data.SQL, data.Args))
	return ctx
}

func (t *queryTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// redactArgs 对 debug 日志的语句参数做脱敏，两类面：
//
//   - set_config 注入的会话令牌哈希：app.session_* 的名字/值成对出现在
//     前两个参数，值按 token 类别脱敏；租户注入 set_config('app.tenant_id',
//     ...) 的名字在 SQL 文本里，租户 ID 非凭据，保留可见。
//   - 凭据哈希语句（SQL 文本涉及 password_hash / token_hash 列）中的字符串
//     参数按值形态判定：sha256 十六进制（64 位）是令牌哈希，argon2/bcrypt
//     前缀是口令哈希，分别按 token / password 类别脱敏。按形态而非参数
//     位序判定，避免解析 SQL；误伤面收敛于「凭据哈希才会长成的样子」，
//     租户 ID（t-*）与数值参数不受影响。未脱敏的参数原样透传。
func redactArgs(r *redact.Registry, sql string, args []any) []any {
	if len(args) >= 2 && strings.Contains(sql, "set_config") {
		if name, ok := args[0].(string); ok && strings.HasPrefix(name, "app.session_") {
			out := slices.Clone(args)
			if v, ok := out[1].(string); ok {
				out[1], _ = r.Redact(redact.CategoryToken, v)
			} else {
				out[1] = "[redacted]"
			}
			return out
		}
	}
	if !credentialStatement(sql) {
		return args
	}
	var out []any
	for i, a := range args {
		s, ok := a.(string)
		if !ok {
			continue
		}
		category := credentialValueCategory(s)
		if category == "" {
			continue
		}
		if out == nil {
			out = slices.Clone(args)
		}
		out[i], _ = r.Redact(category, s)
	}
	if out == nil {
		return args
	}
	return out
}

// credentialStatement 判定语句是否触达凭据哈希列（列名出现在 SET、
// WHERE、INSERT 列清单等任意位置，按子串匹配即可）。
func credentialStatement(sql string) bool {
	return strings.Contains(sql, "password_hash") || strings.Contains(sql, "token_hash")
}

// credentialValueCategory 按字符串形态归类凭据：sha256 十六进制（令牌
// 哈希，见 biz 会话令牌的哈希纪律）归 token，argon2/bcrypt 编码前缀
// （口令哈希）归 password，其余返回空串（非凭据）。
func credentialValueCategory(s string) string {
	if sha256HexShape(s) {
		return redact.CategoryToken
	}
	if strings.HasPrefix(s, "$argon2id$") || strings.HasPrefix(s, "$argon2i$") ||
		strings.HasPrefix(s, "$argon2d$") || bcryptShape(s) {
		return redact.CategoryPassword
	}
	return ""
}

// sha256HexShape 判定 64 位小写十六进制（sha256 摘要形态）。
func sha256HexShape(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// bcryptShape 判定 bcrypt 编码前缀（$2a/$2b/$2x/$2y）。
func bcryptShape(s string) bool {
	if len(s) < 4 || s[0] != '$' || s[1] != '2' || s[3] != '$' {
		return false
	}
	switch s[2] {
	case 'a', 'b', 'x', 'y':
		return true
	}
	return false
}
