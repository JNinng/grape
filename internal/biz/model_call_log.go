package biz

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"

	"grape/pkg/redact"
)

// Agent 模型调用留痕：管线侧模型/类模型调用面（文本生成、向量化、检索
// 重排、外部文档解析）的证据链契约。写入侧由四个能力目录（openaicompleter /
// openaiembedder / openaireranker / docparse）经 ModelCallObserver 观察面
// 消费——观察面
// 在「解析出端点事实之后、调用终态（含 error）」构造写入载体，用例完成
// 身份与链路事实补齐、脱敏与截断后旁路落库；查询侧供运维复现视图的手写
// 端点消费。agent 对话 LLM 不在本域：会话事件流与 /agui/session-usage
// 已承载该面（见 replay.md），避免双写。

// 留痕行的领域取值：
//   - scenario：调用场景。由调用链路经 WithModelCallScene 在调用现场声明；
//     缺失时按实现声明的缺省回落（embed→embed_document、
//     completer→ingest_prefix、reranker→rerank；docparse 无回落，缺失
//     放弃该行），不伪造其他值。词表扩展在此追加常量并同步
//     schema/99_model_call_log.sql 的列注释——表无 CHECK，词表由应用层
//     把门；
//   - status：调用终态。成功 / 失败（失败行同样留痕——尝试即证据，与
//     工具调用审计的终态口径一致）；空产出记 output 为空。
const (
	ModelCallScenarioIngestSummary = "ingest_summary"
	ModelCallScenarioIngestPrefix  = "ingest_prefix"
	ModelCallScenarioEmbedDocument = "embed_document"
	ModelCallScenarioEmbedQuery    = "embed_query"
	ModelCallScenarioParseDocument = "parse_document"
	ModelCallScenarioRerank        = "rerank"

	ModelCallStatusSuccess = "success"
	ModelCallStatusError   = "error"
)

// 留痕列宽与摘要上限：与 schema/99_model_call_log.sql 的列注释一致。
// 摘要列是 text，长度约束由应用层在本文件执行（超限截断），不设数据库
// CHECK——上限调整不伴随存量行迁移。
const (
	modelCallTraceColumnWidth    = 32
	modelCallSessionColumnWidth  = 255
	modelCallScenarioColumnWidth = 32
	modelCallModelKeyColumnWidth = 128
	modelCallEndpointColumnWidth = 255
	// ModelCallSummaryMaxRunes 是输入/输出摘要的截断上限（rune 计）：
	// 脱敏后的兜底截断，与审计摘要口径对齐（pkg/redact 的 model_call
	// 类别同值）。
	ModelCallSummaryMaxRunes = 4096
	// ModelCallErrorMaxRunes 是失败错误摘要的截断上限（error 列
	// varchar(512)）。
	ModelCallErrorMaxRunes = 512
)

// 留痕查询的分页口径：默认页大小与单页上限取 @page 的全局缺省
// （sqlc.yaml 的 page_default/page_max：20/200）；offset 非负。
const (
	modelCallDefaultPageSize = 20
	modelCallMaxPageSize     = 200
)

// ModelCallLogInvalidArgument 表示模型调用留痕查询的入参非法。留痕端点
// 为手写 JSON 端点（无 proto reason 枚举），语义与生成端点的枚举错误一致。
var ErrModelCallInvalidArgument = kratoserrors.BadRequest("AGENT_MODEL_CALL_INVALID_ARGUMENT", "invalid model call log argument")

// ValidModelCallScenario 报告场景取值是否命中词表（含空串——空串是过滤
// 面「不过滤」的合法形态，不是写入面；写入面的空场景由观察面在回落之后
// 校验，docparse 缺失场景在能力目录即放弃该行）。词表是 scenario 列的
// 应用层把门（表无 CHECK），词表扩展同步本函数与 schema 列注释。
func ValidModelCallScenario(scenario string) bool {
	switch scenario {
	case ModelCallScenarioIngestSummary, ModelCallScenarioIngestPrefix,
		ModelCallScenarioEmbedDocument, ModelCallScenarioEmbedQuery,
		ModelCallScenarioParseDocument, ModelCallScenarioRerank:
		return true
	}
	return false
}

// ModelCallEntry 是一次模型调用的写入侧载体：四个能力目录在调用终态
// 构造，身份与租户不是入参——观察面从认证 ctx 提取（缺失放弃该行，
// fail-closed 不伪造归属）。InputSummary / OutputSummary / Error 收未
// 脱敏原文，脱敏与截断是观察面的落库前义务；Endpoint 收解析出的协议
// 路径完整 URL。
type ModelCallEntry struct {
	// TraceID 是链路 ID（ctx SpanContext），由观察面补齐；无链路事实为
	// 空，落库 NULL——链路缺失如实留空，不伪造。
	TraceID string
	// SessionID 是 Agent 会话 ID（ctx 事实载体携带）；管线侧调用无会话
	// 事实为空，落库 NULL。
	SessionID string
	// Scenario 是调用场景（ModelCallScenario* 常量）。空值由消费能力
	// 目录按其声明的缺省回落补齐；词表外取值在观察面放弃该行。
	Scenario string
	// ModelKey 是模型键（文本模型 model_key / 嵌入空间键），按解析现场
	// 原样留痕，不做注册表对账——失败尝试的原始声明正是排查换模型事故
	// 的证据。无模型事实为空，落库 NULL。
	ModelKey string
	// Endpoint 是调用端点（协议路径完整 URL）；端点事实缺失为空，落库
	// NULL。
	Endpoint string
	// DocumentID 是作用文档 ID（kb_document.id）；无文档事实为 0，落库
	// NULL。
	DocumentID int64
	// InputSummary 是输入摘要原文（prompt / 向量化文本 / 原件事实摘要）；
	// 写入前脱敏 + 截断。docparse 的原件字节不进本列（记事实摘要）。
	InputSummary string
	// OutputSummary 是输出摘要原文（生成文本 / 维度说明 / markdown 产物）；
	// 写入前脱敏 + 截断。空产出为空串。
	OutputSummary string
	// InputTokens 是输入 token 用量（端点 usage）；未上报为 0，落库 NULL。
	InputTokens int32
	// OutputTokens 是输出 token 用量（端点 usage）；未上报为 0，落库 NULL。
	OutputTokens int32
	// DurationMS 是调用耗时（毫秒）；未知留 0，落库 NULL。
	DurationMS int64
	// Status 是调用终态（ModelCallStatus* 常量）。
	Status string
	// Error 是失败错误摘要原文（写入前脱敏 + 截断 512）；成功行为空。
	Error string
}

// ModelCallLog 是模型调用留痕的读侧完整行。InputSummary / OutputSummary /
// Error 是落库时已脱敏的形态；InputTokens / OutputTokens / DocumentID 的
// 0 值渲染 NULL（未上报 / 无文档事实）。
type ModelCallLog struct {
	ID            int64
	TenantID      string
	UserID        int64
	TraceID       string
	SessionID     string
	Scenario      string
	ModelKey      string
	Endpoint      string
	DocumentID    int64
	InputSummary  string
	OutputSummary string
	InputTokens   int32
	OutputTokens  int32
	DurationMS    int64 // 0 表示未记录
	Status        string
	Error         string
	CreatedAt     time.Time
}

// ModelCallFilter 是留痕查询的过滤与分页参数：租户边界不在此列（实现从
// 认证 ctx 提取）。各过滤字段零值表示不过滤；Limit 为 0 时取默认页大小。
type ModelCallFilter struct {
	Scenario    string
	ModelKey    string
	Status      string
	DocumentID  int64
	CreatedFrom time.Time
	CreatedTo   time.Time
	Limit       int
	Offset      int
}

// ModelCallList 是模型调用留痕的分页信封。
type ModelCallList struct {
	Items []*ModelCallLog
	Total int64
}

// ModelCallLogRepo 是模型调用留痕读写的仓储契约，由 data 层实现；写入
// 与查询都绑定认证身份（IdentityTx，每事务 set_config('app.tenant_id',
// ..., true)），身份从 ctx 提取，契约不接收身份参数。表为追加写，契约
// 只有写入与分页读取的最小面，没有更新/删除/点查。
type ModelCallLogRepo interface {
	// Append 写入一条模型调用留痕。
	Append(ctx context.Context, entry *ModelCallEntry) error
	// List 按过滤条件分页查询（时间倒序，同秒以 id tie-break）。
	List(ctx context.Context, filter *ModelCallFilter) (*ModelCallList, error)
	// Count 按同条件统计条数（与 List 同口径，供分页信封）。
	Count(ctx context.Context, filter *ModelCallFilter) (int64, error)
}

// ModelCallObserver 是模型调用留痕的观察面挂点：四个能力目录在调用终态
// 调用，实现方（ModelCallLogUsecase）负责身份补齐、脱敏截断与旁路写入。
// 观察面缺省 nil 即不留痕（能力目录零开销跳过）；观察不改变调用结果与
// 错误——写入失败只记日志，不反噬业务主流程。
type ModelCallObserver interface {
	ObserveModelCall(ctx context.Context, entry *ModelCallEntry)
}

// modelCallWriteTimeout 是单次留痕写入的短超时预算：留痕是旁路观察面，
// 存储悬挂不得反噬业务主流程——超时后放弃该行并记日志。变量形态供测试
// 注入极短预算（生产恒为 5s，对齐审计写入的 auditWriteTimeout）。
var modelCallWriteTimeout = 5 * time.Second

// ModelCallLogUsecase 是模型调用留痕的查询用例兼观察面实现：查询侧的
// 入参校验与身份门禁在此落地；观察侧（ObserveModelCall）按审计写入的
// 同一纪律旁路落库（身份缺失放弃、脱敏截断、独立短超时、失败只记日志）。
type ModelCallLogUsecase struct {
	repo     ModelCallLogRepo
	registry *redact.Registry
}

// NewModelCallLogUsecase 构造模型调用留痕用例：registry 是与审计表、
// 检索留痕共用的脱敏注册表（单一代码点，经 wire 提供同一实例）；
// registry 为 nil 时按「放行原值 + 截断兜底」降级（与审计用例同口径）。
func NewModelCallLogUsecase(repo ModelCallLogRepo, registry *redact.Registry) *ModelCallLogUsecase {
	return &ModelCallLogUsecase{repo: repo, registry: registry}
}

// 编译期断言：用例实现观察面接口，接线义务的形状约束在此固定。
var _ ModelCallObserver = (*ModelCallLogUsecase)(nil)

// ObserveModelCall 记录一次模型调用留痕（ModelCallObserver 实现）。身份
// 缺失放弃该行（fail-closed 不伪造）；场景词表外与入参非法同样放弃；
// 写入走脱离请求生命周期的独立短超时 ctx（WithoutCancel 保留身份与链路
// 事实），错误与 panic 都只记日志、不向上传播——留痕不得反噬业务主流程。
func (uc *ModelCallLogUsecase) ObserveModelCall(ctx context.Context, entry *ModelCallEntry) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("model call log observer panicked", "panic", r)
		}
	}()
	if entry == nil {
		return
	}
	if _, ok := IdentityFromContext(ctx); !ok {
		slog.WarnContext(ctx, "model call log dropped: no authenticated identity", "scenario", entry.Scenario)
		return
	}
	rec, err := uc.buildEntry(ctx, entry)
	if err != nil {
		slog.WarnContext(ctx, "model call log dropped: invalid entry", "scenario", entry.Scenario, "error", err)
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), modelCallWriteTimeout)
	defer cancel()
	if err := uc.repo.Append(wctx, rec); err != nil {
		slog.WarnContext(wctx, "model call log write failed", "scenario", rec.Scenario, "status", rec.Status, "error", err)
	}
}

// buildEntry 校验写入载体并补齐链路事实、完成摘要脱敏截断。场景与状态
// 必须命中词表常量（应用层把门，表无 CHECK）；列宽超限按载体非法拒绝——
// 异常输入显式放弃该行而非静默改写（留痕不伪造）。
func (uc *ModelCallLogUsecase) buildEntry(ctx context.Context, in *ModelCallEntry) (*ModelCallEntry, error) {
	switch {
	case !ValidModelCallScenario(in.Scenario) || len(in.Scenario) > modelCallScenarioColumnWidth:
		return nil, ErrModelCallInvalidArgument
	case in.Status != ModelCallStatusSuccess && in.Status != ModelCallStatusError:
		return nil, ErrModelCallInvalidArgument
	case len(in.ModelKey) > modelCallModelKeyColumnWidth:
		return nil, ErrModelCallInvalidArgument
	case len(in.Endpoint) > modelCallEndpointColumnWidth:
		return nil, ErrModelCallInvalidArgument
	case in.DocumentID < 0 || in.InputTokens < 0 || in.OutputTokens < 0 || in.DurationMS < 0:
		return nil, ErrModelCallInvalidArgument
	case in.DurationMS > int64(^uint32(0)>>1):
		// duration_ms 列是 int4：超列宽的耗时按载体非法拒绝，让异常
		// 输入显式失败而非静默回绕。
		return nil, ErrModelCallInvalidArgument
	}
	out := *in
	out.TraceID = TraceIDFromContext(ctx)
	out.SessionID = SessionIDFromContext(ctx)
	if len(out.SessionID) > modelCallSessionColumnWidth {
		return nil, ErrModelCallInvalidArgument
	}
	out.InputSummary = uc.redactSummary(in.InputSummary)
	out.OutputSummary = uc.redactSummary(in.OutputSummary)
	out.Error = truncateModelCallError(uc.redactSummary(in.Error))
	return &out, nil
}

// redactSummary 消费 model_call 类别脱敏并做长度兜底。未登记类别放行
// 原值但保留截断：留痕目的是留证，因脱敏语义未声明而丢弃整行比落一条
// 已截断原文危害更大；已登记输出同样过上限。
func (uc *ModelCallLogUsecase) redactSummary(value string) string {
	if value == "" {
		return ""
	}
	if uc.registry != nil {
		redacted, _ := uc.registry.Redact(redact.CategoryModelCall, value)
		value = redacted
	}
	return truncateModelCallSummary(value)
}

// truncateModelCallSummary 按 rune 截断到 ModelCallSummaryMaxRunes（多
// 字节字符不劈开），尾部以标记说明发生了截断，便于留痕侧识别不完整摘要。
func truncateModelCallSummary(value string) string {
	if utf8.RuneCountInString(value) <= ModelCallSummaryMaxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:ModelCallSummaryMaxRunes]) + "...(truncated)"
}

// truncateModelCallError 按 rune 截断错误摘要到 error 列宽（512）。
func truncateModelCallError(value string) string {
	if utf8.RuneCountInString(value) <= ModelCallErrorMaxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:ModelCallErrorMaxRunes]) + "...(truncated)"
}

// List 分页查询模型调用留痕（时间倒序）。身份缺失按未认证 fail-closed：
// 读面与写面同一纪律，查询只以认证身份发生。
func (uc *ModelCallLogUsecase) List(ctx context.Context, filter *ModelCallFilter) (*ModelCallList, error) {
	if _, ok := IdentityFromContext(ctx); !ok {
		return nil, ErrSessionInvalid
	}
	f, err := modelCallFilterOf(filter)
	if err != nil {
		return nil, err
	}
	return uc.repo.List(ctx, f)
}

// modelCallFilterOf 归一查询过滤：页大小缺省、上限、场景/状态词表与
// offset 非负校验在 biz 落地（fail-closed，不静默截断）。场景与状态取值
// 必须命中词表常量——空串不过滤，其余未知值拒绝而非空结果；模型键做
// 列宽校验。
func modelCallFilterOf(filter *ModelCallFilter) (*ModelCallFilter, error) {
	f := &ModelCallFilter{}
	if filter != nil {
		*f = *filter
	}
	f.Scenario = strings.TrimSpace(f.Scenario)
	f.ModelKey = strings.TrimSpace(f.ModelKey)
	switch {
	case f.Scenario != "" && !ValidModelCallScenario(f.Scenario):
		return nil, ErrModelCallInvalidArgument
	}
	switch f.Status {
	case "", ModelCallStatusSuccess, ModelCallStatusError:
	default:
		return nil, ErrModelCallInvalidArgument
	}
	switch {
	case len(f.ModelKey) > modelCallModelKeyColumnWidth:
		return nil, ErrModelCallInvalidArgument
	case f.DocumentID < 0:
		return nil, ErrModelCallInvalidArgument
		// 时间序倒置只在两端都声明时校验：单端时间范围是合法形态
		//（「某时点之后 / 之前」），零值端表示不过滤。
	case !f.CreatedFrom.IsZero() && !f.CreatedTo.IsZero() && f.CreatedFrom.After(f.CreatedTo):
		return nil, ErrModelCallInvalidArgument
	}
	if f.Limit == 0 {
		f.Limit = modelCallDefaultPageSize
	}
	if f.Limit < 0 || f.Limit > modelCallMaxPageSize || f.Offset < 0 {
		return nil, ErrModelCallInvalidArgument
	}
	return f, nil
}

// 留痕的 ctx 事实载体：场景与作用文档在调用现场（biz 用例）声明后经
// ctx 传入能力目录的观察面——场景由最了解调用语义的调用方声明，能力
// 目录只承载事实；文档 ID 在 kb_document 行上下文里已知（摄取与解析
// 链路），管线直调（无 kb 行）缺失为 NULL，不伪造。空白/零值不注入
// （缺失如实留空，与检索留痕载体同一纪律）。

type modelCallSceneKey struct{}

// WithModelCallScene 声明本次调用的场景（ModelCallScenario* 常量）。
// 调用方在发起模型调用前注入；能力目录的观察面消费，缺失按该目录声明
// 的缺省回落。
func WithModelCallScene(ctx context.Context, scenario string) context.Context {
	if strings.TrimSpace(scenario) == "" {
		return ctx
	}
	return context.WithValue(ctx, modelCallSceneKey{}, scenario)
}

// ModelCallSceneFromContext 读取调用场景事实；缺失返回空串（由消费能力
// 目录决定缺省回落，docparse 缺失放弃该行）。
func ModelCallSceneFromContext(ctx context.Context) string {
	s, _ := ctx.Value(modelCallSceneKey{}).(string)
	return s
}

type modelCallDocumentKey struct{}

// WithModelCallDocument 注入本次调用作用的知识条目 ID（kb_document.id）。
// 摄取与解析链路在调用现场注入；检索查询向量化等无文档事实的调用不注入。
func WithModelCallDocument(ctx context.Context, documentID int64) context.Context {
	if documentID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, modelCallDocumentKey{}, documentID)
}

// ModelCallDocumentFromContext 读取作用文档事实；缺失返回 0（落库 NULL，
// 不伪造文档归属）。
func ModelCallDocumentFromContext(ctx context.Context) int64 {
	id, _ := ctx.Value(modelCallDocumentKey{}).(int64)
	return id
}
