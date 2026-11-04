package biz

import (
	"strings"

	v1 "grape/api/query/v1"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/jninng/sqlc_gen_dynamic/dynq"
)

var (
	// ErrQueryResourceUnknown 表示请求的能力 schema 资源未收录（未暴露
	// 动态 List 的资源），不区分「存在但未开放」与「资源名拼错」。
	ErrQueryResourceUnknown = kratoserrors.BadRequest(v1.ErrorReason_QUERY_RESOURCE_UNKNOWN.String(), "unknown query resource")
)

// QuerySchemaProvider 由存储能力目录实现：返回资源动态 List 的能力 schema。
// schema 是（生成期元数据，opts）的确定性纯函数；出口须与 Parse/List/Count
// 使用同一份 opts（四点同参纪律），当前查询面未传能力收窄 opts，故恒为
// 全量 schema。schema 不触库、无租户边界，是能力元数据而非行数据。
type QuerySchemaProvider interface {
	// ListSchema 返回资源的 List 能力 schema；未收录资源返回 ErrQueryResourceUnknown。
	ListSchema(resource string) (dynq.ResourceSchema, error)
}

// QueryUsecase 是动态查询能力出口用例：为前端契约端点转发资源 schema。
type QueryUsecase struct {
	provider QuerySchemaProvider
}

// NewQueryUsecase 构造查询能力出口用例。
func NewQueryUsecase(provider QuerySchemaProvider) *QueryUsecase {
	return &QueryUsecase{provider: provider}
}

// ListSchema 返回资源的 List 能力 schema。
func (uc *QueryUsecase) ListSchema(resource string) (dynq.ResourceSchema, error) {
	if strings.TrimSpace(resource) == "" {
		return dynq.ResourceSchema{}, ErrQueryResourceUnknown
	}
	return uc.provider.ListSchema(resource)
}
