import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { groupChatUnits, type ChatItem, type MessageItem, type ReasoningItem, type ToolCallItem } from '../../agui/events'
import { fetchHistorySnapshot } from '../../agui/history'
import { fetchAgentSessions, type AgentSessionMeta } from '../../api/agentSessions'
import {
  fetchConfirmationAudits,
  fetchToolCallAudits,
  type ConfirmationAudit,
  type ToolCallAudit,
} from '../../api/agentAudit'
import { fetchRetrievalLogs, type RetrievalLog } from '../../api/agentRetrievalLogs'
import { fetchSessionUsage, type SessionUsageFact } from '../../api/agentSessionUsage'
import { useNavigate } from 'react-router'
import {
  ArrowLeftIcon,
  ChevronDownIcon,
  CircleAlertIcon,
  RotateCwIcon,
  SparklesIcon,
  WrenchIcon,
} from 'lucide-react'
import { detectCompactionRefs } from './compaction'
import {
  collectTraceIds,
  confirmationsForToolCall,
  matchesTraceId,
  parseRetrievalHits,
  parseRetrievalStages,
  relatedChatItemIds,
  relatedToolCallIds,
  retrievalScoreSource,
  toolCallAuditsFor,
  type RetrievalStage,
  type RetrievalStageUnit,
  type TraceFacets,
} from './model'
import { clearReplayDetailCache } from './detailCache'
import { ChunkIdPopover, DocumentIdPopover } from './EntityPopover'
import { CompactionRefPanel } from './CompactionRefPanel'
import { GrapeMark } from '../../ui/icons'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Command, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover'
import { Spinner } from '@/components/ui/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

// formatTime 渲染 UTC RFC3339 为本地时间；非法时间原样呈现。
function formatTime(iso: string): string {
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString()
}

// formatEpochMs 渲染毫秒时间戳为本地时间（秒级精度已足够对账）；缺失
// 或非法值给占位，不伪造时间。
function formatEpochMs(ms: number | null | undefined): string {
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms <= 0) {
    return '—'
  }
  return new Date(ms).toLocaleString()
}

// startInfo 汇总回放项的起始时间与相对本轮用户的耗时：耗时是「本条
// 事件起始时间 − 所在交互的用户消息起始时间」，用于回答「响应慢」时
// 工具与回复各自何时开始、等了多久。任一端缺失时整段不呈现。
function startInfo(startedAt: number | undefined, turnStart: number | undefined): {
  time: string
  latency: string
} {
  const started = typeof startedAt === 'number' && Number.isFinite(startedAt) && startedAt > 0 ? startedAt : undefined
  const latency =
    started !== undefined && typeof turnStart === 'number' && started >= turnStart
      ? `用时 ${((started - turnStart) / 1000).toFixed(1)}s`
      : ''
  return { time: formatEpochMs(started), latency }
}

// usageSummary 汇总一次模型调用的事实片段：模型型号与 token 用量（含缓存
// 与思考细分）及流程级首字/思考时长。缺项不呈现——「未上报」不能读成
// 「为零」；事实缺失返回空数组，调用方跳过整行。includeRunTiming 控制流程
// 级两项：框架只在一次运行的首个模型调用上测量并复制到后续调用用量，逐条
// 呈现会重复，故仅该运行首条展示并标注「本轮」。
function usageSummary(fact: SessionUsageFact | undefined, includeRunTiming: boolean): string[] {
  if (fact === undefined) {
    return []
  }
  const parts: string[] = []
  if (fact.model) {
    parts.push(`模型 ${fact.model}`)
  }
  const usage = fact.usage
  if (usage) {
    let text = `tokens ${usage.total}（输入 ${usage.prompt} / 输出 ${usage.completion}`
    if (usage.cached) {
      text += ` · 缓存 ${usage.cached}`
      // 缓存率由前端按输入侧口径计算：命中缓存的输入 token / 输入 token。
      // 输入为 0（provider 未上报）时不呈现，避免伪造 0.00%。
      if (usage.prompt > 0) {
        text += `（${((usage.cached / usage.prompt) * 100).toFixed(2)}%）`
      }
    }
    if (usage.reasoning) {
      text += ` · 思考 ${usage.reasoning}`
    }
    parts.push(`${text}）`)
  }
  if (includeRunTiming) {
    if (typeof fact.reasoningMs === 'number' && fact.reasoningMs > 0) {
      parts.push(`本轮思考 ${(fact.reasoningMs / 1000).toFixed(1)}s`)
    }
    if (typeof fact.firstTokenMs === 'number' && fact.firstTokenMs > 0) {
      parts.push(`本轮首字 ${fact.firstTokenMs}ms`)
    }
  }
  return parts
}

// runFirstFactIds 找出每次运行（invocationId）的首条模型调用事实：流程级
// 耗时只在这一条上呈现，避免同一次运行内每条调用都重复同一数值。缺失
// invocationId 的事实在后端不可达，前端按自成一组的 messageId 兜底。
function runFirstFactIds(facts: SessionUsageFact[]): Set<string> {
  const seen = new Set<string>()
  const firsts = new Set<string>()
  for (const fact of facts) {
    const run = fact.invocationId ?? fact.messageId
    if (seen.has(run)) {
      continue
    }
    seen.add(run)
    firsts.add(fact.messageId)
  }
  return firsts
}

// turnStarts 计算回放流每一项所在交互的起点：以用户消息为界，段内
// 后续项（思考、工具调用与助手回复）归属该用户消息的起始时间。用户消息
// 自身不作为自己的锚（用时只对段内后续项有意义），段首缺失起始时间时
// 该段无锚点，不计算耗时。
function turnStarts(items: ChatItem[]): Map<string, number | undefined> {
  const starts = new Map<string, number | undefined>()
  let current: number | undefined
  for (const item of items) {
    const key = item.kind === 'tool' ? item.toolCallId : item.id
    if (item.kind === 'message' && item.role === 'user') {
      current = item.startedAt
      starts.set(key, undefined)
      continue
    }
    starts.set(key, current)
  }
  return starts
}

// decisionChipClass 按决定方向约定区分确认行配色：approved / auto /
// `*_auto` 走 success 软色对（放行方向），denied / auto_deny / `*_deny`
// 走 danger 软色对（拒绝方向）；词表外取值 fail-closed 显 danger（决定
// 词表由后端 biz 模式校验把门，前端不再枚举白名单）。软色对随主题色系
// 切换——色盲友好色系下为 青绿/橙，红绿色觉障碍也可分辨方向。
function decisionChipClass(decision: string): string {
  if (decision === 'approved' || decision === 'auto' || decision.endsWith('_auto')) {
    return 'bg-success-soft text-success-text'
  }
  return 'bg-danger-soft text-danger-text'
}

// statusChipClass 按执行状态区分：success 走 success 软色对，其余 danger。
function statusChipClass(status: string): string {
  return status === 'success'
    ? 'bg-success-soft text-success-text'
    : 'bg-danger-soft text-danger-text'
}

// hitRowClass 命中链路的行高亮 / 未命中行灰显（品牌软色底随主题色切换）。
function hitRowClass(hit: boolean, dimMisses: boolean): string {
  return `${hit ? 'bg-brand-soft' : ''} ${dimMisses ? 'opacity-40' : ''}`
}

// retrievalStageLabel 是阶段取值的展示词（时间线语义：向量召回 / 词法召
// 回 → 融合 → 重排 → 回包）；向量与词法两路必须可区分，未知取值原样展示
//（形态漂移不静默吞掉）。
function retrievalStageLabel(stage: string): string {
  switch (stage) {
    case 'vector':
      return '向量召回'
    case 'lexical':
      return '词法召回'
    case 'fusion':
      return '融合'
    case 'rerank':
      return '重排'
    case 'pack':
      return '回包'
    default:
      return stage
  }
}

// formatScore 把分数修约到 4 位小数：原始分数常为长浮点（如 RRF 的
// 0.032018442622950824），展示层只保留可读精度。
function formatScore(score: number): string {
  return Number.isFinite(score) ? score.toFixed(4) : '—'
}

// stageUnitReasonLabel 把回包单元的落包结果映射为展示词：入包按
// admitted，未入包按 reason（merged 同源收编 / redundant 冗余 /
// truncated 截断）；未知 reason 中性呈现，不猜成截断。
function stageUnitReasonLabel(unit: RetrievalStageUnit): string {
  if (unit.admitted) {
    return '入包'
  }
  switch (unit.reason) {
    case 'merged':
      return '同源收编'
    case 'redundant':
      return '冗余'
    case 'truncated':
      return '截断'
    default:
      return '未入包'
  }
}

function stageUnitReasonClass(unit: RetrievalStageUnit): string {
  if (unit.admitted) {
    return 'bg-success-soft text-success-text'
  }
  switch (unit.reason) {
    case 'merged':
      return 'bg-brand-soft text-brand-text'
    case 'truncated':
      return 'bg-danger-soft text-danger-text'
    default:
      return 'bg-muted text-muted-foreground'
  }
}

// stageUnitSummary 汇总 pack 单元的落包分布（按原因分组计数），供阶段
// 摘要行一眼看出「入包几个、截断/收编/冗余各几个」。
function stageUnitSummary(units: RetrievalStageUnit[]): string {
  let admitted = 0
  let merged = 0
  let redundant = 0
  let truncated = 0
  let other = 0
  for (const unit of units) {
    if (unit.admitted) {
      admitted++
    } else if (unit.reason === 'merged') {
      merged++
    } else if (unit.reason === 'redundant') {
      redundant++
    } else if (unit.reason === 'truncated') {
      truncated++
    } else {
      other++
    }
  }
  const parts: string[] = []
  if (admitted > 0) parts.push(`入包 ${admitted}`)
  if (truncated > 0) parts.push(`截断 ${truncated}`)
  if (merged > 0) parts.push(`收编 ${merged}`)
  if (redundant > 0) parts.push(`冗余 ${redundant}`)
  if (other > 0) parts.push(`未入包 ${other}`)
  return parts.join(' · ')
}

// StageCandidateTable 渲染阶段候选明细：名次、分片 ID（可点开反查原
// 文）、分数（按阶段修约 4 位）、锚点。
function StageCandidateTable({ candidates }: { candidates: RetrievalStage['candidates'] }) {
  return (
    <div className="space-y-1.5">
      <h4 className="text-xs font-medium text-muted-foreground">候选明细（{candidates.length}）</h4>
      <Table className="min-w-[520px]">
        <TableHeader>
          <TableRow>
            <TableHead className="text-xs text-muted-foreground">名次</TableHead>
            <TableHead className="text-xs text-muted-foreground">分片 ID</TableHead>
            <TableHead className="text-xs text-muted-foreground">分数</TableHead>
            <TableHead className="text-xs text-muted-foreground">锚点</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {candidates.map((candidate, index) => (
            <TableRow key={`${candidate.documentId}-${index}`}>
              <TableCell className="tabular-nums">{index + 1}</TableCell>
              <TableCell>
                <ChunkIdPopover chunkId={candidate.documentId} />
              </TableCell>
              <TableCell className="tabular-nums">{formatScore(candidate.score)}</TableCell>
              <TableCell className="font-mono text-xs">{candidate.anchor ?? '—'}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

// StageUnitTable 渲染回包单元明细：聚合粒度、来源文档（可点开）、成员
// 分片（逐个可点开）、章节、token 量级与落包结果（入包 / 截断 / 同源收编
// / 冗余，按 reason 映射）。
function StageUnitTable({ units }: { units: RetrievalStageUnit[] }) {
  return (
    <div className="space-y-1.5">
      <h4 className="text-xs font-medium text-muted-foreground">回包单元明细（{units.length}）</h4>
      <Table className="min-w-[760px]">
        <TableHeader>
          <TableRow>
            <TableHead className="text-xs text-muted-foreground">聚合粒度</TableHead>
            <TableHead className="text-xs text-muted-foreground">文档 ID</TableHead>
            <TableHead className="text-xs text-muted-foreground">成员分片</TableHead>
            <TableHead className="text-xs text-muted-foreground">章节</TableHead>
            <TableHead className="text-xs text-muted-foreground">token</TableHead>
            <TableHead className="text-xs text-muted-foreground">结果</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {units.map((unit, index) => (
            <TableRow key={`${unit.mode}-${unit.sourceId ?? ''}-${index}`}>
              <TableCell className="font-mono text-xs">{unit.mode}</TableCell>
              <TableCell>{unit.sourceId ? <DocumentIdPopover documentId={unit.sourceId} /> : '—'}</TableCell>
              <TableCell>
                {unit.documentIds.length === 0 ? (
                  '—'
                ) : (
                  <span className="inline-flex flex-wrap gap-1">
                    {unit.documentIds.map((id, memberIndex) => (
                      <ChunkIdPopover key={`${id}-${memberIndex}`} chunkId={id} />
                    ))}
                  </span>
                )}
              </TableCell>
              <TableCell className="max-w-52 truncate" title={unit.headingPath ?? undefined}>
                {unit.headingPath ?? '—'}
              </TableCell>
              <TableCell className="tabular-nums">{unit.tokens}</TableCell>
              <TableCell>
                <Badge className={stageUnitReasonClass(unit)}>{stageUnitReasonLabel(unit)}</Badge>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

// RetrievalStageTimeline 按执行序渲染一次检索的阶段时间线（编排层写入的
// stages，数组顺序即执行序）：每阶段一行摘要（阶段名、查询变体、重排模
// 型、上限、候选/单元分因计数、耗时、错误摘要），行可点开查看候选明细与
// 回包单元明细（默认收起，button + aria-expanded 键盘可达）。limit 在
// pack 阶段为 token 预算、其余阶段为候选上限；耗时恒显示（0 ms 即不足
// 1ms 的量级）。落库为已脱敏形态，文本按节点渲染（不可信文本不走
// innerHTML）。单路直调行无 stages，不渲染。
function RetrievalStageTimeline({ stages }: { stages: RetrievalStage[] }) {
  if (stages.length === 0) {
    return null
  }
  return (
    <ol aria-label="检索阶段" className="mt-2.5 space-y-1.5 border-t pt-2.5">
      {stages.map((stage, index) => {
        const unitSummary = stageUnitSummary(stage.units)
        return (
          <li key={`${stage.stage}-${index}`}>
            <Collapsible className="group rounded-lg border border-border bg-card">
              <CollapsibleTrigger className="flex w-full cursor-pointer flex-wrap items-center gap-x-2 gap-y-1 px-3 py-2 text-left text-xs text-muted-foreground transition-colors select-none hover:bg-soft">
                <span className="font-medium text-secondary-foreground">
                  {index + 1}. {retrievalStageLabel(stage.stage)}
                </span>
                {stage.queryText && (
                  <span title={stage.queryText} className="max-w-60 truncate text-foreground">
                    「{stage.queryText}」
                  </span>
                )}
                {stage.model && (
                  <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-secondary-foreground">
                    {stage.model}
                  </code>
                )}
                {typeof stage.limit === 'number' && (
                  <span>
                    上限 {stage.limit}
                    {stage.stage === 'pack' ? ' tok' : ''}
                  </span>
                )}
                {stage.candidates.length > 0 && <span>候选 {stage.candidates.length} 条</span>}
                {unitSummary !== '' && <span>单元 {unitSummary}</span>}
                <span className="tabular-nums">{stage.durationMs} ms</span>
                {stage.error && (
                  <span className="inline-flex items-center gap-1 text-danger-text" title={stage.error}>
                    <CircleAlertIcon className="size-3.5 shrink-0" aria-hidden />
                    <span className="max-w-80 truncate">{stage.error}</span>
                  </span>
                )}
                <ChevronDownIcon
                  aria-hidden
                  className="ml-auto size-3.5 shrink-0 transition-transform duration-200 group-data-[state=open]:rotate-180"
                />
              </CollapsibleTrigger>
              <CollapsibleContent>
                <div className="space-y-3 border-t px-3 py-2.5">
                  {stage.candidates.length > 0 && <StageCandidateTable candidates={stage.candidates} />}
                  {stage.units.length > 0 && <StageUnitTable units={stage.units} />}
                  {stage.candidates.length === 0 && stage.units.length === 0 && (
                    <p className="text-xs text-muted-foreground">该阶段无候选或单元明细。</p>
                  )}
                </div>
              </CollapsibleContent>
            </Collapsible>
          </li>
        )
      })}
    </ol>
  )
}

// EmptyNote 证据分区的空态占位：虚线框 + 居中弱化文案。
function EmptyNote({ children }: { children: ReactNode }) {
  return (
    <p className="rounded-xl border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
      {children}
    </p>
  )
}

// TracePickButton 是证据行内的链路追踪按钮：一键选中该 trace 过滤（与
// 下拉选中同一效果，再次点击同值经 pickTrace 反选清除）。凡携带
// trace_id 的证据行（工具调用审计、授权确认、检索留痕）都用它统一入口，
// 避免可点击性埋没在纯文本 chip 里。
function TracePickButton({ traceId, onPickTrace }: { traceId: string; onPickTrace: (traceId: string | null) => void }) {
  return (
    <Badge
      asChild
      className="cursor-pointer bg-brand-soft font-mono text-[11px] text-brand-text transition-colors hover:bg-brand-soft-strong"
    >
      <button
        type="button"
        aria-label={`追踪 ${traceId}`}
        title={`按该链路过滤（trace ${traceId}）`}
        onClick={() => onPickTrace(traceId)}
      >
        追踪 {traceId}
      </button>
    </Badge>
  )
}

// TraceFilterSelect 是可搜索的 trace 过滤下拉（Popover + Command 组合的
// Combobox）：输入即时做前端子串过滤（大小写不敏感），选中后关闭并生效，
// 「全部链路」清除过滤；无匹配时给出占位提示。相较手写 combobox 补齐了
// 方向键导航与 Enter 选中（cmdk 内建）。
function TraceFilterSelect({ traceIds, value, onChange }: {
  traceIds: string[]
  value: string | null
  onChange: (traceId: string | null) => void
}) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  // anchorRef 圈定锚点（搜索输入框）范围：锚点上的点击/聚焦对 Popover
  // 而言是「content 外的交互」，不挡掉会让键盘输入期间的隐式点击把弹层
  // 关掉（与 onFocus 的打开竞态）；真实的外部点击仍照常关闭。
  const anchorRef = useRef<HTMLDivElement | null>(null)
  const keepOpenOnAnchor = (e: { target: EventTarget | null; preventDefault: () => void }) => {
    if (anchorRef.current?.contains(e.target as Node)) {
      e.preventDefault()
    }
  }
  const needle = query.trim().toLowerCase()
  const matches = needle === '' ? traceIds : traceIds.filter((id) => id.toLowerCase().includes(needle))
  const pick = (traceId: string | null) => {
    onChange(traceId)
    setOpen(false)
  }
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild ref={anchorRef}>
        <Command
          label="trace 过滤"
          shouldFilter={false}
          className="w-64 rounded-md border border-input bg-transparent shadow-xs transition-[color,box-shadow] focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring"
        >
          <CommandInput
            placeholder="全部链路（输入以搜索）"
            className="h-8 font-mono text-xs"
            value={query}
            onFocus={() => {
              setQuery('')
              setOpen(true)
            }}
            onValueChange={setQuery}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                setOpen(false)
              }
            }}
          />
        </Command>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        className="w-64 p-0"
        onOpenAutoFocus={(e) => e.preventDefault()}
        onInteractOutside={keepOpenOnAnchor}
        onFocusOutside={keepOpenOnAnchor}
      >
        <Command shouldFilter={false}>
          <CommandList label="trace 候选" className="max-h-60">
            <CommandItem
              value="__all__"
              onSelect={() => pick(null)}
              className={value === null ? 'bg-brand-soft text-brand-text' : 'text-muted-foreground'}
            >
              全部链路
            </CommandItem>
            {matches.map((traceId) => (
              <CommandItem
                key={traceId}
                value={traceId}
                onSelect={() => pick(traceId)}
                className={`truncate font-mono text-xs ${
                  value === traceId ? 'bg-brand-soft text-brand-text' : ''
                }`}
              >
                {traceId}
              </CommandItem>
            ))}
            {matches.length === 0 && (
              <div aria-live="polite" className="px-3 py-1.5 text-xs text-muted-foreground">
                无匹配链路
              </div>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

// CompactibleText 是带压缩占位符识别的文本块：识别到占位符时给出徽标
// 与原文对照展开，其余原样以纯文本渲染（sanitize 基线不变）。
function CompactibleText({ text, threadId }: { text: string; threadId: string }) {
  const refs = useMemo(() => detectCompactionRefs(text), [text])
  return (
    <>
      <pre>{text}</pre>
      {refs.map((ref, index) => (
        <CompactionRefPanel key={`${ref.eventId ?? ref.kind}-${index}`} compactionRef={ref} threadId={threadId} />
      ))}
    </>
  )
}

// ReplayReasoning 复现流里的模型思考内容：默认收起的虚线卡片，摘要行
// 携带起始时间与相对本轮用户消息的用时；展开后以纯文本呈现思考原文
// （与正文同一条 sanitize 基线）。快照中的思考段落没有流式态，一律按
// 已收口渲染。宽度与透明度由所属助手回合统一控制。
function ReplayReasoning({ item, turnStart }: { item: ReasoningItem; turnStart?: number }) {
  const start = startInfo(item.startedAt, turnStart)
  return (
    <Collapsible className="w-full overflow-hidden rounded-xl border border-dashed bg-card shadow-xs">
      <CollapsibleTrigger className="group flex w-full cursor-pointer items-center gap-2 px-3.5 py-2.5 text-left text-sm text-muted-foreground transition-colors select-none hover:bg-soft">
        <SparklesIcon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        思考内容
        <span className="text-xs tabular-nums">始于 {start.time}</span>
        {start.latency && <span className="text-xs tabular-nums">{start.latency}</span>}
        <ChevronDownIcon
          aria-hidden
          className="ml-auto size-4 shrink-0 text-muted-foreground transition-transform duration-200 group-data-[state=open]:rotate-180"
        />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="border-t px-3.5 py-3 text-sm leading-relaxed wrap-break-word whitespace-pre-wrap text-muted-foreground">
          {item.content}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

// ReplayToolCall 复现流里的工具调用折叠项：摘要行始终携带关联审计/确认
// 数量徽标；展开后在参数与「结果」原文之下内嵌同一 toolCallId 的审计行
// 与授权确认行，免去跨区交叉查证。trace 过滤命中的项以品牌紫描边高亮，
// 未命中且链路过滤生效时降透明度。默认收起，点按开合（内容随开合挂载）。
function ReplayToolCall({
  item,
  threadId,
  highlighted,
  dimmed,
  turnStart,
  fact,
  showRunTiming,
  audits,
  confirmations,
  onPickTrace,
}: {
  item: ToolCallItem
  threadId: string
  highlighted: boolean
  dimmed: boolean
  // turnStart 是所在交互的用户消息起点，仅用于算「用时」；缺失时只显示
  // 起始时间。展示值在本组件内派生，父级不传渲染对象。
  turnStart?: number
  // fact 是发起该调用的那次模型调用的用量事实（模型型号与 token）。
  // 工具决策轮通常没有正文，助手消息不单独成项，事实经 parentMessageId
  // 挂到工具卡片上。
  fact?: SessionUsageFact
  // showRunTiming 决定是否呈现流程级首字与思考时长（仅本轮首条调用）。
  showRunTiming: boolean
  audits: ToolCallAudit[]
  confirmations: ConfirmationAudit[]
  onPickTrace: (traceId: string | null) => void
}) {
  const start = startInfo(item.startedAt, turnStart)
  const usage = useMemo(() => usageSummary(fact, showRunTiming), [fact, showRunTiming])
  return (
    <Collapsible
      data-tool-call-id={item.toolCallId}
      data-highlight={highlighted ? 'true' : undefined}
      aria-label={highlighted ? `工具调用 ${item.name}（当前链路）` : `工具调用 ${item.name}`}
      className={`group w-full overflow-hidden rounded-xl border bg-card shadow-sm transition-opacity duration-200 ${
        highlighted ? 'border-brand-border bg-brand-soft ring-1 ring-brand-border' : ''
      } ${dimmed ? 'opacity-40' : ''}`}
    >
      <CollapsibleTrigger className="flex w-full cursor-pointer items-center gap-2 px-3.5 py-2.5 text-left text-sm text-muted-foreground transition-colors select-none hover:bg-soft">
        <WrenchIcon aria-hidden className="size-4 shrink-0 text-muted-foreground" />
        {`工具 ${item.name || '(未知)'}`}
        <span className="text-xs tabular-nums">始于 {start.time}</span>
        {start.latency && <span className="text-xs tabular-nums">{start.latency}</span>}
        {highlighted && (
          <Badge className="bg-brand-soft-strong text-brand-text">当前链路</Badge>
        )}
        {audits.length > 0 && <Badge variant="secondary">审计 {audits.length} 条</Badge>}
        {confirmations.length > 0 && <Badge variant="secondary">确认 {confirmations.length} 条</Badge>}
        <ChevronDownIcon
          aria-hidden
          className="ml-auto size-4 shrink-0 text-muted-foreground transition-transform duration-200 group-data-[state=open]:rotate-180"
        />
      </CollapsibleTrigger>
      {/* 模型与 token 事实放在折叠区之外：收起时也可见，便于快速判断这次
          工具调用背后的模型开销；展开后不再重复呈现。 */}
      {usage.length > 0 && (
        <p className="flex flex-wrap items-center gap-x-2 gap-y-1 px-3.5 pb-2.5 text-xs text-muted-foreground">
          {usage.map((part) => (
            <span key={part} className="tabular-nums">
              {part}
            </span>
          ))}
        </p>
      )}
      <CollapsibleContent>
        <div className="space-y-3 border-t px-3.5 py-3">
          {item.argsText !== '' && (
            <>
              <h4 className="text-xs font-medium text-muted-foreground">参数</h4>
              <pre className="code-block">{item.argsText}</pre>
            </>
          )}
          {item.result !== null && (
            <>
              <h4 className="text-xs font-medium text-muted-foreground">结果</h4>
              <CompactibleText text={item.result} threadId={threadId} />
            </>
          )}
          {audits.length > 0 && (
            <div className="space-y-1.5">
              <h4 className="text-xs font-medium text-muted-foreground">调用审计</h4>
              {audits.map((row) => (
                <div key={row.id} className="space-y-1 rounded-lg bg-soft px-3 py-2 text-xs text-secondary-foreground">
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                    <Badge className={statusChipClass(row.status)}>{row.status}</Badge>
                    <span>耗时 {row.durationMs}ms</span>
                    <span>{formatTime(row.calledAt)}</span>
                    {row.traceId && <TracePickButton traceId={row.traceId} onPickTrace={onPickTrace} />}
                  </div>
                  {row.argumentsSummary !== null && <p className="break-all font-mono">参数摘要：{row.argumentsSummary}</p>}
                  {row.resultSummary !== null && <p className="break-all font-mono">结果摘要：{row.resultSummary}</p>}
                </div>
              ))}
            </div>
          )}
          {confirmations.length > 0 && (
            <div className="space-y-1.5">
              <h4 className="text-xs font-medium text-muted-foreground">授权确认</h4>
              {confirmations.map((row) => (
                <div
                  key={row.id}
                  className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg bg-soft px-3 py-2 text-xs text-secondary-foreground"
                >
                  <Badge className={decisionChipClass(row.decision)}>{row.decision}</Badge>
                  <span>{row.remember || '—'}</span>
                  <span>{row.path}</span>
                  <span>{row.decidedBy}</span>
                  <span>{formatTime(row.decidedAt)}</span>
                  {row.traceId && <TracePickButton traceId={row.traceId} onPickTrace={onPickTrace} />}
                </div>
              ))}
            </div>
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

// ReplayMessage 复现流里的独立消息（用户 / 系统等）：与对话页气泡形态
// 一致——用户消息靠右品牌紫气泡（标签「你」），其余角色靠左卡片气泡（标签
// 沿用原始角色名作证据口径）。助手回合（含思考与回答）由
// ReplayAssistantTurn 承载。压缩占位符面板跟随消息同侧渲染；链路过滤启用
// 且本消息不在命中段内时降透明度。
function ReplayMessage({
  item,
  threadId,
  dimmed,
  turnStart,
  fact,
  showRunTiming,
}: {
  item: MessageItem
  threadId: string
  dimmed: boolean
  // turnStart 语义同 ReplayToolCall：用户消息自身为段首，调用方不传锚，
  // 因此只显示起始时间。
  turnStart?: number
  // fact 是产出该条回复的模型调用事实（模型型号、token 与耗时）。
  fact?: SessionUsageFact
  // showRunTiming 语义同 ReplayToolCall：流程级耗时只在本轮首条调用呈现。
  showRunTiming: boolean
}) {
  const refs = useMemo(() => detectCompactionRefs(item.content), [item.content])
  const start = startInfo(item.startedAt, turnStart)
  const usage = useMemo(() => usageSummary(fact, showRunTiming), [fact, showRunTiming])
  const isUser = item.role === 'user'
  return (
    <article
      data-role={item.role}
      className={`flex flex-col transition-opacity duration-200 ${isUser ? 'items-end' : 'items-start'} ${
        dimmed ? 'opacity-40' : ''
      }`}
    >
      <span className="mb-1 flex items-center gap-2 px-1 text-xs text-muted-foreground">
        {isUser ? '你' : item.role}
        <span className="tabular-nums">始于 {start.time}</span>
        {start.latency && <span className="tabular-nums">{start.latency}</span>}
      </span>
      <div
        className={`max-w-[85%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed wrap-break-word whitespace-pre-wrap ${
          isUser
            ? 'rounded-br-sm bg-primary text-primary-foreground shadow-xs'
            : 'rounded-bl-sm border bg-card text-card-foreground shadow-xs'
        }`}
      >
        {item.content}
      </div>
      {usage.length > 0 && (
        <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 px-1 text-xs text-muted-foreground">
          {usage.map((part) => (
            <span key={part} className="tabular-nums">
              {part}
            </span>
          ))}
        </p>
      )}
      {refs.map((ref, index) => (
        <CompactionRefPanel key={`${ref.eventId ?? ref.kind}-${index}`} compactionRef={ref} threadId={threadId} />
      ))}
    </article>
  )
}

// ReplayAssistantTurn 复现流里的助手回合：思考卡片与回答共用角色标签
// （标签沿用原始角色名作证据口径），思考在上、回答在下，宽度收敛到气泡
// 列。模型用量事实与压缩引用只属于回答正文，呈现口径与 ReplayMessage
// 一致；思考段不重复挂同一次调用的 token。
function ReplayAssistantTurn({
  reasoning,
  message,
  threadId,
  dimmed,
  turnStart,
  fact,
  showRunTiming,
}: {
  reasoning: ReasoningItem[]
  message?: MessageItem
  threadId: string
  dimmed: boolean
  turnStart?: number
  fact?: SessionUsageFact
  showRunTiming: boolean
}) {
  const content = message?.content ?? ''
  const refs = useMemo(() => detectCompactionRefs(content), [content])
  const start = startInfo(message?.startedAt ?? reasoning[0]?.startedAt, turnStart)
  const usage = useMemo(() => usageSummary(fact, showRunTiming), [fact, showRunTiming])
  return (
    <article
      data-role="assistant"
      className={`flex flex-col items-start transition-opacity duration-200 ${dimmed ? 'opacity-40' : ''}`}
    >
      <span className="mb-1 flex items-center gap-2 px-1 text-xs text-muted-foreground">
        assistant
        <span className="tabular-nums">始于 {start.time}</span>
        {start.latency && <span className="tabular-nums">{start.latency}</span>}
      </span>
      <div className="flex w-full max-w-[85%] flex-col items-start gap-2">
        {reasoning.map((item) => (
          <ReplayReasoning key={item.id} item={item} turnStart={turnStart} />
        ))}
        {message !== undefined && (
          <div className="rounded-2xl border bg-card px-4 py-2.5 text-sm leading-relaxed wrap-break-word whitespace-pre-wrap text-card-foreground shadow-xs">
            {message.content}
          </div>
        )}
      </div>
      {usage.length > 0 && (
        <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 px-1 text-xs text-muted-foreground">
          {usage.map((part) => (
            <span key={part} className="tabular-nums">
              {part}
            </span>
          ))}
        </p>
      )}
      {refs.map((ref, index) => (
        <CompactionRefPanel key={`${ref.eventId ?? ref.kind}-${index}`} compactionRef={ref} threadId={threadId} />
      ))}
    </article>
  )
}

// ReplayPage 运维复现视图：会话选择 → 四联证据视图（对话流回放 /
// 工具调用审计 / 授权确认记录 / 检索留痕），以 trace_id 关联同
// 一次交互的全链路证据。回放走 /history 快照（上游 follow 存在同毫秒事
// 件缺陷——快照即回放，刷新按钮手动兜底）；审计与留痕为落库
// 已脱敏形态，直接渲染；压缩占位符经事件窗口端点取原文对照。
export function ReplayPage() {
  const navigate = useNavigate()
  const [sessions, setSessions] = useState<AgentSessionMeta[]>([])
  const [threadId, setThreadId] = useState<string | null>(null)
  const [items, setItems] = useState<ChatItem[]>([])
  const [historyUnavailable, setHistoryUnavailable] = useState(false)
  const [toolCalls, setToolCalls] = useState<ToolCallAudit[]>([])
  const [confirmations, setConfirmations] = useState<ConfirmationAudit[]>([])
  const [retrievals, setRetrievals] = useState<RetrievalLog[]>([])
  const [usageFacts, setUsageFacts] = useState<SessionUsageFact[]>([])
  const [traceFilter, setTraceFilter] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // loadSeq 守卫并发加载的乱序回写：快速切换会话或连点刷新时，只有最后
  // 一次发起的装载允许落状态，先前在途响应按过期丢弃。
  const loadSeq = useRef(0)

  const loadThread = useCallback(async (tid: string) => {
    const seq = ++loadSeq.current
    // 重新装载复现数据（刷新快照 / 切换会话）即让分片与文档详情缓存全部
    // 失效：留痕命中可能已被重摄淘汰，旧事实不得跨快照沿用。
    clearReplayDetailCache()
    setLoading(true)
    setError(null)
    setHistoryUnavailable(false)
    try {
      // /history 快照与模型用量事实独立降级：无 track 事件的会话上游快照
      // 运行会 500，用量端点也可能因会话无事件而空；两者都是回放证据的
      // 附属面，缺失不阻断其余证据装载。
      const [history, auditToolCalls, auditConfirmations, retrievalLogs, usage] = await Promise.all([
        fetchHistorySnapshot(tid).catch(() => null),
        fetchToolCallAudits(tid),
        fetchConfirmationAudits(tid),
        fetchRetrievalLogs(tid),
        fetchSessionUsage(tid).catch(() => []),
      ])
      if (seq !== loadSeq.current) {
        return
      }
      setItems(history ?? [])
      setHistoryUnavailable(history === null)
      setToolCalls(auditToolCalls)
      setConfirmations(auditConfirmations)
      setRetrievals(retrievalLogs)
      setUsageFacts(usage)
    } catch (err) {
      if (seq !== loadSeq.current) {
        return
      }
      setError(err instanceof Error ? err.message : '证据加载失败')
    } finally {
      if (seq === loadSeq.current) {
        setLoading(false)
      }
    }
  }, [])

  // 会话列表：进入页面加载一次并默认选中最近更新的会话。
  useEffect(() => {
    let cancelled = false
    fetchAgentSessions()
      .then((list) => {
        if (cancelled) {
          return
        }
        setSessions(list)
        if (list.length > 0) {
          setThreadId((current) => current ?? list[0].id)
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : '会话列表加载失败')
        }
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (threadId === null) {
      return
    }
    setTraceFilter(null)
    void loadThread(threadId)
  }, [threadId, loadThread])

  const facets: TraceFacets = useMemo(
    () => ({ toolCalls, confirmations, retrievals }),
    [toolCalls, confirmations, retrievals],
  )
  const traceIds = useMemo(() => collectTraceIds(facets), [facets])
  // 链路选中只做高亮对比（命中行高亮、未命中灰显，不隐藏证据行）；
  // 回放流经审计∪确认的 toolCallId 桥接，再按分段邻近归属扩散，仅在
  // 确有命中项时降透明度。
  const relatedToolIds = useMemo(() => relatedToolCallIds(facets, traceFilter), [facets, traceFilter])
  const relatedItemIds = useMemo(() => relatedChatItemIds(items, relatedToolIds), [items, relatedToolIds])
  const dimStream = traceFilter !== null && relatedItemIds.size > 0
  // 每项所在交互的用户消息起点：回放项与工具的「用时」以它为锚。
  const itemTurnStarts = useMemo(() => turnStarts(items), [items])
  // 模型调用事实按 messageId 索引：助手回复取自身 ID，工具调用取发起它的
  // 助手消息 ID（决策轮通常没有正文，助手消息不单独成项）。
  const usageByMessageId = useMemo(
    () => new Map(usageFacts.map((fact) => [fact.messageId, fact])),
    [usageFacts],
  )
  // 流程级耗时（首字/思考）只在每次运行的首条模型调用上呈现。
  const runFirstFacts = useMemo(() => runFirstFactIds(usageFacts), [usageFacts])

  const pickTrace = (traceId: string | null) => {
    setTraceFilter((current) => (current === traceId ? null : traceId))
  }

  return (
    <div className="flex h-full overflow-hidden bg-background">
      <aside aria-label="复现会话侧栏" className="flex w-72 shrink-0 flex-col border-r border-sidebar-border bg-sidebar">
        {/* 头部与对话页侧栏同构（品牌标 + 双行标题）+ 同尺寸按钮行：
            两页互切时侧栏几何保持不动，观感连续。 */}
        <div className="flex items-center gap-2.5 px-5 pt-5 pb-4">
          <GrapeMark className="h-9 w-9" />
          <div className="min-w-0">
            <h1 className="text-sm font-semibold text-foreground">运维复现视图</h1>
            <p className="truncate text-xs text-muted-foreground">证据回放与链路对账</p>
          </div>
        </div>
        <div className="px-4 pb-3">
          <Button type="button" variant="outline" onClick={() => navigate('/')} className="w-full">
            <ArrowLeftIcon aria-hidden className="size-4" />
            返回对话
          </Button>
        </div>
        <nav className="min-h-0 flex-1 overflow-y-auto px-3 pb-4">
          <ul aria-label="复现会话列表" className="space-y-1">
            {sessions.map((session) => {
              const active = session.id === threadId
              return (
                <li key={session.id}>
                  <button
                    type="button"
                    aria-current={active ? 'true' : undefined}
                    onClick={() => setThreadId(session.id)}
                    className={`flex w-full cursor-pointer flex-col items-start gap-0.5 rounded-xl px-3 py-2.5 text-left transition-colors select-none hover:bg-sidebar-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-sidebar-ring ${
                      active ? 'bg-brand-soft hover:bg-brand-soft' : ''
                    }`}
                  >
                    <span
                      className={`truncate text-sm font-medium ${
                        active ? 'text-brand-text' : 'text-foreground'
                      }`}
                    >
                      {`会话 ${session.id.slice(0, 8)}`}
                    </span>
                    <time
                      dateTime={session.updatedAt}
                      className={`text-xs ${active ? 'text-brand-text-soft' : 'text-muted-foreground'}`}
                    >
                      {formatTime(session.updatedAt)}
                    </time>
                  </button>
                </li>
              )
            })}
          </ul>
        </nav>
      </aside>

      <section aria-label="复现证据区" className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b bg-card px-6">
          <span className="flex min-w-0 items-center gap-2 text-sm text-muted-foreground">
            trace 过滤
            <TraceFilterSelect traceIds={traceIds} value={traceFilter} onChange={setTraceFilter} />
          </span>
          {traceFilter !== null && (
            <Button type="button" variant="ghost" size="sm" onClick={() => setTraceFilter(null)}>
              清除过滤
            </Button>
          )}
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={threadId === null || loading}
            onClick={() => threadId !== null && void loadThread(threadId)}
          >
            <RotateCwIcon aria-hidden className="size-3.5" />
            刷新快照
          </Button>
          {loading && (
            <Badge role="status" variant="secondary">
              <Spinner aria-hidden className="size-3" />
              加载中…
            </Badge>
          )}
          <span aria-hidden className="ml-auto hidden text-xs text-muted-foreground xl:inline">
            四联证据 · 以 trace_id 关联同一次交互
          </span>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="w-full space-y-6 px-6 py-6">
            {error !== null && (
              <Alert variant="destructive">
                <CircleAlertIcon aria-hidden className="mt-0.5 shrink-0" />
                <AlertDescription>
                  <span>加载失败：{error}</span>
                </AlertDescription>
              </Alert>
            )}
            {threadId === null && <EmptyNote>暂无会话：先在对话页发起会话。</EmptyNote>}

            {threadId !== null && (
              <>
                <section aria-label="对话流回放">
                  <Card>
                    <CardHeader>
                      <CardTitle className="text-sm font-semibold">对话流回放</CardTitle>
                      <p className="text-right text-xs text-muted-foreground">
                        快照即回放：上游 follow 存在同毫秒事件可能被跳过的缺陷（上游已知缺陷），以快照刷新兜底。
                      </p>
                    </CardHeader>
                    <CardContent className="space-y-3">
                      {items.length === 0 &&
                        (historyUnavailable ? (
                          <EmptyNote>该会话无可回放快照（可能没有持久化的事件），其余证据面不受影响。</EmptyNote>
                        ) : (
                          <EmptyNote>该会话暂无快照消息。</EmptyNote>
                        ))}
                      {groupChatUnits(items).map((unit) => {
                        if (unit.kind === 'assistant') {
                          const memberIds = [
                            ...unit.reasoning.map((reasoning) => reasoning.id),
                            ...(unit.message !== undefined ? [unit.message.id] : []),
                          ]
                          const related = memberIds.some((id) => relatedItemIds.has(id))
                          const dimmed = dimStream && !related
                          const turnStart = itemTurnStarts.get(memberIds[0] ?? '')
                          // 用量事实按模型响应（助手消息 ID）索引：思考段落不带
                          // 独立事实，不重复呈现同一次调用的 token。
                          const fact = usageByMessageId.get(unit.message?.id ?? '')
                          const showRunTiming = fact !== undefined && runFirstFacts.has(fact.messageId)
                          return (
                            <ReplayAssistantTurn
                              key={memberIds[0] ?? 'assistant'}
                              reasoning={unit.reasoning}
                              message={unit.message}
                              threadId={threadId}
                              dimmed={dimmed}
                              turnStart={turnStart}
                              fact={fact}
                              showRunTiming={showRunTiming}
                            />
                          )
                        }
                        const item = unit.item
                        if (item.kind === 'message') {
                          const dimmed = dimStream && !relatedItemIds.has(item.id)
                          return (
                            <ReplayMessage
                              key={item.id}
                              item={item}
                              threadId={threadId}
                              dimmed={dimmed}
                              turnStart={itemTurnStarts.get(item.id)}
                              fact={usageByMessageId.get(item.id)}
                              showRunTiming={false}
                            />
                          )
                        }
                        const related = relatedItemIds.has(item.toolCallId)
                        const dimmed = dimStream && !related
                        const fact = usageByMessageId.get(item.parentMessageId ?? '')
                        const showRunTiming = fact !== undefined && runFirstFacts.has(fact.messageId)
                        return (
                          <ReplayToolCall
                            key={item.toolCallId}
                            item={item}
                            threadId={threadId}
                            highlighted={related}
                            dimmed={dimmed}
                            turnStart={itemTurnStarts.get(item.toolCallId)}
                            fact={fact}
                            showRunTiming={showRunTiming}
                            audits={toolCallAuditsFor(toolCalls, item.toolCallId)}
                            confirmations={confirmationsForToolCall(confirmations, item.toolCallId)}
                            onPickTrace={pickTrace}
                          />
                        )
                      })}
                    </CardContent>
                  </Card>
                </section>

                <section aria-label="工具调用审计">
                  <Card>
                    <CardHeader>
                      <CardTitle className="text-sm font-semibold">工具调用审计</CardTitle>
                    </CardHeader>
                    <CardContent>
                      {toolCalls.length === 0 ? (
                        <EmptyNote>暂无工具调用审计行。</EmptyNote>
                      ) : (
                        <Table className="min-w-[760px]">
                          <TableHeader>
                            <TableRow>
                              <TableHead className="text-xs text-muted-foreground">工具</TableHead>
                              <TableHead className="text-xs text-muted-foreground">状态</TableHead>
                              <TableHead className="text-xs text-muted-foreground">耗时(ms)</TableHead>
                              <TableHead className="text-xs text-muted-foreground">trace_id</TableHead>
                              <TableHead className="text-xs text-muted-foreground">参数摘要</TableHead>
                              <TableHead className="text-xs text-muted-foreground">结果摘要</TableHead>
                              <TableHead className="text-xs text-muted-foreground">时间</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {toolCalls.map((row) => {
                              const hit = matchesTraceId(row, traceFilter)
                              return (
                                <TableRow
                                  key={row.id}
                                  data-tool-call-id={row.toolCallId}
                                  className={hitRowClass(hit, traceFilter !== null && !hit)}
                                >
                                  <TableCell className="font-medium">{row.toolName}</TableCell>
                                  <TableCell>
                                    <Badge className={statusChipClass(row.status)}>{row.status}</Badge>
                                  </TableCell>
                                  <TableCell className="tabular-nums">{row.durationMs}</TableCell>
                                  <TableCell>
                                    {row.traceId ? (
                                      <TracePickButton traceId={row.traceId} onPickTrace={pickTrace} />
                                    ) : (
                                      '—'
                                    )}
                                  </TableCell>
                                  <TableCell className="max-w-52 truncate font-mono text-xs">{row.argumentsSummary ?? '—'}</TableCell>
                                  <TableCell className="max-w-52 truncate font-mono text-xs">{row.resultSummary ?? '—'}</TableCell>
                                  <TableCell className="whitespace-nowrap tabular-nums">{formatTime(row.calledAt)}</TableCell>
                                </TableRow>
                              )
                            })}
                          </TableBody>
                        </Table>
                      )}
                    </CardContent>
                  </Card>
                </section>

                <section aria-label="授权确认记录">
                  <Card>
                    <CardHeader>
                      <CardTitle className="text-sm font-semibold">授权确认记录</CardTitle>
                    </CardHeader>
                    <CardContent>
                      {confirmations.length === 0 ? (
                        <EmptyNote>暂无授权确认记录。</EmptyNote>
                      ) : (
                        <Table className="min-w-[680px]">
                          <TableHeader>
                            <TableRow>
                              <TableHead className="text-xs text-muted-foreground">工具</TableHead>
                              <TableHead className="text-xs text-muted-foreground">决定</TableHead>
                              <TableHead className="text-xs text-muted-foreground">remember</TableHead>
                              <TableHead className="text-xs text-muted-foreground">路径</TableHead>
                              <TableHead className="text-xs text-muted-foreground">操作者</TableHead>
                              <TableHead className="text-xs text-muted-foreground">trace_id</TableHead>
                              <TableHead className="text-xs text-muted-foreground">时间</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {confirmations.map((row) => {
                              const hit = matchesTraceId(row, traceFilter)
                              return (
                                <TableRow key={row.id} className={hitRowClass(hit, traceFilter !== null && !hit)}>
                                  <TableCell className="font-medium">{row.toolName}</TableCell>
                                  <TableCell>
                                    <Badge className={decisionChipClass(row.decision)}>{row.decision}</Badge>
                                  </TableCell>
                                  <TableCell>{row.remember || '—'}</TableCell>
                                  <TableCell className="font-mono text-xs">{row.path}</TableCell>
                                  <TableCell>{row.decidedBy}</TableCell>
                                  <TableCell>
                                    {row.traceId ? (
                                      <TracePickButton traceId={row.traceId} onPickTrace={pickTrace} />
                                    ) : (
                                      '—'
                                    )}
                                  </TableCell>
                                  <TableCell className="whitespace-nowrap tabular-nums">{formatTime(row.decidedAt)}</TableCell>
                                </TableRow>
                              )
                            })}
                          </TableBody>
                        </Table>
                      )}
                    </CardContent>
                  </Card>
                </section>

                <section aria-label="检索留痕">
                  <Card>
                    <CardHeader>
                      <CardTitle className="text-sm font-semibold">检索留痕</CardTitle>
                    </CardHeader>
                    <CardContent>
                      {retrievals.length === 0 ? (
                        <EmptyNote>暂无检索留痕。</EmptyNote>
                      ) : (
                        <ol className="space-y-3">
                          {retrievals.map((row) => {
                            const matched = matchesTraceId(row, traceFilter)
                            const hits = parseRetrievalHits(row.hits)
                            const stages = parseRetrievalStages(row.stages)
                            const scoreSource = retrievalScoreSource(stages)
                            return (
                              <li
                                key={row.id}
                                data-trace-id={row.traceId ?? undefined}
                                className={`rounded-xl border ${
                                  matched
                                    ? 'border-brand-soft-border bg-brand-soft'
                                    : 'border-border bg-soft'
                                } ${traceFilter !== null && !matched ? 'opacity-40' : ''}`}
                              >
                                <Collapsible className="group">
                                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5 p-3.5">
                                    <CollapsibleTrigger className="flex min-w-0 flex-1 cursor-pointer flex-wrap items-center gap-x-2 gap-y-1.5 text-left select-none">
                                      <Badge className={statusChipClass(row.status)}>
                                        {row.source}/{row.status}
                                      </Badge>
                                      <span className="text-sm text-foreground">{row.queryText ?? '（无查询文本）'}</span>
                                      <span className="text-xs text-muted-foreground tabular-nums">
                                        top_k {row.requestedTopK ?? '未指定'} → {row.effectiveTopK ?? '不适用'} · {row.durationMs}
                                        ms · {formatTime(row.createdAt)}
                                      </span>
                                      <ChevronDownIcon
                                        aria-hidden
                                        className="size-3.5 shrink-0 text-muted-foreground transition-transform duration-200 group-data-[state=open]:rotate-180"
                                      />
                                    </CollapsibleTrigger>
                                    {/* 追踪按钮与展开触发器平级：点追踪只切链路，
                                        不吞掉也不触发展开。 */}
                                    {row.traceId && <TracePickButton traceId={row.traceId} onPickTrace={pickTrace} />}
                                  </div>
                                  <CollapsibleContent>
                                    <div className="space-y-3 border-t p-3.5">
                                      {hits.length > 0 && (
                                        <div className="space-y-1.5">
                                          <p className="text-xs text-muted-foreground">
                                            命中 {hits.length} 条 · 分数来源：{scoreSource}
                                          </p>
                                          <ul aria-label="检索命中" className="space-y-1.5">
                                            {hits.map((item, index) => (
                                              <li
                                                key={`${item.documentId}-${index}`}
                                                title={item.anchor ?? undefined}
                                                className="flex flex-wrap items-center gap-1 text-xs text-muted-foreground"
                                              >
                                                分片 <ChunkIdPopover chunkId={item.documentId} /> · score{' '}
                                                <span className="tabular-nums">{formatScore(item.score)}</span>
                                                {item.anchor && ` · 锚点 ${item.anchor}`}
                                              </li>
                                            ))}
                                          </ul>
                                        </div>
                                      )}
                                      {/* 阶段时间线（编排层写入的行）：向量/词法
                                          召回 → 融合 → 重排 → 回包，阶段失败摘
                                          要同榜可见；单路直调行无 stages，不渲
                                          染不伪造。 */}
                                      <RetrievalStageTimeline stages={stages} />
                                    </div>
                                  </CollapsibleContent>
                                </Collapsible>
                              </li>
                            )
                          })}
                        </ol>
                      )}
                    </CardContent>
                  </Card>
                </section>
              </>
            )}
          </div>
        </div>
      </section>
    </div>
  )
}
