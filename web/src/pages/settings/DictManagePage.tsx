import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { DatabaseIcon, PlusIcon } from 'lucide-react'
import {
  createDictData,
  createDictType,
  deleteDictData,
  deleteDictType,
  listDictDatas,
  listDictTypes,
  updateDictData,
  updateDictType,
  type DictDataDTO,
  type DictTypeDTO,
} from '@/api/dicts'
import type { DynamicQuery, QueryCondition, QuerySort } from '@/api/query'
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
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

// 字典状态与存储层 sys_dict_type / sys_dict_data 的 status 取值一致。
const STATUS_DISABLED = 0
const STATUS_ENABLED = 1

// DICT_VALUE_TYPES 是 value_type 的白名单（与后端用例校验同一清单），数据
// 创建弹窗的下拉选项。
const DICT_VALUE_TYPES = ['string', 'integer', 'boolean', 'float', 'json'] as const

// DialogState 是当前打开的弹窗：type 缺省即新建类型；data 弹窗围绕宿主类型。
type DialogState =
  | { kind: 'type-create' }
  | { kind: 'type-edit'; dictType: DictTypeDTO }
  | { kind: 'type-delete'; dictType: DictTypeDTO }
  | { kind: 'data-manage'; dictType: DictTypeDTO }

// DICT_ERROR_HINTS 把后端字典错误（dict/v1/error_reason.proto 各 reason 的
// 消息文案）映射为可读英文提示；词表外原样透传，不吞掉未知错误。
const DICT_ERROR_HINTS: Record<string, string> = {
  'dict type not found': 'dict type not found; it may have been deleted',
  'invalid dict type argument': 'invalid dict type argument',
  'dict type already exists': 'a dict type with the same code already exists in the tenant',
  'dict type has active data': 'the dict type still has active data; clear its data first',
  'dict data not found': 'dict data not found; it may have been deleted',
  'invalid dict data argument': 'invalid dict data; the host type must exist and value_type must be one of string/integer/boolean/float/json',
  'dict data already exists': 'a dict data with the same value already exists under this type',
}

// dictErrorHint 归一动作失败的展示文案：命中 reason 词表给可读提示，
// 否则透传后端原始消息。
function dictErrorHint(err: unknown, fallback: string): string {
  const message = err instanceof Error ? err.message : fallback
  return DICT_ERROR_HINTS[message] ?? message
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

// ---------- 新建字典类型 ----------

function DictTypeCreateDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
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
    setPending(true)
    try {
      await createDictType({
        code: code.trim(),
        name: name.trim(),
        status,
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`字典类型 ${name.trim()} 已创建`)
      onCreated()
      onClose()
    } catch (err) {
      setError(dictErrorHint(err, 'create dict type failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建字典类型</DialogTitle>
          <DialogDescription>
            类型编码在租户内唯一（未删除类型间）；编码创建后不可变，状态缺省启用。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="create-dict-type-code" label="类型编码">
            <Input id="create-dict-type-code" value={code} onChange={(e) => setCode(e.target.value)} autoComplete="off" placeholder="如：sys_user_status" />
          </Field>
          <Field id="create-dict-type-name" label="类型名">
            <Input id="create-dict-type-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-dict-type-remark" label="备注">
            <Input id="create-dict-type-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <StatusField id="create-dict-type-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="创建" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 编辑字典类型 ----------

function DictTypeEditDialog({
  dictType,
  onClose,
  onSaved,
}: {
  dictType: DictTypeDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(dictType.name)
  const [remark, setRemark] = useState(dictType.remark ?? '')
  const [status, setStatus] = useState(dictType.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED)
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
      // 更新只覆盖 name/remark/status：code 不可变，前端不上送。
      await updateDictType({
        id: dictType.id,
        status,
        name: name.trim(),
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`字典类型 ${name.trim()} 已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(dictErrorHint(err, 'update dict type failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑字典类型</DialogTitle>
          <DialogDescription>
            仅类型名、备注与状态可改；编码 {dictType.code} 创建后不可变。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="edit-dict-type-code" label="类型编码">
            <Input id="edit-dict-type-code" value={dictType.code} disabled readOnly />
          </Field>
          <Field id="edit-dict-type-name" label="类型名">
            <Input id="edit-dict-type-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="edit-dict-type-remark" label="备注">
            <Input id="edit-dict-type-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <StatusField id="edit-dict-type-status" status={status} onChange={setStatus} />
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="保存" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 删除字典类型确认 ----------

function DictTypeDeleteDialog({
  dictType,
  onClose,
  onDeleted,
}: {
  dictType: DictTypeDTO
  onClose: () => void
  onDeleted: () => void
}) {
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      await deleteDictType(dictType.id)
      toast.success(`字典类型 ${dictType.name} 已删除`)
      onDeleted()
      onClose()
    } catch (err) {
      toast.error(`删除失败：${dictErrorHint(err, 'delete dict type failed')}`)
      onClose()
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除字典类型 {dictType.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            存在未删除字典数据时删除会被拒绝（先清空该类型下的数据）；软删后编码可被新类型复用。
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

// ---------- 字典数据维护 ----------

// DATA_PAGE_SIZE 是数据面板每页条数（动态协议 offset 分页的固定页大小）。
const DATA_PAGE_SIZE = 50

// hostDataQuery 构造宿主类型的数据页查询：按 dictTypeId 过滤、sortOrder
// 升序展示（同序回退 id 升序保持稳定）。
function hostDataQuery(typeId: string, page: number): DynamicQuery {
  return {
    where: [{ field: 'dictTypeId', op: 'eq', value: typeId }],
    sort: [
      { field: 'sortOrder', order: 'asc' },
      { field: 'id', order: 'asc' },
    ],
    pagination: { page, pageSize: DATA_PAGE_SIZE },
  }
}

// DictDataManageDialog 是字典数据维护面板：宿主类型下的数据分页列表 +
// 添加数据 + 逐行编辑/删除。增删成功后重查当前页；宿主未知与值冲突由后端
// 拒绝，前端透传文案。
function DictDataManageDialog({
  dictType,
  onClose,
}: {
  dictType: DictTypeDTO
  onClose: () => void
}) {
  const [page, setPage] = useState(1)
  const [items, setItems] = useState<DictDataDTO[]>([])
  const [total, setTotal] = useState<string | undefined>(undefined)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const [label, setLabel] = useState('')
  const [value, setValue] = useState('')
  const [valueType, setValueType] = useState<string>('string')
  const [sortOrder, setSortOrder] = useState('0')
  const [addError, setAddError] = useState<string | null>(null)
  const [addPending, setAddPending] = useState(false)

  const [editing, setEditing] = useState<DictDataDTO | null>(null)
  const { can } = usePermissions()
  const canCreate = can(PERM.DictCreate)
  const canUpdate = can(PERM.DictUpdate)
  const canDelete = can(PERM.DictDelete)
  const canOperate = canUpdate || canDelete

  // 取数：page 或 reloadKey（增删后的重查信号）变化即拉当前页；requestSeq
  // 使迟到旧响应被丢弃，卸载后不再写状态。
  const requestSeq = useRef(0)
  useEffect(() => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setLoading(true)
    listDictDatas(hostDataQuery(dictType.id, page))
      .then((set) => {
        if (requestSeq.current !== seq) {
          return
        }
        setItems(set.items)
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
  }, [dictType.id, page, reloadKey])

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  const submitAdd = async (event: FormEvent) => {
    event.preventDefault()
    if (label.trim() === '' || value.trim() === '') {
      setAddError('label and value are required')
      return
    }
    const order = Number(sortOrder)
    if (!Number.isInteger(order) || order < 0) {
      setAddError('sort order must be a non-negative integer')
      return
    }
    setAddPending(true)
    try {
      await createDictData({
        dictTypeId: dictType.id,
        label: label.trim(),
        value: value.trim(),
        valueType,
        sortOrder: order,
      })
      toast.success(`字典数据 ${label.trim()} 已添加`)
      setLabel('')
      setValue('')
      setValueType('string')
      setSortOrder('0')
      setAddError(null)
      reload()
    } catch (err) {
      setAddError(dictErrorHint(err, 'create dict data failed'))
    } finally {
      setAddPending(false)
    }
  }

  const removeData = async (data: DictDataDTO) => {
    try {
      await deleteDictData(data.id)
      toast.success(`字典数据 ${data.label} 已删除`)
      reload()
    } catch (err) {
      toast.error(`删除失败：${dictErrorHint(err, 'delete dict data failed')}`)
    }
  }

  const totalNum = total !== undefined ? Number(total) : undefined

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="lg">
        <DialogHeader>
          <DialogTitle>
            <DatabaseIcon aria-hidden className="mr-1 inline size-4" />
            字典数据维护
          </DialogTitle>
          <DialogDescription>
            维护类型 {dictType.name}（{dictType.code}）下的数据项；字典值同类型下唯一（未删除数据间）。
          </DialogDescription>
        </DialogHeader>

        {canCreate && (
          <form className="grid gap-3" onSubmit={(e) => { void submitAdd(e) }} aria-label="添加字典数据">
            <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
              <Field id="data-add-label" label="标签">
                <Input id="data-add-label" value={label} onChange={(e) => setLabel(e.target.value)} autoComplete="off" />
              </Field>
              <Field id="data-add-value" label="字典值">
                <Input id="data-add-value" value={value} onChange={(e) => setValue(e.target.value)} autoComplete="off" />
              </Field>
              <Field id="data-add-value-type" label="值类型">
                <Select value={valueType} onValueChange={setValueType}>
                  <SelectTrigger id="data-add-value-type" aria-label="值类型">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {DICT_VALUE_TYPES.map((vt) => (
                      <SelectItem key={vt} value={vt}>{vt}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field id="data-add-sort" label="排序号">
                <Input id="data-add-sort" value={sortOrder} onChange={(e) => setSortOrder(e.target.value)} inputMode="numeric" />
              </Field>
            </div>
            <div>
              <Button type="submit" variant="outline" disabled={addPending}>
                <PlusIcon aria-hidden className="size-4" />
                添加数据
              </Button>
            </div>
            <FormError message={addError} />
          </form>
        )}

        {loadError !== null ? (
          <div role="alert" className="flex items-center justify-between gap-3 rounded-lg border border-destructive-soft p-3 text-sm text-destructive">
            <span>数据列表加载失败：{loadError}</span>
            <Button type="button" variant="outline" size="sm" onClick={reload}>
              重试
            </Button>
          </div>
        ) : (
          <div aria-busy={loading} className="overflow-hidden rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>标签</TableHead>
                  <TableHead>字典值</TableHead>
                  <TableHead>值类型</TableHead>
                  <TableHead className="w-20">排序</TableHead>
                  <TableHead className="w-20">状态</TableHead>
                  {canOperate && <TableHead className="w-28 text-right">操作</TableHead>}
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.length === 0 && !loading ? (
                  <TableRow>
                    <TableCell colSpan={canOperate ? 6 : 5} className="h-24 text-center text-muted-foreground">
                      暂无数据项
                    </TableCell>
                  </TableRow>
                ) : (
                  items.map((data) =>
                    editing !== null && editing.id === data.id ? (
                      <DictDataRowEditor
                        key={data.id}
                        data={data}
                        onClose={() => setEditing(null)}
                        onSaved={() => { setEditing(null); reload() }}
                      />
                    ) : (
                      <TableRow key={data.id}>
                        <TableCell>{data.label}</TableCell>
                        <TableCell className="font-mono">{data.value}</TableCell>
                        <TableCell>{data.valueType}</TableCell>
                        <TableCell>{data.sortOrder}</TableCell>
                        <TableCell>{statusBadge(data.status)}</TableCell>
                        {canOperate && (
                          <TableCell className="text-right">
                            {canUpdate && (
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                aria-label={`编辑 ${data.label}`}
                                onClick={() => setEditing(data)}
                              >
                                编辑
                              </Button>
                            )}
                            {canDelete && (
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                aria-label={`删除 ${data.label}`}
                                disabled={loading}
                                onClick={() => { void removeData(data) }}
                              >
                                删除
                              </Button>
                            )}
                          </TableCell>
                        )}
                      </TableRow>
                    ),
                  )
                )}
              </TableBody>
            </Table>
          </div>
        )}

        <DialogFooter className="items-center sm:justify-between">
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            {loading && <Spinner aria-hidden className="size-4" />}
            <span aria-label="数据统计">
              {totalNum !== undefined ? `共 ${totalNum} 个数据项 · 第 ${page} 页` : ''}
            </span>
          </span>
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              aria-label="数据上一页"
              disabled={loading || page <= 1}
              onClick={() => setPage((p) => Math.max(1, p - 1))}
            >
              上一页
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              aria-label="数据下一页"
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

// DictDataRowEditor 是数据行的行内编辑形态：仅 label/sortOrder/status 可改
//（value/valueType 不可变），保存后回到展示行。
function DictDataRowEditor({
  data,
  onClose,
  onSaved,
}: {
  data: DictDataDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [label, setLabel] = useState(data.label)
  const [sortOrder, setSortOrder] = useState(String(data.sortOrder))
  const [status, setStatus] = useState(data.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (label.trim() === '') {
      setError('label is required')
      return
    }
    const order = Number(sortOrder)
    if (!Number.isInteger(order) || order < 0) {
      setError('sort order must be a non-negative integer')
      return
    }
    setPending(true)
    try {
      await updateDictData({ id: data.id, status, label: label.trim(), sortOrder: order })
      toast.success(`字典数据 ${label.trim()} 已更新`)
      onSaved()
    } catch (err) {
      setError(dictErrorHint(err, 'update dict data failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <TableRow>
      <TableCell colSpan={6}>
        <form className="flex flex-wrap items-center gap-2" onSubmit={(e) => { void submit(e) }} aria-label={`编辑 ${data.label}`}>
          <Input aria-label="标签" value={label} onChange={(e) => setLabel(e.target.value)} className="w-40" autoComplete="off" />
          <span className="font-mono text-sm text-muted-foreground">{data.value}</span>
          <span className="text-sm text-muted-foreground">{data.valueType}</span>
          <Input aria-label="排序号" value={sortOrder} onChange={(e) => setSortOrder(e.target.value)} inputMode="numeric" className="w-20" />
          <Switch checked={status === STATUS_ENABLED} onCheckedChange={(checked) => setStatus(checked ? STATUS_ENABLED : STATUS_DISABLED)} aria-label="状态" />
          <span className="text-sm text-muted-foreground">{status === STATUS_ENABLED ? '启用' : '禁用'}</span>
          <Button type="submit" size="sm" disabled={pending}>保存</Button>
          <Button type="button" size="sm" variant="outline" disabled={pending} onClick={onClose}>取消</Button>
          <FormError message={error} />
        </form>
      </TableCell>
    </TableRow>
  )
}

// ---------- 类型行内动作 ----------

function TypeRowActions({
  dictType,
  onOpen,
}: {
  dictType: DictTypeDTO
  onOpen: (state: DialogState) => void
}) {
  const { can } = usePermissions()
  const actions: RowActionItem[] = []
  if (
    can(PERM.DictQuery) ||
    can(PERM.DictCreate) ||
    can(PERM.DictUpdate) ||
    can(PERM.DictDelete)
  ) {
    actions.push({ label: '数据维护', onSelect: () => onOpen({ kind: 'data-manage', dictType }) })
  }
  if (can(PERM.DictUpdate)) {
    actions.push({ label: '编辑', onSelect: () => onOpen({ kind: 'type-edit', dictType }) })
  }
  if (can(PERM.DictDelete)) {
    actions.push({ label: '删除', destructive: true, onSelect: () => onOpen({ kind: 'type-delete', dictType }) })
  }
  if (actions.length === 0) {
    return null
  }
  return (
    <RowActionsMenu
      rowLabel={dictType.name}
      info={[
        { label: '类型名', value: dictType.name },
        { label: '类型编码', value: dictType.code },
      ]}
      actions={actions}
    />
  )
}

// ---------- 页面 ----------

// DictManagePage 字典管理：类型列表（筛选/列定义动态响应 GetListSchema
// 响应）+ 行内动作（数据维护/编辑/删除）+ 新建类型。字典数据随宿主类型
// 在维护面板内展开维护。
export function DictManagePage() {
  const [filters, setFilters] = useState<QueryCondition[]>([])
  const [reloadKey, setReloadKey] = useState(0)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const { schema, error: schemaError, loading: schemaLoading, retry: retrySchema } = useListSchema('sys_dict_type')
  const { can } = usePermissions()
  const canCreate = can(PERM.DictCreate)
  const hasRowActions =
    can(PERM.DictQuery) || can(PERM.DictCreate) || can(PERM.DictUpdate) || can(PERM.DictDelete)

  const filterFields = useMemo(() => (schema === null ? [] : filterFieldsFromSchema(schema)), [schema])
  const defaultSort = useMemo<QuerySort[]>(() => (schema === null ? [] : defaultSortFromSchema(schema)), [schema])
  const pageSize = schema?.pagination?.defaultPageSize ?? 20

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // columns 动态响应 schema：页面仅覆写必要附加项（状态徽标）；行操作列
  // 不对应查询字段，有操作权限时追加在末尾。
  const columns = useMemo<DataTableColumn<DictTypeDTO>[]>(
    () =>
      schema === null
        ? []
        : schemaColumns<DictTypeDTO>(
            schema,
            { status: { header: '状态', render: (t) => statusBadge(t.status) } },
            hasRowActions
              ? [
                  {
                    key: 'actions',
                    header: '操作',
                    className: 'w-16 text-right',
                    sticky: 'right' as const,
                    render: (t: DictTypeDTO) => <TypeRowActions dictType={t} onOpen={setDialog} />,
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
          title="字典管理"
          description="字典类型与字典数据两级维护：数据随宿主类型在维护面板内展开。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={() => setReloadKey((key) => key + 1)} ariaLabel="字典类型筛选" />}
          rowKey={(dictType) => dictType.id}
          columns={columns}
          fetchPage={listDictTypes}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的字典类型"
          toolbar={
            canCreate ? (
              <ToolbarIconButton label="新建字典类型" onClick={() => setDialog({ kind: 'type-create' })}>
                <PlusIcon aria-hidden className="size-4" />
              </ToolbarIconButton>
            ) : undefined
          }
        />
      )}

      {dialog === null ? null : dialog.kind === 'type-create' ? (
        <DictTypeCreateDialog onClose={() => setDialog(null)} onCreated={reload} />
      ) : dialog.kind === 'type-edit' ? (
        <DictTypeEditDialog dictType={dialog.dictType} onClose={() => setDialog(null)} onSaved={reload} />
      ) : dialog.kind === 'type-delete' ? (
        <DictTypeDeleteDialog dictType={dialog.dictType} onClose={() => setDialog(null)} onDeleted={reload} />
      ) : (
        <DictDataManageDialog dictType={dialog.dictType} onClose={() => setDialog(null)} />
      )}
    </div>
  )
}
