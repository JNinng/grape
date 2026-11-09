import { useState, type FormEvent } from 'react'
import { ArrowLeftIcon, FileTextIcon, SearchIcon } from 'lucide-react'
import {
  getDocument,
  searchDocuments,
  type DocumentDTO,
  type LibraryDTO,
  type SearchHitDTO,
} from '@/api/knowledge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

// KnowledgeSearchDialog 是知识库管理页的检索面板：查询文本 + 可选库过滤 +
// top_k，命中展示分数/来源/锚点/正文摘要，可经来源条目 ID 查看条目原文。
// 命中正文与条目原文都是不可信文本，一律以 React 文本节点渲染。

const ALL_LIBRARIES = '__all__'
const DEFAULT_TOP_K = '5'
const SUMMARY_MAX_CHARS = 240

function summarize(content: string): string {
  const chars = Array.from(content)
  if (chars.length <= SUMMARY_MAX_CHARS) {
    return content
  }
  return `${chars.slice(0, SUMMARY_MAX_CHARS).join('')}…`
}

function errorHint(err: unknown, fallback: string): string {
  if (err instanceof Error && err.message) {
    return err.message
  }
  return fallback
}

export function KnowledgeSearchDialog({
  libraries,
  onClose,
}: {
  libraries: LibraryDTO[]
  onClose: () => void
}) {
  const [query, setQuery] = useState('')
  const [libraryId, setLibraryId] = useState(ALL_LIBRARIES)
  const [topK, setTopK] = useState(DEFAULT_TOP_K)
  const [hits, setHits] = useState<SearchHitDTO[] | null>(null)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [viewing, setViewing] = useState<DocumentDTO | null>(null)
  const [viewError, setViewError] = useState<string | null>(null)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const text = query.trim()
    if (!text) {
      return
    }
    setPending(true)
    setError(null)
    setViewError(null)
    try {
      const k = Number(topK)
      const result = await searchDocuments({
        query: text,
        libraryId: libraryId === ALL_LIBRARIES ? undefined : libraryId,
        topK: Number.isInteger(k) && k > 0 ? k : undefined,
      })
      setHits(result.items)
    } catch (err) {
      setHits(null)
      setError(errorHint(err, 'search failed'))
    } finally {
      setPending(false)
    }
  }

  const openSource = async (hit: SearchHitDTO) => {
    setViewError(null)
    try {
      setViewing(await getDocument(hit.sourceId))
    } catch (err) {
      setViewError(errorHint(err, 'load document failed'))
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="lg">
        <DialogHeader>
          <DialogTitle>检索知识库</DialogTitle>
          <DialogDescription>
            与 Agent 检索同一口径：只命中启用且处于有效期内的条目，按相似度降序返回。
          </DialogDescription>
        </DialogHeader>
        {viewing ? (
          <div className="grid gap-3">
            <div className="flex items-center justify-between gap-2">
              <h3 className="min-w-0 truncate text-sm font-semibold">{viewing.title}</h3>
              <Button variant="outline" size="sm" onClick={() => setViewing(null)}>
                <ArrowLeftIcon aria-hidden className="size-3.5" />
                返回结果
              </Button>
            </div>
            <pre
              aria-label="条目原文"
              className="max-h-96 overflow-auto whitespace-pre-wrap rounded-lg border border-border p-3 text-sm"
            >
              {viewing.content ?? ''}
            </pre>
          </div>
        ) : (
          <div className="grid gap-3">
            <form className="grid gap-3" onSubmit={(e) => void submit(e)}>
              <div className="grid gap-1.5">
                <Label htmlFor="kb-search-query">查询</Label>
                <Input id="kb-search-query" value={query} maxLength={512} onChange={(e) => setQuery(e.target.value)} />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="grid gap-1.5">
                  <Label htmlFor="kb-search-library">知识库</Label>
                  <Select value={libraryId} onValueChange={setLibraryId}>
                    <SelectTrigger id="kb-search-library" aria-label="知识库">
                      <SelectValue placeholder="全部知识库" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={ALL_LIBRARIES}>全部知识库</SelectItem>
                      {libraries.map((library) => (
                        <SelectItem key={library.id} value={library.id}>
                          {library.name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor="kb-search-topk">返回条数（最多 20）</Label>
                  <Input id="kb-search-topk" type="number" min={1} max={20} value={topK} onChange={(e) => setTopK(e.target.value)} />
                </div>
              </div>
              <div className="flex justify-end">
                <Button type="submit" disabled={pending || !query.trim()}>
                  <SearchIcon aria-hidden className="size-4" />
                  检索
                </Button>
              </div>
            </form>
            {error ? <p className="text-sm text-destructive">{error}</p> : null}
            {viewError ? <p className="text-sm text-destructive">{viewError}</p> : null}
            {hits && hits.length === 0 ? <p className="text-sm text-muted-foreground">无命中</p> : null}
            {hits && hits.length > 0 ? (
              <ul aria-label="检索结果" className="grid max-h-96 gap-2 overflow-y-auto">
                {hits.map((hit) => (
                  <li key={hit.documentId} className="rounded-lg border border-border p-3">
                    <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
                      <span className="min-w-0 truncate">
                        <span className="font-medium text-foreground">{hit.sourceName || '未命名来源'}</span>
                        {hit.anchor ? ` · ${hit.anchor}` : ''}
                      </span>
                      <span className="shrink-0">相似度 {hit.score.toFixed(3)}</span>
                    </div>
                    <p className="mt-2 whitespace-pre-wrap text-sm">{summarize(hit.content)}</p>
                    <div className="mt-2 flex justify-end">
                      <Button variant="ghost" size="sm" disabled={!hit.sourceId} onClick={() => void openSource(hit)}>
                        <FileTextIcon aria-hidden className="size-3.5" />
                        查看原文
                      </Button>
                    </div>
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
