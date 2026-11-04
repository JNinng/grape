import { useRef, useState } from 'react'
import { MoreHorizontalIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

// RowActionInfo 是弹框顶部的行摘要字段：label 为展示名，value 为该行的
// 字段值（空值出占位横线）。
export interface RowActionInfo {
  label: string
  value: string
}

// RowActionItem 是菜单动作项：destructive 渲染红色（删除类动作）。
export interface RowActionItem {
  label: string
  onSelect: () => void
  destructive?: boolean
}

// RowActionsMenu 是行内操作按钮：悬停即弹出、移出按钮与弹框片刻后收起
// （150ms 容忍间隙，点击/键盘开合不受影响）；顶部按 info 配置展示行摘要
// 字段（如用户名/昵称）。modal={false}：悬停菜单不能用 modal 模式——modal
// 打开时把触发钮置为 pointer-events:none，指针停在按钮上会触发 mouseleave，
// 收起后又 mouseenter，反复显隐。弹框经 portal 渲染，sticky 操作列上不被
// 裁剪。
export function RowActionsMenu({ rowLabel, info, actions }: {
  rowLabel: string
  info?: RowActionInfo[]
  actions: RowActionItem[]
}) {
  const [open, setOpen] = useState(false)
  const closeTimer = useRef<number | undefined>(undefined)

  const scheduleClose = () => {
    window.clearTimeout(closeTimer.current)
    closeTimer.current = window.setTimeout(() => setOpen(false), 150)
  }
  const cancelClose = () => {
    window.clearTimeout(closeTimer.current)
  }
  const openNow = () => {
    cancelClose()
    setOpen(true)
  }

  return (
    <DropdownMenu open={open} onOpenChange={setOpen} modal={false}>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={`操作 ${rowLabel}`}
          onMouseEnter={openNow}
          onMouseLeave={scheduleClose}
          onClick={(event) => {
            // 悬停已打开时点击会被 Radix 的 toggle 收起：阻断默认切换，
            // 点击语义恒为「打开」。
            event.preventDefault()
            openNow()
          }}
        >
          <MoreHorizontalIcon aria-hidden className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onMouseEnter={cancelClose} onMouseLeave={scheduleClose}>
        {info !== undefined && info.length > 0 && (
          <>
            <div className="grid gap-0.5 px-2 py-1.5 text-sm">
              {info.map((item) => (
                <div key={item.label} className="flex items-center justify-between gap-4">
                  <span className="text-muted-foreground">{item.label}</span>
                  <span className="max-w-40 truncate font-medium">{item.value === '' ? '—' : item.value}</span>
                </div>
              ))}
            </div>
            <DropdownMenuSeparator />
          </>
        )}
        {actions.map((action) => (
          <DropdownMenuItem
            key={action.label}
            variant={action.destructive === true ? 'destructive' : undefined}
            onClick={() => {
              setOpen(false)
              action.onSelect()
            }}
          >
            {action.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
