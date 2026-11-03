import { useSyncExternalStore } from 'react'
import { cn } from 'cn'
import { CircleAlertIcon, CircleCheckIcon, InfoIcon, XIcon } from 'lucide-react'

// 轻量 toast：模块级 store + useSyncExternalStore，全局唯一 <Toaster/>
// 挂载点（App）渲染堆叠通知。不引入第三方依赖；反馈只承载「结果已发
// 生」类信息，需要用户阅读排错的持久内容仍走页面内 Alert。
type ToastKind = 'success' | 'error' | 'info'

interface ToastItem {
  id: number
  kind: ToastKind
  description: string
}

// TOAST_DURATION_MS 是自动消失时长：测试以假定时器显式推进，不靠真实
// 等待保证确定性。
export const TOAST_DURATION_MS = 4_000

let items: ToastItem[] = []
let seq = 0
const listeners = new Set<() => void>()

function emit(): void {
  for (const listener of listeners) {
    listener()
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

function getSnapshot(): ToastItem[] {
  return items
}

export function dismissToast(id: number): void {
  const next = items.filter((item) => item.id !== id)
  if (next.length !== items.length) {
    items = next
    emit()
  }
}

function pushToast(kind: ToastKind, description: string): void {
  seq += 1
  const id = seq
  items = [...items, { id, kind, description }]
  emit()
  window.setTimeout(() => dismissToast(id), TOAST_DURATION_MS)
}

// toast 是命令式入口：管理页各异步动作的成功/失败反馈统一走这里。
export const toast = {
  success: (description: string): void => pushToast('success', description),
  error: (description: string): void => pushToast('error', description),
  info: (description: string): void => pushToast('info', description),
}

// clearToasts 清空全部通知：仅供测试在用例间复位 store，保证用例隔离。
export function clearToasts(): void {
  items = []
  emit()
}

const KIND_STYLES: Record<ToastKind, string> = {
  success: 'text-emerald-600 dark:text-emerald-400',
  error: 'text-destructive',
  info: 'text-foreground',
}

// Toaster 渲染当前通知栈：固定于视口右下，错误类以 role=alert 播报，
// 其余 role=status；点击关闭按钮或通知体即可移除。
export function Toaster() {
  const current = useSyncExternalStore(subscribe, getSnapshot)

  if (current.length === 0) {
    return null
  }
  return (
    <div
      aria-label="通知"
      className="fixed right-4 bottom-4 z-50 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2"
    >
      {current.map((item) => (
        <div
          key={item.id}
          role={item.kind === 'error' ? 'alert' : 'status'}
          data-slot="toast"
          data-kind={item.kind}
          data-id={item.id}
          className={cn(
            'flex items-start gap-2 rounded-lg border bg-popover p-3 text-popover-foreground shadow-md',
            item.kind === 'error' ? 'border-destructive-soft' : '',
          )}
          onClick={() => dismissToast(item.id)}
        >
          {item.kind === 'success' ? (
            <CircleCheckIcon aria-hidden className={cn('mt-0.5 size-4 shrink-0', KIND_STYLES[item.kind])} />
          ) : item.kind === 'error' ? (
            <CircleAlertIcon aria-hidden className={cn('mt-0.5 size-4 shrink-0', KIND_STYLES[item.kind])} />
          ) : (
            <InfoIcon aria-hidden className={cn('mt-0.5 size-4 shrink-0', KIND_STYLES[item.kind])} />
          )}
          <span className="min-w-0 flex-1 text-sm break-words">{item.description}</span>
          <button
            type="button"
            aria-label="关闭通知"
            className="shrink-0 cursor-pointer rounded-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
            onClick={(event) => {
              event.stopPropagation()
              dismissToast(item.id)
            }}
          >
            <XIcon aria-hidden className="size-4" />
          </button>
        </div>
      ))}
    </div>
  )
}
