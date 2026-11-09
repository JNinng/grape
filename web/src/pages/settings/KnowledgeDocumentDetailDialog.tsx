import { useEffect, useState } from 'react'
import { ChevronDownIcon, ChevronRightIcon } from 'lucide-react'
import { listDocumentChunks, type DocumentDTO, type KnowledgeChunkDTO } from '@/api/knowledge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

// KnowledgeDocumentDetailDialog 是条目详情：目录事实 + 向量存储中的实际
// 分片列表（分片正文点击展开）。分片来自检索同源的向量表，不是回放切分
// 算法的结果——改过切分参数而未重建时，两者可能不一致。分片正文是不可信
// 文本，一律以 React 文本节点渲染。

function errorHint(err: unknown, fallback: string): string {
  if (err instanceof Error && err.message) {
    return err.message
  }
  return fallback
}

function formatTime(iso?: string): string {
  if (!iso) {
    return '无'
  }
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) {
    return iso
  }
  return d.toLocaleString()
}

function formatRange(from?: string, to?: string): string {
  if (!from && !to) {
    return '长期有效'
  }
  return `${from ? formatTime(from) : '立即生效'} 至 ${to ? formatTime(to) : '长期'}`
}

export function KnowledgeDocumentDetailDialog({
  document,
  statusLabel,
  onClose,
}: {
  document: DocumentDTO
  statusLabel: string
  onClose: () => void
}) {
  const [chunks, setChunks] = useState<KnowledgeChunkDTO[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    void listDocumentChunks(document.id)
      .then((set) => {
        if (!cancelled) {
          setChunks(set.items)
          setLoading(false)
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setError(`加载分片失败：${errorHint(err, 'load chunks failed')}`)
          setLoading(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [document.id])

  const toggle = (chunkID: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(chunkID)) {
        next.delete(chunkID)
      } else {
        next.add(chunkID)
      }
      return next
    })
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="lg">
        <DialogHeader>
          <DialogTitle className="min-w-0 truncate">{document.title}</DialogTitle>
          <DialogDescription>
            条目详情与实际分片（切分结果以向量存储为准，改过切分参数需重建索引才会生效）。
          </DialogDescription>
        </DialogHeader>
        <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
          <div className="grid gap-0.5">
            <dt className="text-xs text-muted-foreground">状态</dt>
            <dd>{statusLabel}</dd>
          </div>
          <div className="grid gap-0.5">
            <dt className="text-xs text-muted-foreground">最近摄取</dt>
            <dd>{formatTime(document.indexedAt)}</dd>
          </div>
          <div className="grid gap-0.5">
            <dt className="text-xs text-muted-foreground">嵌入空间</dt>
            <dd>{document.indexedEmbedder || '未记录'}</dd>
          </div>
          <div className="grid gap-0.5">
            <dt className="text-xs text-muted-foreground">有效期</dt>
            <dd>{formatRange(document.validFrom, document.validTo)}</dd>
          </div>
        </dl>
        <div className="grid gap-2">
          <h3 className="text-sm font-semibold">
            分片{loading ? '' : `（${chunks.length}）`}
          </h3>
          {loading ? <p className="text-sm text-muted-foreground">加载中…</p> : null}
          {error ? <p className="text-sm text-destructive">{error}</p> : null}
          {!loading && !error && chunks.length === 0 ? (
            <p className="text-sm text-muted-foreground">暂无分片（尚未摄取或已被清理）。</p>
          ) : null}
          {chunks.length > 0 ? (
            <ul aria-label="分片列表" className="grid max-h-96 gap-2 overflow-y-auto">
              {chunks.map((chunk) => {
                const open = expanded.has(chunk.chunkId)
                return (
                  <li key={chunk.chunkId} className="rounded-lg border border-border">
                    <Button
                      variant="ghost"
                      className="grid w-full grid-cols-[auto_1fr_auto] items-center gap-2 rounded-b-none px-3 py-2 text-left"
                      aria-expanded={open}
                      onClick={() => toggle(chunk.chunkId)}
                    >
                      {open ? (
                        <ChevronDownIcon aria-hidden className="size-4" />
                      ) : (
                        <ChevronRightIcon aria-hidden className="size-4" />
                      )}
                      <span className="min-w-0 truncate text-sm">
                        #{chunk.index}
                        {chunk.anchor ? ` · ${chunk.anchor}` : ''}
                      </span>
                      <span className="text-xs text-muted-foreground">{chunk.embedder || '未记录空间'}</span>
                    </Button>
                    {open ? (
                      <pre
                        aria-label={`分片 #${chunk.index} 正文`}
                        className="max-h-64 overflow-auto whitespace-pre-wrap border-t border-border p-3 text-sm"
                      >
                        {chunk.content}
                      </pre>
                    ) : null}
                  </li>
                )
              })}
            </ul>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  )
}
