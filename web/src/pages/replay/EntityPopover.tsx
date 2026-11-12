import { useEffect, useState, type ComponentProps } from 'react'
import type { DocumentChunkDetailDTO, DocumentDTO } from '../../api/knowledge'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Spinner } from '@/components/ui/spinner'
import { forgetChunkDetail, forgetDocumentDetail, loadChunkDetail, loadDocumentDetail } from './detailCache'

// 复现页的实体反查浮层：分片 ID 点开取分片原文 + 所属条目摘要，文档 ID
// 点开只取文档摘要。请求经 detailCache 去重（同一 ID 一次），失败不缓存、
// 可重试；浮层内加载态/失败态/无权限态均有可读文案，正文按节点渲染
//（不可信文本不走 innerHTML）。浮层组件复用 components/ui 的 Popover。

type LoadState<T> =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; data: T }

function useDetail<T>(
  open: boolean,
  id: string,
  fetcher: (id: string) => Promise<T>,
  forget: (id: string) => void,
) {
  const [state, setState] = useState<LoadState<T>>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    if (!open) {
      return
    }
    let cancelled = false
    setState({ status: 'loading' })
    fetcher(id)
      .then((data) => {
        if (!cancelled) {
          setState({ status: 'ready', data })
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ status: 'error', message: loadErrorMessage(err) })
        }
      })
    return () => {
      cancelled = true
    }
  }, [open, id, attempt, fetcher])
  // 重试先删掉该 ID 的缓存项：失败项已被缓存实现删除，但请求悬挂时
  // 表项仍在，不删就会继续等同一个永不落地的 Promise。
  const retry = () => {
    forget(id)
    setAttempt((value) => value + 1)
  }
  return { state, retry }
}

// errorReason 读取 ApiError 的稳定 reason 码（按结构判定，不依赖
// instanceof——打包/测试替身环境下类身份可能不同）。
function errorReason(err: unknown): string {
  if (typeof err === 'object' && err !== null && 'reason' in err) {
    const reason = (err as { reason?: unknown }).reason
    if (typeof reason === 'string') {
      return reason
    }
  }
  return ''
}

// loadErrorMessage 把反查失败归一为可读文案：权限拒绝单独给出「无权限」
// 提示（reason 为稳定码表，后端实际取值为 PERM_ACCESS_DENIED），其余透出
// 服务端消息。
function loadErrorMessage(err: unknown): string {
  const reason = errorReason(err)
  if (
    reason === 'PERMISSION_DENIED' ||
    reason === 'FORBIDDEN' ||
    reason === 'PERM_ACCESS_DENIED' ||
    reason.endsWith('_DENIED') ||
    reason.includes('PERMISSION')
  ) {
    return '无权限查看该内容'
  }
  return err instanceof Error ? err.message : '加载失败'
}

function formatTime(iso: string): string {
  if (!iso) {
    return '—'
  }
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString()
}

function statusText(status: number | undefined): string {
  if (status === 1) {
    return '启用'
  }
  if (status === 0) {
    return '禁用'
  }
  return '—'
}

// EntityTrigger 是可点击的 ID 芯片：与正文同形（mono 小字），点按打开
// 浮层；button + aria-expanded 保证键盘可达。
function EntityTrigger({
  id,
  label,
  expanded,
  ...props
}: { id: string; label: string; expanded: boolean } & ComponentProps<'button'>) {
  return (
    <button
      type="button"
      aria-label={`${label} ${id}`}
      aria-expanded={expanded}
      className="cursor-pointer rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-secondary-foreground underline decoration-dotted underline-offset-2 transition-colors hover:bg-soft"
      {...props}
    >
      {id}
    </button>
  )
}

function DetailStateView({ state, retry }: { state: LoadState<unknown>; retry: () => void }) {
  if (state.status === 'loading') {
    return (
      <p className="flex items-center gap-2 text-xs text-muted-foreground">
        <Spinner aria-hidden className="size-3" />
        加载中…
      </p>
    )
  }
  if (state.status === 'error') {
    return (
      <div className="space-y-2 text-xs">
        <p className="text-danger-text">加载失败：{state.message}</p>
        <Button type="button" size="sm" variant="outline" onClick={retry}>
          重试
        </Button>
      </div>
    )
  }
  return null
}

function DocumentFacts({ document }: { document: DocumentDTO }) {
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
      <dt className="text-muted-foreground">标题</dt>
      <dd className="wrap-break-word text-foreground">{document.title || '—'}</dd>
      <dt className="text-muted-foreground">类型</dt>
      <dd className="text-foreground">{document.kind || '—'}</dd>
      <dt className="text-muted-foreground">状态</dt>
      <dd className="text-foreground">{statusText(document.status)}</dd>
      <dt className="text-muted-foreground">所属库</dt>
      <dd className="text-foreground">{document.libraryId || '—'}</dd>
      <dt className="text-muted-foreground">更新时间</dt>
      <dd className="text-foreground">{formatTime(document.updatedAt ?? document.createdAt)}</dd>
    </dl>
  )
}

// ChunkIdPopover 点开分片 ID：展示分片原文、序号、锚点、嵌入空间与退役
// 状态，以及所属条目摘要。
export function ChunkIdPopover({ chunkId }: { chunkId: string }) {
  const [open, setOpen] = useState(false)
  const { state, retry } = useDetail(open, chunkId, loadChunkDetail, forgetChunkDetail)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <EntityTrigger id={chunkId} label="分片" expanded={open} />
      </PopoverTrigger>
      <PopoverContent align="start" className="max-h-96 w-96 overflow-y-auto">
        <div className="space-y-2.5">
          <h3 className="text-sm font-medium text-foreground">分片详情</h3>
          <p className="break-all font-mono text-[11px] text-muted-foreground">{chunkId}</p>
          {state.status !== 'ready' ? (
            <DetailStateView state={state} retry={retry} />
          ) : (
            <ChunkDetailView detail={state.data} />
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}

function ChunkDetailView({ detail }: { detail: DocumentChunkDetailDTO }) {
  const { chunk, document, supersededAt } = detail
  return (
    <div className="space-y-2.5 text-xs">
      {supersededAt !== '' && (
        <p className="rounded bg-danger-soft px-2 py-1 text-danger-text">
          已重摄淘汰，保留期内可反查（{formatTime(supersededAt)}）
        </p>
      )}
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
        <dt className="text-muted-foreground">序号</dt>
        <dd className="text-foreground">{chunk.index}</dd>
        <dt className="text-muted-foreground">锚点</dt>
        <dd className="text-foreground">{chunk.anchor || '—'}</dd>
        <dt className="text-muted-foreground">嵌入空间</dt>
        <dd className="break-all text-foreground">{chunk.embedder || '—'}</dd>
      </dl>
      <div className="space-y-1">
        <h4 className="font-medium text-muted-foreground">分片原文</h4>
        <pre className="max-h-40 overflow-y-auto rounded bg-soft p-2 font-mono text-[11px] break-words whitespace-pre-wrap text-foreground">
          {chunk.content || '（空）'}
        </pre>
      </div>
      <div className="space-y-1 border-t pt-2">
        <h4 className="font-medium text-muted-foreground">所属条目</h4>
        <DocumentFacts document={document} />
      </div>
    </div>
  )
}

// DocumentIdPopover 点开文档 ID（单元的 source_id）：只展示文档摘要。
export function DocumentIdPopover({ documentId }: { documentId: string }) {
  const [open, setOpen] = useState(false)
  const { state, retry } = useDetail(open, documentId, loadDocumentDetail, forgetDocumentDetail)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <EntityTrigger id={documentId} label="文档" expanded={open} />
      </PopoverTrigger>
      <PopoverContent align="start" className="max-h-96 w-80 overflow-y-auto">
        <div className="space-y-2.5">
          <h3 className="text-sm font-medium text-foreground">文档详情</h3>
          <p className="break-all font-mono text-[11px] text-muted-foreground">{documentId}</p>
          {state.status !== 'ready' ? (
            <DetailStateView state={state} retry={retry} />
          ) : (
            <DocumentFacts document={state.data} />
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}
