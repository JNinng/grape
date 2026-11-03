import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ArrowDownIcon, ArrowUpIcon, ArrowUpDownIcon } from 'lucide-react'
import type { DynamicQuery, QueryCondition, QuerySet, QuerySort } from '@/api/query'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Pagination,
  PaginationContent,
  PaginationItem,
  PaginationNext,
  PaginationPrevious,
} from '@/components/ui/pagination'
import { Spinner } from '@/components/ui/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

// DataTable 是管理页通用表格：消费 query.v1 动态查询协议（filters/sorts/
// page 映射为 DynamicQuery）并自带分页与排序交互。取数由调用方注入
// （fetchPage），组件不绑定任何资源——各模块页面提供列定义与筛选产物
// （where）即可复用。
export interface DataTableColumn<T> {
  key: string
  header: string
  // sortable 填该列对应的查询字段名（小驼峰）时表头可点击排序。
  sortable?: string
  className?: string
  render: (item: T) => ReactNode
}

interface DataTableProps<T> {
  // rowKey 供 React 复用行节点：用资源主键（字符串型 ID）。
  rowKey: (item: T) => string
  columns: DataTableColumn<T>[]
  // fetchPage 执行一页查询：组件只保证按协议组装 DynamicQuery。
  fetchPage: (query: DynamicQuery) => Promise<QuerySet<T>>
  // where 是页面筛选的产物（多条 AND）；引用变化即回到第 1 页重查。
  where?: QueryCondition[]
  // defaultSort 是无交互时的排序；表头点击循环 asc → desc → 回落默认。
  defaultSort?: QuerySort[]
  pageSize?: number
  emptyText?: string
  // reloadKey 由页面在增删改后递增，触发重查：列表内容可能整体移位，
  // 与筛选/排序一致回到第 1 页。
  reloadKey?: number
  // toolbar 渲染在表格上方右对齐（如「新建」按钮）。
  toolbar?: ReactNode
}

const DEFAULT_PAGE_SIZE = 20

export function DataTable<T>({
  rowKey,
  columns,
  fetchPage,
  where,
  defaultSort,
  pageSize = DEFAULT_PAGE_SIZE,
  emptyText = '暂无数据',
  reloadKey = 0,
  toolbar,
}: DataTableProps<T>) {
  const [sort, setSort] = useState<QuerySort[]>(() => defaultSort ?? [])
  const [view, setView] = useState<{ page: number; controlsKey: string }>({
    page: 1,
    controlsKey: '',
  })
  const [rows, setRows] = useState<T[]>([])
  const [total, setTotal] = useState<string | undefined>(undefined)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // controlsKey 汇总筛选/排序/重查信号：变化即回到第 1 页（视图状态先
  // 派生归位再异步同步，保证同一轮 props 变化只发一次第 1 页的请求）。
  const whereKey = JSON.stringify(where ?? [])
  const sortKey = JSON.stringify(sort)
  const [retry, setRetry] = useState(0)
  const controlsKey = `${whereKey}|${sortKey}|${reloadKey}|${retry}`
  const page = view.controlsKey === controlsKey ? view.page : 1

  useEffect(() => {
    if (view.controlsKey !== controlsKey) {
      setView({ page: 1, controlsKey })
    }
  }, [controlsKey, view.controlsKey])

  // 取数：deps 稳定后发请求；requestSeq 使迟到旧响应被丢弃（最后发起
  // 的一次为准），卸载后不再写状态。
  const requestSeq = useRef(0)
  useEffect(() => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    const query: DynamicQuery = {
      where: where ?? [],
      sort: sort.length > 0 ? sort : undefined,
      pagination: { page, pageSize },
    }
    setLoading(true)
    fetchPage(query)
      .then((result) => {
        if (requestSeq.current !== seq) {
          return
        }
        setRows(result.items)
        setTotal(result.total)
        setHasMore(result.hasMore)
        setError(null)
      })
      .catch((err: unknown) => {
        if (requestSeq.current !== seq) {
          return
        }
        setError(err instanceof Error ? err.message : '加载失败')
      })
      .finally(() => {
        if (requestSeq.current === seq) {
          setLoading(false)
        }
      })
    // where 与 sort 经 JSON 序列化并入 controlsKey 参与依赖，避免调用方
    // 每次渲染生成新数组引用引发重查。
  }, [controlsKey, page, pageSize, fetchPage])

  // toggleSort 表头点击循环 asc → desc → 清除（回落 defaultSort）。
  const toggleSort = (field: string) => {
    setSort((current) => {
      const head = current[0]
      if (head === undefined || head.field !== field || current.length !== 1) {
        return [{ field, order: 'asc' }]
      }
      if (head.order === 'asc') {
        return [{ field, order: 'desc' }]
      }
      return defaultSort ?? []
    })
  }

  const totalNum = total !== undefined ? Number(total) : undefined
  const canPrev = page > 1 && !loading
  const canNext = hasMore && !loading
  const summary =
    totalNum !== undefined
      ? `共 ${totalNum} 条`
      : rows.length > 0
        ? `已加载 ${rows.length} 条`
        : ''

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3" aria-busy={loading}>
      {(toolbar !== undefined || summary !== '') && (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p aria-label="列表统计" className="text-sm text-muted-foreground">
            {summary}
          </p>
          {toolbar}
        </div>
      )}

      {error !== null ? (
        <Alert variant="destructive">
          <AlertDescription className="flex items-center justify-between gap-3">
            <span>列表加载失败：{error}</span>
            <Button type="button" variant="outline" size="sm" onClick={() => setRetry((n) => n + 1)}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                {columns.map((column) => {
                  const sorted = sort.length === 1 ? sort[0] : undefined
                  const isSorted = sorted !== undefined && column.sortable !== undefined && sorted.field === column.sortable
                  return (
                    <TableHead key={column.key} className={column.className} aria-sort={isSorted ? (sorted?.order === 'asc' ? 'ascending' : 'descending') : undefined}>
                      {column.sortable === undefined ? (
                        column.header
                      ) : (
                        <button
                          type="button"
                          className="flex cursor-pointer items-center gap-1 hover:text-foreground"
                          onClick={() => toggleSort(column.sortable as string)}
                        >
                          {column.header}
                          {isSorted ? (
                            sorted?.order === 'asc' ? (
                              <ArrowUpIcon aria-hidden className="size-3.5" />
                            ) : (
                              <ArrowDownIcon aria-hidden className="size-3.5" />
                            )
                          ) : (
                            <ArrowUpDownIcon aria-hidden className="size-3.5 text-muted-foreground" />
                          )}
                        </button>
                      )}
                    </TableHead>
                  )
                })}
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.length === 0 && !loading ? (
                <TableRow>
                  <TableCell colSpan={columns.length} className="h-32 text-center text-muted-foreground">
                    {emptyText}
                  </TableCell>
                </TableRow>
              ) : (
                rows.map((item) => (
                  <TableRow key={rowKey(item)}>
                    {columns.map((column) => (
                      <TableCell key={column.key} className={column.className}>
                        {column.render(item)}
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      )}

      <div className="flex items-center justify-between gap-2">
        <span className="flex items-center gap-2 text-sm text-muted-foreground">
          {loading && <Spinner aria-hidden className="size-4" />}
          <span>
            第 {page} 页{totalNum !== undefined ? ` / ${Math.max(1, Math.ceil(totalNum / pageSize))} 页` : ''}
          </span>
        </span>
        <Pagination className="w-auto justify-end">
          <PaginationContent>
            <PaginationItem>
              <PaginationPrevious
                aria-label="上一页"
                disabled={!canPrev}
                onClick={() => setView({ page: Math.max(1, page - 1), controlsKey })}
              />
            </PaginationItem>
            <PaginationItem>
              <PaginationNext
                aria-label="下一页"
                disabled={!canNext}
                onClick={() => setView({ page: page + 1, controlsKey })}
              />
            </PaginationItem>
          </PaginationContent>
        </Pagination>
      </div>
    </div>
  )
}
