import { useMemo, useState, type ReactNode } from 'react'
import type { ModelCallDTO } from '@/api/agentModelCalls'
import { listModelCalls } from '@/api/agentModelCalls'
import type { QueryCondition, QuerySort } from '@/api/query'
import type { ListSchemaDTO } from '@/api/querySchema'
import { DataTable, schemaColumns, type DataTableColumn } from '@/components/manage/data-table'
import { RowActionsMenu } from '@/components/manage/row-actions'
import { QueryFilter, defaultSortFromSchema, filterFieldsFromSchema, useListSchema } from '@/components/manage/query-filter'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Spinner } from '@/components/ui/spinner'

// MODEL_CALL_SCHEMA_RESOURCE 是模型调用留痕的动态 List schema 资源名
//（存储表名）。
const MODEL_CALL_SCHEMA_RESOURCE = 'agent_model_call_log'

// formatDateTime 渲染本地时间展示；非法时间回退原文，不伪造语义。
function formatDateTime(iso: string): string {
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString()
}

// tokensText 渲染 token 用量：未上报（缺省）给占位，不以 0 冒充。
function tokensText(row: ModelCallDTO): ReactNode {
  if (row.inputTokens === undefined && row.outputTokens === undefined) {
    return '—'
  }
  return `${row.inputTokens ?? '—'} / ${row.outputTokens ?? '—'}`
}

// scenarioLabelOf 从 schema 的静态字典构建 value→label 映射（摄取摘要 /
// 检索重排等）；schema 未就绪时回退原值展示。
function scenarioLabelOf(schema: ListSchemaDTO | null): (value: string) => string {
  const options = schema?.fields.find((field) => field.field === 'scenario')?.options ?? []
  const mapping = new Map(options.map((option) => [String(option.value), option.label]))
  return (value) => mapping.get(value) ?? value
}

// statusBadge 渲染调用终态：success 走软色对，其余（error 与词表外取值）
// fail-closed 显红（与复现视图证据行同约定）。
function statusBadge(status: string): ReactNode {
  if (status === 'success') {
    return <Badge className="bg-success-soft text-success-text">成功</Badge>
  }
  return <Badge className="bg-danger-soft text-danger-text">{status === 'error' ? '失败' : status}</Badge>
}

// DetailCell 是详情弹窗的字段格：标签 + 值（缺失给占位）。
function DetailCell({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-0.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="break-all font-mono">{children}</span>
    </div>
  )
}

// ModelCallDetailDialog 是留痕详情弹窗：列表行已携带全部展示事实（落库
// 即所显），无需二次点查；输入/输出摘要与错误原样渲染（`<pre>` 纯文本，
// 不走 innerHTML——web/README.md 约束）。
function ModelCallDetailDialog({ row, scenarioLabel, onClose }: { row: ModelCallDTO; scenarioLabel: (value: string) => string; onClose: () => void }) {
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="lg">
        <DialogHeader>
          <DialogTitle>模型调用详情 #{row.id}</DialogTitle>
          <DialogDescription>
            管线侧模型/类模型调用的留痕快照，append-only 只读；摘要为落库时已脱敏形态。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 overflow-y-auto text-sm">
          <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
            <DetailCell label="场景">{scenarioLabel(row.scenario)}</DetailCell>
            <DetailCell label="状态">{row.status === 'success' ? '成功' : row.status === 'error' ? '失败' : row.status}</DetailCell>
            <DetailCell label="模型键">{row.modelKey ?? '（无模型键）'}</DetailCell>
            <DetailCell label="耗时">{row.durationMs !== undefined ? `${row.durationMs} ms` : '（未记录）'}</DetailCell>
            <DetailCell label="token（输入/输出）">{tokensText(row)}</DetailCell>
            <DetailCell label="作用文档 ID">{row.documentId ?? '（无文档事实）'}</DetailCell>
            <DetailCell label="操作用户">{row.userId}</DetailCell>
            <DetailCell label="写入时间">{formatDateTime(row.createdAt)}</DetailCell>
          </div>
          {row.endpoint && (
            <div className="grid gap-1">
              <span className="text-xs text-muted-foreground">端点</span>
              <pre className="code-block">{row.endpoint}</pre>
            </div>
          )}
          {row.traceId && (
            <div className="grid gap-1">
              <span className="text-xs text-muted-foreground">链路 ID</span>
              <pre className="code-block">{row.traceId}</pre>
            </div>
          )}
          {row.inputSummary && (
            <div className="grid gap-1">
              <span className="text-xs text-muted-foreground">输入摘要</span>
              <pre className="code-block">{row.inputSummary}</pre>
            </div>
          )}
          {row.outputSummary && (
            <div className="grid gap-1">
              <span className="text-xs text-muted-foreground">输出摘要</span>
              <pre className="code-block">{row.outputSummary}</pre>
            </div>
          )}
          {row.error && (
            <div className="grid gap-1">
              <span className="text-xs text-muted-foreground">错误</span>
              <pre className="code-block">{row.error}</pre>
            </div>
          )}
        </div>
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline">关闭</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 行内动作 ----------

function ModelCallRowActions({ row, onOpen }: { row: ModelCallDTO; onOpen: (row: ModelCallDTO) => void }) {
  return (
    <RowActionsMenu
      rowLabel={`#${row.id}`}
      info={[
        { label: '场景', value: row.scenario },
        { label: '状态', value: row.status },
      ]}
      actions={[{ label: '详情', onSelect: () => onOpen(row) }]}
    />
  )
}

// ---------- 页面 ----------

// ModelCallManagePage 模型调用：管线侧模型/类模型调用留痕（摄取摘要/
// 块前缀/向量化/检索重排/外部解析）的只读列表。作为 append-only 证据面，
// 页面无写动作；筛选与列定义动态响应 GetListSchema 响应，行为对齐审计
// 日志（筛选、排序、分页全部走 query.v1 动态查询协议）。
export function ModelCallManagePage() {
  const [filters, setFilters] = useState<QueryCondition[]>([])
  const [reloadKey, setReloadKey] = useState(0)
  const [detailRow, setDetailRow] = useState<ModelCallDTO | null>(null)
  const { schema, error: schemaError, loading: schemaLoading, retry: retrySchema } = useListSchema(MODEL_CALL_SCHEMA_RESOURCE)

  const filterFields = useMemo(() => (schema === null ? [] : filterFieldsFromSchema(schema)), [schema])
  const defaultSort = useMemo<QuerySort[]>(() => (schema === null ? [] : defaultSortFromSchema(schema)), [schema])
  const pageSize = schema?.pagination?.defaultPageSize ?? 20
  const scenarioLabel = useMemo(() => scenarioLabelOf(schema), [schema])

  // columns 动态响应 schema：场景渲染中文标签、状态出徽标（其余列走
  // schema 缺省渲染，未上报值出破折号占位）；行操作列恒追加在末尾
  //（详情查看）。
  const columns = useMemo<DataTableColumn<ModelCallDTO>[]>(
    () =>
      schema === null
        ? []
        : schemaColumns<ModelCallDTO>(
            schema,
            {
              scenario: { header: '场景', render: (row) => scenarioLabel(row.scenario) },
              status: { header: '状态', render: (row) => statusBadge(row.status) },
            },
            [
              {
                key: 'actions',
                header: '操作',
                className: 'w-16 text-right',
                sticky: 'right' as const,
                render: (row) => <ModelCallRowActions row={row} onOpen={setDetailRow} />,
              },
            ],
          ),
    [schema, scenarioLabel],
  )

  return (
    <div className="flex min-h-0 flex-1 flex-col p-6">
      {schemaError !== null ? (
        <Alert variant="destructive">
          <AlertDescription className="flex items-center justify-between gap-3">
            <span>查询能力加载失败：{schemaError}</span>
            <Button type="button" variant="outline" size="sm" onClick={retrySchema}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : schemaLoading ? (
        <div className="flex min-h-32 items-center justify-center" aria-label="加载中">
          <Spinner aria-hidden className="size-6" />
        </div>
      ) : (
        <DataTable
          title="模型调用"
          description="管线侧模型/类模型调用留痕（摄取摘要、块前缀、向量化、检索重排、外部解析），append-only 只读；不含对话 LLM（其事实在会话事件流）。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={() => setReloadKey((key) => key + 1)} ariaLabel="模型调用筛选" />}
          rowKey={(row) => row.id}
          columns={columns}
          fetchPage={listModelCalls}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的模型调用留痕"
        />
      )}

      {detailRow === null ? null : (
        <ModelCallDetailDialog row={detailRow} scenarioLabel={scenarioLabel} onClose={() => setDetailRow(null)} />
      )}
    </div>
  )
}
