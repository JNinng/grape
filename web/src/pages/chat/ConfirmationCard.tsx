import { useState } from 'react'
import { AlreadyDecidedError, decideConfirmation, type RememberOption } from '../../agui/confirmation'
import type { ToolConfirmationPayload } from '../../agui/events'

// ConfirmationCardProps 把决定结果交还页面：decided 移除卡片（服务端
// 待确认列表随后由页面兜底查询对账），failed 展示可读错误。
export interface ConfirmationCardProps {
  confirmation: ToolConfirmationPayload
  threadId: string
  onDecided: (confirmationId: string) => void
  onFailed: (confirmationId: string, message: string) => void
}

// argumentsText 把工具入参归一为纯文本摘要：ack 载荷的 arguments 是
// JSON（对象或字符串），一律以文本节点渲染，不作任何 HTML 解释。
function argumentsText(args: unknown): string {
  if (args === undefined || args === null) {
    return ''
  }
  if (typeof args === 'string') {
    return args
  }
  try {
    return JSON.stringify(args, null, 2)
  } catch {
    return String(args)
  }
}

// ConfirmationCard 渲染一次待决定工具确认：工具名、参数摘要、批准/
// 拒绝与 remember 作用域选择。决策经 authFetch 提交 /agui/ack/
// confirmation；「已失效」（404，并发已决定或运行取消）按已处理收敛，
// 不作为错误打断用户。
export function ConfirmationCard({ confirmation, threadId, onDecided, onFailed }: ConfirmationCardProps) {
  const [remember, setRemember] = useState<RememberOption>('none')
  const [deciding, setDeciding] = useState(false)

  const handle = async (approved: boolean) => {
    if (deciding) {
      return
    }
    setDeciding(true)
    try {
      await decideConfirmation({
        threadId,
        confirmationId: confirmation.confirmationId,
        approved,
        remember,
      })
      onDecided(confirmation.confirmationId)
    } catch (err) {
      if (err instanceof AlreadyDecidedError) {
        onDecided(confirmation.confirmationId)
      } else {
        onFailed(confirmation.confirmationId, err instanceof Error ? err.message : '确认提交失败')
      }
    } finally {
      setDeciding(false)
    }
  }

  const args = argumentsText(confirmation.arguments)
  return (
    <section role="group" aria-label={`工具确认：${confirmation.toolName}`} data-testid="confirmation-card">
      <h3>工具请求确认</h3>
      <p>
        工具：<span>{confirmation.toolName}</span>
      </p>
      {args !== '' && (
        <>
          <h4>参数</h4>
          <pre>{args}</pre>
        </>
      )}
      <label>
        记住决定
        <select
          aria-label="记住决定范围"
          value={remember}
          onChange={(e) => setRemember(e.target.value as RememberOption)}
        >
          <option value="none">不记住</option>
          <option value="session">仅本次会话</option>
          <option value="global">全局记住</option>
        </select>
      </label>
      <button type="button" onClick={() => void handle(true)} disabled={deciding}>
        批准
      </button>
      <button type="button" onClick={() => void handle(false)} disabled={deciding}>
        拒绝
      </button>
    </section>
  )
}
