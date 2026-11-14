import { CopyIcon } from 'lucide-react'
import {
  Popover,
  PopoverAnchor,
  PopoverContent,
} from '@/components/ui/popover'
import { CopyTextButton } from '@/components/copy-text-button'
import { useHoverPopover } from './useHoverPopover'

// AnswerCopyMenu 是回答底部工具栏的复制入口：触发器只有一个无文字复制
// 图标，悬浮弹出两个选项（复制全文取渲染后文本、复制原文取 Markdown
// 源文本）。选择即复制并关闭弹框。
export function AnswerCopyMenu({
  whole,
  source,
}: {
  whole: () => string
  source: () => string
}) {
  const { open, setOpen, cancelClose, scheduleClose } = useHoverPopover()

  const closeAfterCopy = () => {
    cancelClose()
    setOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <button
          type="button"
          aria-label="复制回答"
          title="复制"
          aria-haspopup="dialog"
          aria-expanded={open}
          onMouseEnter={() => {
            cancelClose()
            setOpen(true)
          }}
          onMouseLeave={scheduleClose}
          onClick={() => {
            cancelClose()
            setOpen(true)
          }}
          className="flex size-7 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-soft hover:text-foreground"
        >
          <CopyIcon aria-hidden className="size-4" />
        </button>
      </PopoverAnchor>
      <PopoverContent
        side="top"
        align="start"
        sideOffset={6}
        aria-label="复制选项"
        className="w-max p-1.5"
        onMouseEnter={cancelClose}
        onMouseLeave={scheduleClose}
        onOpenAutoFocus={(event) => event.preventDefault()}
      >
        <div className="space-y-0.5">
          <CopyTextButton
            getText={whole}
            label="复制全文"
            showLabel
            onCopied={closeAfterCopy}
            className="h-8 w-full justify-start text-sm"
          />
          <CopyTextButton
            getText={source}
            label="复制原文"
            showLabel
            onCopied={closeAfterCopy}
            className="h-8 w-full justify-start text-sm"
          />
        </div>
      </PopoverContent>
    </Popover>
  )
}
