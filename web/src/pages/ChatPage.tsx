import { useCallback, useEffect, useRef, useState } from 'react'
import type { Event, Message } from '@ag-ui/client'
import { useNavigate } from 'react-router'
import { fetchAgentSessions, type AgentSessionMeta } from '../api/agentSessions'
import { logout } from '../api/session'
import { createChatAgent } from '../agui/agent'
import { cancelRun } from '../agui/cancel'
import { fetchPendingConfirmations } from '../agui/confirmation'
import { applyChatEvent, initialChatState, type ChatState, type MessageItem } from '../agui/events'
import { fetchHistorySnapshot } from '../agui/history'
import { newId } from '../agui/ids'
import { getUser } from '../auth/store'
import { AlertCircleIcon, FlaskIcon, GrapeMark, LogOutIcon, PlusIcon, SendIcon, StopIcon } from '../ui/icons'
import { ConfirmationCard } from './chat/ConfirmationCard'
import { MessageList } from './chat/MessageList'

// 待确认列表的低频兜底轮询周期：覆盖页面加载与运行间隙之外的漂移
// （如另一端决定、恢复路径孤儿确认）。运行结束与切换会话会立即查询，
// 不依赖该定时器。
const PENDING_POLL_INTERVAL_MS = 30_000

// sessionTitle 由会话元数据推导展示标题：框架会话无业务标题字段，取
// 短 ID + 更新时间呈现，不伪造语义。
function sessionTitle(session: AgentSessionMeta): string {
  return `会话 ${session.id.slice(0, 8)}`
}

function formatTime(iso: string): string {
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString()
}

// ChatPage 对话页：流式问答（AG-UI POST SSE）、工具调用事件、
// tool_confirmation 确认卡、取消与会话（thread）切换。所有模型/工具
// 内容经 events reducer 折叠后以纯文本渲染（sanitize 口径见 MessageList）。
export function ChatPage() {
  const navigate = useNavigate()
  const user = getUser()
  const [threadId, setThreadId] = useState(() => newId('thread'))
  const [chat, setChat] = useState<ChatState>(initialChatState)
  const [input, setInput] = useState('')
  const [historyError, setHistoryError] = useState<string | null>(null)
  const [sessions, setSessions] = useState<AgentSessionMeta[]>([])
  const [sessionsError, setSessionsError] = useState<string | null>(null)

  // agentRef/runIdRef 持有进行中的运行；threadIdRef 供异步回调判定
  // 是否已切走会话（陈旧运行的回包不得写入新会话的状态）。
  const agentRef = useRef<ReturnType<typeof createChatAgent> | null>(null)
  const runIdRef = useRef<string | null>(null)
  const threadIdRef = useRef(threadId)
  threadIdRef.current = threadId
  // scrollRef 承载消息滚动区；inputRef 承载输入框（自适应增高）。
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const inputRef = useRef<HTMLTextAreaElement | null>(null)

  // 消息 / 步骤 / 确认卡变化时贴底滚动，保证流式输出始终可见。
  useEffect(() => {
    const el = scrollRef.current
    if (el !== null) {
      el.scrollTop = el.scrollHeight
    }
  }, [chat.items, chat.steps, chat.confirmations])

  // 输入框随内容自适应增高（封顶后内部滚动）。
  useEffect(() => {
    const el = inputRef.current
    if (el !== null) {
      el.style.height = 'auto'
      el.style.height = `${Math.min(el.scrollHeight, 160)}px`
    }
  }, [input])

  const refreshPending = useCallback(async (tid: string) => {
    try {
      const pending = await fetchPendingConfirmations(tid)
      if (threadIdRef.current !== tid) {
        return
      }
      // 服务端待确认列表是对账基准：整表替换，收敛流事件与兜底查询
      // 两路来源的去重问题。
      setChat((s) => ({ ...s, confirmations: pending }))
    } catch {
      // 兜底查询失败不打断对话：卡片仍可由流内 tool_confirmation 事件给出。
    }
  }, [])

  // 会话列表：进入页面加载一次，发送消息后刷新（新会话入库后出现在列表）。
  const reloadSessions = useCallback(async () => {
    try {
      const list = await fetchAgentSessions()
      setSessions(list)
      setSessionsError(null)
    } catch (err) {
      setSessionsError(err instanceof Error ? err.message : '会话列表加载失败')
    }
  }, [])

  useEffect(() => {
    void reloadSessions()
  }, [reloadSessions])

  // 切换会话（含新建）：终止进行中的运行，重置对话态并拉一次历史快照
  // 与待确认列表。历史不跟流（上游 follow 同毫秒缺陷，快照即回放）。
  useEffect(() => {
    agentRef.current?.abortRun()
    agentRef.current = null
    runIdRef.current = null
    setChat(initialChatState())
    setHistoryError(null)
    let cancelled = false
    fetchHistorySnapshot(threadId)
      .then((items) => {
        // 快照仅在对话仍为空时整体应用：用户在快照回传前已发言（本地
        // 已有消息）则以本地为准，避免回包覆盖正在进行的输入。
        if (!cancelled) {
          setChat((s) => (s.items.length === 0 ? { ...s, items } : s))
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setHistoryError(err instanceof Error ? err.message : '历史加载失败')
        }
      })
    void refreshPending(threadId)
    return () => {
      cancelled = true
    }
  }, [threadId, refreshPending])

  useEffect(() => {
    const timer = window.setInterval(() => {
      void refreshPending(threadIdRef.current)
    }, PENDING_POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [refreshPending])

  const send = async () => {
    const text = input.trim()
    if (text === '' || chat.running) {
      return
    }
    setInput('')
    const tid = threadId
    const userMessage: MessageItem = { kind: 'message', id: newId('msg'), role: 'user', content: text }
    setChat((s) => ({ ...s, items: [...s.items, userMessage], running: true, error: null }))

    const agent = createChatAgent({ threadId: tid })
    const message: Message = { id: userMessage.id, role: 'user', content: text }
    agent.addMessage(message)
    agentRef.current = agent
    const runId = newId('run')
    runIdRef.current = runId

    try {
      await agent.runAgent({ runId }, {
        onEvent: ({ event }) => {
          // 订阅器以擦除形态（BaseEvent）回调；reducer 的 default 分支
          // 忽略未知类型，此处断言回联合是安全的。
          if (threadIdRef.current === tid) {
            setChat((s) => applyChatEvent(s, event as Event))
          }
        },
        onRunFailed: ({ error }) => {
          if (threadIdRef.current === tid) {
            setChat((s) => ({ ...s, running: false, error: error.message }))
          }
        },
      })
    } catch (err) {
      if (threadIdRef.current === tid) {
        setChat((s) => ({
          ...s,
          running: false,
          error: err instanceof Error ? err.message : 'run failed',
        }))
      }
    } finally {
      if (runIdRef.current === runId) {
        runIdRef.current = null
        agentRef.current = null
      }
      void refreshPending(tid)
      void reloadSessions()
    }
  }

  const handleCancel = async () => {
    const runId = runIdRef.current
    if (runId === null) {
      return
    }
    try {
      // cancelled：服务端经原流下发终止事件，reducer 收敛 running；
      // not-running：运行已结束的正常竞态，无需处理。
      await cancelRun(threadId, runId)
    } catch (err) {
      setChat((s) => ({
        ...s,
        error: err instanceof Error ? err.message : 'cancel failed',
      }))
    }
  }

  const handleNewThread = () => {
    setThreadId(newId('thread'))
  }

  const handleLogout = async () => {
    // 后端撤销失败也要清本地态（见 api/session.ts logout），随后回登录页。
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="flex h-full overflow-hidden bg-slate-100">
      <aside aria-label="会话侧栏" className="flex w-72 shrink-0 flex-col border-r border-slate-200 bg-white">
        <div className="flex items-center gap-2.5 px-5 pt-5 pb-4">
          <GrapeMark className="h-9 w-9" />
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-slate-900">Grape</h2>
            <p className="truncate text-xs text-slate-400">AI 对话工作台</p>
          </div>
        </div>
        <div className="px-4 pb-3">
          <button type="button" onClick={handleNewThread} className="btn btn-primary w-full">
            <PlusIcon aria-hidden className="h-4 w-4" />
            新建会话
          </button>
        </div>
        {sessionsError !== null && (
          <p role="alert" className="alert mx-4 mb-3">
            <AlertCircleIcon aria-hidden className="mt-0.5 h-4 w-4 shrink-0" />
            <span>会话列表加载失败：{sessionsError}</span>
          </p>
        )}
        <nav className="min-h-0 flex-1 overflow-y-auto px-3 pb-4">
          <ul aria-label="会话列表" className="space-y-1">
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
                      {sessionTitle(session)}
                    </span>
                    <time
                      dateTime={session.updatedAt}
                      className={`text-xs ${active ? 'text-violet-400' : 'text-slate-400'}`}
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

      <section aria-label="对话区" className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center justify-between gap-4 border-b border-slate-200 bg-white px-6">
          <h1 className="truncate text-base font-semibold text-slate-900">Grape AI 对话</h1>
          <div className="flex shrink-0 items-center gap-1.5">
            <span className="mr-1 hidden text-sm text-slate-500 lg:inline">
              欢迎，{user?.nickname || user?.name || '用户'}
            </span>
            <span aria-hidden className="mr-2 hidden h-4 w-px bg-slate-200 lg:inline-block" />
            <button type="button" onClick={() => navigate('/replay')} className="btn btn-ghost btn-sm">
              <FlaskIcon aria-hidden className="h-3.5 w-3.5" />
              运维复现
            </button>
            <button type="button" onClick={handleLogout} className="btn btn-ghost btn-sm">
              <LogOutIcon aria-hidden className="h-3.5 w-3.5" />
              登出
            </button>
          </div>
        </header>

        {(historyError !== null || chat.error !== null) && (
          <div className="mx-auto w-full max-w-3xl space-y-2 px-6 pt-4">
            {historyError !== null && (
              <p role="alert" className="alert">
                <AlertCircleIcon aria-hidden className="mt-0.5 h-4 w-4 shrink-0" />
                <span>历史加载失败：{historyError}</span>
              </p>
            )}
            {chat.error !== null && (
              <p role="alert" className="alert">
                <AlertCircleIcon aria-hidden className="mt-0.5 h-4 w-4 shrink-0" />
                <span>运行出错：{chat.error}</span>
              </p>
            )}
          </div>
        )}

        {chat.steps.length > 0 && (
          <ol aria-label="运行步骤" className="mx-auto flex w-full max-w-3xl flex-wrap items-center gap-2 px-6 pt-4">
            {chat.steps.map((step, index) => (
              <li key={`${step.name}-${index}`} className="chip border border-slate-200 bg-white text-slate-500 shadow-xs">
                <span
                  aria-hidden
                  className={`h-1.5 w-1.5 rounded-full ${
                    step.finished ? 'bg-emerald-500' : 'animate-pulse bg-violet-500'
                  }`}
                />
                {step.name}
                {step.finished ? '' : '…'}
              </li>
            ))}
          </ol>
        )}

        <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-3xl space-y-4 px-6 py-6">
            <MessageList items={chat.items} />
            {chat.confirmations.map((confirmation) => (
              <ConfirmationCard
                key={confirmation.confirmationId}
                confirmation={confirmation}
                threadId={threadId}
                onDecided={(id) => {
                  setChat((s) => ({
                    ...s,
                    confirmations: s.confirmations.filter((c) => c.confirmationId !== id),
                  }))
                  void refreshPending(threadId)
                }}
                onFailed={(_id, message) => {
                  setChat((s) => ({ ...s, error: `确认提交失败：${message}` }))
                }}
              />
            ))}
          </div>
        </div>

        <form
          onSubmit={(e) => {
            e.preventDefault()
            void send()
          }}
          className="shrink-0 border-t border-slate-200 bg-white px-6 pt-4 pb-3"
        >
          <div className="mx-auto w-full max-w-3xl">
            <div className="flex items-end gap-2 rounded-2xl border border-slate-300 bg-white p-2 shadow-xs transition focus-within:border-violet-500 focus-within:ring-2 focus-within:ring-violet-200">
              <textarea
                ref={inputRef}
                aria-label="输入消息"
                rows={1}
                className="max-h-40 min-h-9 flex-1 resize-none bg-transparent px-2 py-1.5 text-sm leading-relaxed text-slate-800 outline-none placeholder:text-slate-400"
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder="输入消息"
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey) {
                    e.preventDefault()
                    void send()
                  }
                }}
              />
              {chat.running ? (
                <button type="button" onClick={() => void handleCancel()} className="btn btn-danger">
                  <StopIcon aria-hidden className="h-4 w-4" />
                  取消
                </button>
              ) : (
                <button type="submit" disabled={input.trim() === ''} className="btn btn-primary">
                  <SendIcon aria-hidden className="h-4 w-4" />
                  发送
                </button>
              )}
            </div>
            <p className="px-1 pt-1.5 text-center text-xs text-slate-400">Enter 发送 · Shift+Enter 换行</p>
          </div>
        </form>
      </section>
    </div>
  )
}
