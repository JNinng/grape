import { useCallback, useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { PlusIcon } from 'lucide-react'
import {
  createConfig,
  createFeatureFlag,
  createTenantConfig,
  createTranslation,
  deleteConfig,
  deleteFeatureFlag,
  deleteTenantConfig,
  deleteTranslation,
  listConfigs,
  listFeatureFlags,
  listTenantConfigs,
  listTranslations,
  updateConfig,
  updateFeatureFlag,
  updateTenantConfig,
  updateTranslation,
  type ConfigDTO,
  type FeatureFlagDTO,
  type TenantConfigDTO,
  type TranslationDTO,
} from '@/api/configs'
import type { QueryCondition, QuerySort } from '@/api/query'
import type { ListSchemaDTO } from '@/api/querySchema'
import { ApiError } from '@/api/toApiError'
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import { toast } from '@/components/ui/toast'

// 配置族数据状态与存储层 status 取值一致。
const STATUS_DISABLED = 0
const STATUS_ENABLED = 1

// CONFIG_VALUE_TYPES 是 value_type 的白名单（与后端用例校验同一清单）。
const CONFIG_VALUE_TYPES = ['string', 'integer', 'boolean', 'float', 'json'] as const

// TRANSLATION_LOCALE_PATTERN 与后端用例的 BCP-47 基本形态校验同口径。
const TRANSLATION_LOCALE_PATTERN = /^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$/
const TRANSLATION_LOCALE_MAX_LENGTH = 10
// TRANSLATION_ROW_ID_MAX 是 int64 上界：rowId 以字符串承载，超出即后端
// 拒绝，前端提交前以内联错误拦截。
const TRANSLATION_ROW_ID_MAX = 9223372036854775807n

// ConfigTab 是配置管理页的四个资源视图（全局/租户/开关/翻译）。
type ConfigTab = 'config' | 'tenant-config' | 'feature-flag' | 'translation'

const TAB_ORDER: ConfigTab[] = ['config', 'tenant-config', 'feature-flag', 'translation']

const TAB_SCHEMA_RESOURCES: Record<ConfigTab, string> = {
  config: 'sys_config',
  'tenant-config': 'sys_tenant_config',
  'feature-flag': 'sys_feature_flag',
  translation: 'sys_translation',
}

const TAB_LABELS: Record<ConfigTab, string> = {
  config: '全局配置',
  'tenant-config': '租户配置',
  'feature-flag': '功能开关',
  translation: '翻译',
}

// ConfigScope 区分全局配置与租户配置：除接口不同，表单与列定义同构。
type ConfigScope = 'global' | 'tenant'

const SCOPE_LABELS: Record<ConfigScope, string> = {
  global: '全局配置',
  tenant: '租户配置',
}

// DialogState 是当前打开的弹窗；kind 与资源视图一一对应。
type DialogState =
  | { kind: 'config-create' }
  | { kind: 'config-edit'; config: ConfigDTO }
  | { kind: 'config-delete'; config: ConfigDTO }
  | { kind: 'tenant-create' }
  | { kind: 'tenant-edit'; config: TenantConfigDTO }
  | { kind: 'tenant-delete'; config: TenantConfigDTO }
  | { kind: 'flag-create' }
  | { kind: 'flag-edit'; flag: FeatureFlagDTO }
  | { kind: 'flag-delete'; flag: FeatureFlagDTO }
  | { kind: 'translation-create' }
  | { kind: 'translation-edit'; translation: TranslationDTO }
  | { kind: 'translation-delete'; translation: TranslationDTO }

// CONFIG_HINTS 是配置域错误的可读英文提示文案；CONFIG_ERROR_HINTS 以
// reason 码（config/v1/error_reason.proto）与 biz 错误消息文案两条键指向
// 同一提示：reason 码稳定优先兜底，消息文案随 Kratos 包装变化时仍可命中；
// 两表都未命中则原样透传，不吞掉未知错误。
const CONFIG_HINTS = {
  configNotFound: 'global config not found; it may have been deleted',
  configInvalid:
    'invalid global config; name and config key are required, and value type must be one of string/integer/boolean/float/json',
  configExists: 'a global config with the same key already exists',
  configPlatformRequired: 'only the platform (root) tenant can modify global configs',
  tenantConfigNotFound: 'tenant config not found; it may have been deleted or belong to another tenant',
  tenantConfigInvalid:
    'invalid tenant config; name and config key are required, and value type must be one of string/integer/boolean/float/json',
  tenantConfigExists: 'a tenant config with the same key already exists in this tenant',
  featureFlagNotFound: 'feature flag not found; it may have been deleted or belong to another tenant',
  featureFlagInvalid: 'invalid feature flag; name and flag key are required and status must be 0 or 1',
  featureFlagExists: 'a feature flag with the same key already exists in this tenant',
  translationNotFound: 'translation not found; it may have been deleted or belong to another tenant',
  translationInvalid:
    'invalid translation; table name, column name, locale and value are required, row id must be a positive int64 and locale must be a BCP-47 tag such as zh-CN',
  translationExists: 'a translation for the same table, row, column and locale already exists in this tenant',
}

const CONFIG_ERROR_HINTS: Record<string, string> = {
  CONFIG_NOT_FOUND: CONFIG_HINTS.configNotFound,
  CONFIG_INVALID_ARGUMENT: CONFIG_HINTS.configInvalid,
  CONFIG_ALREADY_EXISTS: CONFIG_HINTS.configExists,
  CONFIG_PLATFORM_REQUIRED: CONFIG_HINTS.configPlatformRequired,
  TENANT_CONFIG_NOT_FOUND: CONFIG_HINTS.tenantConfigNotFound,
  TENANT_CONFIG_INVALID_ARGUMENT: CONFIG_HINTS.tenantConfigInvalid,
  TENANT_CONFIG_ALREADY_EXISTS: CONFIG_HINTS.tenantConfigExists,
  FEATURE_FLAG_NOT_FOUND: CONFIG_HINTS.featureFlagNotFound,
  FEATURE_FLAG_INVALID_ARGUMENT: CONFIG_HINTS.featureFlagInvalid,
  FEATURE_FLAG_ALREADY_EXISTS: CONFIG_HINTS.featureFlagExists,
  TRANSLATION_NOT_FOUND: CONFIG_HINTS.translationNotFound,
  TRANSLATION_INVALID_ARGUMENT: CONFIG_HINTS.translationInvalid,
  TRANSLATION_ALREADY_EXISTS: CONFIG_HINTS.translationExists,
  'config not found': CONFIG_HINTS.configNotFound,
  'invalid config argument': CONFIG_HINTS.configInvalid,
  'config already exists': CONFIG_HINTS.configExists,
  'platform tenant required': CONFIG_HINTS.configPlatformRequired,
  'tenant config not found': CONFIG_HINTS.tenantConfigNotFound,
  'invalid tenant config argument': CONFIG_HINTS.tenantConfigInvalid,
  'tenant config already exists': CONFIG_HINTS.tenantConfigExists,
  'feature flag not found': CONFIG_HINTS.featureFlagNotFound,
  'invalid feature flag argument': CONFIG_HINTS.featureFlagInvalid,
  'feature flag already exists': CONFIG_HINTS.featureFlagExists,
  'translation not found': CONFIG_HINTS.translationNotFound,
  'invalid translation argument': CONFIG_HINTS.translationInvalid,
  'translation already exists': CONFIG_HINTS.translationExists,
}

// configErrorHint 归一动作失败的展示文案：优先按 ApiError 的稳定 reason 码
// 兜底，其次按后端消息文案查词表；两表都未命中则透传原始消息。
function configErrorHint(err: unknown, fallback: string): string {
  const reason = err instanceof ApiError ? err.reason : undefined
  if (reason !== undefined) {
    const byReason = CONFIG_ERROR_HINTS[reason]
    if (byReason !== undefined) {
      return byReason
    }
  }
  const message = err instanceof Error ? err.message : fallback
  return CONFIG_ERROR_HINTS[message] ?? message
}

// statusBadge 渲染数据状态列：0 禁用、1 启用；缺省按启用兜底（创建缺省启用）。
function statusBadge(status: number | undefined): ReactNode {
  return status === STATUS_DISABLED ? (
    <Badge variant="outline">禁用</Badge>
  ) : (
    <Badge variant="secondary">启用</Badge>
  )
}

// enabledBadge 渲染功能开关的业务取值（is_enabled）：未设置为 false。
function enabledBadge(isEnabled: boolean | undefined): ReactNode {
  return isEnabled === true ? (
    <Badge variant="secondary">开启</Badge>
  ) : (
    <Badge variant="outline">关闭</Badge>
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

// StatusField 是数据状态开关：可访问名保持稳定，状态词另起展示位随开关联动。
function StatusField({ id, status, onChange }: { id: string; status: number; onChange: (status: number) => void }) {
  return (
    <div className="flex items-center gap-2">
      <Switch
        id={id}
        checked={status === STATUS_ENABLED}
        onCheckedChange={(checked) => onChange(checked ? STATUS_ENABLED : STATUS_DISABLED)}
      />
      <Label htmlFor={id}>状态</Label>
      <span aria-live="polite" className="text-sm text-muted-foreground">
        {status === STATUS_ENABLED ? '启用' : '禁用'}
      </span>
    </div>
  )
}

// EnabledField 是功能开关业务取值的开关：功能是否对租户开启，与数据状态
// status 相互独立。
function EnabledField({
  id,
  isEnabled,
  onChange,
}: {
  id: string
  isEnabled: boolean
  onChange: (enabled: boolean) => void
}) {
  return (
    <div className="flex items-center gap-2">
      <Switch id={id} checked={isEnabled} onCheckedChange={onChange} />
      <Label htmlFor={id}>功能开启</Label>
      <span aria-live="polite" className="text-sm text-muted-foreground">
        {isEnabled ? '开启' : '关闭'}
      </span>
    </div>
  )
}

// ---------- 全局配置 / 租户配置：新建 ----------

function ConfigCreateDialog({
  scope,
  onClose,
  onCreated,
}: {
  scope: ConfigScope
  onClose: () => void
  onCreated: () => void
}) {
  const [name, setName] = useState('')
  const [configKey, setConfigKey] = useState('')
  const [value, setValue] = useState('')
  const [valueType, setValueType] = useState<string>('string')
  const [status, setStatus] = useState(STATUS_ENABLED)
  const [remark, setRemark] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const noun = SCOPE_LABELS[scope]
  const fallback = scope === 'global' ? 'create global config failed' : 'create tenant config failed'

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (name.trim() === '' || configKey.trim() === '') {
      setError('name and config key are required')
      return
    }
    setPending(true)
    try {
      // value 是原样透传的可空字符串：界面统一发送输入值（含空串），空串
      // 是合法取值，不清洗为 undefined（compact 只丢弃 undefined）。
      const input = {
        status,
        name: name.trim(),
        configKey: configKey.trim(),
        value,
        valueType,
        remark: remark.trim() === '' ? undefined : remark.trim(),
      }
      if (scope === 'global') {
        await createConfig(input)
      } else {
        await createTenantConfig(input)
      }
      toast.success(`${noun} ${name.trim()} 已创建`)
      onCreated()
      onClose()
    } catch (err) {
      setError(configErrorHint(err, fallback))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建{noun}</DialogTitle>
          <DialogDescription>
            {scope === 'global'
              ? '全局配置由所有租户共享；配置键未删除行间唯一，创建后不可变。'
              : '租户配置仅本租户可见，覆盖全局配置的同名默认值；配置键创建后不可变。'}
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="config-create-name" label="配置名称">
            <Input id="config-create-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="config-create-key" label="配置键">
            <Input id="config-create-key" value={configKey} onChange={(e) => setConfigKey(e.target.value)} autoComplete="off" placeholder="如：smtp.host" />
          </Field>
          <Field id="config-create-value" label="配置值">
            <Input id="config-create-value" value={value} onChange={(e) => setValue(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="config-create-value-type" label="值类型">
            <Select value={valueType} onValueChange={setValueType}>
              <SelectTrigger id="config-create-value-type" aria-label="值类型">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CONFIG_VALUE_TYPES.map((vt) => (
                  <SelectItem key={vt} value={vt}>
                    {vt}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field id="config-create-remark" label="备注">
            <Input id="config-create-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <StatusField id="config-create-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="创建" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 全局配置 / 租户配置：编辑 ----------

function ConfigEditDialog({
  scope,
  config,
  onClose,
  onSaved,
}: {
  scope: ConfigScope
  config: ConfigDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(config.name)
  // valueWasUnset 记录原值是否为 NULL（未设置）：protojson 省略未设置的
  // optional 字段，前端以 undefined 表示 NULL，与空串 "" 语义不同。
  const valueWasUnset = config.value === undefined
  const [value, setValue] = useState(config.value ?? '')
  // valueTouched 记录用户是否动过配置值输入框。提交口径：
  // - 原值 NULL 且未改动 → 不上送 value（保持 NULL，避免被写成空串）；
  // - 原值 "" 且未改动 → 上送 ""（保持空串，既有行为不变）；
  // - 用户改动过（含显式清空后再输入）→ 按输入原样上送，空串也照发。
  const [valueTouched, setValueTouched] = useState(false)
  const [remark, setRemark] = useState(config.remark ?? '')
  const [status, setStatus] = useState(config.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const noun = SCOPE_LABELS[scope]
  const fallback = scope === 'global' ? 'update global config failed' : 'update tenant config failed'

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (name.trim() === '') {
      setError('name is required')
      return
    }
    setPending(true)
    try {
      // 更新只覆盖 name/value/remark/status：config_key 与 value_type 不可变。
      // value 是原样透传的可空字符串：原值 NULL 且用户未改动时省略该字段
      // （保持 NULL）；其余情况原样上送输入值（含空串），空输入不得清洗为
      // undefined，否则会把空串覆盖成 NULL。
      const input = {
        id: config.id,
        status,
        name: name.trim(),
        ...(valueWasUnset && !valueTouched ? {} : { value }),
        remark: remark.trim() === '' ? undefined : remark.trim(),
      }
      if (scope === 'global') {
        await updateConfig(input)
      } else {
        await updateTenantConfig(input)
      }
      toast.success(`${noun} ${name.trim()} 已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(configErrorHint(err, fallback))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑{noun}</DialogTitle>
          <DialogDescription>
            仅名称、配置值、备注与状态可改；配置键 {config.configKey} 与值类型 {config.valueType} 创建后不可变。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="config-edit-name" label="配置名称">
            <Input id="config-edit-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="config-edit-key" label="配置键">
            <Input id="config-edit-key" value={config.configKey} disabled readOnly />
          </Field>
          <Field id="config-edit-value" label="配置值">
            <Input
              id="config-edit-value"
              value={value}
              onChange={(e) => {
                setValue(e.target.value)
                setValueTouched(true)
              }}
              autoComplete="off"
            />
          </Field>
          <Field id="config-edit-value-type" label="值类型">
            <Input id="config-edit-value-type" value={config.valueType} disabled readOnly />
          </Field>
          <Field id="config-edit-remark" label="备注">
            <Input id="config-edit-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <StatusField id="config-edit-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="保存" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 全局配置 / 租户配置：删除确认 ----------

function ConfigDeleteDialog({
  scope,
  config,
  onClose,
  onDeleted,
}: {
  scope: ConfigScope
  config: ConfigDTO
  onClose: () => void
  onDeleted: () => void
}) {
  const [pending, setPending] = useState(false)
  const noun = SCOPE_LABELS[scope]
  const fallback = scope === 'global' ? 'delete global config failed' : 'delete tenant config failed'

  const confirm = async () => {
    setPending(true)
    try {
      if (scope === 'global') {
        await deleteConfig(config.id)
      } else {
        await deleteTenantConfig(config.id)
      }
      toast.success(`${noun} ${config.name} 已删除`)
      onDeleted()
      onClose()
    } catch (err) {
      // 失败保留确认弹窗，便于用户直接重试；错误经 toast 播报。
      toast.error(`删除失败：${configErrorHint(err, fallback)}`)
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除{noun} {config.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            删除为软删除，删除后配置键可被新配置复用；正在消费该配置的服务可能立即读到缺省值。
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

// ---------- 功能开关：新建 / 编辑 / 删除 ----------

function FeatureFlagCreateDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [flagKey, setFlagKey] = useState('')
  const [description, setDescription] = useState('')
  const [isEnabled, setIsEnabled] = useState(false)
  const [status, setStatus] = useState(STATUS_ENABLED)
  const [remark, setRemark] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (name.trim() === '' || flagKey.trim() === '') {
      setError('name and flag key are required')
      return
    }
    setPending(true)
    try {
      await createFeatureFlag({
        status,
        name: name.trim(),
        flagKey: flagKey.trim(),
        description: description.trim() === '' ? undefined : description.trim(),
        isEnabled,
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`功能开关 ${name.trim()} 已创建`)
      onCreated()
      onClose()
    } catch (err) {
      setError(configErrorHint(err, 'create feature flag failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建功能开关</DialogTitle>
          <DialogDescription>
            功能键在租户内唯一且创建后不可变；功能是否开启与数据状态相互独立。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="flag-create-name" label="功能名称">
            <Input id="flag-create-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="flag-create-key" label="功能键">
            <Input id="flag-create-key" value={flagKey} onChange={(e) => setFlagKey(e.target.value)} autoComplete="off" placeholder="如：export_enabled" />
          </Field>
          <Field id="flag-create-description" label="功能描述">
            <Input id="flag-create-description" value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>
          <EnabledField id="flag-create-enabled" isEnabled={isEnabled} onChange={setIsEnabled} />
          <Field id="flag-create-remark" label="备注">
            <Input id="flag-create-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <StatusField id="flag-create-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="创建" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

function FeatureFlagEditDialog({
  flag,
  onClose,
  onSaved,
}: {
  flag: FeatureFlagDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(flag.name)
  const [description, setDescription] = useState(flag.description ?? '')
  const [isEnabled, setIsEnabled] = useState(flag.isEnabled === true)
  const [remark, setRemark] = useState(flag.remark ?? '')
  const [status, setStatus] = useState(flag.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED)
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
      // 更新是覆盖语义：status 与 isEnabled 必须显式给出，避免省略 isEnabled
      // 被后端落为 false。
      await updateFeatureFlag({
        id: flag.id,
        status,
        name: name.trim(),
        description: description.trim() === '' ? undefined : description.trim(),
        isEnabled,
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`功能开关 ${name.trim()} 已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(configErrorHint(err, 'update feature flag failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑功能开关</DialogTitle>
          <DialogDescription>
            仅名称、描述、功能开启、备注与状态可改；功能键 {flag.flagKey} 创建后不可变。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="flag-edit-name" label="功能名称">
            <Input id="flag-edit-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="flag-edit-key" label="功能键">
            <Input id="flag-edit-key" value={flag.flagKey} disabled readOnly />
          </Field>
          <Field id="flag-edit-description" label="功能描述">
            <Input id="flag-edit-description" value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>
          <EnabledField id="flag-edit-enabled" isEnabled={isEnabled} onChange={setIsEnabled} />
          <Field id="flag-edit-remark" label="备注">
            <Input id="flag-edit-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <StatusField id="flag-edit-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="保存" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

function FeatureFlagDeleteDialog({
  flag,
  onClose,
  onDeleted,
}: {
  flag: FeatureFlagDTO
  onClose: () => void
  onDeleted: () => void
}) {
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      await deleteFeatureFlag(flag.id)
      toast.success(`功能开关 ${flag.name} 已删除`)
      onDeleted()
      onClose()
    } catch (err) {
      // 失败保留确认弹窗，便于用户直接重试；错误经 toast 播报。
      toast.error(`删除失败：${configErrorHint(err, 'delete feature flag failed')}`)
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除功能开关 {flag.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            删除为软删除，删除后功能键可被新开关复用；依赖该开关的功能将回落到缺省行为。
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

// ---------- 翻译：新建 / 编辑 / 删除 ----------

function TranslationCreateDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [tableName, setTableName] = useState('')
  const [rowId, setRowId] = useState('')
  const [columnName, setColumnName] = useState('')
  const [locale, setLocale] = useState('')
  const [value, setValue] = useState('')
  const [status, setStatus] = useState(STATUS_ENABLED)
  const [remark, setRemark] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (tableName.trim() === '' || columnName.trim() === '' || value.trim() === '') {
      setError('table name, column name and value are required')
      return
    }
    const rowIdTrim = rowId.trim()
    if (!/^[1-9]\d*$/.test(rowIdTrim)) {
      setError('row id must be a positive integer')
      return
    }
    if (BigInt(rowIdTrim) > TRANSLATION_ROW_ID_MAX) {
      setError('row id exceeds the int64 range')
      return
    }
    const localeTrim = locale.trim()
    if (!TRANSLATION_LOCALE_PATTERN.test(localeTrim) || localeTrim.length > TRANSLATION_LOCALE_MAX_LENGTH) {
      setError('locale must be a BCP-47 tag such as zh-CN')
      return
    }
    setPending(true)
    try {
      await createTranslation({
        status,
        tableName: tableName.trim(),
        rowId: rowIdTrim,
        columnName: columnName.trim(),
        locale: localeTrim,
        value: value.trim(),
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`翻译 ${tableName.trim()}/${columnName.trim()} (${localeTrim}) 已创建`)
      onCreated()
      onClose()
    } catch (err) {
      setError(configErrorHint(err, 'create translation failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建翻译</DialogTitle>
          <DialogDescription>
            来源表、来源行、来源列与语言代码共同确定一条翻译且创建后不可变；语言代码为 BCP-47 基本形态（如 zh-CN）。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="translation-create-table" label="来源表">
            <Input id="translation-create-table" value={tableName} onChange={(e) => setTableName(e.target.value)} autoComplete="off" placeholder="如：sys_menu" />
          </Field>
          <Field id="translation-create-row" label="来源行 ID">
            <Input id="translation-create-row" value={rowId} onChange={(e) => setRowId(e.target.value)} inputMode="numeric" autoComplete="off" placeholder="正整数" />
          </Field>
          <Field id="translation-create-column" label="来源列">
            <Input id="translation-create-column" value={columnName} onChange={(e) => setColumnName(e.target.value)} autoComplete="off" placeholder="如：name" />
          </Field>
          <Field id="translation-create-locale" label="语言代码">
            <Input id="translation-create-locale" value={locale} onChange={(e) => setLocale(e.target.value)} autoComplete="off" placeholder="BCP-47，如 zh-CN" />
          </Field>
          <Field id="translation-create-value" label="翻译文本">
            <Input id="translation-create-value" value={value} onChange={(e) => setValue(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="translation-create-remark" label="备注">
            <Input id="translation-create-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <StatusField id="translation-create-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="创建" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

function TranslationEditDialog({
  translation,
  onClose,
  onSaved,
}: {
  translation: TranslationDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [value, setValue] = useState(translation.value)
  const [remark, setRemark] = useState(translation.remark ?? '')
  const [status, setStatus] = useState(translation.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (value.trim() === '') {
      setError('value is required')
      return
    }
    setPending(true)
    try {
      // 更新只覆盖 status/value/remark：身份四元组不可变，前端不上送。
      await updateTranslation({
        id: translation.id,
        status,
        value: value.trim(),
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`翻译 ${translation.tableName}/${translation.columnName} (${translation.locale}) 已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(configErrorHint(err, 'update translation failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑翻译</DialogTitle>
          <DialogDescription>
            仅翻译文本、备注与状态可改；来源表 {translation.tableName}、来源行 {translation.rowId}、来源列 {translation.columnName} 与语言 {translation.locale} 创建后不可变。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="translation-edit-table" label="来源表">
            <Input id="translation-edit-table" value={translation.tableName} disabled readOnly />
          </Field>
          <Field id="translation-edit-row" label="来源行 ID">
            <Input id="translation-edit-row" value={translation.rowId} disabled readOnly />
          </Field>
          <Field id="translation-edit-column" label="来源列">
            <Input id="translation-edit-column" value={translation.columnName} disabled readOnly />
          </Field>
          <Field id="translation-edit-locale" label="语言代码">
            <Input id="translation-edit-locale" value={translation.locale} disabled readOnly />
          </Field>
          <Field id="translation-edit-value" label="翻译文本">
            <Input id="translation-edit-value" value={value} onChange={(e) => setValue(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="translation-edit-remark" label="备注">
            <Input id="translation-edit-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <StatusField id="translation-edit-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="保存" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

function TranslationDeleteDialog({
  translation,
  onClose,
  onDeleted,
}: {
  translation: TranslationDTO
  onClose: () => void
  onDeleted: () => void
}) {
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      await deleteTranslation(translation.id)
      toast.success(`翻译 ${translation.tableName}/${translation.columnName} (${translation.locale}) 已删除`)
      onDeleted()
      onClose()
    } catch (err) {
      // 失败保留确认弹窗，便于用户直接重试；错误经 toast 播报。
      toast.error(`删除失败：${configErrorHint(err, 'delete translation failed')}`)
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除翻译 {translation.locale}？</AlertDialogTitle>
          <AlertDialogDescription>
            删除为软删除，删除后同一来源表、行、列与语言可重新创建；消费方回退原始文本。
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

function ConfigRowActions({ config, onOpen }: { config: ConfigDTO; onOpen: (state: DialogState) => void }) {
  return (
    <RowActionsMenu
      rowLabel={config.name}
      info={[
        { label: '配置名称', value: config.name },
        { label: '配置键', value: config.configKey },
      ]}
      actions={[
        { label: '编辑', onSelect: () => onOpen({ kind: 'config-edit', config }) },
        { label: '删除', destructive: true, onSelect: () => onOpen({ kind: 'config-delete', config }) },
      ]}
    />
  )
}

function TenantConfigRowActions({ config, onOpen }: { config: TenantConfigDTO; onOpen: (state: DialogState) => void }) {
  return (
    <RowActionsMenu
      rowLabel={config.name}
      info={[
        { label: '配置名称', value: config.name },
        { label: '配置键', value: config.configKey },
      ]}
      actions={[
        { label: '编辑', onSelect: () => onOpen({ kind: 'tenant-edit', config }) },
        { label: '删除', destructive: true, onSelect: () => onOpen({ kind: 'tenant-delete', config }) },
      ]}
    />
  )
}

function FeatureFlagRowActions({
  flag,
  onOpen,
  onToggle,
}: {
  flag: FeatureFlagDTO
  onOpen: (state: DialogState) => void
  onToggle: (flag: FeatureFlagDTO) => void
}) {
  return (
    <RowActionsMenu
      rowLabel={flag.name}
      info={[
        { label: '功能名称', value: flag.name },
        { label: '功能键', value: flag.flagKey },
      ]}
      actions={[
        { label: flag.isEnabled === true ? '停用' : '启用', onSelect: () => onToggle(flag) },
        { label: '编辑', onSelect: () => onOpen({ kind: 'flag-edit', flag }) },
        { label: '删除', destructive: true, onSelect: () => onOpen({ kind: 'flag-delete', flag }) },
      ]}
    />
  )
}

function TranslationRowActions({
  translation,
  onOpen,
}: {
  translation: TranslationDTO
  onOpen: (state: DialogState) => void
}) {
  return (
    <RowActionsMenu
      rowLabel={`${translation.tableName}/${translation.columnName} ${translation.locale}`}
      info={[
        { label: '来源表', value: translation.tableName },
        { label: '来源行', value: translation.rowId },
        { label: '语言', value: translation.locale },
      ]}
      actions={[
        { label: '编辑', onSelect: () => onOpen({ kind: 'translation-edit', translation }) },
        { label: '删除', destructive: true, onSelect: () => onOpen({ kind: 'translation-delete', translation }) },
      ]}
    />
  )
}

// ---------- 列定义 ----------

// insertAfter 在指定列之后插入附加列：value 列不在 ListSchema 的可显隐
// 字段集内（库层未声明 @hideable），由页面补齐在配置键之后。
function insertAfter<T>(
  columns: DataTableColumn<T>[],
  afterKey: string,
  column: DataTableColumn<T>,
): DataTableColumn<T>[] {
  const index = columns.findIndex((item) => item.key === afterKey)
  if (index < 0) {
    return [...columns, column]
  }
  return [...columns.slice(0, index + 1), column, ...columns.slice(index + 1)]
}

function actionsColumn<T>(render: (item: T) => ReactNode): DataTableColumn<T> {
  return { key: 'actions', header: '操作', className: 'w-16 text-right', sticky: 'right', render }
}

function buildConfigColumns<T extends ConfigDTO>(
  schema: ListSchemaDTO,
  renderActions: (row: T) => ReactNode,
): DataTableColumn<T>[] {
  const columns = schemaColumns<T>(
    schema,
    {
      name: { header: '配置名称' },
      configKey: { header: '配置键' },
      valueType: { header: '值类型' },
      status: { header: '状态', render: (row) => statusBadge(row.status) },
      updatedAt: { header: '更新时间' },
    },
    [actionsColumn(renderActions)],
  )
  return insertAfter(columns, 'configKey', {
    key: 'value',
    header: '配置值',
    render: (row) => row.value ?? '—',
  })
}

function buildFeatureFlagColumns(
  schema: ListSchemaDTO,
  renderActions: (row: FeatureFlagDTO) => ReactNode,
): DataTableColumn<FeatureFlagDTO>[] {
  return schemaColumns<FeatureFlagDTO>(
    schema,
    {
      name: { header: '功能名称' },
      flagKey: { header: '功能键' },
      isEnabled: { header: '功能状态', render: (row) => enabledBadge(row.isEnabled) },
      status: { header: '状态', render: (row) => statusBadge(row.status) },
    },
    [actionsColumn(renderActions)],
  )
}

function buildTranslationColumns(
  schema: ListSchemaDTO,
  renderActions: (row: TranslationDTO) => ReactNode,
): DataTableColumn<TranslationDTO>[] {
  return schemaColumns<TranslationDTO>(
    schema,
    {
      tableName: { header: '来源表' },
      rowId: { header: '来源行 ID' },
      columnName: { header: '来源列' },
      locale: { header: '语言代码' },
      value: { header: '翻译文本' },
      status: { header: '状态', render: (row) => statusBadge(row.status) },
    },
    [actionsColumn(renderActions)],
  )
}

// ---------- 页面 ----------

// ConfigManagePage 配置管理：全局配置/租户配置/功能开关/翻译四个视图以
// 标签切换，各自筛选与列定义动态响应 GetListSchema 响应。全局与租户配置
// 的配置键与值类型、功能开关的功能键、翻译的身份四元组均创建后不可变。
export function ConfigManagePage() {
  const [tab, setTab] = useState<ConfigTab>('config')
  const [filters, setFilters] = useState<QueryCondition[]>([])
  const [reloadKey, setReloadKey] = useState(0)
  const [dialog, setDialog] = useState<DialogState | null>(null)

  const { schema, error: schemaError, loading: schemaLoading, retry: retrySchema } = useListSchema(TAB_SCHEMA_RESOURCES[tab])

  // schemaReady 校验 schema 归属当前视图：切换标签后的第一帧里 hook 仍返回
  // 上一视图的响应（effect 未及重跑），直接渲染会让新视图短暂挂上旧 schema
  // 的筛选与列定义、多发一次即弃的请求——以 resource 归属作为就绪判据。
  const schemaReady = schema !== null && schema.resource === TAB_SCHEMA_RESOURCES[tab]

  const filterFields = useMemo(() => (schemaReady ? filterFieldsFromSchema(schema) : []), [schemaReady, schema])
  const defaultSort = useMemo<QuerySort[]>(() => (schemaReady ? defaultSortFromSchema(schema) : []), [schemaReady, schema])
  const pageSize = schemaReady ? schema.pagination?.defaultPageSize ?? 20 : 20

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  const switchTab = useCallback((next: ConfigTab) => {
    setTab(next)
    setFilters([])
    setDialog(null)
    setReloadKey((key) => key + 1)
  }, [])

  // toggleFlag 是功能开关的快捷启停：更新为覆盖语义，status 与 isEnabled
  // 必须同时显式上送；name/description/remark 带存量值，避免覆盖为空。
  const toggleFlag = useCallback(
    async (flag: FeatureFlagDTO) => {
      const nextEnabled = flag.isEnabled !== true
      try {
        await updateFeatureFlag({
          id: flag.id,
          status: flag.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED,
          name: flag.name,
          description: flag.description,
          isEnabled: nextEnabled,
          remark: flag.remark,
        })
        toast.success(`功能开关 ${flag.name} 已${nextEnabled ? '开启' : '关闭'}`)
        reload()
      } catch (err) {
        toast.error(`切换失败：${configErrorHint(err, 'toggle feature flag failed')}`)
      }
    },
    [reload],
  )

  const configColumns = useMemo<DataTableColumn<ConfigDTO>[]>(
    () =>
      schemaReady
        ? buildConfigColumns<ConfigDTO>(schema, (row) => <ConfigRowActions config={row} onOpen={setDialog} />)
        : [],
    [schemaReady, schema],
  )

  const tenantColumns = useMemo<DataTableColumn<TenantConfigDTO>[]>(
    () =>
      schemaReady
        ? buildConfigColumns<TenantConfigDTO>(schema, (row) => <TenantConfigRowActions config={row} onOpen={setDialog} />)
        : [],
    [schemaReady, schema],
  )

  const flagColumns = useMemo<DataTableColumn<FeatureFlagDTO>[]>(
    () =>
      schemaReady
        ? buildFeatureFlagColumns(schema, (row) => (
            <FeatureFlagRowActions flag={row} onOpen={setDialog} onToggle={(flag) => { void toggleFlag(flag) }} />
          ))
        : [],
    [schemaReady, schema, toggleFlag],
  )

  const translationColumns = useMemo<DataTableColumn<TranslationDTO>[]>(
    () =>
      schemaReady
        ? buildTranslationColumns(schema, (row) => <TranslationRowActions translation={row} onOpen={setDialog} />)
        : [],
    [schemaReady, schema],
  )

  const renderDialog = () => {
    if (dialog === null) {
      return null
    }
    switch (dialog.kind) {
      case 'config-create':
        return <ConfigCreateDialog scope="global" onClose={() => setDialog(null)} onCreated={reload} />
      case 'config-edit':
        return <ConfigEditDialog scope="global" config={dialog.config} onClose={() => setDialog(null)} onSaved={reload} />
      case 'config-delete':
        return <ConfigDeleteDialog scope="global" config={dialog.config} onClose={() => setDialog(null)} onDeleted={reload} />
      case 'tenant-create':
        return <ConfigCreateDialog scope="tenant" onClose={() => setDialog(null)} onCreated={reload} />
      case 'tenant-edit':
        return <ConfigEditDialog scope="tenant" config={dialog.config} onClose={() => setDialog(null)} onSaved={reload} />
      case 'tenant-delete':
        return <ConfigDeleteDialog scope="tenant" config={dialog.config} onClose={() => setDialog(null)} onDeleted={reload} />
      case 'flag-create':
        return <FeatureFlagCreateDialog onClose={() => setDialog(null)} onCreated={reload} />
      case 'flag-edit':
        return <FeatureFlagEditDialog flag={dialog.flag} onClose={() => setDialog(null)} onSaved={reload} />
      case 'flag-delete':
        return <FeatureFlagDeleteDialog flag={dialog.flag} onClose={() => setDialog(null)} onDeleted={reload} />
      case 'translation-create':
        return <TranslationCreateDialog onClose={() => setDialog(null)} onCreated={reload} />
      case 'translation-edit':
        return (
          <TranslationEditDialog translation={dialog.translation} onClose={() => setDialog(null)} onSaved={reload} />
        )
      case 'translation-delete':
        return (
          <TranslationDeleteDialog translation={dialog.translation} onClose={() => setDialog(null)} onDeleted={reload} />
        )
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col p-6">
      {/* 标签切换：四个资源视图共享管理页骨架，切换即清空筛选并重查。 */}
      <div role="tablist" aria-label="配置管理视图" className="mb-4 flex items-center gap-1">
        {TAB_ORDER.map((key) => (
          <button
            key={key}
            type="button"
            role="tab"
            aria-selected={tab === key}
            onClick={() => switchTab(key)}
            className={
              tab === key
                ? 'rounded-lg bg-brand-soft px-3 py-1.5 text-sm font-medium text-brand-text transition-colors select-none'
                : 'rounded-lg px-3 py-1.5 text-sm font-medium text-foreground transition-colors select-none hover:bg-muted'
            }
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
      ) : tab === 'config' ? (
        <DataTable
          key={tab}
          title="全局配置"
          description="平台级全局配置：不绑定租户，所有租户共享同一份取值；配置键与值类型创建后不可变。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={reload} ariaLabel="全局配置筛选" />}
          rowKey={(config) => config.id}
          columns={configColumns}
          fetchPage={listConfigs}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的全局配置"
          toolbar={
            <ToolbarIconButton label="新建全局配置" onClick={() => setDialog({ kind: 'config-create' })}>
              <PlusIcon aria-hidden className="size-4" />
            </ToolbarIconButton>
          }
        />
      ) : tab === 'tenant-config' ? (
        <DataTable
          key={tab}
          title="租户配置"
          description="租户级配置：仅本租户可见，覆盖全局配置的同名默认值；配置键在租户内唯一且创建后不可变。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={reload} ariaLabel="租户配置筛选" />}
          rowKey={(config) => config.id}
          columns={tenantColumns}
          fetchPage={listTenantConfigs}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的租户配置"
          toolbar={
            <ToolbarIconButton label="新建租户配置" onClick={() => setDialog({ kind: 'tenant-create' })}>
              <PlusIcon aria-hidden className="size-4" />
            </ToolbarIconButton>
          }
        />
      ) : tab === 'feature-flag' ? (
        <DataTable
          key={tab}
          title="功能开关"
          description="租户级功能开关：功能键在租户内唯一且创建后不可变；功能是否开启与数据状态相互独立。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={reload} ariaLabel="功能开关筛选" />}
          rowKey={(flag) => flag.id}
          columns={flagColumns}
          fetchPage={listFeatureFlags}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的功能开关"
          toolbar={
            <ToolbarIconButton label="新建功能开关" onClick={() => setDialog({ kind: 'flag-create' })}>
              <PlusIcon aria-hidden className="size-4" />
            </ToolbarIconButton>
          }
        />
      ) : (
        <DataTable
          key={tab}
          title="翻译"
          description="租户级通用翻译：来源表、来源行、来源列与语言代码共同确定一条翻译，身份四列创建后不可变。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={reload} ariaLabel="翻译筛选" />}
          rowKey={(translation) => translation.id}
          columns={translationColumns}
          fetchPage={listTranslations}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的翻译"
          toolbar={
            <ToolbarIconButton label="新建翻译" onClick={() => setDialog({ kind: 'translation-create' })}>
              <PlusIcon aria-hidden className="size-4" />
            </ToolbarIconButton>
          }
        />
      )}

      {renderDialog()}
    </div>
  )
}
