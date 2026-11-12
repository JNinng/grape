package biz

import (
	"context"
	"encoding/json"
	"log/slog"
	"unicode/utf8"

	"grape/pkg/redact"
)

// 检索分阶段留痕（stages）：混合检索编排层在完成处自写一行完整留痕，
// stages 列承载「召回 → 融合 → 重排 → 回包」的阶段数组——数组顺序即
// 执行序，后一阶段的输入序即前一阶段的输出序，不重复记录。编排路径经
// WithRetrievalLogSuppressed 让 data 层装饰器跳过逐路留痕（保住「一次
// 操作一行」）；未启用混合的单路直调不经本文件，由装饰器记行且 stages
// 为 NULL（不伪造空阶段）。
//
// 阶段形态（落库 jsonb，schema/97_agent_retrieval_log.sql 列注释为权威
// 声明）：元素为 {"stage","query_text","limit","model","candidates",
// "units","error","duration_ms"}——query_text 仅召回阶段承载（记变体
// 文本，与主查询同脱敏口径）；candidates 形态对齐 RetrievalHit，每阶段
// 上限 50（沿用 hits 列的截断口径），分数按阶段自解释（向量相似度 [0,1]
// / 词法分 / RRF 分 / rerank 模型分），同阶段可比、跨阶段不可比，不做
// 归一；units 仅 pack 承载（回包单元，admitted 区分入包与被预算截断）；
// error 记阶段失败摘要（候选空，降级路径由编排层继续）；整行 status 仍
// 记操作终态。
const (
	// 检索阶段取值（stages 数组元素的 stage 键）。
	RetrievalStageVector  = "vector"
	RetrievalStageLexical = "lexical"
	RetrievalStageFusion  = "fusion"
	RetrievalStageRerank  = "rerank"
	RetrievalStagePack    = "pack"

	// RetrievalStageMaxCandidates 是单阶段候选/单元数上限（应用层截断，
	// 与 hits 列的 50 条口径同源）。
	RetrievalStageMaxCandidates = 50
	// retrievalStageTextMaxRunes 是阶段内文本（查询变体、错误摘要）的
	// 兜底截断上限（rune 计，与装饰器的查询文本口径一致）。
	retrievalStageTextMaxRunes = 4096
)

// retrievalStage 是 stages 数组的一个阶段元素。json 标签即落库 jsonb 的
// 元素键（snake_case，与列注释声明一致）；不适用的键以 omitempty 省略
// （query_text 仅召回、model 仅 rerank、units 仅 pack）。
type retrievalStage struct {
	// Stage 是阶段取值（RetrievalStage* 常量）。
	Stage string `json:"stage"`
	// QueryText 是该召回路的变体文本（已脱敏截断）；仅召回阶段承载。
	QueryText string `json:"query_text,omitempty"`
	// Limit 是该阶段的上限，口径随阶段：召回与融合为候选条数上限
	//（2×top_k），rerank 为输出上限（最终 top_k——candidates 记的是重排
	// 输入候选的重排后序，条数可为 2×top_k，不因输出上限而截断），pack 为
	// 回包 token 预算。
	Limit int `json:"limit,omitempty"`
	// Model 是重排模型键；仅 rerank 阶段承载（解析失败为空）。
	Model string `json:"model,omitempty"`
	// Candidates 是该阶段的输出候选（后一阶段的输入序）；失败阶段为空。
	Candidates []RetrievalHit `json:"candidates,omitempty"`
	// Units 是回包单元（含被预算截断者，admitted 区分）；仅 pack 承载。
	Units []retrievalPackUnit `json:"units,omitempty"`
	// Error 是阶段失败摘要（已脱敏截断）；成功为空。降级阶段（词法路、
	// rerank）失败不翻转整行 status——status 记操作终态。
	Error string `json:"error,omitempty"`
	// DurationMS 是该阶段耗时（毫秒）。
	DurationMS int64 `json:"duration_ms"`
}

// retrievalPackUnit 是 pack 阶段的回包单元元素：对照 fusion 序与全部
// 单元（含 admitted=false 的被截断者）可还原预算截断掉了谁。tokens 为
// 确定性估算量级（estimatePackTokens，非精确分词）。
type retrievalPackUnit struct {
	// Mode 是聚合粒度（chunk / window / section / document）。
	Mode string `json:"mode"`
	// SourceID 是来源条目 ID（直回块的来源缺失时省略）。
	SourceID string `json:"source_id,omitempty"`
	// DocumentIDs 是聚合的全部成员切片 ID（含未命中兄弟块）。
	DocumentIDs []string `json:"document_ids"`
	// HeadingPath 仅 section / document 档承载。
	HeadingPath string `json:"heading_path,omitempty"`
	// Anchor 是单元内最小索引成员的锚点。
	Anchor string `json:"anchor,omitempty"`
	// Tokens 是单元正文的 token 量级估算。
	Tokens int `json:"tokens"`
	// Admitted 标记该单元是否进入回包（false 即被预算截断或同源收编）。
	Admitted bool `json:"admitted"`
}

// retrievalStageTracker 在检索编排过程中按执行序收集阶段元素。
type retrievalStageTracker struct {
	stages []retrievalStage
}

// add 追加一个阶段元素：候选与单元在此收口上限（沿用 hits 列的截断
// 口径，超限不再另行标记）。
func (t *retrievalStageTracker) add(stage retrievalStage) {
	if len(stage.Candidates) > RetrievalStageMaxCandidates {
		stage.Candidates = stage.Candidates[:RetrievalStageMaxCandidates]
	}
	if len(stage.Units) > RetrievalStageMaxCandidates {
		stage.Units = stage.Units[:RetrievalStageMaxCandidates]
	}
	t.stages = append(t.stages, stage)
}

// payload 序列化阶段数组为落库载体：无阶段（编排未进入召回管线）返回
// nil，落库 NULL。元素只含字符串/数值，序列化失败按无阶段降级（防御
// 分支，正常不可达）。
func (t *retrievalStageTracker) payload() json.RawMessage {
	if len(t.stages) == 0 {
		return nil
	}
	raw, err := json.Marshal(t.stages)
	if err != nil {
		return nil
	}
	return raw
}

// redactRetrievalText 消费 retrieval 类别脱敏并做长度兜底（标准注册表
// 512 rune 截断 + 4096 rune 兜底，与装饰器的查询文本同口径）。编排层
// 自写留痕行时，查询文本（主查询与变体）与阶段错误摘要的落库前脱敏
// 义务在此落地；registry 为 nil 时按「放行原值 + 截断兜底」降级（与
// 审计用例同口径）。
func (uc *KnowledgeSearchUsecase) redactRetrievalText(value string) string {
	if value == "" {
		return ""
	}
	if uc.registry != nil {
		redacted, _ := uc.registry.Redact(redact.CategoryRetrieval, value)
		value = redacted
	}
	return truncateRetrievalStageText(value)
}

// truncateRetrievalStageText 按 rune 截断到兜底上限（多字节字符不劈开），
// 尾部以标记说明发生了截断。
func truncateRetrievalStageText(value string) string {
	if utf8.RuneCountInString(value) <= retrievalStageTextMaxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:retrievalStageTextMaxRunes]) + "...(truncated)"
}

// retrievalStageCandidates 把检索命中折叠为阶段候选（形态对齐
// RetrievalHit，anchor 取切片元数据）；nil 命中保持 nil（失败阶段候选空）。
func retrievalStageCandidates(hits []*ScoredVectorDocument) []RetrievalHit {
	if len(hits) == 0 {
		return nil
	}
	out := make([]RetrievalHit, 0, len(hits))
	for _, hit := range hits {
		if hit == nil || hit.Document == nil {
			continue
		}
		out = append(out, RetrievalHit{
			DocumentID: hit.Document.ID,
			Score:      hit.Score,
			Anchor:     metadataText(hit.Document.Metadata, "anchor"),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// appendSearchLog 由编排层在完成处写入留痕行：留痕写入失败只记日志、
// 不反噬检索主流程（旁路观察面纪律，与装饰器同一口径）。
func (uc *KnowledgeSearchUsecase) appendSearchLog(ctx context.Context, rec *RetrievalLogRecord) {
	if err := uc.logs.Append(ctx, rec); err != nil {
		slog.WarnContext(ctx, "retrieval log write failed", "source", rec.Source, "status", rec.Status, "error", err)
	}
}
