import { useCallback, useEffect, useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { PlusIcon } from 'lucide-react'
import {
  listAssignableRoles,
  listOwnerDeptOptions,
  listOwnerGroupOptions,
  listOwnerUserOptions,
  listUserRoles,
  setUserRoles,
  type OptionQueryInput,
  type UserRoleSetDTO,
} from '@/api/assignOptions'
import { createUser, deleteUser, listUsers, setUserPassword, transferUserOwner, updateUser, type CreateUserInput, type OwnerTripleInput, type UserDTO } from '@/api/users'
import { ApiError } from '@/api/toApiError'
import type { QueryCondition, QuerySort } from '@/api/query'
import { PERM } from '@/auth/permissionCodes'
import { usePermissions } from '@/auth/permissions'
import { DataTable, schemaColumns, ToolbarIconButton, type DataTableColumn } from '@/components/manage/data-table'
import { OptionSelect, type OptionPage } from '@/components/manage/option-select'
import { RowActionsMenu, type RowActionItem } from '@/components/manage/row-actions'
import { QueryFilter, defaultSortFromSchema, filterFieldsFromSchema, useListSchema } from '@/components/manage/query-filter'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Spinner } from '@/components/ui/spinner'
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

// ---------- 归属三元组 ----------

// OwnerTripleDraft 是归属三元组的三个单选值：空数组表示不设该归属。
interface OwnerTripleDraft {
  userId: string[]
  deptId: string[]
  groupId: string[]
}

// emptyOwnerTriple 返回空的归属草稿（用作创建弹窗的初始值）。
function emptyOwnerTriple(): OwnerTripleDraft {
  return { userId: [], deptId: [], groupId: [] }
}

// OwnerTripleSelects 渲染归属三元组的三个单选选择器：候选由对应 owner
// options 端点按操作者可见范围返回，空选择表示不设该归属；pinned 用于
// 固定保留当前值，避免清空后无法重新选回。
function OwnerTripleSelects({
  idPrefix,
  value,
  onChange,
  pinned,
}: {
  idPrefix: string
  value: OwnerTripleDraft
  onChange: (next: OwnerTripleDraft) => void
  pinned?: OwnerTripleDraft
}) {
  return (
    <>
      <Field id={`${idPrefix}-user-id`} label="归属用户">
        <OptionSelect
          id={`${idPrefix}-user-id`}
          ariaLabel="归属用户"
          mode="single"
          value={value.userId}
          onChange={(userId) => onChange({ ...value, userId })}
          loadOptions={listOwnerUserOptions}
          pinnedValues={pinned?.userId ?? []}
          placeholder="不设归属用户"
        />
      </Field>
      <Field id={`${idPrefix}-dept-id`} label="归属部门">
        <OptionSelect
          id={`${idPrefix}-dept-id`}
          ariaLabel="归属部门"
          mode="single"
          value={value.deptId}
          onChange={(deptId) => onChange({ ...value, deptId })}
          loadOptions={listOwnerDeptOptions}
          pinnedValues={pinned?.deptId ?? []}
          placeholder="不设归属部门"
        />
      </Field>
      <Field id={`${idPrefix}-user-group-id`} label="归属用户组">
        <OptionSelect
          id={`${idPrefix}-user-group-id`}
          ariaLabel="归属用户组"
          mode="single"
          value={value.groupId}
          onChange={(groupId) => onChange({ ...value, groupId })}
          loadOptions={listOwnerGroupOptions}
          pinnedValues={pinned?.groupId ?? []}
          placeholder="不设归属用户组"
        />
      </Field>
    </>
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
  const [owner, setOwner] = useState<OwnerTripleDraft>(emptyOwnerTriple)
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
      const input: CreateUserInput = {
        name: name.trim(),
        nickname: nickname.trim(),
        email: email.trim() === '' ? undefined : email.trim(),
        phone: phone.trim() === '' ? undefined : phone.trim(),
        remark: remark.trim() === '' ? undefined : remark.trim(),
        locale: locale.trim() === '' ? undefined : locale.trim(),
      }
      if (owner.userId[0] !== undefined) {
        input.ownerUserId = owner.userId[0]
      }
      if (owner.deptId[0] !== undefined) {
        input.ownerDeptId = owner.deptId[0]
      }
      if (owner.groupId[0] !== undefined) {
        input.ownerUserGroupId = owner.groupId[0]
      }
      await createUser(input, password)
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
          <DialogDescription>初始密码服务端哈希后落库；归属未选时归属用户默认为创建人。</DialogDescription>
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
          <OwnerTripleSelects idPrefix="create-owner" value={owner} onChange={setOwner} />
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

// ---------- 设置角色 ----------

// UserRolesDialog 以「管理范围内整组替换」设置目标用户的角色：当前可
// 管理角色预勾选，候选来自 ListAssignableRoles；locked 角色只显示数量。
// 版本冲突（409 USER_ROLE_SET_CHANGED）提示并重载，不重试写入。
function UserRolesDialog({ user, onClose }: { user: UserDTO; onClose: () => void }) {
  const [current, setCurrent] = useState<UserRoleSetDTO | null>(null)
  const [initialPage, setInitialPage] = useState<OptionPage | undefined>(undefined)
  const [selected, setSelected] = useState<string[]>([])
  const [selectedLabels, setSelectedLabels] = useState<Record<string, string>>({})
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [pending, setPending] = useState(false)

  const applyCurrent = (set: UserRoleSetDTO) => {
    setCurrent(set)
    setSelected(set.items.map((item) => item.value))
    setSelectedLabels(Object.fromEntries(set.items.map((item) => [item.value, item.label])))
  }

  const reloadCurrent = useCallback(async () => {
    applyCurrent(await listUserRoles(user.id))
  }, [user.id])

  // 并行加载当前角色集合与候选首页：当前集合决定预勾选与版本，候选首页
  // 作为选择器首次展开的数据（避免打开弹窗后二次等待）。
  useEffect(() => {
    let cancelled = false
    const run = async () => {
      setLoading(true)
      setError(null)
      try {
        const [set, page] = await Promise.all([
          listUserRoles(user.id),
          listAssignableRoles(user.id, { page: 1, pageSize: 20 }),
        ])
        if (cancelled) {
          return
        }
        applyCurrent(set)
        setInitialPage({ items: page.items, hasMore: page.hasMore, page: page.page > 0 ? page.page : 1 })
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'load user roles failed')
        }
      } finally {
        if (!cancelled) {
          setLoading(false)
        }
      }
    }
    void run()
    return () => {
      cancelled = true
    }
  }, [user.id])

  const loadCandidates = useCallback(
    (query: OptionQueryInput) => listAssignableRoles(user.id, query),
    [user.id],
  )

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (current === null) {
      return
    }
    setPending(true)
    setError(null)
    try {
      const result = await setUserRoles(user.id, selected, current.version)
      toast.success(`用户 ${user.name} 的角色已更新（${Number(result.items.length)} 个可管理角色）`)
      onClose()
    } catch (err) {
      if (err instanceof ApiError && err.reason === 'USER_ROLE_SET_CHANGED') {
        setError('角色集合已被他人修改，已重新加载，请确认后再次提交')
        try {
          await reloadCurrent()
        } catch (reloadErr) {
          setError(reloadErr instanceof Error ? reloadErr.message : 'reload user roles failed')
        }
        return
      }
      setError(err instanceof Error ? err.message : 'set user roles failed')
    } finally {
      setPending(false)
    }
  }

  const unmanaged = current === null ? 0 : Number(current.unmanagedCount)

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>设置角色</DialogTitle>
          <DialogDescription>
            设置用户 {user.name} 的角色；只提交你可管理的角色，范围外角色保持不变。
          </DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="flex min-h-24 items-center justify-center" aria-label="角色加载中">
            <Spinner aria-hidden className="size-6" />
          </div>
        ) : current === null ? (
          <div className="grid gap-4">
            <FormError message={error ?? 'load user roles failed'} />
            <DialogFooter>
              <DialogClose asChild>
                <Button type="button" variant="outline">
                  关闭
                </Button>
              </DialogClose>
            </DialogFooter>
          </div>
        ) : (
          <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
            {unmanaged > 0 ? (
              <p className="text-sm text-muted-foreground">
                另有 {unmanaged} 个角色不在你的管理范围，本次保存不会改动
              </p>
            ) : null}
            <Field id="user-roles-select" label="角色">
              <OptionSelect
                id="user-roles-select"
                ariaLabel="角色"
                mode="multi"
                value={selected}
                onChange={setSelected}
                loadOptions={loadCandidates}
                initialPage={initialPage}
                selectedLabels={selectedLabels}
                pinnedValues={current.items.map((item) => item.value)}
                placeholder="选择角色"
              />
            </Field>
            <FormError message={error} />
            <DialogActions pending={pending} submitLabel="保存" />
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

// ---------- 转移归属 ----------

// UserOwnerDialog 用三个单选选择器维护归属三元组：候选由对应 owner
// options 端点按操作者可见范围返回；整组替换语义，清空即落 NULL。
function UserOwnerDialog({
  user,
  onClose,
  onSaved,
}: {
  user: UserDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [owner, setOwner] = useState<OwnerTripleDraft>(() => ({
    userId: user.ownerUserId === undefined ? [] : [user.ownerUserId],
    deptId: user.ownerDeptId === undefined ? [] : [user.ownerDeptId],
    groupId: user.ownerUserGroupId === undefined ? [] : [user.ownerUserGroupId],
  }))
  const pinned: OwnerTripleDraft = {
    userId: user.ownerUserId === undefined ? [] : [user.ownerUserId],
    deptId: user.ownerDeptId === undefined ? [] : [user.ownerDeptId],
    groupId: user.ownerUserGroupId === undefined ? [] : [user.ownerUserGroupId],
  }
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const to: OwnerTripleInput = {
      ownerUserId: owner.userId[0] ?? null,
      ownerDeptId: owner.deptId[0] ?? null,
      ownerUserGroupId: owner.groupId[0] ?? null,
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
            修改用户 {user.name} 的归属三元组；清除即落空。新归属必须在你可见的范围内。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <OwnerTripleSelects idPrefix="owner" value={owner} onChange={setOwner} pinned={pinned} />
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

// RowActions 是行内操作弹框：顶部展示用户名/昵称摘要，悬停即弹出。
function RowActions({ user, onOpen }: { user: UserDTO; onOpen: (state: UserDialogState) => void }) {
  const { can } = usePermissions()
  const actions: RowActionItem[] = []
  if (can(PERM.UserUpdate)) {
    actions.push({ label: '编辑', onSelect: () => onOpen({ kind: 'edit', user }) })
  }
  if (can(PERM.UserResetPwd)) {
    actions.push({ label: '修改密码', onSelect: () => onOpen({ kind: 'password', user }) })
  }
  if (can(PERM.UserAssignRole)) {
    actions.push({ label: '设置角色', onSelect: () => onOpen({ kind: 'roles', user }) })
  }
  if (can(PERM.UserTransferOwner)) {
    actions.push({ label: '转移归属', onSelect: () => onOpen({ kind: 'owner', user }) })
  }
  if (can(PERM.UserDelete)) {
    actions.push({ label: '删除', destructive: true, onSelect: () => onOpen({ kind: 'delete', user }) })
  }
  if (actions.length === 0) {
    return null
  }
  return (
    <RowActionsMenu
      rowLabel={user.name}
      info={[
        { label: '用户名', value: user.name },
        { label: '昵称', value: user.nickname },
      ]}
      actions={actions}
    />
  )
}

// ---------- 页面 ----------

// UserManagePage 用户管理：列表 + 渐进式筛选 + 行内动作（编辑/改密/分配
// 角色/转移归属/删除）+ 新建。作为后续管理模块的样板页：取数经 DataTable
// 注入 listUsers；筛选与列定义均动态响应 GetListSchema 响应。
export function UserManagePage() {
  const [filters, setFilters] = useState<QueryCondition[]>([])
  const [reloadKey, setReloadKey] = useState(0)
  const [dialog, setDialog] = useState<UserDialogState | null>(null)
  const { schema, error: schemaError, loading: schemaLoading, retry: retrySchema } = useListSchema('sys_user')
  const { can } = usePermissions()
  const canCreate = can(PERM.UserCreate)
  const hasRowActions =
    can(PERM.UserUpdate) ||
    can(PERM.UserResetPwd) ||
    can(PERM.UserAssignRole) ||
    can(PERM.UserTransferOwner) ||
    can(PERM.UserDelete)

  const filterFields = useMemo(() => (schema === null ? [] : filterFieldsFromSchema(schema)), [schema])
  const defaultSort = useMemo<QuerySort[]>(() => (schema === null ? [] : defaultSortFromSchema(schema)), [schema])
  const pageSize = schema?.pagination?.defaultPageSize ?? 20

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // columns 动态响应 schema：列集/顺序/表头/排序/显隐全部来自 GetListSchema
  // 响应（visibility 决定列初始显隐），页面仅覆写必要附加项——schema label
  // 为长文案无法直接作列头的字段与需自定义渲染的状态徽标；行操作列不
  // 对应查询字段，有操作权限时追加在末尾。
  const columns = useMemo<DataTableColumn<UserDTO>[]>(
    () =>
      schema === null
        ? []
        : schemaColumns<UserDTO>(
            schema,
            {
              status: { header: '状态', render: (u) => statusBadge(u.status) },
              locale: { header: '语言' },
            },
            hasRowActions
              ? [
                  {
                    key: 'actions',
                    header: '操作',
                    className: 'w-16 text-right',
                    sticky: 'right' as const,
                    render: (u: UserDTO) => <RowActions user={u} onOpen={setDialog} />,
                  },
                ]
              : [],
          ),
    [schema, hasRowActions],
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
          title="用户管理"
          description="租户内用户账号与数据范围归属的维护入口。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={() => setReloadKey((key) => key + 1)} ariaLabel="用户筛选" />}
          rowKey={(user) => user.id}
          columns={columns}
          fetchPage={listUsers}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的用户"
          toolbar={
            canCreate ? (
              <ToolbarIconButton label="新建用户" onClick={() => setDialog({ kind: 'create' })}>
                <PlusIcon aria-hidden className="size-4" />
              </ToolbarIconButton>
            ) : undefined
          }
        />
      )}

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
