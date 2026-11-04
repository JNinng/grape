import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { PlusIcon } from 'lucide-react'
import {
  addMembers,
  createUserGroup,
  deleteUserGroup,
  listUserGroupMembers,
  listUserGroups,
  removeMembers,
  updateUserGroup,
  type UserGroupDTO,
  type UserGroupMemberDTO,
} from '@/api/userGroups'
import type { QueryCondition, QuerySort } from '@/api/query'
import { DataTable, schemaColumns, ToolbarIconButton, type DataTableColumn } from '@/components/manage/data-table'
import { RowActionsMenu } from '@/components/manage/row-actions'
import { QueryFilter, defaultSortFromSchema, filterFieldsFromSchema, useListSchema } from '@/components/manage/query-filter'
import { Alert, AlertDescription } from '@/components/ui/alert'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toast } from '@/components/ui/toast'

// 用户组状态与存储层 sys_user_group.status 取值一致。
const STATUS_DISABLED = 0
const STATUS_ENABLED = 1

// DialogState 是当前打开的行内动作弹窗：group 缺省即新建。
type DialogState =
  | { kind: 'create' }
  | { kind: 'edit'; group: UserGroupDTO }
  | { kind: 'members'; group: UserGroupDTO }
  | { kind: 'delete'; group: UserGroupDTO }

// USER_GROUP_ERROR_HINTS 把后端用户组错误（org/v1/error_reason.proto 各
// reason 的消息文案）映射为可读英文提示；词表外原样透传，不吞掉未知错误。
const USER_GROUP_ERROR_HINTS: Record<string, string> = {
  'user group not found': 'user group not found; it may have been deleted',
  'invalid user group argument': 'invalid argument; every user id must refer to an existing active user in the tenant',
  'user group already exists': 'a group with the same code or name already exists in the tenant',
}

// userGroupErrorHint 归一动作失败的展示文案：命中 reason 词表给可读提示，
// 否则透传后端原始消息。
function userGroupErrorHint(err: unknown, fallback: string): string {
  const message = err instanceof Error ? err.message : fallback
  return USER_GROUP_ERROR_HINTS[message] ?? message
}

// formatDateTime 渲染本地时间展示；非法时间回退原文，不伪造语义。
function formatDateTime(iso: string): string {
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString()
}

// statusBadge 渲染状态列：0 禁用、1 启用；缺省按启用兜底（创建缺省启用）。
function statusBadge(status: number | undefined): ReactNode {
  return status === STATUS_DISABLED ? (
    <Badge variant="outline">禁用</Badge>
  ) : (
    <Badge variant="secondary">启用</Badge>
  )
}

// Field 是表单字段布局：标签 + 控件，标签经 htmlFor 与控件关联。
function Field({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
    </div>
  )
}

// DialogActions 是弹窗底部按钮行：取消 + 提交（提交进行中禁用）。
function DialogActions({
  pending,
  submitLabel,
}: {
  pending: boolean
  submitLabel: string
}) {
  return (
    <DialogFooter>
      <DialogClose asChild>
        <Button type="button" variant="outline" disabled={pending}>
          取消
        </Button>
      </DialogClose>
      <Button type="submit" disabled={pending}>
        {submitLabel}
      </Button>
    </DialogFooter>
  )
}

// FormError 渲染表单内联错误（提交前校验与后端拒绝共用一条展示位）。
function FormError({ message }: { message: string | null }) {
  if (message === null) {
    return null
  }
  return (
    <p role="alert" className="text-sm text-destructive">
      {message}
    </p>
  )
}

// parseUserIds 把逗号/空白分隔的用户 ID 串解析为去重后的字符串 ID 数组；
// 非正整数或空输入返回 null 由调用方报错。
function parseUserIds(raw: string): string[] | null {
  const parts = raw
    .split(/[\s,，;；]+/)
    .map((part) => part.trim())
    .filter((part) => part !== '')
  if (parts.length === 0) {
    return null
  }
  const ids = new Set<string>()
  for (const part of parts) {
    if (!/^[1-9]\d*$/.test(part)) {
      return null
    }
    ids.add(part)
  }
  return [...ids]
}

// StatusField 是编辑弹窗的状态开关：可访问名保持稳定，状态词另起展示位
// 随开关联动。
function StatusField({
  id,
  status,
  onChange,
}: {
  id: string
  status: number
  onChange: (status: number) => void
}) {
  return (
    <div className="flex items-center gap-2">
      <Switch id={id} checked={status === STATUS_ENABLED} onCheckedChange={(checked) => onChange(checked ? STATUS_ENABLED : STATUS_DISABLED)} />
      <Label htmlFor={id}>状态</Label>
      <span aria-live="polite" className="text-sm text-muted-foreground">
        {status === STATUS_ENABLED ? '启用' : '禁用'}
      </span>
    </div>
  )
}

// ---------- 新建用户组 ----------

function UserGroupCreateDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [ownerUserId, setOwnerUserId] = useState('')
  const [remark, setRemark] = useState('')
  const [status, setStatus] = useState(STATUS_ENABLED)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (code.trim() === '' || name.trim() === '') {
      setError('code and name are required')
      return
    }
    const owner = ownerUserId.trim()
    if (owner !== '' && !/^[1-9]\d*$/.test(owner)) {
      setError('owner user must be a positive id')
      return
    }
    setPending(true)
    try {
      await createUserGroup({
        code: code.trim(),
        name: name.trim(),
        status,
        remark: remark.trim() === '' ? undefined : remark.trim(),
        ownerUserId: owner === '' ? undefined : owner,
      })
      toast.success(`用户组 ${name.trim()} 已创建`)
      onCreated()
      onClose()
    } catch (err) {
      setError(userGroupErrorHint(err, 'create user group failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建用户组</DialogTitle>
          <DialogDescription>
            组编码与组名在租户内唯一（未删除组间）；组编码创建后不可变，状态缺省启用。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="create-group-code" label="组编码">
            <Input id="create-group-code" value={code} onChange={(e) => setCode(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-group-name" label="组名">
            <Input id="create-group-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-group-owner" label="组长用户 ID（可选）">
            <Input id="create-group-owner" value={ownerUserId} onChange={(e) => setOwnerUserId(e.target.value)} autoComplete="off" placeholder="如：7" />
          </Field>
          <Field id="create-group-remark" label="备注">
            <Input id="create-group-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <StatusField id="create-group-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="创建" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 编辑用户组 ----------

function UserGroupEditDialog({
  group,
  onClose,
  onSaved,
}: {
  group: UserGroupDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(group.name)
  const [remark, setRemark] = useState(group.remark ?? '')
  const [status, setStatus] = useState(group.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (name.trim() === '') {
      setError('name is required')
      return
    }
    setPending(true)
    try {
      // 更新只覆盖 name/remark/status：code 不可变、组长变更走部署面
      // 专项口径，前端不上送这两个字段。
      await updateUserGroup({
        id: group.id,
        status,
        name: name.trim(),
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`用户组 ${name.trim()} 已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(userGroupErrorHint(err, 'update user group failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑用户组</DialogTitle>
          <DialogDescription>
            仅组名、备注与状态可改；组编码 {group.code} 创建后不可变。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="edit-group-code" label="组编码">
            <Input id="edit-group-code" value={group.code} disabled readOnly />
          </Field>
          <Field id="edit-group-name" label="组名">
            <Input id="edit-group-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="edit-group-remark" label="备注">
            <Input id="edit-group-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <StatusField id="edit-group-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="保存" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 成员管理 ----------

// MEMBER_PAGE_SIZE 是成员面板每页条数（后端上限 200、缺省 20）。
const MEMBER_PAGE_SIZE = 20

// UserGroupMembersDialog 是组成员面板：成员分页列表 + 批量添加（用户 ID
// 可多个）+ 逐行移除。增删成功后重查当前页；添加幂等跳过已有成员，提示
// 实际新增条数；整批拒绝（任一用户未知/跨租户/已删除）由后端返回 400，
// 前端透传文案。
function UserGroupMembersDialog({
  group,
  onClose,
}: {
  group: UserGroupDTO
  onClose: () => void
}) {
  const [page, setPage] = useState(1)
  const [members, setMembers] = useState<UserGroupMemberDTO[]>([])
  const [total, setTotal] = useState<string | undefined>(undefined)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [raw, setRaw] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)

  // 取数：page 或 reloadKey（增删后的重查信号）变化即拉当前页；requestSeq
  // 使迟到旧响应被丢弃，卸载后不再写状态。
  const requestSeq = useRef(0)
  useEffect(() => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setLoading(true)
    listUserGroupMembers(group.id, page, MEMBER_PAGE_SIZE)
      .then((set) => {
        if (requestSeq.current !== seq) {
          return
        }
        setMembers(set.items)
        setTotal(set.total)
        setHasMore(set.hasMore)
        setLoadError(null)
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
  }, [group.id, page, reloadKey])

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const userIds = parseUserIds(raw)
    if (userIds === null) {
      setError('enter at least one positive user id')
      return
    }
    setPending(true)
    try {
      const reply = await addMembers(group.id, userIds)
      toast.success(`已向组 ${group.name} 新增 ${Number(reply.added)} 个成员`)
      setRaw('')
      setError(null)
      reload()
    } catch (err) {
      setError(userGroupErrorHint(err, 'add members failed'))
    } finally {
      setPending(false)
    }
  }

  const remove = async (member: UserGroupMemberDTO) => {
    setError(null)
    try {
      const reply = await removeMembers(group.id, [member.userId])
      toast.success(`已从组 ${group.name} 移除 ${Number(reply.removed)} 个成员`)
      reload()
    } catch (err) {
      setError(userGroupErrorHint(err, 'remove member failed'))
    }
  }

  const totalNum = total !== undefined ? Number(total) : undefined

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="lg">
        <DialogHeader>
          <DialogTitle>成员管理</DialogTitle>
          <DialogDescription>
            维护用户组 {group.name}（#{group.id}）的成员；重复添加自动跳过，移除非成员不算错误。
          </DialogDescription>
        </DialogHeader>

        <form className="flex flex-wrap items-end gap-2" onSubmit={(e) => { void submit(e) }} aria-label="添加成员">
          <div className="grid min-w-0 flex-1 gap-1.5">
            <Label htmlFor="members-add-input">用户 ID</Label>
            <Input
              id="members-add-input"
              value={raw}
              onChange={(e) => setRaw(e.target.value)}
              placeholder="如：11, 12"
              autoComplete="off"
            />
          </div>
          <Button type="submit" variant="outline" disabled={pending}>
            <PlusIcon aria-hidden className="size-4" />
            添加成员
          </Button>
        </form>
        <FormError message={error} />

        {loadError !== null ? (
          <div role="alert" className="flex items-center justify-between gap-3 rounded-lg border border-destructive-soft p-3 text-sm text-destructive">
            <span>成员列表加载失败：{loadError}</span>
            <Button type="button" variant="outline" size="sm" onClick={reload}>
              重试
            </Button>
          </div>
        ) : (
          <div aria-busy={loading} className="overflow-hidden rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-20">用户 ID</TableHead>
                  <TableHead>用户名</TableHead>
                  <TableHead>昵称</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>添加时间</TableHead>
                  <TableHead className="w-16 text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {members.length === 0 && !loading ? (
                  <TableRow>
                    <TableCell colSpan={6} className="h-24 text-center text-muted-foreground">
                      暂无成员
                    </TableCell>
                  </TableRow>
                ) : (
                  members.map((member) => (
                    <TableRow key={member.userId}>
                      <TableCell>{member.userId}</TableCell>
                      <TableCell>{member.name}</TableCell>
                      <TableCell>{member.nickname}</TableCell>
                      <TableCell>{statusBadge(member.status)}</TableCell>
                      <TableCell>{formatDateTime(member.addedAt)}</TableCell>
                      <TableCell className="text-right">
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label={`移除 ${member.name}`}
                          disabled={loading}
                          onClick={() => { void remove(member) }}
                        >
                          移除
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        )}

        <DialogFooter className="items-center sm:justify-between">
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            {loading && <Spinner aria-hidden className="size-4" />}
            <span aria-label="成员统计">
              {totalNum !== undefined ? `共 ${totalNum} 个成员 · 第 ${page} 页` : ''}
            </span>
          </span>
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              aria-label="成员上一页"
              disabled={loading || page <= 1}
              onClick={() => setPage((p) => Math.max(1, p - 1))}
            >
              上一页
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              aria-label="成员下一页"
              disabled={loading || !hasMore}
              onClick={() => setPage((p) => p + 1)}
            >
              下一页
            </Button>
            <DialogClose asChild>
              <Button type="button">关闭</Button>
            </DialogClose>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 删除确认 ----------

function UserGroupDeleteDialog({
  group,
  onClose,
  onDeleted,
}: {
  group: UserGroupDTO
  onClose: () => void
  onDeleted: () => void
}) {
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      await deleteUserGroup(group.id)
      toast.success(`用户组 ${group.name} 已删除`)
      onDeleted()
      onClose()
    } catch (err) {
      toast.error(`删除失败：${userGroupErrorHint(err, 'delete user group failed')}`)
      onClose()
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除用户组 {group.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            软删除后该组不再生效，其编码与组名在租户内可被新组复用。
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>取消</AlertDialogCancel>
          <AlertDialogAction variant="destructive" disabled={pending} onClick={(e) => { e.preventDefault(); void confirm() }}>
            删除
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

// ---------- 行内动作 ----------

function RowActions({
  group,
  onOpen,
}: {
  group: UserGroupDTO
  onOpen: (state: DialogState) => void
}) {
  return (
    <RowActionsMenu
      rowLabel={group.name}
      info={[
        { label: '组名', value: group.name },
        { label: '组编码', value: group.code },
      ]}
      actions={[
        { label: '成员管理', onSelect: () => onOpen({ kind: 'members', group }) },
        { label: '编辑', onSelect: () => onOpen({ kind: 'edit', group }) },
        { label: '删除', destructive: true, onSelect: () => onOpen({ kind: 'delete', group }) },
      ]}
    />
  )
}

// ---------- 页面 ----------

// UserGroupManagePage 用户组管理：列表（筛选/列定义动态响应 GetListSchema
// 响应）+ 行内动作（成员管理/编辑/删除）+ 新建。筛选产物为 where，取数经
// DataTable 注入 listUserGroups。
export function UserGroupManagePage() {
  const [filters, setFilters] = useState<QueryCondition[]>([])
  const [reloadKey, setReloadKey] = useState(0)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const { schema, error: schemaError, loading: schemaLoading, retry: retrySchema } = useListSchema('sys_user_group')

  const filterFields = useMemo(() => (schema === null ? [] : filterFieldsFromSchema(schema)), [schema])
  const defaultSort = useMemo<QuerySort[]>(() => (schema === null ? [] : defaultSortFromSchema(schema)), [schema])
  const pageSize = schema?.pagination?.defaultPageSize ?? 20

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // columns 动态响应 schema：页面仅覆写必要附加项（状态徽标 + 长 label
  // 列头）；行操作列不对应查询字段，恒追加在末尾。
  const columns = useMemo<DataTableColumn<UserGroupDTO>[]>(
    () =>
      schema === null
        ? []
        : schemaColumns<UserGroupDTO>(
            schema,
            { status: { header: '状态', render: (g) => statusBadge(g.status) } },
            [
              {
                key: 'actions',
                header: '操作',
                className: 'w-16 text-right',
                sticky: 'right',
                render: (g) => <RowActions group={g} onOpen={setDialog} />,
              },
            ],
          ),
    [schema],
  )

  return (
    <div className="flex min-h-0 flex-1 flex-col p-6">
      {/* 落条即查：QueryFilter 的条件串直接作为 DataTable 的 where；标题与
       * 描述由表格标题行承载（描述悬停弹出）。 */}
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
          title="用户组管理"
          description="租户内用户组与成员映射的维护入口。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={() => setReloadKey((key) => key + 1)} ariaLabel="用户组筛选" />}
          rowKey={(group) => group.id}
          columns={columns}
          fetchPage={listUserGroups}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的用户组"
          toolbar={
            <ToolbarIconButton label="新建用户组" onClick={() => setDialog({ kind: 'create' })}>
              <PlusIcon aria-hidden className="size-4" />
            </ToolbarIconButton>
          }
        />
      )}

      {dialog === null ? null : dialog.kind === 'create' ? (
        <UserGroupCreateDialog onClose={() => setDialog(null)} onCreated={reload} />
      ) : dialog.kind === 'edit' ? (
        <UserGroupEditDialog group={dialog.group} onClose={() => setDialog(null)} onSaved={reload} />
      ) : dialog.kind === 'members' ? (
        <UserGroupMembersDialog group={dialog.group} onClose={() => setDialog(null)} />
      ) : (
        <UserGroupDeleteDialog group={dialog.group} onClose={() => setDialog(null)} onDeleted={reload} />
      )}
    </div>
  )
}
