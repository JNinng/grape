package pg_sqlc

import (
	"context"
	"errors"
	"fmt"
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
// 挂 queryTracer。不触库、不建池，装配期校验与建池共用，可独立单测。
func parseSlot(slot *conf.Data_Database_Slot) (*pgxpool.Config, error) {
	if slot.GetSource() == "" {
		return nil, errors.New("source is empty")
	}
	cfg, err := pgxpool.ParseConfig(slot.GetSource())
	if err != nil {
		return nil, err
	}
	if slot.GetDebug() {
		cfg.ConnConfig.Tracer = &queryTracer{logger: slog.Default()}
	}
	return cfg, nil
}

// queryTracer 逐条记录执行的语句与参数。仅开发便利，
// 经 data.database.<槽位>.debug 开启。
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
