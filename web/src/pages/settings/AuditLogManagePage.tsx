import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { cn } from 'cn'
import { fetchOperationLog, listLoginLogs, listOperationLogs, type LoginLogDTO, type OperationLogDTO } from '@/api/auditLogs'
import type { QueryCondition, QuerySort } from '@/api/query'
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

// 审计日志页的两个列表视图与各自的动态 List schema 资源名。
type AuditTab = 'operation' | 'login'

const TAB_SCHEMA_RESOURCES: Record<AuditTab, string> = {
  operation: 'sys_operation_log',
  login: 'sys_login_log',
}

const TAB_LABELS: Record<AuditTab, string> = {
  operation: '操作日志',
  login: '登录日志',
}

// formatDateTime 渲染本地时间展示；非法时间回退原文，不伪造语义。
function formatDateTime(iso: string): string {
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString()
}

// ---------- 操作日志详情 ----------

// OperationLogDetailDialog 是操作日志详情弹窗：按 id 点查回显全字段与
// 操作前后数据快照（JSON 格式化展示）。
function OperationLogDetailDialog({
  logId,
  onClose,
}: {
  logId: string
  onClose: () => void
}) {
  const [log, setLog] = useState<OperationLogDTO | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [reloadKey, setReloadKey] = useState(0)

  // 取数：requestSeq 使迟到旧响应被丢弃，卸载后不再写状态。
  const requestSeq = useRef(0)
  useEffect(() => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setLoading(true)
    fetchOperationLog(logId)
      .then((row) => {
        if (requestSeq.current === seq) {
          setLog(row)
          setLoadError(null)
        }
      })
      .catch((err: unknown) => {
        if (requestSeq.current === seq) {
          setLoadError(err instanceof Error ? err.message : '加载失败')
        }
      })
      .finally(() => {
        if (requestSeq.current === seq) {
          setLoading(false)
        }
      })
  }, [logId, reloadKey])

  const retry = useCallback(() => setReloadKey((key) => key + 1), [])

  const prettyJSON = (value: Record<string, unknown> | undefined): string =>
    value === undefined ? '（无）' : JSON.stringify(value, null, 2)

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="lg">
        <DialogHeader>
          <DialogTitle>操作日志详情</DialogTitle>
          <DialogDescription>
            操作日志为追加写入的只读审计快照；操作前后数据为写入时的 JSON 对象。
          </DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="flex min-h-32 items-center justify-center" aria-label="加载中">
            <Spinner aria-hidden className="size-6" />
          </div>
        ) : loadError !== null ? (
          <div role="alert" className="flex items-center justify-between gap-3 rounded-lg border border-destructive-soft p-3 text-sm text-destructive">
            <span>详情加载失败：{loadError}</span>
            <Button type="button" variant="outline" size="sm" onClick={retry}>
              重试
            </Button>
          </div>
        ) : log === null ? null : (
          <div className="grid gap-3 overflow-y-auto text-sm">
            <div className="grid grid-cols-2 gap-2 md:grid-cols-4">
              <DetailCell label="操作类型" value={log.operationType} />
              <DetailCell label="操作用户" value={`${log.username} (#${log.userId})`} />
              <DetailCell label="目标表" value={log.targetTable ?? '（无）'} />
              <DetailCell label="目标 ID" value={log.targetId ?? '（无）'} />
              <DetailCell label="API 路径" value={log.apiPath ?? '（无）'} />
              <DetailCell label="HTTP 方法" value={log.httpMethod ?? '（无）'} />
              <DetailCell label="耗时" value={log.durationMs !== undefined ? `${log.durationMs} ms` : '（无）'} />
              <DetailCell label="结果码" value={log.resultCode ?? '（无）'} />
              <DetailCell label="请求 IP" value={log.ipAddress ?? '（无）'} />
              <DetailCell label="请求 ID" value={log.requestId ?? '（无）'} />
              <DetailCell label="操作时间" value={formatDateTime(log.operatedAt)} />
              <DetailCell label="结果消息" value={log.resultMsg ?? '（无）'} />
            </div>
            <div className="grid gap-1.5">
              <span className="font-medium">操作前数据</span>
              <pre aria-label="操作前数据" className="max-h-40 overflow-auto rounded-lg bg-muted p-3 font-mono text-xs">
                {prettyJSON(log.oldValue)}
              </pre>
            </div>
            <div className="grid gap-1.5">
              <span className="font-medium">操作后数据</span>
              <pre aria-label="操作后数据" className="max-h-40 overflow-auto rounded-lg bg-muted p-3 font-mono text-xs">
                {prettyJSON(log.newValue)}
              </pre>
            </div>
          </div>
        )}
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button">关闭</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// DetailCell 是详情网格的只读单元。
function DetailCell({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid gap-0.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="break-all font-mono">{value}</span>
    </div>
  )
}

// ---------- 行内动作 ----------

function OperationRowActions({ log, onOpen }: { log: OperationLogDTO; onOpen: (log: OperationLogDTO) => void }) {
  return (
    <RowActionsMenu
      rowLabel={`#${log.id}`}
      info={[
        { label: '操作类型', value: log.operationType },
        { label: '操作用户', value: log.username },
      ]}
      actions={[{ label: '详情', onSelect: () => onOpen(log) }]}
    />
  )
}

// ---------- 页面 ----------

// AuditLogManagePage 审计日志：操作日志/登录日志两个只读列表以标签切换，
// 各自筛选与列定义动态响应 GetListSchema 响应。append-only 无写动作，
// 行内动作仅操作日志的详情查看。
export function AuditLogManagePage() {
  const [tab, setTab] = useState<AuditTab>('operation')
  const [filters, setFilters] = useState<QueryCondition[]>([])
  const [reloadKey, setReloadKey] = useState(0)
  const [detailLog, setDetailLog] = useState<OperationLogDTO | null>(null)

  const { schema, error: schemaError, loading: schemaLoading, retry: retrySchema } = useListSchema(TAB_SCHEMA_RESOURCES[tab])

  // schemaReady 校验 schema 归属当前视图：切换标签后的第一帧里 hook 仍返回
  // 上一视图的响应（effect 未及重跑），直接渲染会让新视图短暂挂上旧 schema
  // 的筛选与列定义、多发一次即弃的请求——以 resource 归属作为就绪判据。
  const schemaReady = schema !== null && schema.resource === TAB_SCHEMA_RESOURCES[tab]

  const filterFields = useMemo(() => (schemaReady ? filterFieldsFromSchema(schema) : []), [schemaReady, schema])
  const defaultSort = useMemo<QuerySort[]>(() => (schemaReady ? defaultSortFromSchema(schema) : []), [schemaReady, schema])
  const pageSize = schemaReady ? schema.pagination?.defaultPageSize ?? 20 : 20

  const switchTab = useCallback((next: AuditTab) => {
    setTab(next)
    setFilters([])
    setReloadKey((key) => key + 1)
  }, [])

  // columns 动态响应 schema：页面仅覆写必要附加项；行操作列不对应查询
  // 字段，恒追加在末尾（仅操作日志视图）。
  const operationColumns = useMemo<DataTableColumn<OperationLogDTO>[]>(
    () =>
      schema === null
        ? []
        : schemaColumns<OperationLogDTO>(
            schema,
            {},
            [
              {
                key: 'actions',
                header: '操作',
                className: 'w-16 text-right',
                sticky: 'right',
                render: (log) => <OperationRowActions log={log} onOpen={setDetailLog} />,
              },
            ],
          ),
    [schema],
  )

  const loginColumns = useMemo<DataTableColumn<LoginLogDTO>[]>(
    () =>
      schema === null
        ? []
        : schemaColumns<LoginLogDTO>(
            schema,
            {
              isSuccess: { header: '结果', render: (row) => row.isSuccess ? <Badge variant="secondary">成功</Badge> : <Badge variant="outline">失败</Badge> },
            },
            [],
          ),
    [schema],
  )

  return (
    <div className="flex min-h-0 flex-1 flex-col p-6">
      {/* 标签切换：同一管理页内的两个只读视图，切换即清空筛选并重查。 */}
      <div role="tablist" aria-label="审计日志视图" className="mb-4 flex items-center gap-1">
        {(['operation', 'login'] as const).map((key) => (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={tab === key}
            onClick={() => switchTab(key)}
            className={cn(
              'rounded-lg px-3 py-1.5 text-sm font-medium transition-colors select-none',
              tab === key ? 'bg-brand-soft text-brand-text' : 'text-foreground hover:bg-muted',
            )}
          >
            {TAB_LABELS[key]}
          </button>
        ))}
      </div>

      {schemaError !== null && schemaLoading ? (
        // 错误展示跟随当前 resource 的加载轮次：滞后帧里的旧错误不渲染。
        <div className="flex min-h-32 items-center justify-center" aria-label="加载中">
          <Spinner aria-hidden className="size-6" />
        </div>
      ) : schemaError !== null ? (
        <Alert variant="destructive">
          <AlertDescription className="flex items-center justify-between gap-3">
            <span>查询能力加载失败：{schemaError}</span>
            <Button type="button" variant="outline" size="sm" onClick={retrySchema}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : !schemaReady ? (
        <div className="flex min-h-32 items-center justify-center" aria-label="加载中">
          <Spinner aria-hidden className="size-6" />
        </div>
      ) : tab === 'operation' ? (
        <DataTable
          key={tab}
          title="操作日志"
          description="租户内操作行为的追加写入审计快照，只读。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={() => setReloadKey((key) => key + 1)} ariaLabel="操作日志筛选" />}
          rowKey={(log) => log.id}
          columns={operationColumns}
          fetchPage={listOperationLogs}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的操作日志"
        />
      ) : (
        <DataTable
          key={tab}
          title="登录日志"
          description="租户内登录尝试（含失败记录）的追加写入审计快照，只读。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={() => setReloadKey((key) => key + 1)} ariaLabel="登录日志筛选" />}
          rowKey={(log) => log.id}
          columns={loginColumns}
          fetchPage={listLoginLogs}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的登录日志"
        />
      )}

      {detailLog === null ? null : (
        <OperationLogDetailDialog logId={detailLog.id} onClose={() => setDetailLog(null)} />
      )}
    </div>
  )
}
