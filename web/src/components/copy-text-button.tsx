import { useEffect, useRef, useState } from 'react'
import { CheckIcon, CopyIcon } from 'lucide-react'
import { cn } from 'cn'
import { toast } from '@/components/ui/toast'

const COPIED_FEEDBACK_MS = 1_500

// CopyTextButton 复制不可信文本：text 是现成字符串，getText 用于按点击
// 时刻取值（如从渲染后的 DOM 提取整体文本），避免每次渲染重复计算。
// 成功短暂切勾选图标并弹 toast，失败只提示不抛错；剪贴板不可用（非安全
// 上下文等）按失败处理，不做 execCommand 回退。showLabel 为 true 时以
// 文字按钮形态出现（复制弹框与工具栏），否则是图标按钮（折叠头部）。
export function CopyTextButton({
  text,
  getText,
  label = '复制原文',
  successMessage,
  showLabel = false,
  onCopied,
  className,
}: {
  text?: string
  getText?: () => string
  label?: string
  successMessage?: string
  showLabel?: boolean
  onCopied?: () => void
  className?: string
}) {
  const [copied, setCopied] = useState(false)
  const timerRef = useRef<number | null>(null)

  useEffect(() => {
    return () => {
      if (timerRef.current !== null) {
        window.clearTimeout(timerRef.current)
      }
    }
  }, [])

  const copy = async () => {
    const value = getText !== undefined ? getText() : (text ?? '')
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      if (timerRef.current !== null) {
        window.clearTimeout(timerRef.current)
      }
      timerRef.current = window.setTimeout(() => setCopied(false), COPIED_FEEDBACK_MS)
      toast.success(successMessage ?? `已${label}`)
      onCopied?.()
    } catch {
      toast.error('复制失败，请检查浏览器剪贴板权限')
    }
  }

  const baseClass = showLabel
    ? 'flex h-7 shrink-0 cursor-pointer items-center gap-1 rounded-md px-2 text-xs'
    : 'flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-md'

  return (
    <button
      type="button"
      onClick={() => void copy()}
      aria-label={label}
      title={label}
      className={cn(
        baseClass,
        'text-muted-foreground transition-colors hover:bg-soft hover:text-foreground',
        className,
      )}
    >
      {copied ? <CheckIcon aria-hidden className="size-3.5" /> : <CopyIcon aria-hidden className="size-3.5" />}
      {showLabel && <span>{copied ? '已复制' : label}</span>}
    </button>
  )
}
