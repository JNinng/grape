import { useCallback, useEffect, useState } from 'react'
import { BookOpenIcon, PencilIcon, PlusIcon, RefreshCwIcon, SearchIcon, Trash2Icon } from 'lucide-react'
import {
  createDocument,
  createLibrary,
  deleteDocument,
  deleteLibrary,
  listLibraries,
  listLibraryDocuments,
  listModelOptions,
  reindexDocument,
  updateDocument,
  updateLibrary,
  type DocumentDTO,
  type LibraryDTO,
  type ModelOptionDTO,
} from '@/api/knowledge'
import { usePermissions } from '@/auth/permissions'
import { PERM } from '@/auth/permissionCodes'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { toast } from '@/components/ui/toast'
import {
  DEFAULT_CHUNK_DELIMITER,
  decodeChunkDelimiter,
  describeChunkDelimiter,
  encodeChunkDelimiter,
} from './chunkDelimiter'
import { KnowledgeSearchDialog } from './KnowledgeSearchDialog'

// KnowledgeManagePage 是知识库管理页：左侧库列表，右侧选中库的条目表。
// 条目列表走库内固定分页端点（宿主 id 在路径参数，规避动态协议整型过滤
// 值的 JSON number 精度限制）；状态与有效期在列表内合成展示。检索面板
// 复用已加载的库列表做库过滤。

const DOC_PAGE_SIZE = 20
const LIBRARY_PAGE_SIZE = 200


function errorHint(err: unknown, fallback: string): string {
  if (err instanceof Error && err.message) {
    return err.message
  }
  return fallback
}

// statusLabel 合成启用/停用/未生效/已过期展示态。
function statusLabel(status: number | undefined, validFrom?: string, validTo?: string): string {
  if (status === 0) {
    return '停用'
  }
  const now = Date.now()
  if (validFrom && new Date(validFrom).getTime() > now) {
    return '未生效'
  }
  if (validTo && new Date(validTo).getTime() <= now) {
    return '已过期'
  }
  return '启用'
}

function statusClass(label: string): string {
  switch (label) {
    case '启用':
      return 'text-emerald-700'
    case '未生效':
      return 'text-amber-700'
    case '已过期':
      return 'text-orange-700'
    default:
      return 'text-muted-foreground'
  }
}

function toLocalInput(iso?: string): string {
  if (!iso) {
    return ''
  }
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) {
    return ''
  }
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function fromLocalInput(value: string): string | undefined {
  if (!value) {
    return undefined
  }
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) {
    return undefined
  }
  return d.toISOString()
}

export function KnowledgeManagePage() {
  const { can } = usePermissions()
  const canCreate = can(PERM.KnowledgeCreate)
  const canUpdate = can(PERM.KnowledgeUpdate)
  const canDelete = can(PERM.KnowledgeDelete)

  const [libraries, setLibraries] = useState<LibraryDTO[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [librariesLoading, setLibrariesLoading] = useState(true)
  const [libraryError, setLibraryError] = useState<string | null>(null)

  const [documents, setDocuments] = useState<DocumentDTO[]>([])
  const [docTotal, setDocTotal] = useState<string | undefined>(undefined)
  const [docPage, setDocPage] = useState(1)
  const [documentsLoading, setDocumentsLoading] = useState(false)
  const [documentError, setDocumentError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const [libraryDialog, setLibraryDialog] = useState<{ mode: 'create' } | { mode: 'edit'; library: LibraryDTO } | null>(null)
  const [documentDialog, setDocumentDialog] = useState<{ mode: 'create' } | { mode: 'edit'; document: DocumentDTO } | null>(null)
  const [searchOpen, setSearchOpen] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<
    | { kind: 'library'; library: LibraryDTO }
    | { kind: 'document'; document: DocumentDTO }
    | null
  >(null)

  const selected = libraries.find((item) => item.id === selectedId) ?? null

  const loadLibraries = useCallback(async () => {
    setLibrariesLoading(true)
    setLibraryError(null)
    try {
      const set = await listLibraries({ pagination: { page: 1, pageSize: LIBRARY_PAGE_SIZE } })
      setLibraries(set.items)
      setSelectedId((current) => {
        if (current && set.items.some((item) => item.id === current)) {
          return current
        }
        return set.items[0]?.id ?? null
      })
    } catch (err) {
      setLibraryError(errorHint(err, '加载知识库失败'))
    } finally {
      setLibrariesLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadLibraries()
  }, [loadLibraries])

  const loadDocuments = useCallback(async () => {
    if (!selectedId) {
      setDocuments([])
      setDocTotal(undefined)
      return
    }
    setDocumentsLoading(true)
    setDocumentError(null)
    try {
      const set = await listLibraryDocuments(selectedId, docPage, DOC_PAGE_SIZE)
      setDocuments(set.items)
      setDocTotal(set.total)
    } catch (err) {
      setDocumentError(errorHint(err, '加载条目失败'))
    } finally {
      setDocumentsLoading(false)
    }
  }, [selectedId, docPage])

  useEffect(() => {
    void loadDocuments()
  }, [loadDocuments, reloadKey])

  const total = docTotal ? Number(docTotal) : 0
  const hasNext = docPage * DOC_PAGE_SIZE < total

  const onReindex = async (doc: DocumentDTO) => {
    try {
      await reindexDocument(doc.id)
      toast.success(`条目 ${doc.title} 已重建索引`)
      setReloadKey((key) => key + 1)
    } catch (err) {
      toast.error(`重建索引失败：${errorHint(err, 'reindex failed')}`)
    }
  }

  const performDelete = async () => {
    if (!confirmDelete) {
      return
    }
    try {
      if (confirmDelete.kind === 'library') {
        await deleteLibrary(confirmDelete.library.id)
        toast.success(`知识库 ${confirmDelete.library.name} 已删除`)
        setSelectedId(null)
        await loadLibraries()
      } else {
        await deleteDocument(confirmDelete.document.id)
        toast.success(`条目 ${confirmDelete.document.title} 已删除`)
        setReloadKey((key) => key + 1)
      }
    } catch (err) {
      toast.error(`删除失败：${errorHint(err, 'delete failed')}`)
    } finally {
      setConfirmDelete(null)
    }
  }

  return (
    <div className="flex h-full min-h-0 gap-4 p-6">
      <aside className="flex w-72 shrink-0 flex-col rounded-xl border border-border bg-card">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h1 className="flex items-center gap-2 text-sm font-semibold">
            <BookOpenIcon aria-hidden className="size-4" />
            知识库
          </h1>
          <div className="flex items-center gap-1">
            <Button variant="outline" size="sm" onClick={() => setSearchOpen(true)}>
              <SearchIcon aria-hidden className="size-4" />
              检索
            </Button>
            {canCreate ? (
              <Button size="sm" onClick={() => setLibraryDialog({ mode: 'create' })}>
                <PlusIcon aria-hidden className="size-4" />
                新建
              </Button>
            ) : null}
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-2">
          {librariesLoading ? <p className="p-3 text-sm text-muted-foreground">加载中…</p> : null}
          {libraryError ? <p className="p-3 text-sm text-destructive">{libraryError}</p> : null}
          {!librariesLoading && libraries.length === 0 ? (
            <p className="p-3 text-sm text-muted-foreground">暂无知识库</p>
          ) : null}
          <ul className="space-y-1">
            {libraries.map((library) => {
              const label = statusLabel(library.status, library.validFrom, library.validTo)
              return (
                <li key={library.id}>
                  <div
                    className={
                      library.id === selectedId
                        ? 'rounded-lg bg-brand-soft px-3 py-2'
                        : 'rounded-lg px-3 py-2 hover:bg-accent'
                    }
                  >
                    <button
                      type="button"
                      className="flex w-full items-start justify-between gap-2 text-left"
                      onClick={() => {
                        setDocPage(1)
                        setSelectedId(library.id)
                      }}
                    >
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium">{library.name}</span>
                        <span className={`block text-xs ${statusClass(label)}`}>{label}</span>
                      </span>
                    </button>
                    {canUpdate || canDelete ? (
                      <div className="mt-2 flex justify-end gap-1">
                        {canUpdate ? (
                          <Button variant="ghost" size="sm" onClick={() => setLibraryDialog({ mode: 'edit', library })}>
                            <PencilIcon aria-hidden className="size-3.5" />
                            编辑
                          </Button>
                        ) : null}
                        {canDelete ? (
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => setConfirmDelete({ kind: 'library', library })}
                          >
                            <Trash2Icon aria-hidden className="size-3.5" />
                            删除
                          </Button>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                </li>
              )
            })}
          </ul>
        </div>
      </aside>

      <section className="flex min-w-0 flex-1 flex-col rounded-xl border border-border bg-card">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <div>
            <h2 className="text-sm font-semibold">{selected ? selected.name : '未选择知识库'}</h2>
            {selected ? (
              <p className="text-xs text-muted-foreground">
                切分 {selected.chunkSize}/{selected.chunkOverlap} · {describeChunkDelimiter(selected.chunkDelimiter)} · 嵌入模型{' '}
                {selected.embeddingModelKey ?? '跟随默认'}
              </p>
            ) : null}
          </div>
          {selected && canCreate ? (
            <Button size="sm" onClick={() => setDocumentDialog({ mode: 'create' })}>
              <PlusIcon aria-hidden className="size-4" />
              新建条目
            </Button>
          ) : null}
        </div>
        <div className="min-h-0 flex-1 overflow-auto p-4">
          {documentError ? <p className="mb-3 text-sm text-destructive">{documentError}</p> : null}
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>标题</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>最近摄取</TableHead>
                <TableHead className="w-40">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {documentsLoading ? (
                <TableRow>
                  <TableCell colSpan={4} className="text-sm text-muted-foreground">
                    加载中…
                  </TableCell>
                </TableRow>
              ) : null}
              {!documentsLoading && documents.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={4} className="text-sm text-muted-foreground">
                    该知识库暂无条目
                  </TableCell>
                </TableRow>
              ) : null}
              {documents.map((doc) => {
                const label = statusLabel(doc.status, doc.validFrom, doc.validTo)
                return (
                  <TableRow key={doc.id}>
                    <TableCell className="max-w-80 truncate font-medium">{doc.title}</TableCell>
                    <TableCell className={statusClass(label)}>{label}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {doc.indexedAt ? new Date(doc.indexedAt).toLocaleString() : '未摄取'}
                      {doc.indexedEmbedder ? ` · ${doc.indexedEmbedder}` : ''}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {canUpdate ? (
                          <>
                            <Button variant="ghost" size="sm" onClick={() => setDocumentDialog({ mode: 'edit', document: doc })}>
                              <PencilIcon aria-hidden className="size-3.5" />
                              编辑
                            </Button>
                            <Button variant="ghost" size="sm" onClick={() => void onReindex(doc)}>
                              <RefreshCwIcon aria-hidden className="size-3.5" />
                              重建
                            </Button>
                          </>
                        ) : null}
                        {canDelete ? (
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => setConfirmDelete({ kind: 'document', document: doc })}
                          >
                            <Trash2Icon aria-hidden className="size-3.5" />
                            删除
                          </Button>
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
        <div className="flex items-center justify-between border-t border-border px-4 py-2 text-sm">
          <span className="text-muted-foreground">共 {total} 条</span>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={docPage <= 1 || documentsLoading}
              onClick={() => setDocPage((page) => Math.max(1, page - 1))}
            >
              上一页
            </Button>
            <span>第 {docPage} 页</span>
            <Button
              variant="outline"
              size="sm"
              disabled={!hasNext || documentsLoading}
              onClick={() => setDocPage((page) => page + 1)}
            >
              下一页
            </Button>
          </div>
        </div>
      </section>

      {libraryDialog ? (
        <LibraryDialog
          library={libraryDialog.mode === 'edit' ? libraryDialog.library : undefined}
          onClose={() => setLibraryDialog(null)}
          onSaved={async () => {
            setLibraryDialog(null)
            await loadLibraries()
          }}
        />
      ) : null}

      {documentDialog && selected ? (
        <DocumentDialog
          library={selected}
          document={documentDialog.mode === 'edit' ? documentDialog.document : undefined}
          onClose={() => setDocumentDialog(null)}
          onSaved={async () => {
            setDocumentDialog(null)
            setReloadKey((key) => key + 1)
          }}
        />
      ) : null}

      {searchOpen ? <KnowledgeSearchDialog libraries={libraries} onClose={() => setSearchOpen(false)} /> : null}

      {confirmDelete ? (
        <AlertDialog open onOpenChange={(open) => { if (!open) setConfirmDelete(null) }}>
          <AlertDialogContent size="sm">
            <AlertDialogHeader>
              <AlertDialogTitle>
                确认删除
                {confirmDelete.kind === 'library' ? confirmDelete.library.name : confirmDelete.document.title}？
              </AlertDialogTitle>
              <AlertDialogDescription>
                {confirmDelete.kind === 'library'
                  ? '库内仍有未删除条目时删除会被拒绝；删除会清理库内全部 chunk。'
                  : '删除条目会同时清理其 chunk（软删除目录行）。'}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>取消</AlertDialogCancel>
              <AlertDialogAction variant="destructive" onClick={(e) => { e.preventDefault(); void performDelete() }}>
                删除
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      ) : null}
    </div>
  )
}

function LibraryDialog({
  library,
  onClose,
  onSaved,
}: {
  library?: LibraryDTO
  onClose: () => void
  onSaved: () => Promise<void> | void
}) {
  const [name, setName] = useState(library?.name ?? '')
  const [status, setStatus] = useState(library ? library.status === 1 : true)
  const [remark, setRemark] = useState(library?.remark ?? '')
  const [embeddingModelKey, setEmbeddingModelKey] = useState(library?.embeddingModelKey ?? '__default__')
  const [chunkSize, setChunkSize] = useState(String(library?.chunkSize ?? 800))
  const [chunkOverlap, setChunkOverlap] = useState(String(library?.chunkOverlap ?? 100))
  const [chunkDelimiter, setChunkDelimiter] = useState(
    encodeChunkDelimiter(library?.chunkDelimiter ?? DEFAULT_CHUNK_DELIMITER),
  )
  const [validFrom, setValidFrom] = useState(toLocalInput(library?.validFrom))
  const [validTo, setValidTo] = useState(toLocalInput(library?.validTo))
  const [options, setOptions] = useState<ModelOptionDTO[]>([])
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    void listModelOptions('embedding')
      .then((set) => {
        if (!cancelled) {
          setOptions(set.items)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setOptions([])
        }
      })
    return () => {
      cancelled = true
    }
  }, [])

  const submit = async () => {
    setPending(true)
    setError(null)
    try {
      const embeddingKey = embeddingModelKey === '__default__' ? undefined : embeddingModelKey
      if (library) {
        await updateLibrary({
          id: library.id,
          status: status ? 1 : 0,
          name: name.trim(),
          remark: remark.trim() || undefined,
          embeddingModelKey: embeddingKey,
          chunkSize: Number(chunkSize) || 0,
          chunkOverlap: Number(chunkOverlap) || 0,
          chunkDelimiter: decodeChunkDelimiter(chunkDelimiter),
          validFrom: fromLocalInput(validFrom),
          validTo: fromLocalInput(validTo),
        })
        toast.success(`知识库 ${name.trim()} 已更新`)
      } else {
        await createLibrary({
          status: status ? 1 : 0,
          name: name.trim(),
          remark: remark.trim() || undefined,
          embeddingModelKey: embeddingKey,
          chunkSize: Number(chunkSize) || 0,
          chunkOverlap: Number(chunkOverlap) || 0,
          chunkDelimiter: decodeChunkDelimiter(chunkDelimiter),
          validFrom: fromLocalInput(validFrom),
          validTo: fromLocalInput(validTo),
        })
        toast.success(`知识库 ${name.trim()} 已创建`)
      }
      await onSaved()
    } catch (err) {
      setError(errorHint(err, 'save failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{library ? '编辑知识库' : '新建知识库'}</DialogTitle>
          <DialogDescription>库级嵌入模型与切分参数在该库下次摄取时生效。</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="grid gap-1.5">
            <Label htmlFor="kb-name">名称</Label>
            <Input id="kb-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="kb-embed">嵌入模型</Label>
            <Select value={embeddingModelKey} onValueChange={setEmbeddingModelKey}>
              <SelectTrigger id="kb-embed">
                <SelectValue placeholder="跟随默认" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">跟随租户/全局默认</SelectItem>
                {options.map((option) => (
                  <SelectItem key={option.modelKey} value={option.modelKey}>
                    {option.name}（{option.modelKey}）
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="kb-chunk">切分大小（rune）</Label>
              <Input id="kb-chunk" value={chunkSize} onChange={(e) => setChunkSize(e.target.value)} />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="kb-overlap">重叠（rune）</Label>
              <Input id="kb-overlap" value={chunkOverlap} onChange={(e) => setChunkOverlap(e.target.value)} />
            </div>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="kb-delimiter">区分串</Label>
            <Input
              id="kb-delimiter"
              aria-describedby="kb-delimiter-hint"
              value={chunkDelimiter}
              placeholder="留空表示不按区分串切分"
              onChange={(e) => setChunkDelimiter(e.target.value)}
            />
            <p id="kb-delimiter-hint" className="text-xs text-muted-foreground">
              {'先按区分串切出段落、超长段落再按切分大小切分；\\n 表示换行、\\t 表示制表，缺省 \\n\\n（段落空行），至多 32 字符。'}
            </p>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="kb-from">生效时间</Label>
              <Input id="kb-from" type="datetime-local" value={validFrom} onChange={(e) => setValidFrom(e.target.value)} />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="kb-to">失效时间</Label>
              <Input id="kb-to" type="datetime-local" value={validTo} onChange={(e) => setValidTo(e.target.value)} />
            </div>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="kb-remark">备注</Label>
            <Textarea id="kb-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </div>
          <div className="flex items-center gap-2">
            <Switch id="kb-status" checked={status} onCheckedChange={setStatus} />
            <Label htmlFor="kb-status">启用</Label>
          </div>
          {error ? <p className="text-sm text-destructive">{error}</p> : null}
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={pending} onClick={onClose}>
            取消
          </Button>
          <Button disabled={pending || !name.trim()} onClick={() => void submit()}>
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function DocumentDialog({
  library,
  document,
  onClose,
  onSaved,
}: {
  library: LibraryDTO
  document?: DocumentDTO
  onClose: () => void
  onSaved: () => Promise<void> | void
}) {
  const [title, setTitle] = useState(document?.title ?? '')
  const [content, setContent] = useState(document?.content ?? '')
  const [sourceName, setSourceName] = useState(document?.sourceName ?? '')
  const [status, setStatus] = useState(document ? document.status === 1 : true)
  const [validFrom, setValidFrom] = useState(toLocalInput(document?.validFrom))
  const [validTo, setValidTo] = useState(toLocalInput(document?.validTo))
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    setPending(true)
    setError(null)
    try {
      if (document) {
        await updateDocument({
          id: document.id,
          status: status ? 1 : 0,
          title: title.trim(),
          content: content.trim() ? content : undefined,
          sourceName: sourceName.trim() || undefined,
          validFrom: fromLocalInput(validFrom),
          validTo: fromLocalInput(validTo),
        })
        toast.success(`条目 ${title.trim()} 已更新`)
      } else {
        await createDocument({
          libraryId: library.id,
          kind: 'text',
          title: title.trim(),
          content,
          sourceName: sourceName.trim() || undefined,
          status: status ? 1 : 0,
          validFrom: fromLocalInput(validFrom),
          validTo: fromLocalInput(validTo),
        })
        toast.success(`条目 ${title.trim()} 已创建并摄取`)
      }
      await onSaved()
    } catch (err) {
      setError(errorHint(err, 'save failed'))
    } finally {
      setPending(false)
    }
  }

  const requiresContent = !document

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{document ? '编辑条目' : '新建条目'}</DialogTitle>
          <DialogDescription>
            文本条目保存后自动切分、向量化；停用或超出有效期的条目不出现在检索结果。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="grid gap-1.5">
            <Label htmlFor="doc-title">标题</Label>
            <Input id="doc-title" value={title} onChange={(e) => setTitle(e.target.value)} />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="doc-content">正文{document ? '（留空保持不变）' : ''}</Label>
            <Textarea
              id="doc-content"
              className="min-h-40"
              value={content}
              onChange={(e) => setContent(e.target.value)}
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="doc-source">来源名</Label>
            <Input id="doc-source" value={sourceName} onChange={(e) => setSourceName(e.target.value)} />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="doc-from">生效时间</Label>
              <Input id="doc-from" type="datetime-local" value={validFrom} onChange={(e) => setValidFrom(e.target.value)} />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="doc-to">失效时间</Label>
              <Input id="doc-to" type="datetime-local" value={validTo} onChange={(e) => setValidTo(e.target.value)} />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Switch id="doc-status" checked={status} onCheckedChange={setStatus} />
            <Label htmlFor="doc-status">启用</Label>
          </div>
          {error ? <p className="text-sm text-destructive">{error}</p> : null}
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={pending} onClick={onClose}>
            取消
          </Button>
          <Button disabled={pending || !title.trim() || (requiresContent && !content.trim())} onClick={() => void submit()}>
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
