import { useState } from 'react'
import { fetchEventWindow, type EventWindow } from '../../api/agentEventWindow'
import type { CompactionRef } from './compaction'
import { ChevronDownIcon } from '../../ui/icons'

// CompactionRefPanel 是压缩占位符的「已压缩」徽标与原文对照展开：
// 占位符携带 event_id 恢复引用时，展开经事件窗口端点取回会话事件原文
// 与占位文本并排对照；不可恢复占位符（无引用）只标注说明、不提供展开。
// 原文按需懒加载（首次展开才请求），失败显式报错且可重试。
export function CompactionRefPanel({ compactionRef, threadId }: { compactionRef: CompactionRef; threadId: string }) {
  const [window, setWindow] = useState<EventWindow | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const recoverable = typeof compactionRef.eventId === 'string' && compactionRef.eventId !== ''
  const label = compactionRef.kind === 'truncated' ? '部分截断' : '已压缩'

  const loadOriginal = async () => {
    if (window !== null || loading || !recoverable) {
      return
    }
    setLoading(true)
    setError(null)
    try {
      setWindow(await fetchEventWindow(threadId, compactionRef.eventId as string))
    } catch (err) {
      setError(err instanceof Error ? err.message : '原文加载失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <span className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1">
      <span
        role="note"
        aria-label="压缩标记"
        data-kind={compactionRef.kind}
        className={`chip ${compactionRef.kind === 'truncated' ? 'bg-amber-50 text-amber-600' : 'bg-violet-50 text-violet-600'}`}
      >
        {label}
      </span>
      {recoverable ? (
        <details
          className="min-w-0 flex-1 basis-full sm:basis-auto"
          onToggle={(e) => {
            if ((e.target as HTMLDetailsElement).open) {
              void loadOriginal()
            }
          }}
        >
          <summary className="inline-flex cursor-pointer list-none items-center gap-1 text-xs font-medium text-violet-600 transition-colors select-none hover:text-violet-500 [&::-webkit-details-marker]:hidden">
            对照原文
            <ChevronDownIcon aria-hidden className="h-3.5 w-3.5" />
          </summary>
          <div className="mt-2 space-y-2 rounded-xl border border-slate-200 bg-slate-50 p-3">
            {error !== null && (
              <p role="alert" className="alert">
                <span>
                  原文加载失败：{error}{' '}
                  <button type="button" onClick={() => void loadOriginal()} className="btn btn-outline btn-sm ml-1">
                    重试
                  </button>
                </span>
              </p>
            )}
            {loading && <p className="text-xs text-slate-400">原文加载中…</p>}
            {window !== null && (
              <>
                <h4 className="text-xs font-medium text-slate-400">事件原文（event {window.anchorEventId}）</h4>
                {window.events.map((evt) => (
                  <pre key={evt.eventId} aria-label="事件原文" className="code-block">
                    {evt.content ?? '（该事件无文本内容）'}
                  </pre>
                ))}
              </>
            )}
          </div>
        </details>
      ) : (
        <span aria-label="压缩说明" className="text-xs text-slate-400">
          （无恢复引用，原文不可反查）
        </span>
      )}
    </span>
  )
}
