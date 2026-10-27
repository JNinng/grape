import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ChatItem } from '../../agui/events'
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
  highlightedToolCallIds,
  parseRetrievalHits,
  toolCallAuditsFor,
  type TraceFacets,
} from './model'
import { CompactionRefPanel } from './CompactionRefPanel'

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
  const [selectedToolCallId, setSelectedToolCallId] = useState<string | null>(null)
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
    setSelectedToolCallId(null)
    void loadThread(threadId)
  }, [threadId, loadThread])

  const facets: TraceFacets = useMemo(
    () => ({ toolCalls, confirmations, retrievals }),
    [toolCalls, confirmations, retrievals],
  )
  const traceIds = useMemo(() => collectTraceIds(facets), [facets])
  const highlight = useMemo(() => highlightedToolCallIds(toolCalls, traceFilter), [toolCalls, traceFilter])
  const visibleToolCalls = useMemo(() => filterByTraceId(toolCalls, traceFilter), [toolCalls, traceFilter])
  const visibleConfirmations = useMemo(() => filterByTraceId(confirmations, traceFilter), [confirmations, traceFilter])
  const visibleRetrievals = useMemo(() => filterByTraceId(retrievals, traceFilter), [retrievals, traceFilter])
  const linkedAudits = selectedToolCallId === null ? [] : toolCallAuditsFor(toolCalls, selectedToolCallId)
  const linkedConfirmations =
    selectedToolCallId === null ? [] : confirmationsForToolCall(confirmations, selectedToolCallId)

  const pickTrace = (traceId: string | null) => {
    setTraceFilter((current) => (current === traceId ? null : traceId))
  }

  return (
    <main className="replay-page">
      <aside aria-label="复现会话侧栏">
        <header>
          <h1>运维复现视图</h1>
          <button type="button" onClick={() => navigate('/')}>
            返回对话
          </button>
        </header>
        <ul aria-label="复现会话列表">
          {sessions.map((session) => (
            <li key={session.id}>
              <button
                type="button"
                aria-current={session.id === threadId ? 'true' : undefined}
                onClick={() => setThreadId(session.id)}
              >
                会话 {session.id.slice(0, 8)}
              </button>
            </li>
          ))}
        </ul>
      </aside>

      <section aria-label="复现证据区">
        <header>
          <label>
            trace 过滤{' '}
            <select
              aria-label="trace 过滤"
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
          <button type="button" disabled={threadId === null || loading} onClick={() => threadId !== null && void loadThread(threadId)}>
            刷新快照
          </button>
          {loading && <span role="status">加载中…</span>}
        </header>
        {error !== null && <p role="alert">加载失败：{error}</p>}
        {threadId === null && <p>暂无会话：先在对话页发起会话。</p>}

        {threadId !== null && (
          <>
            <section aria-label="对话流回放">
              <h2>对话流回放</h2>
              <p>快照即回放：上游 follow 存在同毫秒事件可能被跳过的缺陷（上游已知缺陷），以快照刷新兜底。</p>
              {items.length === 0 &&
                (historyUnavailable ? (
                  <p>该会话无可回放快照（可能没有持久化的事件），其余证据面不受影响。</p>
                ) : (
                  <p>该会话暂无快照消息。</p>
                ))}
              {items.map((item) =>
                item.kind === 'message' ? (
                  <article key={item.id} data-role={item.role}>
                    <span>{item.role}</span>
                    <CompactibleText text={item.content} threadId={threadId} />
                  </article>
                ) : (
                  <details
                    key={item.toolCallId}
                    data-tool-call-id={item.toolCallId}
                    data-highlight={highlight.has(item.toolCallId) ? 'true' : undefined}
                    aria-label={highlight.has(item.toolCallId) ? `工具调用 ${item.name}（当前链路）` : `工具调用 ${item.name}`}
                  >
                    <summary onClick={() => setSelectedToolCallId(item.toolCallId)}>
                      工具 {item.name || '(未知)'}
                      {highlight.has(item.toolCallId) && ' · 当前链路'}
                      {linkedAudits.length > 0 && ` · 审计 ${linkedAudits.length} 条`}
                      {linkedConfirmations.length > 0 && ` · 确认 ${linkedConfirmations.length} 条`}
                    </summary>
                    {item.argsText !== '' && (
                      <>
                        <h4>参数</h4>
                        <pre>{item.argsText}</pre>
                      </>
                    )}
                    {item.result !== null && <CompactibleText text={item.result} threadId={threadId} />}
                  </details>
                ),
              )}
            </section>

            <section aria-label="工具调用审计">
              <h2>工具调用审计</h2>
              {visibleToolCalls.length === 0 && <p>无匹配的工具调用审计行。</p>}
              <table>
                <thead>
                  <tr>
                    <th>工具</th>
                    <th>状态</th>
                    <th>耗时(ms)</th>
                    <th>重试</th>
                    <th>trace_id</th>
                    <th>参数摘要</th>
                    <th>结果摘要</th>
                    <th>时间</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleToolCalls.map((row) => (
                    <tr key={row.id} data-tool-call-id={row.toolCallId}>
                      <td>{row.toolName}</td>
                      <td>{row.status}</td>
                      <td>{row.durationMs}</td>
                      <td>{formatRetryCount(row.retryCount)}</td>
                      <td>
                        {row.traceId ? (
                          <button type="button" onClick={() => pickTrace(row.traceId ?? null)}>
                            {row.traceId}
                          </button>
                        ) : (
                          '—'
                        )}
                      </td>
                      <td>{row.argumentsSummary ?? '—'}</td>
                      <td>{row.resultSummary ?? '—'}</td>
                      <td>{formatTime(row.calledAt)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>

            <section aria-label="授权确认记录">
              <h2>授权确认记录</h2>
              {visibleConfirmations.length === 0 && <p>无匹配的授权确认记录。</p>}
              <table>
                <thead>
                  <tr>
                    <th>工具</th>
                    <th>决定</th>
                    <th>remember</th>
                    <th>路径</th>
                    <th>操作者</th>
                    <th>trace_id</th>
                    <th>时间</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleConfirmations.map((row) => (
                    <tr key={row.id}>
                      <td>{row.toolName}</td>
                      <td>{row.decision}</td>
                      <td>{row.remember || '—'}</td>
                      <td>{row.path}</td>
                      <td>{row.decidedBy}</td>
                      <td>
                        {row.traceId ? (
                          <button type="button" onClick={() => pickTrace(row.traceId ?? null)}>
                            {row.traceId}
                          </button>
                        ) : (
                          '—'
                        )}
                      </td>
                      <td>{formatTime(row.decidedAt)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>

            <section aria-label="检索留痕">
              <h2>检索留痕</h2>
              {visibleRetrievals.length === 0 && <p>无匹配的检索留痕。</p>}
              <ol>
                {visibleRetrievals.map((row) => {
                  const hits = parseRetrievalHits(row.hits)
                  return (
                    <li key={row.id} data-trace-id={row.traceId ?? undefined}>
                      <p>
                        [{row.source}/{row.status}] {row.queryText ?? '（无查询文本）'} · top_k{' '}
                        {row.requestedTopK ?? '未指定'} → {row.effectiveTopK ?? '不适用'} · {row.durationMs}ms ·{' '}
                        {formatTime(row.createdAt)}
                      </p>
                      {row.traceId && (
                        <button type="button" onClick={() => pickTrace(row.traceId ?? null)}>
                          trace {row.traceId}
                        </button>
                      )}
                      {hits.length > 0 && (
                        <ul aria-label="检索命中">
                          {hits.map((hit, index) => (
                            <li key={`${hit.documentId}-${index}`} title={hit.anchor ?? undefined}>
                              文档 <code>{hit.documentId}</code> · score {hit.score}
                              {hit.anchor && ` · 锚点 ${hit.anchor}`}
                            </li>
                          ))}
                        </ul>
                      )}
                    </li>
                  )
                })}
              </ol>
            </section>
          </>
        )}
      </section>
    </main>
  )
}
