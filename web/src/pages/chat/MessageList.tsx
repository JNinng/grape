import { useState } from 'react'
import { ChevronDownIcon } from 'lucide-react'
import type { ChatItem, MessageItem, ToolCallItem } from '../../agui/events'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'

// sanitize 口径：模型文本与工具参数/结果一律不可信，全部以 React 文本
// 节点渲染（等价于 textContent），不引入 innerHTML / markdown 注入路径；
// 将来若引入 markdown 渲染，必须先过 sanitizer（如 dompurify）并另行
// 评审。
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

// MessageBubble 单条文本消息：用户消息右对齐品牌紫气泡，助手/系统消息
// 左对齐卡片气泡；角色标签置于气泡上方弱化呈现。
function MessageBubble({ item }: { item: MessageItem }) {
  const isUser = item.role === 'user'
  return (
    <article data-role={item.role} className={isUser ? 'flex flex-col items-end' : 'flex flex-col items-start'}>
      <span className="mb-1 px-1 text-xs text-muted-foreground">{roleLabel(item.role)}</span>
      <p
        className={`max-w-[85%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed wrap-break-word whitespace-pre-wrap ${
          isUser
            ? 'rounded-br-sm bg-primary text-primary-foreground shadow-xs'
            : 'rounded-bl-sm border bg-card text-card-foreground shadow-xs'
        }`}
      >
        {item.content}
      </p>
    </article>
  )
}

// ToolCallEntry 折叠展示一次工具调用：摘要行含工具名与状态；参数与
// 结果仅在展开后可见。摘要行的文案保持单一直接文本节点（工具名与状态
// 一体呈现），状态点与展开箭头为无障碍隐藏的装饰。
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
              <h4 className="text-xs font-medium text-muted-foreground">参数</h4>
              <pre className="code-block">{item.argsText}</pre>
            </>
          )}
          {item.result !== null && (
            <>
              <h4 className="text-xs font-medium text-muted-foreground">结果</h4>
              <pre className="code-block">{item.result}</pre>
            </>
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

// MessageList 按到达顺序渲染对话项：文本消息与工具调用交错，保持流内
// 时序。空列表给出占位文案。
export function MessageList({ items }: { items: ChatItem[] }) {
  return (
    <div role="log" aria-label="对话消息" className="space-y-4">
      {items.length === 0 && (
        <div className="rounded-xl border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
          <p>暂无消息，发送第一条消息开始对话。</p>
        </div>
      )}
      {items.map((item) =>
        item.kind === 'message' ? (
          <MessageBubble key={item.id} item={item} />
        ) : (
          <ToolCallEntry key={item.toolCallId} item={item} />
        ),
      )}
    </div>
  )
}
