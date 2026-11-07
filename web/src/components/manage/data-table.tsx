import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowDownIcon, ArrowUpIcon, ArrowUpDownIcon, Columns3Icon, InfoIcon, XIcon } from 'lucide-react'
import type { DynamicQuery, QueryCondition, QuerySet, QuerySort } from '@/api/query'
import type { ListSchemaDTO } from '@/api/querySchema'
import { cn } from 'cn'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Pagination,
  PaginationContent,
  PaginationItem,
  PaginationNext,
  PaginationPrevious,
} from '@/components/ui/pagination'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
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
  // field 是该列对应的查询字段名（小驼峰）：声明后受 schema 约束——
  // 仅 schema 声明 sortable 的字段可点表头排序，列头显隐由 schema 的
  // visibility（always / optional / hidden）驱动；未声明 field（或 schema
  // 未下发）的列恒可见。
  field?: string
  // sortable 填该列对应的查询字段名（小驼峰）时表头可点击排序；最终以
  // schema 的 sortable 声明为准（两者同时声明且 schema 允许才生效）。
  sortable?: string
  // sticky 为 'right' 时该列吸附在表格右侧（行操作列惯例）：横向滚动时
  // 列头与单元格恒可见，底色取卡片色遮住滚过的列。
  sticky?: 'right'
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
  // schema 是 GetListSchema 响应：下发时排序门控、列显隐与每页上限均以
  // 其为准（契约：未声明 sortable 不可排序，visibility 决定列初始显隐）。
  schema?: ListSchemaDTO | null
  // title 是表格标题（展示在表格左上），description 悬停标题弹出。
  title?: string
  description?: string
  // filters 渲染在标题行右侧（如 QueryFilter），先于列设置与 toolbar。
  filters?: ReactNode
  // defaultSort 是无交互时的排序；表头点击循环 asc → desc → 回落默认。
  defaultSort?: QuerySort[]
  // pageSize 是初始页大小：运行期由底栏的页大小选择器接管（选项受
  // schema 的 maxPageSize 钳制）。
  pageSize?: number
  emptyText?: string
  // reloadKey 由页面在增删改后递增，触发重查：列表内容可能整体移位，
  // 与筛选/排序一致回到第 1 页。
  reloadKey?: number
  // toolbar 渲染在标题行右端（如「新建」图标按钮）。
  toolbar?: ReactNode
}

const DEFAULT_PAGE_SIZE = 20

// SchemaColumnOverride 是页面对单个 schema 字段列的覆写：header 可给出
// 精炼列头（schema label 常为列注释原文，偏长），render 提供徽标/格式化
// 等自定义呈现，className 调整列宽。未覆写的维度全部取 schema。
export interface SchemaColumnOverride<T> {
  header?: string
  className?: string
  render?: (item: T) => ReactNode
}

// formatSchemaDateTime 渲染本地时间展示；非法时间回退原文，不伪造语义。
function formatSchemaDateTime(iso: string): string {
  const time = new Date(iso)
  return Number.isNaN(time.getTime()) ? iso : time.toLocaleString()
}

// renderSchemaValue 是 schema 列的缺省渲染：可空字段空值出破折号占位，
// datetime/date 格式化本地时间，boolean 出是/否，其余按字符串展示。
function renderSchemaValue<T>(field: ListSchemaDTO['fields'][number], item: T): ReactNode {
  const raw = (item as unknown as Record<string, unknown>)[field.field]
  if (raw === undefined || raw === null || raw === '') {
    return '—'
  }
  if (field.dataType === 'datetime' || field.dataType === 'date') {
    return formatSchemaDateTime(String(raw))
  }
  if (field.dataType === 'boolean') {
    return raw === true || raw === 'true' ? '是' : '否'
  }
  return String(raw)
}

// schemaColumns 由 ListSchema 生成列定义，使表格列集/顺序/表头/排序/
// 显隐全部动态响应 schema：非 @internal 字段逐列输出（schema 顺序即列序，
// 含 op 集为空的纯投影列），列头取 schema label，sortable 字段带排序声明
// （DataTable 仍按 schema 门控）。页面经 overrides 覆写展示细节，
// extraColumns（如行操作列）恒追加在末尾且不参与显隐。列初始显隐由
// visibility 在 DataTable 内统一处理，不在此处过滤。
export function schemaColumns<T>(
  schema: ListSchemaDTO,
  overrides: Record<string, SchemaColumnOverride<T>> = {},
  extraColumns: DataTableColumn<T>[] = [],
): DataTableColumn<T>[] {
  const columns = schema.fields.map((field): DataTableColumn<T> => {
    const override = overrides[field.field]
    return {
      key: field.field,
      field: field.field,
      header: override?.header ?? field.label,
      className: override?.className,
      sortable: field.sortable === true ? field.field : undefined,
      render: override?.render ?? ((item: T) => renderSchemaValue(field, item)),
    }
  })
  return [...columns, ...extraColumns]
}

export function DataTable<T>({
  rowKey,
  columns,
  fetchPage,
  where,
  schema,
  title,
  description,
  filters,
  defaultSort,
  pageSize = DEFAULT_PAGE_SIZE,
  emptyText = '暂无数据',
  reloadKey = 0,
  toolbar,
}: DataTableProps<T>) {
  const [sort, setSort] = useState<QuerySort[]>(() => defaultSort ?? [])
  // visibilityOverrides 是用户经「列设置」对列显隐的覆盖（按列 key）；
  // 未覆盖时按 schema 的 visibility 缺省：optional 显示、hidden 收起、
  // always 恒显示（不可切换）。
  const [visibilityOverrides, setVisibilityOverrides] = useState<ReadonlyMap<string, boolean>>(new Map())
  // pageSizeState 是运行期页大小：初值取 pageSize prop（schema 默认值），
  // 之后由底栏选择器接管。
  const [pageSizeState, setPageSizeState] = useState(pageSize)
  const [view, setView] = useState<{ page: number; controlsKey: string }>({
    page: 1,
    controlsKey: '',
  })
  const [rows, setRows] = useState<T[]>([])
  const [total, setTotal] = useState<string | undefined>(undefined)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // fieldCaps 以 schema 字段键索引能力声明；未下发 schema 时不施加约束。
  const fieldCaps = useMemo(() => {
    const map = new Map<string, ListSchemaDTO['fields'][number]>()
    for (const field of schema?.fields ?? []) {
      map.set(field.field, field)
    }
    return map
  }, [schema])

  // visibilityOf 返回列的契约显隐；未声明 field 或 schema 未下发时按恒显示
  // 处理（页面自定义列不受 schema 门控）。
  const visibilityOf = useCallback(
    (column: DataTableColumn<T>): 'always' | 'optional' | 'hidden' => {
      if (column.field === undefined) {
        return 'always'
      }
      const caps = fieldCaps.get(column.field)
      if (caps === undefined) {
        return 'always'
      }
      return caps.visibility ?? 'optional'
    },
    [fieldCaps],
  )

  // isColumnVisible 应用用户覆盖与契约缺省。
  const isColumnVisible = useCallback(
    (column: DataTableColumn<T>): boolean => {
      const visibility = visibilityOf(column)
      if (visibility === 'always') {
        return true
      }
      const override = visibilityOverrides.get(column.key)
      if (override !== undefined) {
        return override
      }
      return visibility === 'optional'
    },
    [visibilityOf, visibilityOverrides],
  )

  const visibleColumns = useMemo(() => columns.filter(isColumnVisible), [columns, isColumnVisible])

  // toggleableColumns 是可显隐交互的列（列设置菜单项）：optional 与 hidden；
  // always 固定显示，不进入菜单。
  const toggleableColumns = useMemo(
    () =>
      columns.filter((column) => {
        const visibility = visibilityOf(column)
        return visibility === 'optional' || visibility === 'hidden'
      }),
    [columns, visibilityOf],
  )

  // canSortColumn：schema 下发时以 sortable 声明为准，未下发时沿用列声明。
  const canSortColumn = (column: DataTableColumn<T>): boolean => {
    if (column.sortable === undefined) {
      return false
    }
    return schema === undefined || schema === null ? true : fieldCaps.get(column.sortable)?.sortable === true
  }

  // effectivePageSize 受 schema 的 maxPageSize 钳制（协议上限服务端强制）。
  const effectivePageSize = Math.max(1, Math.min(pageSizeState, schema?.pagination?.maxPageSize ?? pageSizeState))

  // pageSizeOptions 是页大小选择器的候选：常用档位并入当前值与 schema
  // 默认值，超出 maxPageSize 的档位剔除（协议上限服务端强制）。
  const pageSizeOptions = useMemo(() => {
    const max = schema?.pagination?.maxPageSize
    const candidates = new Set<number>([10, 20, 50, 100, effectivePageSize])
    if (schema?.pagination?.defaultPageSize !== undefined) {
      candidates.add(schema.pagination.defaultPageSize)
    }
    return [...candidates]
      .filter((size) => max === undefined || size <= max)
      .sort((a, b) => a - b)
  }, [schema, effectivePageSize])

  // controlsKey 汇总筛选/排序/重查/页大小信号：变化即回到第 1 页（视图状
  // 态先派生归位再异步同步，保证同一轮 props 变化只发一次第 1 页的请求）。
  const whereKey = JSON.stringify(where ?? [])
  const sortKey = JSON.stringify(sort)
  const [retry, setRetry] = useState(0)
  const controlsKey = `${whereKey}|${sortKey}|${reloadKey}|${retry}|${effectivePageSize}`
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
      pagination: { page, pageSize: effectivePageSize },
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
  }, [controlsKey, page, effectivePageSize, fetchPage])

  // toggleSort 表头点击循环 asc → desc → 回落 defaultSort；默认排序即该
  // 字段时（如 createdAt desc）从默认点击进入 asc，避免 default→default
  // 的空转。
  const toggleSort = (field: string) => {
    setSort((current) => {
      const head = current[0]
      if (head === undefined || head.field !== field || current.length !== 1) {
        return [{ field, order: 'asc' }]
      }
      if (head.order === 'asc') {
        return [{ field, order: 'desc' }]
      }
      const defaultHead = defaultSort?.[0]
      const atDefault = defaultHead !== undefined && defaultHead.field === field && head.order === (defaultHead.order ?? 'asc')
      if (atDefault) {
        return [{ field, order: 'asc' }]
      }
      return defaultSort ?? []
    })
  }

  const totalNum = total !== undefined ? Number(total) : undefined
  const canPrev = page > 1 && !loading
  const canNext = hasMore && !loading
  // summary 是底栏左侧的总量文案；页码紧挨分页按钮展示在右侧。
  const summary =
    totalNum !== undefined
      ? `共 ${totalNum} 条`
      : rows.length > 0
        ? `已加载 ${rows.length} 条`
        : ''
  const pageLabel =
    totalNum !== undefined
      ? `第 ${page} / ${Math.max(1, Math.ceil(totalNum / effectivePageSize))} 页`
      : `第 ${page} 页`
  // sortActive 表示用户已施加排序（偏离默认排序）：出「清除排序」入口。
  const sortActive = JSON.stringify(sort) !== JSON.stringify(defaultSort ?? [])
  const stickyClass = (column: DataTableColumn<T>): string | undefined =>
    column.sticky === 'right' ? 'sticky right-0 bg-card' : undefined

  return (
    <TooltipProvider>
      <div className="flex min-h-0 flex-1 flex-col gap-3" aria-busy={loading}>
        {(title !== undefined || filters !== undefined || toolbar !== undefined || toggleableColumns.length > 0 || sortActive) && (
          <div className="flex items-center justify-between gap-2">
            {title !== undefined ? (
              <div className="flex items-center gap-1">
                <h2 className="text-lg font-semibold text-foreground">{title}</h2>
                {description !== undefined && (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span
                        tabIndex={0}
                        role="button"
                        aria-label={`关于${title}`}
                        className="cursor-help rounded-full p-0.5 text-muted-foreground hover:text-foreground"
                      >
                        <InfoIcon aria-hidden className="size-4" />
                      </span>
                    </TooltipTrigger>
                    <TooltipContent className="max-w-72">{description}</TooltipContent>
                  </Tooltip>
                )}
              </div>
            ) : (
              <span />
            )}
            <div className="flex min-w-0 items-center gap-2">
              {filters}
              {sortActive && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button type="button" variant="ghost" size="icon-sm" aria-label="清除排序" onClick={() => setSort(defaultSort ?? [])}>
                      <XIcon aria-hidden className="size-4" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>清除排序</TooltipContent>
                </Tooltip>
              )}
              {toggleableColumns.length > 0 && (
                <DropdownMenu>
                  {/* 官方嵌套：Tooltip 在外、两个 trigger 以 asChild 链式
                   * 合并到按钮，保证 DropdownMenuTrigger 的 ref 与事件不丢。 */}
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <DropdownMenuTrigger asChild>
                        <Button type="button" variant="outline" size="icon-sm" aria-label="列设置">
                          <Columns3Icon aria-hidden className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                    </TooltipTrigger>
                    <TooltipContent>列设置</TooltipContent>
                  </Tooltip>
                  <DropdownMenuContent align="end">
                    <DropdownMenuLabel>显示列</DropdownMenuLabel>
                    <DropdownMenuSeparator />
                    {toggleableColumns.map((column) => (
                      <DropdownMenuCheckboxItem
                        key={column.key}
                        checked={isColumnVisible(column)}
                        onSelect={(event) => event.preventDefault()}
                        onCheckedChange={(checked) => {
                          setVisibilityOverrides((current) => {
                            const next = new Map(current)
                            next.set(column.key, checked)
                            return next
                          })
                        }}
                      >
                        {column.header}
                      </DropdownMenuCheckboxItem>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
              {toolbar}
            </div>
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
                {visibleColumns.map((column) => {
                  const sortable = canSortColumn(column)
                  const sorted = sort.length === 1 ? sort[0] : undefined
                  const isSorted = sorted !== undefined && column.sortable !== undefined && sorted.field === column.sortable
                  return (
                    <TableHead key={column.key} className={cn(column.className, stickyClass(column))} aria-sort={isSorted ? (sorted?.order === 'asc' ? 'ascending' : 'descending') : undefined}>
                      {!sortable ? (
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
                  <TableCell colSpan={visibleColumns.length} className="h-32 text-center text-muted-foreground">
                    {emptyText}
                  </TableCell>
                </TableRow>
              ) : (
                rows.map((item) => (
                  <TableRow key={rowKey(item)}>
                    {visibleColumns.map((column) => (
                      <TableCell key={column.key} className={cn(column.className, stickyClass(column))}>
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
        <span className="flex items-center gap-2 text-sm text-muted-foreground" aria-label="列表统计">
          {loading && <Spinner aria-hidden className="size-4" />}
          <span>{summary}</span>
        </span>
        <div className="flex items-center gap-2">
          <span className="text-sm text-muted-foreground">{pageLabel}</span>
          <Select value={String(effectivePageSize)} onValueChange={(value) => setPageSizeState(Number(value))}>
            <SelectTrigger aria-label="每页条数" size="sm" className="w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {pageSizeOptions.map((size) => (
                <SelectItem key={size} value={String(size)}>
                  {size} 条/页
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
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
    </div>
    </TooltipProvider>
  )
}

// ToolbarIconButton 是表格标题行的图标工具按钮：主题色实底（新建是页面
// 主操作），图标示形、tooltip 与 aria-label 同源出文案。
export function ToolbarIconButton({ label, onClick, disabled, children }: { label: string; onClick?: () => void; disabled?: boolean; children: ReactNode }) {
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button type="button" variant="default" size="icon-sm" aria-label={label} onClick={onClick} disabled={disabled}>
            {children}
          </Button>
        </TooltipTrigger>
        <TooltipContent>{label}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
