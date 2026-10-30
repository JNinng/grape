import { useState } from 'react'
import { ShieldCheckIcon } from 'lucide-react'
import { AlreadyDecidedError, decideConfirmation, type RememberOption } from '../../agui/confirmation'
import type { ToolConfirmationPayload } from '../../agui/events'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

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
      className="overflow-hidden rounded-xl border border-brand-soft-border bg-card text-card-foreground shadow-sm"
    >
      <header className="flex items-center gap-2 border-b border-brand-soft-border bg-brand-soft px-4 py-3">
        <ShieldCheckIcon aria-hidden className="size-4 shrink-0 text-primary" />
        <h3 className="text-sm font-semibold text-brand-text">工具请求确认</h3>
      </header>
      <div className="space-y-3 px-4 py-3.5">
        <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
          工具：
          <span className="inline-flex items-center rounded-full bg-brand-soft px-2 py-0.5 font-mono text-xs font-medium text-brand-text">
            {confirmation.toolName}
          </span>
        </p>
        {args !== '' && (
          <>
            <h4 className="text-xs font-medium text-muted-foreground">参数</h4>
            <pre className="code-block">{args}</pre>
          </>
        )}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 pt-1">
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            记住决定
            <Select value={remember} onValueChange={(value) => setRemember(value as RememberOption)}>
              <SelectTrigger size="sm" aria-label="记住决定范围" className="w-28 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">不记住</SelectItem>
                <SelectItem value="session">仅本次会话</SelectItem>
                <SelectItem value="global">全局记住</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <Button type="button" size="sm" onClick={() => void handle(true)} disabled={deciding}>
              批准
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void handle(false)}
              disabled={deciding}
              className="text-destructive hover:bg-destructive-soft hover:text-destructive"
            >
              拒绝
            </Button>
          </div>
        </div>
      </div>
    </section>
  )
}
