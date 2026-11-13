package pg_sqlc

import (
	"grape/internal/biz"
	"grape/internal/data/pg_sqlc/gen"

	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

// querySchemaFunc 是生成代码 schema 出口函数的统一形态。
type querySchemaFunc = func(opts ...dynq.QueryOption) dynq.ResourceSchema

// querySchemaRegistry 将资源名（存储表名）映射到生成代码的 schema 出口。
// 仅收录已暴露动态 List 的资源；新资源接入 List API 时在此追加一行。
// 出口不传能力收窄 opts，与仓储 Parse/List 的调用形态保持同参。
var querySchemaRegistry = map[string]querySchemaFunc{
	"agent_model_call_log": gen.AgentModelCallLogQuerySchema,
	"sys_config":           gen.SysConfigQuerySchema,
	"sys_dept":             gen.SysDeptQuerySchema,
	"sys_dict_data":        gen.SysDictDataQuerySchema,
	"sys_dict_type":        gen.SysDictTypeQuerySchema,
	"sys_feature_flag":     gen.SysFeatureFlagQuerySchema,
	"sys_login_log":        gen.SysLoginLogQuerySchema,
	"sys_operation_log":    gen.SysOperationLogQuerySchema,
	"sys_role":             gen.SysRoleQuerySchema,
	"sys_session":          gen.SysSessionQuerySchema,
	"sys_tenant":           gen.SysTenantQuerySchema,
	"sys_tenant_config":    gen.SysTenantConfigQuerySchema,
	"sys_translation":      gen.SysTranslationQuerySchema,
	"sys_user":             gen.SysUserQuerySchema,
	"sys_user_group":       gen.SysUserGroupQuerySchema,
	"sys_model":            gen.SysModelQuerySchema,
	"kb_library":           gen.KbLibraryQuerySchema,
	"kb_document":          gen.KbDocumentQuerySchema,
}

// querySchemaRepo 是 biz.QuerySchemaProvider 的实现：转发到生成代码出口。
// schema 是纯函数，不触库、无租户边界，无需会话门面。
type querySchemaRepo struct{}

// NewQuerySchemaRepo 构造查询 schema 仓储。
func NewQuerySchemaRepo() biz.QuerySchemaProvider {
	return querySchemaRepo{}
}

// ListSchema 返回资源的能力 schema；未收录资源返回 ErrQueryResourceUnknown。
func (querySchemaRepo) ListSchema(resource string) (dynq.ResourceSchema, error) {
	f, ok := querySchemaRegistry[resource]
	if !ok {
		return dynq.ResourceSchema{}, biz.ErrQueryResourceUnknown
	}
	return f(), nil
}
