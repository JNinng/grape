import type { ChatItem, MessageItem, ToolCallItem } from '../../agui/events'

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
// 左对齐白底卡片；角色标签置于气泡上方弱化呈现。
function MessageBubble({ item }: { item: MessageItem }) {
  const isUser = item.role === 'user'
  return (
    <article data-role={item.role} className={isUser ? 'flex flex-col items-end' : 'flex flex-col items-start'}>
      <span className="mb-1 px-1 text-xs text-slate-400">{roleLabel(item.role)}</span>
      <p
        className={`max-w-[85%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed wrap-break-word whitespace-pre-wrap ${
          isUser
            ? 'rounded-br-sm bg-violet-600 text-white shadow-xs'
            : 'rounded-bl-sm border border-slate-200 bg-white text-slate-800 shadow-xs'
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
  const state = item.result !== null ? '结果' : item.done ? '已结束' : '运行中'
  const settled = item.result !== null || item.done
  return (
    <details className="card group w-full overflow-hidden" data-tool-state={item.result !== null ? 'result' : 'running'}>
      <summary className="flex cursor-pointer list-none items-center gap-2 px-3.5 py-2.5 text-sm text-slate-500 transition-colors select-none hover:bg-slate-50 [&::-webkit-details-marker]:hidden">
        <span
          aria-hidden
          className={`h-1.5 w-1.5 shrink-0 rounded-full ${settled ? 'bg-emerald-500' : 'animate-pulse bg-amber-500'}`}
        />
        {`工具 ${item.name || '(未知)'} · ${state}`}
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
      <div className="space-y-2 border-t border-slate-100 px-3.5 py-3">
        {item.argsText !== '' && (
          <>
            <h4 className="text-xs font-medium text-slate-400">参数</h4>
            <pre className="code-block">{item.argsText}</pre>
          </>
        )}
        {item.result !== null && (
          <>
            <h4 className="text-xs font-medium text-slate-400">结果</h4>
            <pre className="code-block">{item.result}</pre>
          </>
        )}
      </div>
    </details>
  )
}

// MessageList 按到达顺序渲染对话项：文本消息与工具调用交错，保持流内
// 时序。空列表给出占位文案。
export function MessageList({ items }: { items: ChatItem[] }) {
  return (
    <div role="log" aria-label="对话消息" className="space-y-4">
      {items.length === 0 && (
        <div className="empty-note">
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
