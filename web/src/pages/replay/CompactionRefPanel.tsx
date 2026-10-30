import { useState } from 'react'
import { ChevronDownIcon } from 'lucide-react'
import { fetchEventWindow, type EventWindow } from '../../api/agentEventWindow'
import type { CompactionRef } from './compaction'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'

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
      <Badge
        role="note"
        aria-label="压缩标记"
        data-kind={compactionRef.kind}
        className={
          compactionRef.kind === 'truncated'
            ? 'bg-amber-50 text-amber-600 dark:bg-amber-950 dark:text-amber-400'
            : 'bg-violet-50 text-violet-600 dark:bg-violet-950 dark:text-violet-400'
        }
      >
        {label}
      </Badge>
      {recoverable ? (
        <Collapsible
          className="min-w-0 flex-1 basis-full sm:basis-auto"
          onOpenChange={(open) => {
            if (open) {
              void loadOriginal()
            }
          }}
        >
          <CollapsibleTrigger className="inline-flex cursor-pointer items-center gap-1 text-xs font-medium text-violet-600 transition-colors select-none hover:text-violet-500 dark:text-violet-400">
            对照原文
            <ChevronDownIcon aria-hidden className="size-3.5" />
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div className="mt-2 space-y-2 rounded-xl border bg-soft p-3">
              {error !== null && (
                <Alert variant="destructive">
                  <AlertDescription>
                    <span>
                      原文加载失败：{error}{' '}
                      <Button
                        type="button"
                        variant="outline"
                        size="xs"
                        className="ml-1"
                        onClick={() => void loadOriginal()}
                      >
                        重试
                      </Button>
                    </span>
                  </AlertDescription>
                </Alert>
              )}
              {loading && <p className="text-xs text-muted-foreground">原文加载中…</p>}
              {window !== null && (
                <>
                  <h4 className="text-xs font-medium text-muted-foreground">事件原文（event {window.anchorEventId}）</h4>
                  {window.events.map((evt) => (
                    <pre key={evt.eventId} aria-label="事件原文" className="code-block">
                      {evt.content ?? '（该事件无文本内容）'}
                    </pre>
                  ))}
                </>
              )}
            </div>
          </CollapsibleContent>
        </Collapsible>
      ) : (
        <span aria-label="压缩说明" className="text-xs text-muted-foreground">
          （无恢复引用，原文不可反查）
        </span>
      )}
    </span>
  )
}
