import { useCallback, useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { MoreHorizontalIcon, PlusIcon, SearchIcon } from 'lucide-react'
import { assignRoles, createUser, deleteUser, listUsers, setUserPassword, transferUserOwner, updateUser, type OwnerTripleInput, type UserDTO } from '@/api/users'
import type { QueryCondition } from '@/api/query'
import { DataTable, type DataTableColumn } from '@/components/manage/data-table'
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { toast } from '@/components/ui/toast'

// 用户状态与存储层 sys_user.status 取值一致。
const STATUS_DISABLED = 0
const STATUS_ENABLED = 1

// UserDialogState 是当前打开的行内动作弹窗：user 缺省即新建。
type UserDialogState =
  | { kind: 'create' }
  | { kind: 'edit'; user: UserDTO }
  | { kind: 'password'; user: UserDTO }
  | { kind: 'roles'; user: UserDTO }
  | { kind: 'owner'; user: UserDTO }
  | { kind: 'delete'; user: UserDTO }

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

// orDash 把可空字段渲染为占位横线，避免空白单元格。
function orDash(value: string | undefined): string {
  return value === undefined || value === '' ? '—' : value
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
  submitVariant = 'default',
}: {
  pending: boolean
  submitLabel: string
  submitVariant?: 'default' | 'destructive'
}) {
  return (
    <DialogFooter>
      <DialogClose asChild>
        <Button type="button" variant="outline" disabled={pending}>
          取消
        </Button>
      </DialogClose>
      <Button type="submit" variant={submitVariant} disabled={pending}>
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

// ---------- 新建用户 ----------

function UserCreateDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [nickname, setNickname] = useState('')
  const [email, setEmail] = useState('')
  const [phone, setPhone] = useState('')
  const [remark, setRemark] = useState('')
  const [locale, setLocale] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (name.trim() === '' || nickname.trim() === '' || password === '') {
      setError('username, nickname and password are required')
      return
    }
    setPending(true)
    try {
      await createUser(
        {
          name: name.trim(),
          nickname: nickname.trim(),
          email: email.trim() === '' ? undefined : email.trim(),
          phone: phone.trim() === '' ? undefined : phone.trim(),
          remark: remark.trim() === '' ? undefined : remark.trim(),
          locale: locale.trim() === '' ? undefined : locale.trim(),
        },
        password,
      )
      toast.success(`用户 ${name.trim()} 已创建`)
      onCreated()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'create user failed')
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建用户</DialogTitle>
          <DialogDescription>初始密码服务端哈希后落库；创建后可用「转移归属」设置归属。</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="create-user-name" label="用户名">
            <Input id="create-user-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-user-nickname" label="昵称">
            <Input id="create-user-nickname" value={nickname} onChange={(e) => setNickname(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-user-password" label="初始密码">
            <Input id="create-user-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
          </Field>
          <Field id="create-user-email" label="邮箱">
            <Input id="create-user-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-user-phone" label="手机号">
            <Input id="create-user-phone" value={phone} onChange={(e) => setPhone(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-user-locale" label="语言（BCP-47）">
            <Input id="create-user-locale" value={locale} onChange={(e) => setLocale(e.target.value)} placeholder="zh-CN" />
          </Field>
          <Field id="create-user-remark" label="备注">
            <Input id="create-user-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="创建" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 编辑用户 ----------

function UserEditDialog({
  user,
  onClose,
  onSaved,
}: {
  user: UserDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(user.name)
  const [nickname, setNickname] = useState(user.nickname)
  const [status, setStatus] = useState(user.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED)
  const [email, setEmail] = useState(user.email ?? '')
  const [phone, setPhone] = useState(user.phone ?? '')
  const [remark, setRemark] = useState(user.remark ?? '')
  const [locale, setLocale] = useState(user.locale)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (name.trim() === '' || nickname.trim() === '') {
      setError('username and nickname are required')
      return
    }
    setPending(true)
    try {
      await updateUser({
        id: user.id,
        status,
        name: name.trim(),
        nickname: nickname.trim(),
        email: email.trim() === '' ? undefined : email.trim(),
        phone: phone.trim() === '' ? undefined : phone.trim(),
        remark: remark.trim() === '' ? undefined : remark.trim(),
        locale: locale.trim() === '' ? undefined : locale.trim(),
      })
      toast.success(`用户 ${name.trim()} 已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'update user failed')
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑用户</DialogTitle>
          <DialogDescription>覆盖可变字段；归属三元组经「转移归属」单独维护。</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="edit-user-name" label="用户名">
            <Input id="edit-user-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="edit-user-nickname" label="昵称">
            <Input id="edit-user-nickname" value={nickname} onChange={(e) => setNickname(e.target.value)} autoComplete="off" />
          </Field>
          <div className="flex items-center gap-2">
            {/* 开关的可访问名保持稳定，状态词另起展示位随开关联动。 */}
            <Switch id="edit-user-status" checked={status === STATUS_ENABLED} onCheckedChange={(checked) => setStatus(checked ? STATUS_ENABLED : STATUS_DISABLED)} />
            <Label htmlFor="edit-user-status">状态</Label>
            <span aria-live="polite" className="text-sm text-muted-foreground">
              {status === STATUS_ENABLED ? '启用' : '禁用'}
            </span>
          </div>
          <Field id="edit-user-email" label="邮箱">
            <Input id="edit-user-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="edit-user-phone" label="手机号">
            <Input id="edit-user-phone" value={phone} onChange={(e) => setPhone(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="edit-user-locale" label="语言（BCP-47）">
            <Input id="edit-user-locale" value={locale} onChange={(e) => setLocale(e.target.value)} placeholder="zh-CN" />
          </Field>
          <Field id="edit-user-remark" label="备注">
            <Input id="edit-user-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="保存" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 修改密码 ----------

function UserPasswordDialog({ user, onClose }: { user: UserDTO; onClose: () => void }) {
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (password === '' || password !== confirm) {
      setError('passwords must match and be non-empty')
      return
    }
    setPending(true)
    try {
      await setUserPassword(user.id, password)
      toast.success(`用户 ${user.name} 的密码已更新`)
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'set password failed')
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>修改密码</DialogTitle>
          <DialogDescription>为用户 {user.name} 设置新密码；密码不出现在任何读接口。</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="password-new" label="新密码">
            <Input id="password-new" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
          </Field>
          <Field id="password-confirm" label="确认新密码">
            <Input id="password-confirm" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="更新密码" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 分配角色 ----------

// parseRoleIds 把逗号/空白分隔的 ID 串解析为去重后的字符串 ID 数组；
// 非正整数输入返回 null 由调用方报错。
function parseRoleIds(raw: string): string[] | null {
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

function UserRolesDialog({ user, onClose }: { user: UserDTO; onClose: () => void }) {
  const [raw, setRaw] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const roleIds = parseRoleIds(raw)
    if (roleIds === null) {
      setError('enter at least one positive role id')
      return
    }
    setPending(true)
    try {
      const reply = await assignRoles(user.id, roleIds)
      toast.success(`已为用户 ${user.name} 新增 ${Number(reply.added)} 个角色`)
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'assign roles failed')
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>分配角色</DialogTitle>
          <DialogDescription>为用户 {user.name} 分配角色 ID（逗号或空白分隔）；已拥有的角色自动跳过。</DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="roles-input" label="角色 ID">
            <Input id="roles-input" value={raw} onChange={(e) => setRaw(e.target.value)} placeholder="如：1, 3" autoComplete="off" />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="分配" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 转移归属 ----------

function UserOwnerDialog({
  user,
  onClose,
  onSaved,
}: {
  user: UserDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [ownerUserId, setOwnerUserId] = useState(user.ownerUserId ?? '')
  const [ownerDeptId, setOwnerDeptId] = useState(user.ownerDeptId ?? '')
  const [ownerUserGroupId, setOwnerUserGroupId] = useState(user.ownerUserGroupId ?? '')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    // 三元组整组替换语义：留空字段即清掉该归属；填了就必须是正整数。
    const fields: [string, string][] = [
      ['owner user', ownerUserId],
      ['owner dept', ownerDeptId],
      ['owner user group', ownerUserGroupId],
    ]
    for (const [label, value] of fields) {
      if (value.trim() !== '' && !/^[1-9]\d*$/.test(value.trim())) {
        setError(`${label} must be a positive id`)
        return
      }
    }
    const to: OwnerTripleInput = {
      ownerUserId: ownerUserId.trim() === '' ? null : ownerUserId.trim(),
      ownerDeptId: ownerDeptId.trim() === '' ? null : ownerDeptId.trim(),
      ownerUserGroupId: ownerUserGroupId.trim() === '' ? null : ownerUserGroupId.trim(),
    }
    setPending(true)
    try {
      await transferUserOwner(user.id, to)
      toast.success(`用户 ${user.name} 的归属已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'transfer owner failed')
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>转移归属</DialogTitle>
          <DialogDescription>
            修改用户 {user.name} 的归属三元组；留空即清掉该归属。新归属必须在你可见的范围内。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="owner-user-id" label="归属用户 ID">
            <Input id="owner-user-id" value={ownerUserId} onChange={(e) => setOwnerUserId(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="owner-dept-id" label="归属部门 ID">
            <Input id="owner-dept-id" value={ownerDeptId} onChange={(e) => setOwnerDeptId(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="owner-user-group-id" label="归属用户组 ID">
            <Input id="owner-user-group-id" value={ownerUserGroupId} onChange={(e) => setOwnerUserGroupId(e.target.value)} autoComplete="off" />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="转移" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 删除确认 ----------

function UserDeleteDialog({
  user,
  onClose,
  onDeleted,
}: {
  user: UserDTO
  onClose: () => void
  onDeleted: () => void
}) {
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      await deleteUser(user.id)
      toast.success(`用户 ${user.name} 已删除`)
      onDeleted()
      onClose()
    } catch (err) {
      toast.error(`删除失败：${err instanceof Error ? err.message : 'delete user failed'}`)
      onClose()
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除用户 {user.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            软删除后该用户不再可见，其用户名在租户内可被新用户复用。
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
  user,
  onOpen,
}: {
  user: UserDTO
  onOpen: (state: UserDialogState) => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" variant="ghost" size="icon-sm" aria-label={`操作 ${user.name}`}>
          <MoreHorizontalIcon aria-hidden className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => onOpen({ kind: 'edit', user })}>编辑</DropdownMenuItem>
        <DropdownMenuItem onClick={() => onOpen({ kind: 'password', user })}>修改密码</DropdownMenuItem>
        <DropdownMenuItem onClick={() => onOpen({ kind: 'roles', user })}>分配角色</DropdownMenuItem>
        <DropdownMenuItem onClick={() => onOpen({ kind: 'owner', user })}>转移归属</DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onClick={() => onOpen({ kind: 'delete', user })}>
          删除
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// ---------- 页面 ----------

// UserManagePage 用户管理：列表 + 筛选 + 行内动作（编辑/改密/分配角色/
// 转移归属/删除）+ 新建。作为后续管理模块的样板页：筛选产物为 where，
// 取数经 DataTable 注入 listUsers。
export function UserManagePage() {
  const [keyword, setKeyword] = useState('')
  const [appliedKeyword, setAppliedKeyword] = useState('')
  const [statusFilter, setStatusFilter] = useState<'all' | 'enabled' | 'disabled'>('all')
  const [reloadKey, setReloadKey] = useState(0)
  const [dialog, setDialog] = useState<UserDialogState | null>(null)

  // where 是筛选表单的产物（多条 AND）：应用关键字或切换状态即生效。
  const where = useMemo<QueryCondition[]>(() => {
    const conditions: QueryCondition[] = []
    const kw = appliedKeyword.trim()
    if (kw !== '') {
      conditions.push({ field: 'name', op: 'contains', value: kw })
    }
    if (statusFilter === 'enabled') {
      conditions.push({ field: 'status', op: 'eq', value: STATUS_ENABLED })
    } else if (statusFilter === 'disabled') {
      conditions.push({ field: 'status', op: 'eq', value: STATUS_DISABLED })
    }
    return conditions
  }, [appliedKeyword, statusFilter])

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  const columns = useMemo<DataTableColumn<UserDTO>[]>(
    () => [
      { key: 'id', header: 'ID', className: 'w-16', render: (u) => u.id },
      { key: 'name', header: '用户名', render: (u) => u.name },
      { key: 'nickname', header: '昵称', render: (u) => u.nickname },
      { key: 'email', header: '邮箱', render: (u) => orDash(u.email) },
      { key: 'phone', header: '手机号', render: (u) => orDash(u.phone) },
      { key: 'status', header: '状态', render: (u) => statusBadge(u.status) },
      { key: 'createdAt', header: '创建时间', sortable: 'createdAt', render: (u) => formatDateTime(u.createdAt) },
      {
        key: 'actions',
        header: '操作',
        className: 'w-16 text-right',
        render: (u) => <RowActions user={u} onOpen={setDialog} />,
      },
    ],
    [],
  )

  const applyFilters = (event: FormEvent) => {
    event.preventDefault()
    setAppliedKeyword(keyword)
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 p-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold text-foreground">用户管理</h1>
          <p className="text-sm text-muted-foreground">租户内用户账号与数据范围归属的维护入口。</p>
        </div>
        <form className="flex flex-wrap items-center gap-2" onSubmit={applyFilters} aria-label="用户筛选">
          <Input
            aria-label="用户名关键字"
            placeholder="用户名包含…"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            className="w-56"
          />
          <Select value={statusFilter} onValueChange={(value) => setStatusFilter(value as 'all' | 'enabled' | 'disabled')}>
            <SelectTrigger aria-label="状态筛选" className="w-28">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部状态</SelectItem>
              <SelectItem value="enabled">启用</SelectItem>
              <SelectItem value="disabled">禁用</SelectItem>
            </SelectContent>
          </Select>
          <Button type="submit" variant="outline">
            <SearchIcon aria-hidden className="size-4" />
            筛选
          </Button>
        </form>
      </div>

      <DataTable
        rowKey={(user) => user.id}
        columns={columns}
        fetchPage={listUsers}
        where={where}
        reloadKey={reloadKey}
        defaultSort={[{ field: 'createdAt', order: 'desc' }]}
        emptyText="没有符合条件的用户"
        toolbar={
          <Button type="button" onClick={() => setDialog({ kind: 'create' })}>
            <PlusIcon aria-hidden className="size-4" />
            新建用户
          </Button>
        }
      />

      {dialog === null ? null : dialog.kind === 'create' ? (
        <UserCreateDialog onClose={() => setDialog(null)} onCreated={reload} />
      ) : dialog.kind === 'edit' ? (
        <UserEditDialog user={dialog.user} onClose={() => setDialog(null)} onSaved={reload} />
      ) : dialog.kind === 'password' ? (
        <UserPasswordDialog user={dialog.user} onClose={() => setDialog(null)} />
      ) : dialog.kind === 'roles' ? (
        <UserRolesDialog user={dialog.user} onClose={() => setDialog(null)} />
      ) : dialog.kind === 'owner' ? (
        <UserOwnerDialog user={dialog.user} onClose={() => setDialog(null)} onSaved={reload} />
      ) : (
        <UserDeleteDialog user={dialog.user} onClose={() => setDialog(null)} onDeleted={reload} />
      )}
    </div>
  )
}
