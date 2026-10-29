import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
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
import { detectCompactionRefs } from './compaction'
import {
  collectTraceIds,
  confirmationsForToolCall,
  filterByTraceId,
  parseRetrievalHits,
  relatedChatItemIds,
  relatedToolCallIds,
  toolCallAuditsFor,
  type TraceFacets,
} from './model'
import { CompactionRefPanel } from './CompactionRefPanel'
import { AlertCircleIcon, ArrowLeftIcon, GrapeMark, RefreshIcon, SpinnerIcon, WrenchIcon } from '../../ui/icons'

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
// 未命中且链路过滤生效时降透明度。
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
    <details
      data-tool-call-id={item.toolCallId}
      data-highlight={highlighted ? 'true' : undefined}
      aria-label={highlighted ? `工具调用 ${item.name}（当前链路）` : `工具调用 ${item.name}`}
      className={`card group overflow-hidden transition-opacity duration-200 ${
        highlighted ? 'border-violet-300 bg-violet-50 ring-1 ring-violet-300' : ''
      } ${dimmed ? 'opacity-40' : ''}`}
    >
      <summary className="flex cursor-pointer list-none items-center gap-2 px-3.5 py-2.5 text-sm text-slate-500 transition-colors select-none hover:bg-slate-50 [&::-webkit-details-marker]:hidden">
        <WrenchIcon aria-hidden className="h-4 w-4 shrink-0 text-slate-400" />
        {`工具 ${item.name || '(未知)'}`}
        {highlighted && <span className="chip bg-violet-100 text-violet-700">当前链路</span>}
        {audits.length > 0 && <span className="chip bg-slate-100 text-slate-500">审计 {audits.length} 条</span>}
        {confirmations.length > 0 && <span className="chip bg-slate-100 text-slate-500">确认 {confirmations.length} 条</span>}
        <svg
          aria-hidden
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth={2}
          strokeLinecap="round"
          strokeLinejoin="round"
          className="ml-auto h-4 w-4 shrink-0 text-slate-400 transition-transform duration-200 group-open:rotate-180"
        >
          <path d="m6 9 6 6 6-6" />
        </svg>
      </summary>
      <div className="space-y-3 border-t border-slate-100 px-3.5 py-3">
        {item.argsText !== '' && (
          <>
            <h4 className="text-xs font-medium text-slate-400">参数</h4>
            <pre className="code-block">{item.argsText}</pre>
          </>
        )}
        {item.result !== null && (
          <>
            <h4 className="text-xs font-medium text-slate-400">结果</h4>
            <CompactibleText text={item.result} threadId={threadId} />
          </>
        )}
        {audits.length > 0 && (
          <div className="space-y-1.5">
            <h4 className="text-xs font-medium text-slate-400">调用审计</h4>
            {audits.map((row) => (
              <div key={row.id} className="space-y-1 rounded-lg bg-slate-50 px-3 py-2 text-xs text-slate-600">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <span
                    className={`chip ${row.status === 'success' ? 'bg-emerald-50 text-emerald-600' : 'bg-red-50 text-red-600'}`}
                  >
                    {row.status}
                  </span>
                  <span>耗时 {row.durationMs}ms</span>
                  <span>重试 {formatRetryCount(row.retryCount)}</span>
                  <span>{formatTime(row.calledAt)}</span>
                </div>
                {row.argumentsSummary !== null && <p className="break-all font-mono">参数摘要：{row.argumentsSummary}</p>}
                {row.resultSummary !== null && <p className="break-all font-mono">结果摘要：{row.resultSummary}</p>}
              </div>
            ))}
          </div>
        )}
        {confirmations.length > 0 && (
          <div className="space-y-1.5">
            <h4 className="text-xs font-medium text-slate-400">授权确认</h4>
            {confirmations.map((row) => (
              <div
                key={row.id}
                className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg bg-slate-50 px-3 py-2 text-xs text-slate-600"
              >
                <span
                  className={`chip ${
                    row.decision === 'approve' || row.decision === 'approved'
                      ? 'bg-emerald-50 text-emerald-600'
                      : 'bg-red-50 text-red-600'
                  }`}
                >
                  {row.decision}
                </span>
                <span>{row.remember || '—'}</span>
                <span>{row.path}</span>
                <span>{row.decidedBy}</span>
                <span>{formatTime(row.decidedAt)}</span>
                {row.traceId && (
                  <button
                    type="button"
                    onClick={() => onPickTrace(row.traceId ?? null)}
                    className="chip cursor-pointer bg-violet-50 font-mono text-[11px] text-violet-600 transition-colors hover:bg-violet-100"
                  >
                    trace {row.traceId}
                  </button>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </details>
  )
}

// ReplayMessage 复现流里的单条消息：与对话页气泡形态一致——用户消息
// 靠右品牌紫气泡（标签「你」），assistant 等其余角色靠左白底气泡（标签
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
      <span className="mb-1 px-1 text-xs text-slate-400">{isUser ? '你' : item.role}</span>
      <div
        className={`max-w-[85%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed wrap-break-word whitespace-pre-wrap ${
          isUser
            ? 'rounded-br-sm bg-violet-600 text-white shadow-xs'
            : 'rounded-bl-sm border border-slate-200 bg-white text-slate-800 shadow-xs'
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
// 工具调用审计 / 授权确认记录 / 检索留痕），以 trace_id 关联过滤串联同
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
  const visibleToolCalls = useMemo(() => filterByTraceId(toolCalls, traceFilter), [toolCalls, traceFilter])
  const visibleConfirmations = useMemo(() => filterByTraceId(confirmations, traceFilter), [confirmations, traceFilter])
  const visibleRetrievals = useMemo(() => filterByTraceId(retrievals, traceFilter), [retrievals, traceFilter])
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
    <div className="flex h-full overflow-hidden bg-slate-100">
      <aside aria-label="复现会话侧栏" className="flex w-72 shrink-0 flex-col border-r border-slate-200 bg-white">
        <div className="flex items-center gap-2.5 px-5 pt-5 pb-4">
          <GrapeMark className="h-9 w-9" />
          <h1 className="text-sm font-semibold text-slate-900">运维复现视图</h1>
        </div>
        <div className="px-4 pb-3">
          <button type="button" onClick={() => navigate('/')} className="btn btn-outline btn-sm w-full">
            <ArrowLeftIcon aria-hidden className="h-3.5 w-3.5" />
            返回对话
          </button>
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
                    className={active ? 'session-item session-item-active' : 'session-item'}
                  >
                    <span className={`truncate text-sm font-medium ${active ? 'text-violet-700' : 'text-slate-700'}`}>
                      {`会话 ${session.id.slice(0, 8)}`}
                    </span>
                  </button>
                  <time
                    dateTime={session.updatedAt}
                    className={`block px-3 text-xs ${active ? 'text-violet-400' : 'text-slate-400'}`}
                  >
                    {formatTime(session.updatedAt)}
                  </time>
                </li>
              )
            })}
          </ul>
        </nav>
      </aside>

      <section aria-label="复现证据区" className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b border-slate-200 bg-white px-6">
          <label className="flex min-w-0 items-center gap-2 text-sm text-slate-500">
            trace 过滤{' '}
            <select
              aria-label="trace 过滤"
              className="field w-64 min-w-0 font-mono text-xs"
              value={traceFilter ?? ''}
              onChange={(e) => setTraceFilter(e.target.value === '' ? null : e.target.value)}
            >
              <option value="">全部链路</option>
              {traceIds.map((traceId) => (
                <option key={traceId} value={traceId}>
                  {traceId}
                </option>
              ))}
            </select>
          </label>
          {traceFilter !== null && (
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => setTraceFilter(null)}>
              清除过滤
            </button>
          )}
          <button
            type="button"
            className="btn btn-outline btn-sm"
            disabled={threadId === null || loading}
            onClick={() => threadId !== null && void loadThread(threadId)}
          >
            <RefreshIcon aria-hidden className="h-3.5 w-3.5" />
            刷新快照
          </button>
          {loading && (
            <span role="status" className="chip bg-violet-50 text-violet-600">
              <SpinnerIcon aria-hidden className="h-3 w-3" />
              加载中…
            </span>
          )}
          <span aria-hidden className="ml-auto hidden text-xs text-slate-400 xl:inline">
            四联证据 · 以 trace_id 关联同一次交互
          </span>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="w-full space-y-6 px-6 py-6">
            {error !== null && (
              <p role="alert" className="alert">
                <AlertCircleIcon aria-hidden className="mt-0.5 h-4 w-4 shrink-0" />
                <span>加载失败：{error}</span>
              </p>
            )}
            {threadId === null && (
              <p className="empty-note">暂无会话：先在对话页发起会话。</p>
            )}

            {threadId !== null && (
              <>
                <section aria-label="对话流回放" className="card p-5">
                  <div className="mb-3 flex items-baseline justify-between gap-3">
                    <h2 className="text-sm font-semibold text-slate-900">对话流回放</h2>
                    <p className="text-right text-xs text-slate-400">
                      快照即回放：上游 follow 存在同毫秒事件可能被跳过的缺陷（上游已知缺陷），以快照刷新兜底。
                    </p>
                  </div>
                  <div className="space-y-3">
                    {items.length === 0 &&
                      (historyUnavailable ? (
                        <p className="empty-note">该会话无可回放快照（可能没有持久化的事件），其余证据面不受影响。</p>
                      ) : (
                        <p className="empty-note">该会话暂无快照消息。</p>
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
                  </div>
                </section>

                <section aria-label="工具调用审计" className="card p-5">
                  <h2 className="mb-3 text-sm font-semibold text-slate-900">工具调用审计</h2>
                  {visibleToolCalls.length === 0 ? (
                    <p className="empty-note">无匹配的工具调用审计行。</p>
                  ) : (
                    <div className="overflow-x-auto">
                      <table className="w-full min-w-[760px] border-collapse text-sm">
                        <thead>
                          <tr className="border-b border-slate-200 text-left text-xs text-slate-400">
                            <th className="px-2 py-2 font-medium">工具</th>
                            <th className="px-2 py-2 font-medium">状态</th>
                            <th className="px-2 py-2 font-medium">耗时(ms)</th>
                            <th className="px-2 py-2 font-medium">重试</th>
                            <th className="px-2 py-2 font-medium">trace_id</th>
                            <th className="px-2 py-2 font-medium">参数摘要</th>
                            <th className="px-2 py-2 font-medium">结果摘要</th>
                            <th className="px-2 py-2 font-medium">时间</th>
                          </tr>
                        </thead>
                        <tbody>
                          {visibleToolCalls.map((row) => (
                            <tr
                              key={row.id}
                              data-tool-call-id={row.toolCallId}
                              className={`border-b border-slate-100 text-slate-600 transition-colors last:border-0 ${
                                traceFilter !== null ? 'bg-violet-50 hover:bg-violet-100' : 'hover:bg-slate-50'
                              }`}
                            >
                              <td className="px-2 py-2 font-medium text-slate-800">{row.toolName}</td>
                              <td className="px-2 py-2">
                                <span
                                  className={`chip ${
                                    row.status === 'success' ? 'bg-emerald-50 text-emerald-600' : 'bg-red-50 text-red-600'
                                  }`}
                                >
                                  {row.status}
                                </span>
                              </td>
                              <td className="px-2 py-2 tabular-nums">{row.durationMs}</td>
                              <td className="px-2 py-2 text-slate-400">{formatRetryCount(row.retryCount)}</td>
                              <td className="px-2 py-2">
                                {row.traceId ? (
                                  <button
                                    type="button"
                                    onClick={() => pickTrace(row.traceId ?? null)}
                                    className="chip cursor-pointer bg-violet-50 font-mono text-[11px] text-violet-600 transition-colors hover:bg-violet-100"
                                  >
                                    {row.traceId}
                                  </button>
                                ) : (
                                  '—'
                                )}
                              </td>
                              <td className="max-w-52 truncate px-2 py-2 font-mono text-xs">{row.argumentsSummary ?? '—'}</td>
                              <td className="max-w-52 truncate px-2 py-2 font-mono text-xs">{row.resultSummary ?? '—'}</td>
                              <td className="px-2 py-2 whitespace-nowrap tabular-nums">{formatTime(row.calledAt)}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                </section>

                <section aria-label="授权确认记录" className="card p-5">
                  <h2 className="mb-3 text-sm font-semibold text-slate-900">授权确认记录</h2>
                  {visibleConfirmations.length === 0 ? (
                    <p className="empty-note">无匹配的授权确认记录。</p>
                  ) : (
                    <div className="overflow-x-auto">
                      <table className="w-full min-w-[680px] border-collapse text-sm">
                        <thead>
                          <tr className="border-b border-slate-200 text-left text-xs text-slate-400">
                            <th className="px-2 py-2 font-medium">工具</th>
                            <th className="px-2 py-2 font-medium">决定</th>
                            <th className="px-2 py-2 font-medium">remember</th>
                            <th className="px-2 py-2 font-medium">路径</th>
                            <th className="px-2 py-2 font-medium">操作者</th>
                            <th className="px-2 py-2 font-medium">trace_id</th>
                            <th className="px-2 py-2 font-medium">时间</th>
                          </tr>
                        </thead>
                        <tbody>
                          {visibleConfirmations.map((row) => (
                            <tr
                              key={row.id}
                              className={`border-b border-slate-100 text-slate-600 transition-colors last:border-0 ${
                                traceFilter !== null ? 'bg-violet-50 hover:bg-violet-100' : 'hover:bg-slate-50'
                              }`}
                            >
                              <td className="px-2 py-2 font-medium text-slate-800">{row.toolName}</td>
                              <td className="px-2 py-2">
                                <span
                                  className={`chip ${
                                    row.decision === 'approve' || row.decision === 'approved'
                                      ? 'bg-emerald-50 text-emerald-600'
                                      : 'bg-red-50 text-red-600'
                                  }`}
                                >
                                  {row.decision}
                                </span>
                              </td>
                              <td className="px-2 py-2">{row.remember || '—'}</td>
                              <td className="px-2 py-2 font-mono text-xs">{row.path}</td>
                              <td className="px-2 py-2">{row.decidedBy}</td>
                              <td className="px-2 py-2">
                                {row.traceId ? (
                                  <button
                                    type="button"
                                    onClick={() => pickTrace(row.traceId ?? null)}
                                    className="chip cursor-pointer bg-violet-50 font-mono text-[11px] text-violet-600 transition-colors hover:bg-violet-100"
                                  >
                                    {row.traceId}
                                  </button>
                                ) : (
                                  '—'
                                )}
                              </td>
                              <td className="px-2 py-2 whitespace-nowrap tabular-nums">{formatTime(row.decidedAt)}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                </section>

                <section aria-label="检索留痕" className="card p-5">
                  <h2 className="mb-3 text-sm font-semibold text-slate-900">检索留痕</h2>
                  {visibleRetrievals.length === 0 ? (
                    <p className="empty-note">无匹配的检索留痕。</p>
                  ) : (
                    <ol className="space-y-3">
                      {visibleRetrievals.map((row) => {
                        const hits = parseRetrievalHits(row.hits)
                        return (
                          <li
                            key={row.id}
                            data-trace-id={row.traceId ?? undefined}
                            className={`rounded-xl border p-3.5 ${
                              traceFilter !== null ? 'border-violet-200 bg-violet-50' : 'border-slate-200 bg-slate-50'
                            }`}
                          >
                            <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
                              <span
                                className={`chip ${
                                  row.status === 'success' ? 'bg-emerald-50 text-emerald-600' : 'bg-red-50 text-red-600'
                                }`}
                              >
                                {row.source}/{row.status}
                              </span>
                              <span className="text-sm text-slate-700">{row.queryText ?? '（无查询文本）'}</span>
                              <span className="text-xs text-slate-400 tabular-nums">
                                top_k {row.requestedTopK ?? '未指定'} → {row.effectiveTopK ?? '不适用'} · {row.durationMs}
                                ms · {formatTime(row.createdAt)}
                              </span>
                              {row.traceId && (
                                <button
                                  type="button"
                                  onClick={() => pickTrace(row.traceId ?? null)}
                                  className="chip cursor-pointer bg-violet-50 font-mono text-[11px] text-violet-600 transition-colors hover:bg-violet-100"
                                >
                                  trace {row.traceId}
                                </button>
                              )}
                            </div>
                            {hits.length > 0 && (
                              <ul aria-label="检索命中" className="mt-2.5 space-y-1.5 border-t border-slate-200 pt-2.5">
                                {hits.map((hit, index) => (
                                  <li
                                    key={`${hit.documentId}-${index}`}
                                    title={hit.anchor ?? undefined}
                                    className="flex flex-wrap items-center gap-1 text-xs text-slate-500"
                                  >
                                    文档{' '}
                                    <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] text-slate-700">
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
                </section>
              </>
            )}
          </div>
        </div>
      </section>
    </div>
  )
}
