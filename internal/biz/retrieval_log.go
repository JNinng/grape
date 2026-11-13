package biz

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
)

// Agent 检索留痕：向量检索与摄取写入的证据链契约。写入侧为 data 层装饰器
//（单路直调检索与摄取侧）与知识检索编排层（自写完整行）两处，共同保住
// 「一次操作一行」（见 WithRetrievalLogSuppressed）；查询侧供运维复现视图的
// 手写端点消费。检索内容与过滤摘要在写入方内经 pkg/redact 脱敏后落库——
// 编排层自写行时脱敏义务随之迁移到编排层；本域读侧直接返回已脱敏形态
//（复现视图不二次脱敏也不回传原文）。

// 留痕行的领域取值：
//   - source：检索来源。tool-agent 检索工具链路（调用检索用例前经
//     WithRetrievalSource 显式声明）；console-前端主动检索（检索用例在
//     调用方未声明时的缺省）；pipeline-摄取管线（Add/Update/DeleteByFilter）；
//     管理面（索引重建/过滤策略调整，super 路径占位）的扩展取值与触发点见
//     sqlc/schema/97_agent_retrieval_log.sql 的头注释；
//   - status：操作终态，成功 / 失败（失败行同样留痕——尝试即证据，
//     与工具调用审计的终态口径一致）。
const (
	RetrievalSourceTool     = "tool"
	RetrievalSourceConsole  = "console"
	RetrievalSourcePipeline = "pipeline"

	RetrievalStatusSuccess = "success"
	RetrievalStatusError   = "error"
)

// 留痕列宽与分页口径：与 schema/97_agent_retrieval_log.sql 的列注释及
// 审计的分页惯例一致（默认 20、单页上限 200、offset 非负）。
const (
	retrievalSessionColumnWidth = 255
	retrievalLogDefaultPageSize = 20
	retrievalLogMaxPageSize     = 200
)

// RetrievalLogInvalidArgument 表示检索留痕查询的入参非法。留痕端点为
// 手写 JSON 端点（无 proto reason 枚举），语义与生成端点的枚举错误一致。
var ErrRetrievalLogInvalidArgument = kratoserrors.BadRequest("AGENT_RETRIEVAL_INVALID_ARGUMENT", "invalid retrieval log argument")

// RetrievalHit 是单条命中（或摄取行为的作用文档）的证据单元：三个字段
// 都是服务端产出的标识与数值，不含文档正文——正文反查经
// agent.agent_document（验收链路：检索命中 → 留痕行 → document）。
// json 标签即落库 jsonb 的元素键（snake_case，与列注释声明一致）。
type RetrievalHit struct {
	// DocumentID 是命中文档（切片）ID，即 agent_document.id。
	DocumentID string `json:"document_id"`
	// Score 是随阶段自解释的分数，不做归一：向量召回为归一化相似度
	//（[0,1]，越大越相似）、词法召回为 ts_rank_cd 相关性（未归一）、融合
	// 为 RRF 分（Σ 1/(k+rank)）、重排为 rerank 模型相关性分；hits 列取最终
	// 命中，值为 rerank 分（装配重排层时）或 RRF 融合分（未装配/降级）。
	// 同阶段（同一次输出的同一路）内可比，跨阶段与跨方法不可比；摄取行为
	// 无分数语义，取 0。
	Score float64 `json:"score"`
	// Anchor 是原文锚点（切片元数据携带的 rune 区间描述）；无锚点为空。
	Anchor string `json:"anchor,omitempty"`
}

// RetrievalLogRecord 是一条留痕的写入侧领域模型：身份与租户不是入参——
// 仓储经 IdentityTx 从认证 ctx 提取，trace_id/session_id 在写入方从 ctx
// 事实提取，调用方无法伪造归属。QueryText 与 FilterSummary 收「已脱敏
// 成型」的最终形态（脱敏与截断是写入方的落库前义务：装饰器侧在其构建
// 行时完成，编排层自写行时由编排层完成——见 knowledge_search 的阶段
// 留痕）；Stages 同为已成型 JSON（阶段内查询文本与错误摘要已脱敏截断）。
type RetrievalLogRecord struct {
	// TraceID 是链路 ID（ctx SpanContext）；无链路事实为空，
	// 落库为 NULL——链路缺失如实留空，不伪造。
	TraceID string
	// SessionID 是 Agent 会话 ID（检索工具链路经 ctx 事实载体携带）；
	// 不可得为空，落库为 NULL。
	SessionID string
	// Source 是检索来源（RetrievalSource* 常量）。
	Source string
	// Embedder 是本次向量操作的嵌入空间键，按操作现场声明原样留痕；
	// 不做注册表对账——失败尝试的原始声明正是排查换空间事故的证据。
	Embedder string
	// QueryText 是已脱敏的查询文本；无查询事实（摄取侧）为空，落库 NULL。
	QueryText string
	// RequestedTopK 是请求的 top_k（编排层行取归一化后的 top_k，装饰器
	// 单路行取存储契约收到的 Limit）；0 表示未显式指定，落库 NULL。
	// 硬上限钳制发生在本值上游。
	RequestedTopK int
	// EffectiveTopK 是实际生效的命中上限（检索行必 >0）；非检索行为 0，
	// 落库 NULL。
	EffectiveTopK int
	// FilterSummary 是过滤条件摘要（已脱敏成型的 JSON 形态载体）；
	// 无条件为 nil，落库 NULL。
	FilterSummary map[string]any
	// Hits 是命中（检索行）或作用文档（摄取行，单元素）；空为无命中，
	// 落库 NULL。
	Hits []RetrievalHit
	// Stages 是检索阶段数组（已脱敏成型的 JSON 载体，见
	// knowledge_search_stages.go 的元素形态）；装饰器单路行不携带（nil，
	// 落库 NULL——不伪造空阶段），摄取行同样为 nil。
	Stages json.RawMessage
	// DurationMS 是操作耗时（毫秒）；未知为 0，落库 NULL。
	DurationMS int64
	// Status 是操作终态（RetrievalStatus* 常量）。
	Status string
}

// RetrievalLog 是检索留痕的读侧完整行。FilterSummary / Hits / Stages 以
// 落库的 JSON 形态透传（json.RawMessage）：命中、过滤摘要与阶段时间线是
// 证据面，按落库原样渲染，不做读侧重塑（同「返回脱敏后形态」口径——
// 写入侧已脱敏）。装饰器单路行的 Stages 为 NULL（不伪造空阶段）。
// RequestedTopK / EffectiveTopK 的 0 值渲染 NULL（未指定 / 不适用）。
type RetrievalLog struct {
	ID            int64
	TenantID      string
	UserID        int64
	TraceID       string
	SessionID     string
	Source        string
	Embedder      string
	QueryText     string
	RequestedTopK int32
	EffectiveTopK int32
	FilterSummary json.RawMessage
	Hits          json.RawMessage
	Stages        json.RawMessage
	DurationMS    int64 // 0 表示未记录
	Status        string
	CreatedAt     time.Time
}

// RetrievalLogFilter 是留痕查询的过滤与分页参数：租户边界不在此列
// （实现从认证 ctx 提取）。SessionID / Source 为空表示不过滤；Limit
// 为 0 时取默认页大小。
type RetrievalLogFilter struct {
	SessionID string
	Source    string
	Limit     int
	Offset    int
}

// RetrievalLogList 是检索留痕的分页信封。
type RetrievalLogList struct {
	Items []*RetrievalLog
	Total int64
}

// RetrievalLogRepo 是检索留痕读写的仓储契约，由 data 层实现；写入与
// 查询都绑定认证身份（IdentityTx，每事务 set_config('app.tenant_id',
// ..., true)），身份从 ctx 提取，契约不接收身份参数。表为追加写，
// 契约只有写入与分页读取的最小面，没有更新/删除/点查。
type RetrievalLogRepo interface {
	// Append 写入一条检索留痕。
	Append(ctx context.Context, rec *RetrievalLogRecord) error
	// List 按会话/来源过滤分页查询（时间倒序，同秒以 id tie-break）。
	List(ctx context.Context, filter *RetrievalLogFilter) (*RetrievalLogList, error)
}

// RetrievalLogUsecase 是检索留痕的查询用例（写入侧不经用例：留痕行由
// data 层装饰器在操作现场构造并直写仓储，脱敏与截断在装饰器完成）。
// 查询侧的入参校验与身份门禁在此落地。
type RetrievalLogUsecase struct {
	repo RetrievalLogRepo
}

// NewRetrievalLogUsecase 构造检索留痕查询用例。
func NewRetrievalLogUsecase(repo RetrievalLogRepo) *RetrievalLogUsecase {
	return &RetrievalLogUsecase{repo: repo}
}

// List 分页查询检索留痕（时间倒序）。身份缺失按未认证 fail-closed：
// 读面与写面同一纪律，查询只以认证身份发生。
func (uc *RetrievalLogUsecase) List(ctx context.Context, filter *RetrievalLogFilter) (*RetrievalLogList, error) {
	if _, ok := IdentityFromContext(ctx); !ok {
		return nil, ErrSessionInvalid
	}
	f, err := retrievalFilterOf(filter)
	if err != nil {
		return nil, err
	}
	return uc.repo.List(ctx, f)
}

// retrievalFilterOf 归一查询过滤：页大小缺省、上限、来源取值与 offset
// 非负校验在 biz 落地（fail-closed，不静默截断）。来源取值必须命中
// RetrievalSource* 常量——空串不过滤，其余未知值拒绝而非空结果。
func retrievalFilterOf(filter *RetrievalLogFilter) (*RetrievalLogFilter, error) {
	f := &RetrievalLogFilter{}
	if filter != nil {
		*f = *filter
	}
	f.SessionID = strings.TrimSpace(f.SessionID)
	if len(f.SessionID) > retrievalSessionColumnWidth {
		return nil, ErrRetrievalLogInvalidArgument
	}
	switch f.Source {
	case "", RetrievalSourceTool, RetrievalSourceConsole, RetrievalSourcePipeline:
	default:
		return nil, ErrRetrievalLogInvalidArgument
	}
	if f.Limit == 0 {
		f.Limit = retrievalLogDefaultPageSize
	}
	if f.Limit < 0 || f.Limit > retrievalLogMaxPageSize || f.Offset < 0 {
		return nil, ErrRetrievalLogInvalidArgument
	}
	return f, nil
}

// 留痕的 ctx 事实载体：查询文本与会话 ID 在检索工具执行现场（agent 层）
// 提取后经 ctx 传入写入方——框架类型不向下游渗透，data 层无从感知框架执行
// ctx；消费方为编排行（知识检索用例）与未抑制的单路直调行（装饰器）。

type sessionIDKey struct{}

// WithSessionID 注入当前请求的 Agent 会话 ID。仅供框架适配层在执行
// 现场调用；检索编排层（编排行）与留痕装饰器（未抑制的单路直调行）消费，
// 其他面不得依赖该事实的存在。
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if strings.TrimSpace(sessionID) == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDKey{}, sessionID)
}

// SessionIDFromContext 读取会话 ID 事实；缺失返回空串。
func SessionIDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(sessionIDKey{}).(string)
	return s
}

type retrievalQueryKey struct{}

// WithRetrievalQuery 注入本次检索的查询文本（Embedder 调用的原文）。
// 注入的是原始文本：脱敏在写入方落库前执行（编排层行与装饰器行各按
// retrieval 类别处理），载体只负责传递事实。
func WithRetrievalQuery(ctx context.Context, query string) context.Context {
	if strings.TrimSpace(query) == "" {
		return ctx
	}
	return context.WithValue(ctx, retrievalQueryKey{}, query)
}

// RetrievalQueryFromContext 读取查询文本事实；缺失返回空串（写入方落库
// 为 NULL，不伪造查询文本）。
func RetrievalQueryFromContext(ctx context.Context) string {
	s, _ := ctx.Value(retrievalQueryKey{}).(string)
	return s
}

type retrievalSourceKey struct{}

// WithRetrievalSource 声明本次检索的来源（RetrievalSource* 常量）。检索
// 用例在调用方未声明时缺省记 console（前端链路），agent 工具链路在调用
// 前显式声明 tool——否则会被误记为前端检索。
func WithRetrievalSource(ctx context.Context, source string) context.Context {
	if strings.TrimSpace(source) == "" {
		return ctx
	}
	return context.WithValue(ctx, retrievalSourceKey{}, source)
}

// RetrievalSourceFromContext 读取检索来源事实；缺失返回空串（由消费方
// 决定缺省）。
func RetrievalSourceFromContext(ctx context.Context) string {
	s, _ := ctx.Value(retrievalSourceKey{}).(string)
	return s
}

// 留痕抑制标记：混合检索编排一次操作多路调用 VectorStore，逐路留痕会把
// 「一次操作一行」拆成多行。编排路径打上本标记让装饰器跳过 Search /
// SearchLexical 的逐路留痕，由编排层在完成处自写完整行；摄取行不受影响。

type retrievalLogSuppressedKey struct{}

// WithRetrievalLogSuppressed 声明本次调用链的检索留痕由编排层负责：
// 装饰器消费该标记跳过 Search / SearchLexical 的逐路留痕。仅供检索
// 编排层在其内部调用链上注入。
func WithRetrievalLogSuppressed(ctx context.Context) context.Context {
	return context.WithValue(ctx, retrievalLogSuppressedKey{}, true)
}

// RetrievalLogSuppressedFromContext 读取留痕抑制标记；未声明返回 false。
func RetrievalLogSuppressedFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(retrievalLogSuppressedKey{}).(bool)
	return v
}
