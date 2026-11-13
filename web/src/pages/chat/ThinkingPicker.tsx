import { useEffect, useRef, useState } from 'react'
import { BrainIcon, CheckIcon } from 'lucide-react'
import {
  Popover,
  PopoverAnchor,
  PopoverContent,
} from '@/components/ui/popover'
import {
  THINKING_LEVELS,
  thinkingLevelLabel,
  type ThinkingLevel,
} from '../../agui/thinking'

// 关闭延迟：指针在触发器与弹框之间移动时留出缓冲，避免闪烁。
const CLOSE_DELAY_MS = 120

// ThinkingPicker 是输入框左侧的思考强度控件：触发器只呈现图标与当前档位
// 文字，悬浮（或聚焦后点开）弹出档位单选；选择即生效并关闭弹框。运行中
// 禁用，避免误解为对在途运行即时生效。
export function ThinkingPicker({
  value,
  onChange,
  disabled = false,
}: {
  value: ThinkingLevel
  onChange: (level: ThinkingLevel) => void
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const closeTimer = useRef<number | null>(null)

  const cancelClose = () => {
    if (closeTimer.current !== null) {
      window.clearTimeout(closeTimer.current)
      closeTimer.current = null
    }
  }

  const scheduleClose = () => {
    cancelClose()
    closeTimer.current = window.setTimeout(() => setOpen(false), CLOSE_DELAY_MS)
  }

  useEffect(() => {
    return () => {
      if (closeTimer.current !== null) {
        window.clearTimeout(closeTimer.current)
      }
    }
  }, [])

  const choose = (level: ThinkingLevel) => {
    onChange(level)
    cancelClose()
    setOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      {/* 用 Anchor 而非 Trigger：悬浮与点击都只负责「打开」，不做 Radix 的
          开合翻转——否则指针移到按钮上的那次 enter 会先打开、同一次点击
          再被判成第二次点击而关掉。 */}
      <PopoverAnchor asChild>
        <button
          type="button"
          disabled={disabled}
          aria-haspopup="dialog"
          aria-expanded={open}
          aria-label={`思考强度：${thinkingLevelLabel(value)}`}
          onMouseEnter={() => {
            if (!disabled) {
              cancelClose()
              setOpen(true)
            }
          }}
          onMouseLeave={scheduleClose}
          onClick={() => {
            if (!disabled) {
              cancelClose()
              setOpen(true)
            }
          }}
          title="思考强度（仅影响后续发送的消息）"
          className="flex h-9 shrink-0 cursor-pointer items-center gap-1.5 rounded-lg px-1.5 text-xs text-muted-foreground transition-colors select-none hover:bg-soft disabled:cursor-not-allowed disabled:opacity-50"
        >
          <BrainIcon aria-hidden className="size-4 shrink-0" />
          {thinkingLevelLabel(value)}
        </button>
      </PopoverAnchor>
      <PopoverContent
        side="top"
        align="start"
        sideOffset={6}
        aria-label="思考强度"
        className="w-40 p-1.5"
        onMouseEnter={cancelClose}
        onMouseLeave={scheduleClose}
        // 悬浮打开不抢焦点：焦点留给输入框，键盘用户仍可点开弹框操作。
        onOpenAutoFocus={(event) => event.preventDefault()}
      >
        <p className="px-2 py-1 text-xs text-muted-foreground">思考强度</p>
        <div role="radiogroup" aria-label="思考强度" className="space-y-0.5">
          {THINKING_LEVELS.map((level) => {
            const selected = level.value === value
            return (
              <button
                key={level.value}
                type="button"
                role="radio"
                aria-checked={selected}
                onClick={() => choose(level.value)}
                className={`flex w-full cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors select-none hover:bg-soft ${
                  selected ? 'bg-brand-soft text-brand-text' : 'text-foreground'
                }`}
              >
                {level.label}
                {selected && <CheckIcon aria-hidden className="ml-auto size-3.5" />}
              </button>
            )
          })}
        </div>
      </PopoverContent>
    </Popover>
  )
}
