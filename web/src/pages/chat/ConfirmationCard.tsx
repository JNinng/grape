import { useState } from 'react'
import { AlreadyDecidedError, decideConfirmation, type RememberOption } from '../../agui/confirmation'
import type { ToolConfirmationPayload } from '../../agui/events'
import { ShieldIcon } from '../../ui/icons'

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
    <section
      role="group"
      aria-label={`工具确认：${confirmation.toolName}`}
      data-testid="confirmation-card"
      className="card overflow-hidden border-violet-200"
    >
      <header className="flex items-center gap-2 border-b border-violet-100 bg-violet-50 px-4 py-3">
        <ShieldIcon aria-hidden className="h-4 w-4 shrink-0 text-violet-500" />
        <h3 className="text-sm font-semibold text-violet-900">工具请求确认</h3>
      </header>
      <div className="space-y-3 px-4 py-3.5">
        <p className="flex items-center gap-1.5 text-sm text-slate-500">
          工具：
          <span className="chip bg-violet-50 font-mono text-violet-700">{confirmation.toolName}</span>
        </p>
        {args !== '' && (
          <>
            <h4 className="text-xs font-medium text-slate-400">参数</h4>
            <pre className="code-block">{args}</pre>
          </>
        )}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 pt-1">
          <label className="flex items-center gap-2 text-sm text-slate-500">
            记住决定
            <select
              aria-label="记住决定范围"
              className="field w-auto py-1.5 text-xs"
              value={remember}
              onChange={(e) => setRemember(e.target.value as RememberOption)}
            >
              <option value="none">不记住</option>
              <option value="session">仅本次会话</option>
              <option value="global">全局记住</option>
            </select>
          </label>
          <div className="ml-auto flex items-center gap-2">
            <button type="button" onClick={() => void handle(true)} disabled={deciding} className="btn btn-primary btn-sm">
              批准
            </button>
            <button type="button" onClick={() => void handle(false)} disabled={deciding} className="btn btn-danger btn-sm">
              拒绝
            </button>
          </div>
        </div>
      </div>
    </section>
  )
}
