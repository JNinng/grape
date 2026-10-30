import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { ChatItem, MessageItem, ToolCallItem } from '../../agui/events'
import { fetchHistorySnapshot } from '../../agui/history'
import { fetchAgentSessions, type AgentSessionMeta } from '../../api/agentSessions'
import {
  fetchConfirmationAudits,
  fetchToolCallAudits,
  type ConfirmationAudit,
  type ToolCallAudit,
} from '../../api/agentAudit'
import { fetchRetrievalLogs, type RetrievalLog } from '../../api/agentRetrievalLogs'
import { useNavigate } from 'react-router'
import {
  ArrowLeftIcon,
  ChevronDownIcon,
  CircleAlertIcon,
  RotateCwIcon,
  WrenchIcon,
} from 'lucide-react'
import { detectCompactionRefs } from './compaction'
import {
  collectTraceIds,
  confirmationsForToolCall,
  matchesTraceId,
  parseRetrievalHits,
  relatedChatItemIds,
  relatedToolCallIds,
  toolCallAuditsFor,
  type TraceFacets,
} from './model'
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

// formatRetryCount 呈现重试次数：框架不对外暴露工具重试（审计恒记 0，
// 见 internal/agent/tool_audit.go），0 值以占位说明呈现避免误读为
// 「确认无重试」。
function formatRetryCount(retryCount: number): string {
  return retryCount === 0 ? '0（框架未暴露）' : String(retryCount)
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

// ReplayToolCall 复现流里的工具调用折叠项：摘要行始终携带关联审计/确认
// 数量徽标；展开后在参数与「结果」原文之下内嵌同一 toolCallId 的审计行
// 与授权确认行，免去跨区交叉查证。trace 过滤命中的项以品牌紫描边高亮，
// 未命中且链路过滤生效时降透明度。默认收起，点按开合（内容随开合挂载）。
function ReplayToolCall({
  item,
  threadId,
  highlighted,
  dimmed,
  audits,
  confirmations,
  onPickTrace,
}: {
  item: ToolCallItem
  threadId: string
  highlighted: boolean
  dimmed: boolean
  audits: ToolCallAudit[]
  confirmations: ConfirmationAudit[]
  onPickTrace: (traceId: string | null) => void
}) {
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
                    <span>重试 {formatRetryCount(row.retryCount)}</span>
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

// ReplayMessage 复现流里的单条消息：与对话页气泡形态一致——用户消息
// 靠右品牌紫气泡（标签「你」），assistant 等其余角色靠左卡片气泡（标签
// 沿用原始角色名作证据口径）。压缩占位符面板跟随消息同侧渲染；链路过
// 滤启用且本消息不在命中段内时降透明度。
function ReplayMessage({
  item,
  threadId,
  dimmed,
}: {
  item: MessageItem
  threadId: string
  dimmed: boolean
}) {
  const refs = useMemo(() => detectCompactionRefs(item.content), [item.content])
  const isUser = item.role === 'user'
  return (
    <article
      data-role={item.role}
      className={`flex flex-col transition-opacity duration-200 ${isUser ? 'items-end' : 'items-start'} ${
        dimmed ? 'opacity-40' : ''
      }`}
    >
      <span className="mb-1 px-1 text-xs text-muted-foreground">{isUser ? '你' : item.role}</span>
      <div
        className={`max-w-[85%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed wrap-break-word whitespace-pre-wrap ${
          isUser
            ? 'rounded-br-sm bg-primary text-primary-foreground shadow-xs'
            : 'rounded-bl-sm border bg-card text-card-foreground shadow-xs'
        }`}
      >
        {item.content}
      </div>
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
  const [traceFilter, setTraceFilter] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // loadSeq 守卫并发加载的乱序回写：快速切换会话或连点刷新时，只有最后
  // 一次发起的装载允许落状态，先前在途响应按过期丢弃。
  const loadSeq = useRef(0)

  const loadThread = useCallback(async (tid: string) => {
    const seq = ++loadSeq.current
    setLoading(true)
    setError(null)
    setHistoryUnavailable(false)
    try {
      // /history 快照独立降级：无 track 事件的会话（仅存 ack 待确认指针、
      // 或 track 事件已过保留期的会话）上游快照运行会 500（RunStartedEvent
      // validation failed，同族的上游快照缺陷）。回放只是四联
      // 证据之一，缺失按空态提示降级，其余证据面照常装载。
      const [history, auditToolCalls, auditConfirmations, retrievalLogs] = await Promise.all([
        fetchHistorySnapshot(tid).catch(() => null),
        fetchToolCallAudits(tid),
        fetchConfirmationAudits(tid),
        fetchRetrievalLogs(tid),
      ])
      if (seq !== loadSeq.current) {
        return
      }
      setItems(history ?? [])
      setHistoryUnavailable(history === null)
      setToolCalls(auditToolCalls)
      setConfirmations(auditConfirmations)
      setRetrievals(retrievalLogs)
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
  // 链路选中只做高亮对比：三证据面全量渲染，命中行品牌紫高亮、未命中
  // 行灰显，不隐藏任何证据行（对比呈现保住证据间的上下文）。
  // 回放流关联：桥接 ID 取审计∪确认（live 路径两面 trace 不同），再按分
  // 段邻近归属扩散到整段消息；仅在确有命中项时降透明度，避免整流变灰
  // 而无所指。
  const relatedToolIds = useMemo(() => relatedToolCallIds(facets, traceFilter), [facets, traceFilter])
  const relatedItemIds = useMemo(() => relatedChatItemIds(items, relatedToolIds), [items, relatedToolIds])
  const dimStream = traceFilter !== null && relatedItemIds.size > 0

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
                      {items.map((item) => {
                        const related = relatedItemIds.has(item.kind === 'message' ? item.id : item.toolCallId)
                        const dimmed = dimStream && !related
                        return item.kind === 'message' ? (
                          <ReplayMessage key={item.id} item={item} threadId={threadId} dimmed={dimmed} />
                        ) : (
                          <ReplayToolCall
                            key={item.toolCallId}
                            item={item}
                            threadId={threadId}
                            highlighted={related}
                            dimmed={dimmed}
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
                              <TableHead className="text-xs text-muted-foreground">重试</TableHead>
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
                                  <TableCell className="text-muted-foreground">{formatRetryCount(row.retryCount)}</TableCell>
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
                            const hit = matchesTraceId(row, traceFilter)
                            const hits = parseRetrievalHits(row.hits)
                            return (
                              <li
                                key={row.id}
                                data-trace-id={row.traceId ?? undefined}
                                className={`rounded-xl border p-3.5 ${
                                  hit
                                    ? 'border-brand-soft-border bg-brand-soft'
                                    : 'border-border bg-soft'
                                } ${traceFilter !== null && !hit ? 'opacity-40' : ''}`}
                              >
                                <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
                                  <Badge className={statusChipClass(row.status)}>
                                    {row.source}/{row.status}
                                  </Badge>
                                  <span className="text-sm text-foreground">{row.queryText ?? '（无查询文本）'}</span>
                                  <span className="text-xs text-muted-foreground tabular-nums">
                                    top_k {row.requestedTopK ?? '未指定'} → {row.effectiveTopK ?? '不适用'} · {row.durationMs}
                                    ms · {formatTime(row.createdAt)}
                                  </span>
                                  {row.traceId && <TracePickButton traceId={row.traceId} onPickTrace={pickTrace} />}
                                </div>
                                {hits.length > 0 && (
                                  <ul aria-label="检索命中" className="mt-2.5 space-y-1.5 border-t pt-2.5">
                                    {hits.map((hit, index) => (
                                      <li
                                        key={`${hit.documentId}-${index}`}
                                        title={hit.anchor ?? undefined}
                                        className="flex flex-wrap items-center gap-1 text-xs text-muted-foreground"
                                      >
                                        文档{' '}
                                        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-secondary-foreground">
                                          {hit.documentId}
                                        </code>{' '}
                                        · score {hit.score}
                                        {hit.anchor && ` · 锚点 ${hit.anchor}`}
                                      </li>
                                    ))}
                                  </ul>
                                )}
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
