import { useCallback, useEffect, useRef, useState } from 'react'
import type { Event, Message } from '@ag-ui/client'
import { Link, useNavigate } from 'react-router'
import {
  CircleAlertIcon,
  FlaskConicalIcon,
  LogOutIcon,
  MoreHorizontalIcon,
  PlusIcon,
  SendIcon,
  SettingsIcon,
  SquareIcon,
} from 'lucide-react'
import { fetchAgentSessions, type AgentSessionMeta } from '../api/agentSessions'
import { logout } from '../api/session'
import { createChatAgent } from '../agui/agent'
import { cancelRun } from '../agui/cancel'
import { fetchPendingConfirmations } from '../agui/confirmation'
import {
  applyChatEvent,
  initialChatState,
  type ChatState,
  type MessageItem,
} from '../agui/events'
import { fetchHistorySnapshot } from '../agui/history'
import { newId } from '../agui/ids'
import {
  buildThinkingForwardedProps,
  loadThinkingPreference,
  saveThinkingPreference,
  type ThinkingLevel,
} from '../agui/thinking'
import { getUser } from '../auth/store'
import { GrapeMark } from '../ui/icons'
import { ConfirmationCard } from './chat/ConfirmationCard'
import { MessageList } from './chat/MessageList'
import { QuestionNavigator } from './chat/QuestionNavigator'
import { ThinkingPicker } from './chat/ThinkingPicker'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { AccentPicker } from '@/components/accent-picker'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { ModeToggle } from '@/components/mode-toggle'
import { Separator } from '@/components/ui/separator'
import { Textarea } from '@/components/ui/textarea'

// 待确认列表的低频兜底轮询周期：覆盖页面加载与运行间隙之外的漂移
// （如另一端决定、恢复路径孤儿确认）。运行结束与切换会话会立即查询，
// 不依赖该定时器。
const PENDING_POLL_INTERVAL_MS = 30_000

// 决定后历史收敛的重拉节奏：确认沿 ack 落定后，续跑产物要等服务端写
// 回快照才能拉到；按先密后疏的间隔重拉，相邻两轮一致（快照已稳定）
// 或达轮次上限即停。
const HISTORY_CONVERGENCE_DELAYS_MS = [1_000, 2_000, 2_000, 3_000, 3_000]

// 贴底判定的容差：滚动位置距底部不超过该像素即视为「停在底部」，流式
// 增量继续跟随；用户向上翻阅后不再抢滚动位置。
const SCROLL_BOTTOM_THRESHOLD_PX = 32

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
  const [logoutOpen, setLogoutOpen] = useState(false)
  // thinkingLevel 是思考强度的本地偏好：每次运行经 forwardedProps 下发，
  // 后端据此覆盖本次运行的模型生成配置。
  const [thinkingLevel, setThinkingLevel] = useState(loadThinkingPreference)

  const changeThinkingLevel = (level: ThinkingLevel) => {
    setThinkingLevel(level)
    saveThinkingPreference(level)
  }

  // agentRef/runIdRef 持有进行中的运行；threadIdRef 供异步回调判定
  // 是否已切走会话（陈旧运行的回包不得写入新会话的状态）。
  const agentRef = useRef<ReturnType<typeof createChatAgent> | null>(null)
  const runIdRef = useRef<string | null>(null)
  const threadIdRef = useRef(threadId)
  threadIdRef.current = threadId
  // pollTimerRef 持有决定后历史收敛轮询的当前定时器：重复决定重置节奏，
  // 会话切换或卸载即时作废。
  const pollTimerRef = useRef<number | null>(null)
  // scrollRef 承载消息滚动区；inputRef 承载输入框（自适应增高）。
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const inputRef = useRef<HTMLTextAreaElement | null>(null)
  // stickToBottomRef 记录用户是否停在底部：只有停底时才跟随流式增量，
  // 向上翻阅历史后不再被强行拉回；滚回底部或发送新消息即恢复跟随。
  const stickToBottomRef = useRef(true)

  // 消息 / 步骤 / 确认卡变化时贴底滚动：仅当用户停在底部时执行，避免
  // 阅读历史时被增量输出打断。
  useEffect(() => {
    const el = scrollRef.current
    if (el !== null && stickToBottomRef.current) {
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

  const clearHistoryPoll = useCallback(() => {
    if (pollTimerRef.current !== null) {
      window.clearTimeout(pollTimerRef.current)
      pollTimerRef.current = null
    }
  }, [])

  // convergeHistoryAfterDecision 在一次决定（含 404 失效）后按先密后疏
  // 重拉历史快照：相邻两轮一致或达上限即停；会话切走停止、运行中跳过、
  // 单轮失败静默。
  const convergeHistoryAfterDecision = useCallback(
    (tid: string) => {
      clearHistoryPoll()
      let last: string | null = null
      let round = 0
      const schedule = () => {
        pollTimerRef.current = window.setTimeout(pull, HISTORY_CONVERGENCE_DELAYS_MS[round])
        round += 1
      }
      const pull = () => {
        pollTimerRef.current = null
        if (threadIdRef.current !== tid) {
          return
        }
        if (runIdRef.current !== null) {
          if (round < HISTORY_CONVERGENCE_DELAYS_MS.length) {
            schedule()
          }
          return
        }
        void fetchHistorySnapshot(tid)
          .then((items) => {
            if (threadIdRef.current !== tid) {
              return
            }
            const serialized = JSON.stringify(items)
            const stable = last !== null && last === serialized
            last = serialized
            setChat((s) => ({ ...s, items }))
            if (!stable && round < HISTORY_CONVERGENCE_DELAYS_MS.length) {
              schedule()
            }
          })
          .catch(() => {
            if (round < HISTORY_CONVERGENCE_DELAYS_MS.length) {
              schedule()
            }
          })
      }
      schedule()
    },
    [clearHistoryPoll],
  )

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

  // 切换会话（含新建）：终止进行中的运行与决定后收敛轮询，重置对话态
  // 并拉一次历史快照与待确认列表。历史不跟流（上游 follow 同毫秒缺陷，
  // 快照即回放）。
  useEffect(() => {
    agentRef.current?.abortRun()
    agentRef.current = null
    runIdRef.current = null
    clearHistoryPoll()
    // 换会话回到「跟随底部」：新会话的历史与后续输出都应贴底呈现。
    stickToBottomRef.current = true
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
      clearHistoryPoll()
    }
  }, [threadId, refreshPending, clearHistoryPoll])

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
    // 发送即恢复跟随：新消息与其输出应贴底可见，即使此前在翻阅历史。
    stickToBottomRef.current = true
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
      // 思考强度随运行下发：本次运行内的全部模型调用按同一档位覆盖，
      // 切换档位只影响后续运行（在途运行保持启动时的取值）。
      await agent.runAgent({ runId, forwardedProps: buildThinkingForwardedProps(thinkingLevel) }, {
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

  // jumpToQuestion 按用户消息 id 定位并平滑滚到该提问上沿：滚动由
  // scrollIntoView 在滚动区内完成，滚动事件随后把贴底跟随判定为 false，
  // 后续流式增量不会把读者拉回底部；系统开启减弱动效时退化为即时定位。
  const jumpToQuestion = (messageId: string) => {
    const container = scrollRef.current
    if (container === null) {
      return
    }
    const target = container.querySelector<HTMLElement>(`[data-message-id="${messageId}"]`)
    if (target === null) {
      return
    }
    const reduceMotion =
      typeof window.matchMedia === 'function' &&
      window.matchMedia('(prefers-reduced-motion: reduce)').matches
    target.scrollIntoView({ block: 'start', behavior: reduceMotion ? 'auto' : 'smooth' })
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
    <div className="flex h-full overflow-hidden bg-background">
      <aside aria-label="会话侧栏" className="flex w-72 shrink-0 flex-col border-r border-sidebar-border bg-sidebar">
        <div className="flex items-center gap-2.5 px-5 pt-5 pb-4">
          <GrapeMark className="h-9 w-9" />
          <div className="min-w-0 flex-1">
            <h2 className="text-sm font-semibold text-foreground">Grape</h2>
            <p className="truncate text-xs text-muted-foreground">AI 对话工作台</p>
          </div>
        </div>
        <div className="px-4 pb-3">
          <Button type="button" onClick={handleNewThread} className="w-full">
            <PlusIcon aria-hidden className="size-4" />
            新建会话
          </Button>
        </div>
        {sessionsError !== null && (
          <Alert variant="destructive" className="mx-4 mb-3">
            <CircleAlertIcon aria-hidden className="mt-0.5 shrink-0" />
            <AlertDescription>
              <span>会话列表加载失败：{sessionsError}</span>
            </AlertDescription>
          </Alert>
        )}
        {/* 滚动条常显（thin 轨道）以固定占位：内容从不满到溢出时不再因
            滚动条出现/消失而挤动消息宽度。 */}
        <nav className="min-h-0 flex-1 overflow-y-scroll px-3 pb-4">
          <ul aria-label="会话列表" className="space-y-1">
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
                      {sessionTitle(session)}
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
        {/* 系统设置固定侧栏底部，与设置页「返回工作台」同位置同款式；
            运维复现/登出收进同行的更多菜单，系统设置链接本身仍直达设置页。 */}
        <div className="shrink-0 border-t border-sidebar-border p-3">
          <div className="flex items-center gap-1">
            <Link
              to="/settings"
              className="flex min-w-0 flex-1 items-center gap-2 rounded-xl px-3 py-2 text-sm font-medium text-foreground transition-colors select-none hover:bg-sidebar-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-sidebar-ring"
            >
              <SettingsIcon aria-hidden className="size-4 shrink-0" />
              系统设置
            </Link>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button type="button" variant="ghost" size="icon-sm" aria-label="更多操作">
                  <MoreHorizontalIcon aria-hidden className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent side="top" align="end" className="w-40">
                <DropdownMenuItem onSelect={() => navigate('/replay')}>
                  <FlaskConicalIcon aria-hidden className="size-4" />
                  运维复现
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onSelect={() => setLogoutOpen(true)}>
                  <LogOutIcon aria-hidden className="size-4" />
                  登出
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
          {/* 登出二次确认：撤销会话不可撤回，误触菜单项的代价高。 */}
          <AlertDialog open={logoutOpen} onOpenChange={setLogoutOpen}>
            <AlertDialogContent size="sm">
              <AlertDialogHeader>
                <AlertDialogTitle>确认登出？</AlertDialogTitle>
                <AlertDialogDescription>
                  登出将撤销当前会话并返回登录页，未发送的输入将丢失。
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>取消</AlertDialogCancel>
                <AlertDialogAction onClick={handleLogout}>登出</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </aside>

      <section aria-label="对话区" className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center justify-between gap-4 border-b bg-card px-6">
          <h1 className="truncate text-base font-semibold text-foreground">Grape AI 对话</h1>
          <div className="flex shrink-0 items-center gap-1.5">
            <span className="hidden text-sm text-muted-foreground lg:inline">
              欢迎，{user?.nickname || user?.name || '用户'}
            </span>
            <Separator orientation="vertical" className="hidden h-4 lg:block" />
            <AccentPicker />
            <ModeToggle />
          </div>
        </header>

        {(historyError !== null || chat.error !== null) && (
          <div className="mx-auto w-full max-w-3xl space-y-2 px-6 pt-4">
            {historyError !== null && (
              <Alert variant="destructive">
                <CircleAlertIcon aria-hidden className="mt-0.5 shrink-0" />
                <AlertDescription>
                  <span>历史加载失败：{historyError}</span>
                </AlertDescription>
              </Alert>
            )}
            {chat.error !== null && (
              <Alert variant="destructive">
                <CircleAlertIcon aria-hidden className="mt-0.5 shrink-0" />
                <AlertDescription>
                  <span>运行出错：{chat.error}</span>
                </AlertDescription>
              </Alert>
            )}
          </div>
        )}

        {chat.steps.length > 0 && (
          <ol aria-label="运行步骤" className="mx-auto flex w-full max-w-3xl flex-wrap items-center gap-2 px-6 pt-4">
            {chat.steps.map((step, index) => (
              <li key={`${step.name}-${index}`}>
                <Badge variant="outline" className="gap-1.5 shadow-xs">
                  <span
                    aria-hidden
                    className={`size-1.5 rounded-full ${
                      step.finished ? 'bg-emerald-500' : 'animate-pulse bg-primary'
                    }`}
                  />
                  {step.name}
                  {step.finished ? '' : '…'}
                </Badge>
              </li>
            ))}
          </ol>
        )}

        {/* 滚动区与提问导航同处一个定位容器：导航固定在右侧中部，不随
            消息滚动。overflow-y-scroll + 全局 thin 滚动条：轨道常驻，避免
            内容越过视口高度时滚动条出现挤动消息宽度。onScroll 只更新
            「是否停在底部」，供贴底跟随判定。 */}
        <div className="relative flex min-h-0 flex-1 flex-col">
          <div
            ref={scrollRef}
            data-testid="chat-scroll"
            onScroll={(event) => {
              const el = event.currentTarget
              stickToBottomRef.current =
                el.scrollHeight - el.scrollTop - el.clientHeight <= SCROLL_BOTTOM_THRESHOLD_PX
            }}
            className="min-h-0 flex-1 overflow-y-scroll"
          >
            <div className="mx-auto w-full max-w-3xl space-y-4 px-6 py-6">
              {/* 待确认时运行挂起等人决定，不呈现「正在思考」：载入提示只
                  表示模型侧仍在处理。 */}
              <MessageList items={chat.items} running={chat.running && chat.confirmations.length === 0} />
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
                    // 决定成功与 404 已失效共用此回调：卡片移除后重拉历史
                    // 快照，收敛续跑产物（另一端决定同路径生效）。
                    convergeHistoryAfterDecision(threadId)
                  }}
                  onFailed={(_id, message) => {
                    setChat((s) => ({ ...s, error: `确认提交失败：${message}` }))
                  }}
                />
              ))}
            </div>
          </div>
          <QuestionNavigator items={chat.items} onJump={jumpToQuestion} />
        </div>

        <form
          onSubmit={(e) => {
            e.preventDefault()
            void send()
          }}
          className="shrink-0 border-t bg-card px-6 pt-4 pb-3"
        >
          <div className="mx-auto w-full max-w-3xl">
            <div className="flex items-end gap-2 rounded-2xl border border-input bg-card p-2 shadow-xs transition focus-within:border-ring focus-within:ring-2 focus-within:ring-ring">
              {/* 思考强度：触发器只显示图标与档位，悬浮弹出档位单选；在途
                  运行保持启动时的取值，运行时禁用避免误解为即时生效。 */}
              <ThinkingPicker
                value={thinkingLevel}
                onChange={changeThinkingLevel}
                disabled={chat.running}
              />
              <Textarea
                ref={inputRef}
                aria-label="输入消息"
                rows={1}
                className="max-h-40 min-h-9 flex-1 resize-none border-0 bg-transparent px-2 py-1.5 text-sm leading-relaxed shadow-none focus-visible:border-0 focus-visible:ring-0"
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
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => void handleCancel()}
                  className="text-destructive hover:bg-destructive-soft hover:text-destructive"
                >
                  <SquareIcon aria-hidden className="size-4" />
                  取消
                </Button>
              ) : (
                <Button type="submit" disabled={input.trim() === ''}>
                  <SendIcon aria-hidden className="size-4" />
                  发送
                </Button>
              )}
            </div>
            <p className="px-1 pt-1.5 text-center text-xs text-muted-foreground">Enter 发送 · Shift+Enter 换行</p>
          </div>
        </form>
      </section>
    </div>
  )
}
