import { useCallback, useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { PlusIcon } from 'lucide-react'
import {
  createTenant,
  deleteTenant,
  disableTenant,
  listTenants,
  restoreTenant,
  updateTenant,
  type CreateTenantInput,
  type TenantDTO,
  type UpdateTenantInput,
} from '@/api/tenants'
import type { QueryCondition, QuerySort } from '@/api/query'
import type { ListSchemaDTO } from '@/api/querySchema'
import { ApiError } from '@/api/toApiError'
import { getUser } from '@/auth/store'
import { DataTable, schemaColumns, ToolbarIconButton, type DataTableColumn } from '@/components/manage/data-table'
import { RowActionsMenu, type RowActionItem } from '@/components/manage/row-actions'
import { PERM } from '@/auth/permissionCodes'
import { usePermissions } from '@/auth/permissions'
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
import { toast } from '@/components/ui/toast'

// 租户数据状态与 sys_tenant.status 取值一致。
const STATUS_DISABLED = 0
const STATUS_ENABLED = 1

// 租户列宽与 locale 校验口径与后端用例对齐（varchar 宽度按字符数）。
const TENANT_CODE_MAX_LENGTH = 32
const TENANT_NAME_MAX_LENGTH = 256
const TENANT_CONTACT_NAME_MAX_LENGTH = 64
const TENANT_CONTACT_PHONE_MAX_LENGTH = 32
const TENANT_LOCALE_MAX_LENGTH = 10
const TENANT_LOCALE_PATTERN = /^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$/
const TENANT_MAX_USER_COUNT_MAX = 2147483647

// DialogState 是当前打开的弹窗；停用/恢复按目标租户当前状态二选一。
type DialogState =
  | { kind: 'create' }
  | { kind: 'edit'; tenant: TenantDTO }
  | { kind: 'delete'; tenant: TenantDTO }
  | { kind: 'disable'; tenant: TenantDTO }
  | { kind: 'restore'; tenant: TenantDTO }

// TENANT_HINTS 是租户域错误的可读英文提示文案；TENANT_MESSAGE_HINTS 按
// 后端消息文案匹配（消息专用条目优先），TENANT_REASON_HINTS 按 reason 码
// （tenant/v1/error_reason.proto）兜底——自身状态变更、caller 租户缺失等
// 与通用 INVALID_ARGUMENT 同 reason 的专用文案必须先于通用 reason 命中。
// 两表都未命中则原样透传，不吞掉未知错误。
const TENANT_HINTS = {
  notFound: 'tenant not found; it may have been deleted or be outside the current tenant tree',
  invalid:
    'invalid tenant; check required fields and column widths, use a BCP-47 locale such as zh-CN, and give a positive max user count',
  invalidOwnStatus: 'the current tenant status cannot be changed here; use disable or restore',
  callerMissing: 'the caller tenant is missing or deleted; sign in again and retry',
  exists: 'a tenant with the same code or name already exists',
  hasChildren: 'the tenant still has active child tenants; delete them first',
}

const TENANT_MESSAGE_HINTS: Record<string, string> = {
  'tenant not found': TENANT_HINTS.notFound,
  'invalid tenant argument': TENANT_HINTS.invalid,
  'cannot change own tenant status; use disable or restore': TENANT_HINTS.invalidOwnStatus,
  'caller tenant not found or deleted': TENANT_HINTS.callerMissing,
  'tenant already exists': TENANT_HINTS.exists,
  'tenant has active children': TENANT_HINTS.hasChildren,
}

const TENANT_REASON_HINTS: Record<string, string> = {
  TENANT_NOT_FOUND: TENANT_HINTS.notFound,
  TENANT_INVALID_ARGUMENT: TENANT_HINTS.invalid,
  TENANT_ALREADY_EXISTS: TENANT_HINTS.exists,
  TENANT_HAS_CHILDREN: TENANT_HINTS.hasChildren,
}

// tenantErrorHint 归一动作失败的展示文案：消息专用词表优先（自身状态、
// caller 缺失等与通用 INVALID_ARGUMENT 共用 reason 的文案得以区分），
// 其次按 ApiError 的稳定 reason 码兜底；都未命中则透传原始消息。
function tenantErrorHint(err: unknown, fallback: string): string {
  const message = err instanceof Error ? err.message : fallback
  const byMessage = TENANT_MESSAGE_HINTS[message]
  if (byMessage !== undefined) {
    return byMessage
  }
  const reason = err instanceof ApiError ? err.reason : undefined
  if (reason !== undefined) {
    const byReason = TENANT_REASON_HINTS[reason]
    if (byReason !== undefined) {
      return byReason
    }
  }
  return message
}

// statusBadge 渲染数据状态列：0 停用、1 启用；缺省按启用兜底（创建缺省启用）。
function statusBadge(status: number | undefined): ReactNode {
  return status === STATUS_DISABLED ? (
    <Badge variant="outline">停用</Badge>
  ) : (
    <Badge variant="secondary">启用</Badge>
  )
}

// runeLength 按字符（而非 UTF-16 码元）计数，与后端 RuneCount 的列宽
// 校验口径一致。
function runeLength(value: string): number {
  return Array.from(value).length
}

// formatTenantDate 把 RFC3339 时间截为日期部分展示：expired_at 是不带时区
// 的 timestamp，业务上是日粒度事实，展示 UTC 日期避免客户端时区偏移。
function formatTenantDate(iso: string): string {
  const match = /^(\d{4}-\d{2}-\d{2})/.exec(iso)
  return match?.[1] ?? iso
}

// formatTenantDateTime 渲染本地时间展示；非法时间回退原文，不伪造语义。
function formatTenantDateTime(iso: string): string {
  const time = new Date(iso)
  return Number.isNaN(time.getTime()) ? iso : time.toLocaleString()
}

// tenantDateInputValue 把 RFC3339 还原为 date 输入框的 YYYY-MM-DD；无法识别
// 时留空，不伪造日期。
function tenantDateInputValue(iso: string | undefined): string {
  if (iso === undefined) {
    return ''
  }
  const match = /^(\d{4}-\d{2}-\d{2})/.exec(iso)
  return match?.[1] ?? ''
}

// tenantDateRFC3339 把 date 输入框的 YYYY-MM-DD 转成当日 UTC 的 RFC3339；
// 空输入表示不设置到期时间（undefined），非法日期（如 2026-02-30）拒绝。
function tenantDateRFC3339(raw: string): { ok: true; value: string | undefined } | { ok: false } {
  const value = raw.trim()
  if (value === '') {
    return { ok: true, value: undefined }
  }
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) {
    return { ok: false }
  }
  const date = new Date(`${value}T00:00:00Z`)
  if (Number.isNaN(date.getTime()) || date.toISOString().slice(0, 10) !== value) {
    return { ok: false }
  }
  return { ok: true, value: `${value}T00:00:00Z` }
}

// tenantMaxUserCount 归一最大用户数输入：空即未设置（不限），否则按数字上送。
function tenantMaxUserCount(raw: string): number | undefined {
  const value = raw.trim()
  return value === '' ? undefined : Number(value)
}

// trimmedOrUndefined 把可空输入的空串归一为未设置（protojson optional 省略）。
function trimmedOrUndefined(raw: string): string | undefined {
  const value = raw.trim()
  return value === '' ? undefined : value
}

// TenantFormValues 是新建/编辑弹窗共享的校验输入。
interface TenantFormValues {
  code: string
  name: string
  contactName: string
  contactPhone: string
  expiredAt: string
  maxUserCount: string
  defaultLocale: string
}

// validateTenantValues 校验租户表单：必填、列宽、最大用户数正整数、locale
// 形态与到期日合法性；返回首条内联错误，null 表示通过。code 仅创建路径必填
//（更新输入不携带身份列）。
function validateTenantValues(values: TenantFormValues, requireCode: boolean): string | null {
  const code = values.code.trim()
  if (requireCode) {
    if (code === '' && values.name.trim() === '') {
      return 'code and name are required'
    }
    if (code === '') {
      return 'code is required'
    }
    if (runeLength(code) > TENANT_CODE_MAX_LENGTH) {
      return `code must be at most ${TENANT_CODE_MAX_LENGTH} characters`
    }
  }
  const name = values.name.trim()
  if (name === '') {
    return 'name is required'
  }
  if (runeLength(name) > TENANT_NAME_MAX_LENGTH) {
    return `name must be at most ${TENANT_NAME_MAX_LENGTH} characters`
  }
  if (runeLength(values.contactName.trim()) > TENANT_CONTACT_NAME_MAX_LENGTH) {
    return `contact name must be at most ${TENANT_CONTACT_NAME_MAX_LENGTH} characters`
  }
  if (runeLength(values.contactPhone.trim()) > TENANT_CONTACT_PHONE_MAX_LENGTH) {
    return `contact phone must be at most ${TENANT_CONTACT_PHONE_MAX_LENGTH} characters`
  }
  const maxUserCount = values.maxUserCount.trim()
  if (maxUserCount !== '') {
    if (!/^[1-9]\d*$/.test(maxUserCount) || Number(maxUserCount) > TENANT_MAX_USER_COUNT_MAX) {
      return 'max user count must be a positive integer'
    }
  }
  const locale = values.defaultLocale.trim()
  if (
    locale !== '' &&
    (runeLength(locale) > TENANT_LOCALE_MAX_LENGTH || !TENANT_LOCALE_PATTERN.test(locale))
  ) {
    return 'locale must be a BCP-47 tag such as zh-CN'
  }
  if (!tenantDateRFC3339(values.expiredAt).ok) {
    return 'expiry date must be a valid date'
  }
  return null
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
function DialogActions({ pending, submitLabel }: { pending: boolean; submitLabel: string }) {
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

// StatusField 是数据状态开关：可访问名保持稳定，状态词另起展示位随开关
// 联动；disabled 用于自身租户编辑（状态只能由停用/恢复维护），description
// 给出可见说明并作为开关的 aria-describedby。
function StatusField({
  id,
  status,
  onChange,
  disabled = false,
  description,
}: {
  id: string
  status: number
  onChange: (status: number) => void
  disabled?: boolean
  description?: string
}) {
  const descriptionId = `${id}-description`
  return (
    <div className="grid gap-1.5">
      <div className="flex items-center gap-2">
        <Switch
          id={id}
          checked={status === STATUS_ENABLED}
          disabled={disabled}
          aria-describedby={description === undefined ? undefined : descriptionId}
          onCheckedChange={(checked) => onChange(checked ? STATUS_ENABLED : STATUS_DISABLED)}
        />
        <Label htmlFor={id}>状态</Label>
        <span aria-live="polite" className="text-sm text-muted-foreground">
          {status === STATUS_ENABLED ? '启用' : '停用'}
        </span>
      </div>
      {description !== undefined && (
        <p id={descriptionId} className="text-xs text-muted-foreground">
          {description}
        </p>
      )}
    </div>
  )
}

// ---------- 新建租户 ----------

function TenantCreateDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [contactName, setContactName] = useState('')
  const [contactPhone, setContactPhone] = useState('')
  const [expiredAt, setExpiredAt] = useState('')
  const [maxUserCount, setMaxUserCount] = useState('')
  const [defaultLocale, setDefaultLocale] = useState('zh-CN')
  const [status, setStatus] = useState(STATUS_ENABLED)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const values = { code, name, contactName, contactPhone, expiredAt, maxUserCount, defaultLocale }
    const validationError = validateTenantValues(values, true)
    if (validationError !== null) {
      setError(validationError)
      return
    }
    const expired = tenantDateRFC3339(expiredAt)
    if (!expired.ok) {
      setError('expiry date must be a valid date')
      return
    }
    setPending(true)
    try {
      // parentId 不上送：创建时省略即固定挂在调用方（当前登录）租户下。
      const input: CreateTenantInput = {
        status,
        code: code.trim(),
        name: name.trim(),
        contactName: trimmedOrUndefined(contactName),
        contactPhone: trimmedOrUndefined(contactPhone),
        expiredAt: expired.value,
        maxUserCount: tenantMaxUserCount(maxUserCount),
        defaultLocale: trimmedOrUndefined(defaultLocale),
      }
      await createTenant(input)
      toast.success(`租户 ${name.trim()} 已创建`)
      onCreated()
      onClose()
    } catch (err) {
      setError(tenantErrorHint(err, 'create tenant failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建租户</DialogTitle>
          <DialogDescription>
            新租户固定挂在当前登录租户下；编码与名称在未删除租户间唯一，编码创建后不可变。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="tenant-create-code" label="租户编码">
            <Input id="tenant-create-code" value={code} onChange={(e) => setCode(e.target.value)} autoComplete="off" placeholder="如：demo" />
          </Field>
          <Field id="tenant-create-name" label="租户名">
            <Input id="tenant-create-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="tenant-create-contact-name" label="联系人">
            <Input id="tenant-create-contact-name" value={contactName} onChange={(e) => setContactName(e.target.value)} />
          </Field>
          <Field id="tenant-create-contact-phone" label="联系电话">
            <Input id="tenant-create-contact-phone" value={contactPhone} onChange={(e) => setContactPhone(e.target.value)} />
          </Field>
          <Field id="tenant-create-expired-at" label="到期时间">
            <Input id="tenant-create-expired-at" type="date" value={expiredAt} onChange={(e) => setExpiredAt(e.target.value)} />
          </Field>
          <Field id="tenant-create-max-user-count" label="最大用户数">
            <Input id="tenant-create-max-user-count" value={maxUserCount} onChange={(e) => setMaxUserCount(e.target.value)} inputMode="numeric" autoComplete="off" placeholder="正整数，留空不限制" />
          </Field>
          <Field id="tenant-create-default-locale" label="默认语言">
            <Input id="tenant-create-default-locale" value={defaultLocale} onChange={(e) => setDefaultLocale(e.target.value)} autoComplete="off" placeholder="BCP-47，如 zh-CN" />
          </Field>
          <StatusField id="tenant-create-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="创建" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 编辑租户 ----------

function TenantEditDialog({
  tenant,
  isSelf,
  onClose,
  onSaved,
}: {
  tenant: TenantDTO
  isSelf: boolean
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(tenant.name)
  const [contactName, setContactName] = useState(tenant.contactName ?? '')
  const [contactPhone, setContactPhone] = useState(tenant.contactPhone ?? '')
  const [expiredAt, setExpiredAt] = useState(tenantDateInputValue(tenant.expiredAt))
  const [maxUserCount, setMaxUserCount] = useState(
    tenant.maxUserCount === undefined ? '' : String(tenant.maxUserCount),
  )
  const [defaultLocale, setDefaultLocale] = useState(tenant.defaultLocale)
  const currentStatus = tenant.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED
  const [status, setStatus] = useState(currentStatus)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const values = {
      code: tenant.code,
      name,
      contactName,
      contactPhone,
      expiredAt,
      maxUserCount,
      defaultLocale,
    }
    const validationError = validateTenantValues(values, false)
    if (validationError !== null) {
      setError(validationError)
      return
    }
    const expired = tenantDateRFC3339(expiredAt)
    if (!expired.ok) {
      setError('expiry date must be a valid date')
      return
    }
    setPending(true)
    try {
      // 覆盖语义：status 必填；自身租户的状态未经停用/恢复不得变化，提交
      // 时强制沿用当前值。expiredAt/maxUserCount 省略即清空。code 是身份
      // 列、parentId 是结构列、tenantId 是归属列，前端均不上送。
      const input: UpdateTenantInput = {
        id: tenant.id,
        status: isSelf ? currentStatus : status,
        name: name.trim(),
        contactName: trimmedOrUndefined(contactName),
        contactPhone: trimmedOrUndefined(contactPhone),
        expiredAt: expired.value,
        maxUserCount: tenantMaxUserCount(maxUserCount),
        defaultLocale: trimmedOrUndefined(defaultLocale),
      }
      await updateTenant(input)
      toast.success(`租户 ${name.trim()} 已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(tenantErrorHint(err, 'update tenant failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑租户</DialogTitle>
          <DialogDescription>
            仅名称、联系人、到期时间、最大用户数、默认语言与状态可改；编码 {tenant.code} 与父级租户创建后不可变。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="tenant-edit-code" label="租户编码">
            <Input id="tenant-edit-code" value={tenant.code} disabled readOnly />
          </Field>
          <Field id="tenant-edit-name" label="租户名">
            <Input id="tenant-edit-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="tenant-edit-contact-name" label="联系人">
            <Input id="tenant-edit-contact-name" value={contactName} onChange={(e) => setContactName(e.target.value)} />
          </Field>
          <Field id="tenant-edit-contact-phone" label="联系电话">
            <Input id="tenant-edit-contact-phone" value={contactPhone} onChange={(e) => setContactPhone(e.target.value)} />
          </Field>
          <Field id="tenant-edit-expired-at" label="到期时间">
            <Input id="tenant-edit-expired-at" type="date" value={expiredAt} onChange={(e) => setExpiredAt(e.target.value)} />
          </Field>
          <Field id="tenant-edit-max-user-count" label="最大用户数">
            <Input id="tenant-edit-max-user-count" value={maxUserCount} onChange={(e) => setMaxUserCount(e.target.value)} inputMode="numeric" autoComplete="off" placeholder="正整数，留空不限制" />
          </Field>
          <Field id="tenant-edit-default-locale" label="默认语言">
            <Input id="tenant-edit-default-locale" value={defaultLocale} onChange={(e) => setDefaultLocale(e.target.value)} autoComplete="off" placeholder="BCP-47，如 zh-CN" />
          </Field>
          <StatusField
            id="tenant-edit-status"
            status={status}
            onChange={setStatus}
            disabled={isSelf}
            description={
              isSelf ? '当前登录租户不能停用/恢复自身；状态保持不变。' : undefined
            }
          />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="保存" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 停用 / 恢复确认 ----------

function TenantStatusDialog({
  tenant,
  mode,
  onClose,
  onChanged,
}: {
  tenant: TenantDTO
  mode: 'disable' | 'restore'
  onClose: () => void
  onChanged: () => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const disabling = mode === 'disable'
  const actionLabel = disabling ? '停用' : '恢复'

  const confirm = async () => {
    setPending(true)
    setError(null)
    try {
      if (disabling) {
        await disableTenant(tenant.id)
      } else {
        await restoreTenant(tenant.id)
      }
      toast.success(`租户 ${tenant.name} 已${actionLabel}`)
      onChanged()
      onClose()
    } catch (err) {
      // 失败保留确认弹窗，便于用户直接重试；错误在弹窗内联播报并同时 toast。
      const hint = tenantErrorHint(err, `${mode} tenant failed`)
      setError(`${actionLabel}失败：${hint}`)
      toast.error(`${actionLabel}失败：${hint}`)
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认{actionLabel}租户 {tenant.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            {disabling
              ? '停用仅把数据状态置为停用；当前版本不会阻断该租户用户的登录与访问，可随时恢复。'
              : '恢复后该租户回到启用状态；操作幂等，重复恢复不出错。'}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error !== null && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>取消</AlertDialogCancel>
          <AlertDialogAction disabled={pending} onClick={(e) => { e.preventDefault(); void confirm() }}>
            {actionLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

// ---------- 删除确认 ----------

function TenantDeleteDialog({
  tenant,
  onClose,
  onDeleted,
}: {
  tenant: TenantDTO
  onClose: () => void
  onDeleted: () => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const confirm = async () => {
    setPending(true)
    setError(null)
    try {
      await deleteTenant(tenant.id)
      toast.success(`租户 ${tenant.name} 已删除`)
      onDeleted()
      onClose()
    } catch (err) {
      // 失败保留确认弹窗（尤其存在子租户时需先删子租户）；错误在弹窗内联
      // 播报并同时 toast。
      const hint = tenantErrorHint(err, 'delete tenant failed')
      setError(`删除失败：${hint}`)
      toast.error(`删除失败：${hint}`)
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除租户 {tenant.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            删除为软删除；存在未删除的直接子租户时拒绝，需先自叶向根逐层删除子租户。
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error !== null && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
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

function TenantRowActions({
  tenant,
  isSelf,
  onOpen,
}: {
  tenant: TenantDTO
  isSelf: boolean
  onOpen: (state: DialogState) => void
}) {
  const { can } = usePermissions()
  const restoring = tenant.status === STATUS_DISABLED
  // 自身租户的禁用动作在菜单文字上直接标注原因（title 在禁用项上不可感知，
  // 仅作补充）；页面另有常驻说明行。
  const selfSuffix = isSelf ? '（当前登录租户）' : ''
  const actions: RowActionItem[] = []
  if (can(PERM.TenantUpdate)) {
    actions.push({ label: '编辑', onSelect: () => onOpen({ kind: 'edit', tenant }) })
    actions.push(
      restoring
        ? {
            label: `恢复${selfSuffix}`,
            disabled: isSelf,
            title: isSelf ? '不能恢复当前登录租户' : undefined,
            onSelect: () => onOpen({ kind: 'restore', tenant }),
          }
        : {
            label: `停用${selfSuffix}`,
            disabled: isSelf,
            title: isSelf ? '不能停用当前登录租户' : undefined,
            onSelect: () => onOpen({ kind: 'disable', tenant }),
          },
    )
  }
  if (can(PERM.TenantDelete)) {
    actions.push({
      label: `删除${selfSuffix}`,
      destructive: true,
      disabled: isSelf,
      title: isSelf ? '不能删除当前登录租户' : undefined,
      onSelect: () => onOpen({ kind: 'delete', tenant }),
    })
  }
  if (actions.length === 0) {
    return null
  }
  return (
    <RowActionsMenu
      rowLabel={tenant.name}
      info={[
        { label: '租户编码', value: tenant.code },
        { label: '父级租户', value: tenant.parentId ?? '' },
      ]}
      actions={actions}
    />
  )
}

// ---------- 列定义 ----------

// buildTenantColumns 以 schemaColumns 生成列定义（列序、列头、可排序性与
// 列显隐全部动态响应 ListSchema）；parentId/maxUserCount/expiredAt 由
// UI manifest 声明为 optional，这里只覆写业务列头与日期展示形态。
function buildTenantColumns(
  schema: ListSchemaDTO,
  renderActions?: (row: TenantDTO) => ReactNode,
): DataTableColumn<TenantDTO>[] {
  return schemaColumns<TenantDTO>(
    schema,
    {
      code: { header: '租户编码' },
      name: { header: '租户名' },
      status: { header: '状态', render: (row) => statusBadge(row.status) },
      parentId: { header: '父级租户' },
      maxUserCount: { header: '最大用户数' },
      expiredAt: {
        header: '到期时间',
        render: (row) => (row.expiredAt === undefined ? '—' : formatTenantDate(row.expiredAt)),
      },
      defaultLocale: { header: '默认语言' },
      updatedAt: {
        header: '更新时间',
        render: (row) => (row.updatedAt === undefined ? '—' : formatTenantDateTime(row.updatedAt)),
      },
    },
    renderActions === undefined
      ? []
      : [
          {
            key: 'actions',
            header: '操作',
            className: 'w-16 text-right',
            sticky: 'right' as const,
            render: renderActions,
          },
        ],
  )
}

// ---------- 页面 ----------

// TenantManagePage 租户管理（平台级租户实体）：列表/筛选/分页动态响应
// GetListSchema('sys_tenant')；新建租户固定挂当前租户下；编辑不含身份列；
// 停用/恢复/删除对当前登录租户禁用（后端同样拒绝）。
export function TenantManagePage() {
  const [filters, setFilters] = useState<QueryCondition[]>([])
  const [reloadKey, setReloadKey] = useState(0)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const { schema, error: schemaError, loading: schemaLoading, retry: retrySchema } = useListSchema('sys_tenant')
  const { can } = usePermissions()
  const canCreate = can(PERM.TenantCreate)
  const hasRowActions = can(PERM.TenantUpdate) || can(PERM.TenantDelete)

  const filterFields = useMemo(() => (schema === null ? [] : filterFieldsFromSchema(schema)), [schema])
  const defaultSort = useMemo<QuerySort[]>(() => (schema === null ? [] : defaultSortFromSchema(schema)), [schema])
  const pageSize = schema?.pagination?.defaultPageSize ?? 20
  const selfTenantId = getUser()?.tenantId

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  const columns = useMemo<DataTableColumn<TenantDTO>[]>(
    () =>
      schema === null
        ? []
        : buildTenantColumns(
            schema,
            hasRowActions
              ? (row) => <TenantRowActions tenant={row} isSelf={row.id === selfTenantId} onOpen={setDialog} />
              : undefined,
          ),
    [schema, selfTenantId, hasRowActions],
  )

  const renderDialog = () => {
    if (dialog === null) {
      return null
    }
    switch (dialog.kind) {
      case 'create':
        return <TenantCreateDialog onClose={() => setDialog(null)} onCreated={reload} />
      case 'edit':
        return (
          <TenantEditDialog
            tenant={dialog.tenant}
            isSelf={dialog.tenant.id === selfTenantId}
            onClose={() => setDialog(null)}
            onSaved={reload}
          />
        )
      case 'disable':
        return <TenantStatusDialog tenant={dialog.tenant} mode="disable" onClose={() => setDialog(null)} onChanged={reload} />
      case 'restore':
        return <TenantStatusDialog tenant={dialog.tenant} mode="restore" onClose={() => setDialog(null)} onChanged={reload} />
      case 'delete':
        return <TenantDeleteDialog tenant={dialog.tenant} onClose={() => setDialog(null)} onDeleted={reload} />
    }
  }

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
        <>
          {/* 自身租户动作禁用的常驻可见说明：菜单项的禁用原因同时写进
           * 菜单文字，不依赖 title 提示。 */}
          <p id="tenant-self-actions-note" className="mb-3 text-sm text-muted-foreground">
            当前登录租户不可停用、恢复或删除自身；其状态保持不变，编辑弹窗中的状态开关同样不可改。
          </p>
          <DataTable
            title="租户管理"
            description="平台级租户实体：列表覆盖当前租户树可见部分；新建租户固定挂在当前登录租户下；停用/恢复/删除对当前登录租户不可用。"
            filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={reload} ariaLabel="租户筛选" />}
            rowKey={(tenant) => tenant.id}
            columns={columns}
            fetchPage={listTenants}
            where={filters}
            schema={schema}
            reloadKey={reloadKey}
            defaultSort={defaultSort}
            pageSize={pageSize}
            emptyText="没有符合条件的租户"
            toolbar={
              canCreate ? (
                <ToolbarIconButton label="新建租户" onClick={() => setDialog({ kind: 'create' })}>
                  <PlusIcon aria-hidden className="size-4" />
                </ToolbarIconButton>
              ) : undefined
            }
          />
        </>
      )}

      {renderDialog()}
    </div>
  )
}
