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

// ToolCallEntry 折叠展示一次工具调用：摘要行含工具名与状态；参数与
// 结果仅在展开后可见。
function ToolCallEntry({ item }: { item: ToolCallItem }) {
  const state = item.result !== null ? '结果' : item.done ? '已结束' : '运行中'
  return (
    <details className="chat-tool" data-tool-state={item.result !== null ? 'result' : 'running'}>
      <summary>
        工具 {item.name || '(未知)'} · {state}
      </summary>
      {item.argsText !== '' && (
        <>
          <h4>参数</h4>
          <pre>{item.argsText}</pre>
        </>
      )}
      {item.result !== null && (
        <>
          <h4>结果</h4>
          <pre>{item.result}</pre>
        </>
      )}
    </details>
  )
}

// MessageList 按到达顺序渲染对话项：文本消息与工具调用交错，保持流内
// 时序。空列表给出占位文案。
export function MessageList({ items }: { items: ChatItem[] }) {
  return (
    <div role="log" aria-label="对话消息">
      {items.length === 0 && <p>暂无消息，发送第一条消息开始对话。</p>}
      {items.map((item) =>
        item.kind === 'message' ? (
          <article key={item.id} data-role={item.role}>
            <span>{roleLabel(item.role)}</span>
            <p>{item.content}</p>
          </article>
        ) : (
          <ToolCallEntry key={item.toolCallId} item={item} />
        ),
      )}
    </div>
  )
}
