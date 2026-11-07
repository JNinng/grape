import { useCallback, useMemo, useState, type ReactNode } from 'react'
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
import { PERM } from '@/auth/permissionCodes'
import { usePermissions } from '@/auth/permissions'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { listSessions, revokeSession, type SessionAdminDTO } from '@/api/sessionAdmin'
import type { QueryCondition, QuerySort } from '@/api/query'
import { DataTable, schemaColumns, type DataTableColumn } from '@/components/manage/data-table'
import { RowActionsMenu } from '@/components/manage/row-actions'
import { QueryFilter, defaultSortFromSchema, filterFieldsFromSchema, useListSchema } from '@/components/manage/query-filter'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Spinner } from '@/components/ui/spinner'
import { toast } from '@/components/ui/toast'

// 会话状态与存储层 sys_session.status 取值一致。
const SESSION_STATUS_DISABLED = 0

// sessionErrorHint 归一动作失败的展示文案：透传后端原始消息（会话治理
// 错误 reason 归 session.v1，文案即 biz 错误定义），无词表改写。
function sessionErrorHint(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback
}

// statusBadge 渲染状态列：0 禁用、1 启用（会话被禁用即不可用于认证）。
function statusBadge(status: number): ReactNode {
  return status === SESSION_STATUS_DISABLED ? (
    <Badge variant="outline">禁用</Badge>
  ) : (
    <Badge variant="secondary">启用</Badge>
  )
}

// revokedBadge 渲染撤销标记：is_revoked 后即使令牌未过期也不可用。
function revokedBadge(revoked: boolean): ReactNode {
  return revoked ? <Badge variant="destructive">已撤销</Badge> : <Badge variant="secondary">活跃</Badge>
}

// ---------- 撤销确认 ----------

function SessionRevokeDialog({
  session,
  onClose,
  onRevoked,
}: {
  session: SessionAdminDTO
  onClose: () => void
  onRevoked: () => void
}) {
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      // 撤销幂等：目标不存在/已撤销/已被清理均成功返回，不泄露存在性。
      await revokeSession(session.id)
      toast.success(`会话 #${session.id} 已撤销，其访问与刷新令牌一并失效`)
      onRevoked()
      onClose()
    } catch (err) {
      toast.error(`撤销失败：${sessionErrorHint(err, 'revoke session failed')}`)
      onClose()
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认撤销会话 #{session.id}？</AlertDialogTitle>
          <AlertDialogDescription>
            撤销后该会话的访问令牌与刷新令牌立即失效（即使用户端令牌尚未过期）；
            目标已撤销或不存在时同样按成功处理。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>取消</AlertDialogCancel>
          <AlertDialogAction variant="destructive" disabled={pending} onClick={(e) => { e.preventDefault(); void confirm() }}>
            撤销
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

// ---------- 行内动作 ----------

function RowActions({
  session,
  onOpen,
}: {
  session: SessionAdminDTO
  onOpen: (session: SessionAdminDTO) => void
}) {
  const { can } = usePermissions()
  if (!can(PERM.SessionRevoke)) {
    return null
  }
  return (
    <RowActionsMenu
      rowLabel={`#${session.id}`}
      info={[
        { label: '会话 ID', value: session.id },
        { label: '用户 ID', value: session.userId },
      ]}
      actions={[{ label: '撤销', destructive: true, onSelect: () => onOpen(session) }]}
    />
  )
}

// ---------- 页面 ----------

// SessionManagePage 会话治理：租户内会话列表（筛选/列定义动态响应
// GetListSchema 响应）+ 行内撤销（幂等确认动作）。
export function SessionManagePage() {
  const [filters, setFilters] = useState<QueryCondition[]>([])
  const [reloadKey, setReloadKey] = useState(0)
  const [revokeTarget, setRevokeTarget] = useState<SessionAdminDTO | null>(null)
  const { schema, error: schemaError, loading: schemaLoading, retry: retrySchema } = useListSchema('sys_session')
  const { can } = usePermissions()
  const canRevoke = can(PERM.SessionRevoke)

  const filterFields = useMemo(() => (schema === null ? [] : filterFieldsFromSchema(schema)), [schema])
  const defaultSort = useMemo<QuerySort[]>(() => (schema === null ? [] : defaultSortFromSchema(schema)), [schema])
  const pageSize = schema?.pagination?.defaultPageSize ?? 20

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // columns 动态响应 schema：页面覆写状态与撤销徽标；行操作列有撤销权限
  // 时追加在末尾。
  const columns = useMemo<DataTableColumn<SessionAdminDTO>[]>(
    () =>
      schema === null
        ? []
        : schemaColumns<SessionAdminDTO>(
            schema,
            {
              status: { header: '状态', render: (row) => statusBadge(row.status) },
              isRevoked: { header: '撤销', render: (row) => revokedBadge(row.isRevoked) },
            },
            canRevoke
              ? [
                  {
                    key: 'actions',
                    header: '操作',
                    className: 'w-16 text-right',
                    sticky: 'right' as const,
                    render: (row: SessionAdminDTO) => <RowActions session={row} onOpen={setRevokeTarget} />,
                  },
                ]
              : [],
          ),
    [schema, canRevoke],
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
          title="会话管理"
          description="租户内登录会话列表与主动撤销治理面；撤销即该会话全部令牌失效。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={() => setReloadKey((key) => key + 1)} ariaLabel="会话筛选" />}
          rowKey={(session) => session.id}
          columns={columns}
          fetchPage={listSessions}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的会话"
        />
      )}

      {revokeTarget === null ? null : (
        <SessionRevokeDialog session={revokeTarget} onClose={() => setRevokeTarget(null)} onRevoked={reload} />
      )}
    </div>
  )
}
