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
    <main className="chat-page">
      <aside aria-label="会话侧栏">
        <h2>会话</h2>
        <button type="button" onClick={handleNewThread}>
          新建会话
        </button>
        {sessionsError !== null && <p role="alert">会话列表加载失败：{sessionsError}</p>}
        <ul aria-label="会话列表">
          {sessions.map((session) => (
            <li key={session.id}>
              <button
                type="button"
                aria-current={session.id === threadId ? 'true' : undefined}
                onClick={() => setThreadId(session.id)}
              >
                <span>{sessionTitle(session)}</span>
                <time dateTime={session.updatedAt}>{formatTime(session.updatedAt)}</time>
              </button>
            </li>
          ))}
        </ul>
      </aside>
      <section aria-label="对话区">
        <header>
          <h1>Grape AI 对话</h1>
          <span>欢迎，{user?.nickname || user?.name || '用户'}</span>
          <button type="button" onClick={() => navigate('/replay')}>
            运维复现
          </button>
          <button type="button" onClick={handleLogout}>
            登出
          </button>
        </header>
        {historyError !== null && <p role="alert">历史加载失败：{historyError}</p>}
        {chat.error !== null && (
          <p role="alert">运行出错：{chat.error}</p>
        )}
        {chat.steps.length > 0 && (
          <ol aria-label="运行步骤">
            {chat.steps.map((step, index) => (
              <li key={`${step.name}-${index}`}>
                {step.name}
                {step.finished ? '' : '…'}
              </li>
            ))}
          </ol>
        )}
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
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void send()
          }}
        >
          <textarea
            aria-label="输入消息"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="输入消息，Enter 发送（Shift+Enter 换行）"
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                void send()
              }
            }}
          />
          <button type="submit" disabled={chat.running || input.trim() === ''}>
            发送
          </button>
          {chat.running && (
            <button type="button" onClick={() => void handleCancel()}>
              取消
            </button>
          )}
        </form>
      </section>
    </main>
  )
}
