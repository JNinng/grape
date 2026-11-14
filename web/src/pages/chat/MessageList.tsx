import { useRef, useState } from 'react'
import { ChevronDownIcon, SparklesIcon } from 'lucide-react'
import { groupChatUnits, type ChatItem, type MessageItem, type ReasoningItem, type ToolCallItem } from '../../agui/events'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Spinner } from '@/components/ui/spinner'
import { CopyTextButton } from '@/components/copy-text-button'
import { MarkdownContent, renderedPlainTextOf } from '@/components/markdown'
import { AnswerCopyMenu } from './AnswerCopyMenu'

// sanitize 口径：模型文本与工具参数/结果一律不可信。assistant 正文与思考
// 内容经 components/markdown 的 Streamdown 管线渲染（不解析原始 HTML，
// sanitize + 链接/图片加固，外链图片不渲染）；用户消息与工具参数/结果仍
// 以 React 文本节点渲染（等价于 textContent），不走 HTML 注入路径。
function roleLabel(role: MessageItem['role']): string {
  switch (role) {
    case 'user':
      return '你'
    case 'assistant':
      return '助手'
    default:
      return '系统'
  }
}

// MessageBubble 是文本气泡本体（不含角色标签与对齐外壳）：用户消息为
// 品牌紫气泡，系统消息为卡片气泡；只有用户/系统消息走这里（原文直显，
// 不做 Markdown 解析）。流式输出中在文末追加闪烁竖线，表示仍在增量。
function MessageBubble({ item }: { item: MessageItem }) {
  const isUser = item.role === 'user'
  return (
    <p
      data-streaming={item.streaming ? 'true' : undefined}
      className={`max-w-[85%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed wrap-break-word whitespace-pre-wrap ${
        isUser
          ? 'rounded-br-sm bg-primary text-primary-foreground shadow-xs'
          : 'rounded-bl-sm border bg-card text-card-foreground shadow-xs'
      }`}
    >
      {item.content}
      {item.streaming && (
        <span aria-hidden className="ml-0.5 inline-block h-4 w-1 animate-pulse rounded-sm bg-current align-text-bottom" />
      )}
    </p>
  )
}

// MessageTurn 是带角色标签的单项消息（用户 / 系统）：标签在气泡上方，
// 用户消息右对齐。data-message-id 供提问导航按 id 定位滚动；scroll-mt-6
// 让 scrollIntoView 停在气泡上沿时留出呼吸空间。用户提问下方右侧有
// 「复制提问」图标，默认透明，指针进入该条消息或键盘聚焦后显示。
function MessageTurn({ item }: { item: MessageItem }) {
  const isUser = item.role === 'user'
  return (
    <article
      data-role={item.role}
      data-message-id={item.id}
      className={`scroll-mt-6 ${isUser ? 'group/user flex flex-col items-end' : 'flex flex-col items-start'}`}
    >
      <span className="mb-1 px-1 text-xs text-muted-foreground">{roleLabel(item.role)}</span>
      <MessageBubble item={item} />
      {isUser && item.content !== '' && (
        <div className="mt-1 px-1 opacity-0 transition-opacity focus-within:opacity-100 group-hover/user:opacity-100">
          <CopyTextButton text={item.content} label="复制提问" />
        </div>
      )}
    </article>
  )
}

// AssistantTurn 是助手回合：思考框与回答共用「助手」标签，思考在上、
// 回答在下，宽度收敛到气泡列（85%）。正文与思考都经 Markdown 渲染；
// 回答底部左侧是复制工具栏：「复制全文」取渲染后的整体文本（innerText
// 口径，隐藏控件不计入），「复制原文」复制 Markdown 源文本。空内容流式
// 阶段保留闪烁光标占位。
function AssistantTurn({
  reasoning,
  message,
}: {
  reasoning: ReasoningItem[]
  message?: MessageItem
}) {
  const answerRef = useRef<HTMLDivElement | null>(null)
  return (
    <article data-role="assistant" className="flex flex-col items-start">
      <span className="mb-1 flex items-center gap-0.5 px-1 text-xs text-muted-foreground">
        {roleLabel('assistant')}
      </span>
      <div className="flex w-full max-w-[85%] flex-col items-start gap-2">
        {reasoning.map((item) => (
          <ReasoningEntry key={item.id} item={item} />
        ))}
        {message !== undefined && (
          <>
            <div
              ref={answerRef}
              data-streaming={message.streaming ? 'true' : undefined}
              className="w-fit max-w-full rounded-2xl rounded-bl-sm border bg-card px-4 py-2.5 text-sm leading-relaxed text-card-foreground shadow-xs"
            >
              {message.content === '' && message.streaming ? (
                <span aria-hidden className="inline-block h-4 w-1 animate-pulse rounded-sm bg-current align-text-bottom" />
              ) : (
                <MarkdownContent text={message.content} streaming={message.streaming ?? false} />
              )}
            </div>
            {message.content !== '' && (
              <div role="toolbar" aria-label="回答工具" className="flex items-center gap-1 px-1">
                <AnswerCopyMenu
                  whole={() => renderedPlainTextOf(answerRef.current)}
                  source={() => message.content}
                />
              </div>
            )}
          </>
        )}
      </div>
    </article>
  )
}

// ReasoningEntry 折叠展示一段模型思考内容：摘要行在思考中显示载入态
// （旋转图标 +「正在思考…」），结束后退化为「思考内容」；正文默认收起，
// 点按展开，Markdown 渲染并可复制原文。复制按钮位于展开箭头左侧、默认
// 透明，指针进入思考框（或键盘聚焦）后才显现；空内容且已收口的段落不
// 渲染空块。
function ReasoningEntry({ item }: { item: ReasoningItem }) {
  const [open, setOpen] = useState(false)
  if (!item.streaming && item.content === '') {
    return null
  }
  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      data-reasoning-state={item.streaming ? 'streaming' : 'done'}
      className="group/reasoning w-full overflow-hidden rounded-xl border border-dashed bg-card shadow-xs"
    >
      <div className="flex items-center gap-0.5 pr-1.5">
        <CollapsibleTrigger className="group flex min-w-0 flex-1 cursor-pointer items-center gap-2 px-3.5 py-2.5 text-left text-sm text-muted-foreground transition-colors select-none hover:bg-soft">
          {item.streaming ? (
            <Spinner aria-hidden className="size-3.5 shrink-0" />
          ) : (
            <SparklesIcon aria-hidden className="size-3.5 shrink-0" />
          )}
          {item.streaming ? '正在思考…' : '思考内容'}
        </CollapsibleTrigger>
        {item.content !== '' && (
          <span className="opacity-0 transition-opacity focus-within:opacity-100 group-hover/reasoning:opacity-100">
            <CopyTextButton text={item.content} />
          </span>
        )}
        <button
          type="button"
          aria-label={open ? '收起思考内容' : '展开思考内容'}
          aria-expanded={open}
          onClick={() => setOpen(!open)}
          className="flex size-7 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-soft hover:text-foreground"
        >
          <ChevronDownIcon
            aria-hidden
            className={`size-4 transition-transform duration-200 ${open ? '[transform:rotate(180deg)]' : ''}`}
          />
        </button>
      </div>
      <CollapsibleContent>
        <div className="border-t px-3.5 py-3 text-sm leading-relaxed text-muted-foreground">
          <MarkdownContent text={item.content} streaming={item.streaming} />
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

// ToolCallEntry 折叠展示一次工具调用：摘要行含工具名与状态；参数与
// 结果仅在展开后可见，两段标题右侧各有复制图标（复制对应原文）。摘要行
// 的文案保持单一直接文本节点（工具名与状态一体呈现），状态点与展开箭头
// 为无障碍隐藏的装饰。
function ToolCallEntry({ item }: { item: ToolCallItem }) {
  const [open, setOpen] = useState(false)
  const state = item.result !== null ? '结果' : item.done ? '已结束' : '运行中'
  const settled = item.result !== null || item.done
  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      data-tool-state={item.result !== null ? 'result' : 'running'}
      className="w-full overflow-hidden rounded-xl border bg-card shadow-sm"
    >
      <CollapsibleTrigger className="group flex w-full cursor-pointer items-center gap-2 px-3.5 py-2.5 text-left text-sm text-muted-foreground transition-colors select-none hover:bg-soft">
        <span
          aria-hidden
          className={`size-1.5 shrink-0 rounded-full ${settled ? 'bg-emerald-500' : 'animate-pulse bg-amber-500'}`}
        />
        {`工具 ${item.name || '(未知)'} · ${state}`}
        <ChevronDownIcon
          aria-hidden
          className="ml-auto size-4 shrink-0 text-muted-foreground transition-transform duration-200 group-data-[state=open]:rotate-180"
        />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="space-y-2 border-t px-3.5 py-3">
          {item.argsText !== '' && (
            <>
              <div className="flex items-center justify-between gap-2">
                <h4 className="text-xs font-medium text-muted-foreground">参数</h4>
                <CopyTextButton text={item.argsText} label="复制参数" />
              </div>
              <pre className="code-block">{item.argsText}</pre>
            </>
          )}
          {item.result !== null && (
            <>
              <div className="flex items-center justify-between gap-2">
                <h4 className="text-xs font-medium text-muted-foreground">结果</h4>
                <CopyTextButton text={item.result} label="复制结果" />
              </div>
              <pre className="code-block">{item.result}</pre>
            </>
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

// MessageList 按到达顺序渲染对话项：用户/系统消息、助手回合（思考框 +
// 回答）与工具调用交错，保持流内时序；助手回合共用「助手」标签，思考框
// 落在标签之下。运行中且无任何流式项时补一行载入提示——模型在等首个
// token 或内部处理阶段，界面仍需表达「正在思考」；工具执行中由工具卡
// 自身的运行态承担提示，不重复呈现。空列表给出占位文案。
export function MessageList({ items, running = false }: { items: ChatItem[]; running?: boolean }) {
  const streaming = items.some(
    (item) => (item.kind === 'message' && item.streaming) || (item.kind === 'reasoning' && item.streaming),
  )
  const toolRunning = items.some((item) => item.kind === 'tool' && !item.done)
  return (
    <div role="log" aria-label="对话消息" className="space-y-4">
      {items.length === 0 && !running && (
        <div className="rounded-xl border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
          <p>暂无消息，发送第一条消息开始对话。</p>
        </div>
      )}
      {groupChatUnits(items).map((unit) =>
        unit.kind === 'item' ? (
          unit.item.kind === 'tool' ? (
            <ToolCallEntry key={unit.item.toolCallId} item={unit.item} />
          ) : (
            <MessageTurn key={unit.item.id} item={unit.item} />
          )
        ) : (
          <AssistantTurn
            key={unit.message?.id ?? unit.reasoning[0]?.id}
            reasoning={unit.reasoning}
            message={unit.message}
          />
        ),
      )}
      {running && !streaming && !toolRunning && (
        <div role="status" aria-live="polite" className="flex items-center gap-2 text-sm text-muted-foreground">
          <Spinner aria-hidden className="size-3.5 shrink-0" />
          正在思考…
        </div>
      )}
    </div>
  )
}
