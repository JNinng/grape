import { useEffect, useRef, useState } from 'react'

// 关闭延迟：指针在触发器与浮层内容之间移动时留出缓冲，避免闪烁。
const CLOSE_DELAY_MS = 120

// useHoverPopover 是「悬浮展开、移出延迟收起」的受控浮层状态：触发器与
// 浮层内容共用同一组事件（进入取消关闭、移出延迟关闭），指针从触发器
// 移入浮层时不会闪断。选择与点击关闭由调用方通过 setOpen 完成。
export function useHoverPopover() {
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

  return { open, setOpen, cancelClose, scheduleClose }
}
