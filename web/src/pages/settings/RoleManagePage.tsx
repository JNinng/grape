import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { PlusIcon } from 'lucide-react'
import {
  createRole,
  deleteRole,
  fetchRoleDataScope,
  listRoleMenus,
  listRoles,
  setRoleDataScope,
  setRoleMenus,
  updateRole,
  type RoleDTO,
  type RoleDataScopeDTO,
} from '@/api/roles'
import { fetchMenuTree, type MenuTreeNodeDTO, type MenuTreeReplyDTO } from '@/api/menus'
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
import { Checkbox } from '@/components/ui/checkbox'
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

// 角色状态与存储层 sys_role.status 取值一致。
const STATUS_DISABLED = 0
const STATUS_ENABLED = 1

// 角色类型：1 系统预置（不可删除）、2 租户自定义。
const ROLE_TYPE_PRESET = 1

// 数据范围规则与 sys_role.data_scope 取值一致；ID 范围明细仅在 4/5/6 下
// 分别消费 type 1/2/3 的条目。
const DATA_SCOPE_SELF = 1
const DATA_SCOPE_DEPT = 2
const DATA_SCOPE_DEPT_TREE = 3
const DATA_SCOPE_USER_IDS = 4
const DATA_SCOPE_DEPT_IDS = 5
const DATA_SCOPE_GROUP_IDS = 6
const DATA_SCOPE_ALL = 7

// ID_SCOPE_TYPE_* 是数据范围明细条目的 data_id 指向（sys_role_id_scope.type）。
const ID_SCOPE_TYPE_USER = 1
const ID_SCOPE_TYPE_DEPT = 2
const ID_SCOPE_TYPE_GROUP = 3

// DATA_SCOPE_LABELS 渲染数据范围规则列与下拉；键为 1-7。
const DATA_SCOPE_LABELS: Record<number, string> = {
  [DATA_SCOPE_SELF]: '仅本人',
  [DATA_SCOPE_DEPT]: '本部门',
  [DATA_SCOPE_DEPT_TREE]: '本部门及子部门',
  [DATA_SCOPE_USER_IDS]: '指定人',
  [DATA_SCOPE_DEPT_IDS]: '指定部门',
  [DATA_SCOPE_GROUP_IDS]: '用户组',
  [DATA_SCOPE_ALL]: '全租户',
}

// scopeIdTypeOf 返回数据范围规则对应的明细条目类型（4/5/6 → 1/2/3）；
// 其余规则不消费明细（undefined，编辑面不渲染 ID 集合输入）。
function scopeIdTypeOf(dataScope: number): number | undefined {
  if (dataScope === DATA_SCOPE_USER_IDS) {
    return ID_SCOPE_TYPE_USER
  }
  if (dataScope === DATA_SCOPE_DEPT_IDS) {
    return ID_SCOPE_TYPE_DEPT
  }
  if (dataScope === DATA_SCOPE_GROUP_IDS) {
    return ID_SCOPE_TYPE_GROUP
  }
  return undefined
}

// SCOPE_FACE_LABELS 是 4/5/6 三种规则下 ID 集合输入的标签。
const SCOPE_FACE_LABELS: Record<number, string> = {
  [ID_SCOPE_TYPE_USER]: '指定用户 ID（逗号或空白分隔）',
  [ID_SCOPE_TYPE_DEPT]: '指定部门 ID（逗号或空白分隔）',
  [ID_SCOPE_TYPE_GROUP]: '指定用户组 ID（逗号或空白分隔）',
}

// RoleDialogState 是当前打开的行内动作弹窗：role 缺省即新建。
type RoleDialogState =
  | { kind: 'create' }
  | { kind: 'edit'; role: RoleDTO }
  | { kind: 'data-scope'; role: RoleDTO }
  | { kind: 'menus'; role: RoleDTO }
  | { kind: 'delete'; role: RoleDTO }

// ROLE_ERROR_HINTS 把后端角色错误（perm/v1/error_reason.proto 各 reason 的
// 消息文案）映射为可读英文提示；词表外原样透传，不吞掉未知错误。
const ROLE_ERROR_HINTS: Record<string, string> = {
  'role not found': 'role not found; it may have been deleted',
  'invalid role argument': 'invalid argument; referenced users, depts or groups must exist and be enabled',
  'role already exists': 'a role with the same code or name already exists in the tenant',
  'preset role cannot be deleted': 'preset role cannot be deleted',
}

// roleErrorHint 归一动作失败的展示文案：命中 reason 词表给可读提示，
// 否则透传后端原始消息。
function roleErrorHint(err: unknown, fallback: string): string {
  const message = err instanceof Error ? err.message : fallback
  return ROLE_ERROR_HINTS[message] ?? message
}

// statusBadge 渲染状态列：0 禁用、1 启用；缺省按启用兜底（创建缺省启用）。
function statusBadge(status: number | undefined): ReactNode {
  return status === STATUS_DISABLED ? (
    <Badge variant="outline">禁用</Badge>
  ) : (
    <Badge variant="secondary">启用</Badge>
  )
}

// roleTypeBadge 渲染类型列：1 系统预置、其余按自定义兜底。
function roleTypeBadge(roleType: number): ReactNode {
  return roleType === ROLE_TYPE_PRESET ? (
    <Badge variant="outline">系统预置</Badge>
  ) : (
    <Badge variant="secondary">自定义</Badge>
  )
}

// dataScopeLabel 渲染数据范围规则名；未知取值回退原值不伪造语义。
function dataScopeLabel(dataScope: number): string {
  return DATA_SCOPE_LABELS[dataScope] ?? String(dataScope)
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
  submitDisabled,
}: {
  pending: boolean
  submitLabel: string
  submitDisabled?: boolean
}) {
  return (
    <DialogFooter>
      <DialogClose asChild>
        <Button type="button" variant="outline" disabled={pending}>
          取消
        </Button>
      </DialogClose>
      <Button type="submit" disabled={pending || submitDisabled === true}>
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

// parsePositiveId 校验正整数形态的字符串 ID（int64 字符串口径），空值视为
// 未填写返回 undefined。
function parsePositiveId(raw: string): string | undefined | null {
  const trimmed = raw.trim()
  if (trimmed === '') {
    return undefined
  }
  return /^[1-9]\d*$/.test(trimmed) ? trimmed : null
}

// parseIdList 把逗号/空白分隔的 ID 串解析为去重后的字符串 ID 数组；空串
// 返回空数组；非正整数输入返回 null 由调用方报错。
function parseIdList(raw: string): string[] | null {
  const parts = raw
    .split(/[\s,，;；]+/)
    .map((part) => part.trim())
    .filter((part) => part !== '')
  const ids = new Set<string>()
  for (const part of parts) {
    if (!/^[1-9]\d*$/.test(part)) {
      return null
    }
    ids.add(part)
  }
  return [...ids]
}

// ---------- 新建角色 ----------

function RoleCreateDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState('')
  const [code, setCode] = useState('')
  const [remark, setRemark] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (name.trim() === '' || code.trim() === '') {
      setError('name and code are required')
      return
    }
    setPending(true)
    try {
      await createRole({
        name: name.trim(),
        code: code.trim(),
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`角色 ${name.trim()} 已创建`)
      onCreated()
      onClose()
    } catch (err) {
      setError(roleErrorHint(err, 'create role failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建角色</DialogTitle>
          <DialogDescription>
            编码与角色名租户内唯一且创建后不可变；数据范围缺省仅本人，走「数据范围」单独配置。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="create-role-name" label="角色名">
            <Input id="create-role-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-role-code" label="角色编码">
            <Input id="create-role-code" value={code} onChange={(e) => setCode(e.target.value)} autoComplete="off" placeholder="如：operator" />
          </Field>
          <Field id="create-role-remark" label="备注">
            <Input id="create-role-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="创建" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 编辑角色 ----------

function RoleEditDialog({
  role,
  onClose,
  onSaved,
}: {
  role: RoleDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(role.name)
  const [status, setStatus] = useState(role.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED)
  const [remark, setRemark] = useState(role.remark ?? '')
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
      // 更新只覆盖 name/status/remark：编码与类型是身份列，数据范围走
      // 「数据范围」弹窗单独维护。
      await updateRole({
        id: role.id,
        status,
        name: name.trim(),
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`角色 ${name.trim()} 已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(roleErrorHint(err, 'update role failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑角色</DialogTitle>
          <DialogDescription>
            仅名称、状态与备注可改；编码 {role.code} 创建后不可变，数据范围走「数据范围」弹窗。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="edit-role-name" label="角色名">
            <Input id="edit-role-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <div className="flex items-center gap-2">
            {/* 开关的可访问名保持稳定，状态词另起展示位随开关联动。 */}
            <Switch id="edit-role-status" checked={status === STATUS_ENABLED} onCheckedChange={(checked) => setStatus(checked ? STATUS_ENABLED : STATUS_DISABLED)} />
            <Label htmlFor="edit-role-status">状态</Label>
            <span aria-live="polite" className="text-sm text-muted-foreground">
              {status === STATUS_ENABLED ? '启用' : '禁用'}
            </span>
          </div>
          <Field id="edit-role-remark" label="备注">
            <Input id="edit-role-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="保存" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 数据范围配置 ----------

// RoleDataScopeForm 是数据范围配置表单：在已加载配置上编辑。整组替换语义
// 下表单只改它展示的面——ID 范围明细仅替换当前规则对应类型的条目（其余
// 类型的惰性条目原样透传），分发部门集合整组替换，留空即清空。
function RoleDataScopeForm({
  role,
  cfg,
  onClose,
  onSaved,
}: {
  role: RoleDTO
  cfg: RoleDataScopeDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [dataScope, setDataScope] = useState(cfg.dataScope)
  // scopeDeptIdRaw 留空即 NULL（全租户可分配）。
  const [scopeDeptIdRaw, setScopeDeptIdRaw] = useState(cfg.scopeDeptId ?? '')
  // scopeIdsRaw 是当前规则对应类型的明细 ID 输入（逗号/空白分隔）。
  const [scopeIdsRaw, setScopeIdsRaw] = useState(() =>
    cfg.idScopes
      .filter((entry) => entry.type === scopeIdTypeOf(cfg.dataScope))
      .map((entry) => entry.dataId)
      .join(', '),
  )
  const [allowedRaw, setAllowedRaw] = useState(cfg.allowedDeptIds.join(', '))
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const faceType = scopeIdTypeOf(dataScope)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const scopeDeptId = parsePositiveId(scopeDeptIdRaw)
    if (scopeDeptId === null) {
      setError('scope dept must be a positive id')
      return
    }
    let scopeIds: string[] = []
    if (faceType !== undefined) {
      const parsed = parseIdList(scopeIdsRaw)
      if (parsed === null) {
        setError('scope ids must be positive ids')
        return
      }
      scopeIds = parsed
    }
    const allowed = parseIdList(allowedRaw)
    if (allowed === null) {
      setError('allowed dept ids must be positive ids')
      return
    }
    // 明细整组差量同步：非当前类型的惰性条目原样保留，当前类型按输入替换。
    const idScopes = [
      ...cfg.idScopes.filter((entry) => faceType === undefined || entry.type !== faceType),
      ...(faceType !== undefined ? scopeIds.map((dataId) => ({ type: faceType, dataId })) : []),
    ]
    setPending(true)
    try {
      await setRoleDataScope(role.id, {
        dataScope,
        scopeDeptId,
        idScopes,
        allowedDeptIds: allowed,
      })
      toast.success(`角色 ${role.name} 的数据范围已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(roleErrorHint(err, 'set data scope failed'))
    } finally {
      setPending(false)
    }
  }

  // 切换规则时同步清空/重填 ID 集合输入：面随规则切换，旧类型输入不残留。
  const changeDataScope = (next: number) => {
    setDataScope(next)
    const nextType = scopeIdTypeOf(next)
    setScopeIdsRaw(
      nextType === undefined
        ? ''
        : cfg.idScopes
            .filter((entry) => entry.type === nextType)
            .map((entry) => entry.dataId)
            .join(', '),
    )
  }

  return (
    <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
      <Field id="data-scope-rule" label="数据范围规则">
        <Select
          value={String(dataScope)}
          onValueChange={(value) => changeDataScope(Number(value))}
        >
          <SelectTrigger id="data-scope-rule" aria-label="数据范围规则" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {[DATA_SCOPE_SELF, DATA_SCOPE_DEPT, DATA_SCOPE_DEPT_TREE, DATA_SCOPE_USER_IDS, DATA_SCOPE_DEPT_IDS, DATA_SCOPE_GROUP_IDS, DATA_SCOPE_ALL].map((value) => (
              <SelectItem key={value} value={String(value)}>
                {DATA_SCOPE_LABELS[value]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field id="data-scope-scope-dept" label="分配层级限定部门 ID（可选，留空=全租户可分配）">
        <Input
          id="data-scope-scope-dept"
          value={scopeDeptIdRaw}
          onChange={(e) => setScopeDeptIdRaw(e.target.value)}
          autoComplete="off"
          placeholder="仅该部门及子部门内可分配该角色"
        />
      </Field>
      {faceType !== undefined && (
        <Field id="data-scope-scope-ids" label={SCOPE_FACE_LABELS[faceType]}>
          <Input
            id="data-scope-scope-ids"
            value={scopeIdsRaw}
            onChange={(e) => setScopeIdsRaw(e.target.value)}
            autoComplete="off"
            placeholder="如：11, 12"
          />
        </Field>
      )}
      <Field id="data-scope-allowed-depts" label="分发部门 ID（逗号或空白分隔，留空=遵循通用分配逻辑）">
        <Input
          id="data-scope-allowed-depts"
          value={allowedRaw}
          onChange={(e) => setAllowedRaw(e.target.value)}
          autoComplete="off"
        />
      </Field>
      <p className="text-xs text-muted-foreground">
        分发部门只决定谁能被分配该角色（命中映射时目标用户须属映射部门）；持有者的数据可见边界由上方规则与明细决定。
      </p>
      <FormError message={error} />
      <DialogActions pending={pending} submitLabel="保存" />
    </form>
  )
}

function RoleDataScopeDialog({
  role,
  onClose,
  onSaved,
}: {
  role: RoleDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [cfg, setCfg] = useState<RoleDataScopeDTO | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  // 取数：requestSeq 使迟到旧响应被丢弃，卸载后不再写状态。
  const requestSeq = useRef(0)
  useEffect(() => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setLoadError(null)
    fetchRoleDataScope(role.id)
      .then((loaded) => {
        if (requestSeq.current === seq) {
          setCfg(loaded)
        }
      })
      .catch((err: unknown) => {
        if (requestSeq.current === seq) {
          setLoadError(err instanceof Error ? err.message : '加载失败')
        }
      })
  }, [role.id, reloadKey])

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>数据范围 — {role.name}</DialogTitle>
          <DialogDescription>
            整组替换：保存即以表单当前内容整体覆盖该角色的数据范围配置。
          </DialogDescription>
        </DialogHeader>
        {loadError !== null ? (
          <div className="grid gap-3">
            <Alert variant="destructive">
              <AlertDescription className="flex items-center justify-between gap-3">
                <span>数据范围加载失败：{loadError}</span>
                <Button type="button" variant="outline" size="sm" onClick={reload}>
                  重试
                </Button>
              </AlertDescription>
            </Alert>
          </div>
        ) : cfg === null ? (
          <div className="grid h-24 place-items-center">
            <Spinner aria-hidden className="size-5" />
          </div>
        ) : (
          <RoleDataScopeForm role={role} cfg={cfg} onClose={onClose} onSaved={onSaved} />
        )}
      </DialogContent>
    </Dialog>
  )
}

// ---------- 菜单授权 ----------

// collectIds 按先序收集子树内（含自身）的菜单 ID；onlyEnabled 时跳过停用
// 菜单（停用菜单不可授权，SetRoleMenus 按存在且启用判定）。
function collectIds(node: MenuTreeNodeDTO, onlyEnabled: boolean): string[] {
  const out: string[] = []
  const walk = (current: MenuTreeNodeDTO) => {
    if (!onlyEnabled || current.menu.status !== STATUS_DISABLED) {
      out.push(current.menu.id)
    }
    for (const child of current.children) {
      walk(child)
    }
  }
  walk(node)
  return out
}

// MenuGrantNode 渲染授权树节点：勾选仅表达「该菜单被授权」，切换目录时
// 级联勾选/取消整棵子树（停用节点除外）。
function MenuGrantNode({
  node,
  depth,
  checked,
  onToggle,
}: {
  node: MenuTreeNodeDTO
  depth: number
  checked: ReadonlySet<string>
  onToggle: (node: MenuTreeNodeDTO) => void
}) {
  const { menu, children } = node
  const disabled = menu.status === STATUS_DISABLED
  const selfChecked = checked.has(menu.id)
  const descendantChecked = children.some(
    (child) => collectIds(child, false).some((id) => checked.has(id)),
  )
  const state = selfChecked ? true : descendantChecked ? 'indeterminate' : false
  return (
    <li>
      <div className="flex items-center gap-2 rounded-lg py-1 pr-2 hover:bg-soft" style={{ paddingLeft: depth * 20 + 4 }}>
        <Checkbox
          aria-label={menu.name}
          checked={state}
          disabled={disabled}
          onCheckedChange={() => onToggle(node)}
        />
        <span className={disabled ? 'truncate text-sm text-muted-foreground' : 'truncate text-sm font-medium text-foreground'}>
          {menu.name}
        </span>
        <span className="shrink-0 text-xs text-muted-foreground">#{menu.id}</span>
        <span className="shrink-0 text-xs text-muted-foreground">{menu.permissionCode}</span>
        {disabled && <Badge variant="outline">禁用</Badge>}
      </div>
      {children.length > 0 && (
        <ul aria-label={`${menu.name} 的子菜单`} className="space-y-0.5">
          {children.map((child) => (
            <MenuGrantNode key={child.menu.id} node={child} depth={depth + 1} checked={checked} onToggle={onToggle} />
          ))}
        </ul>
      )}
    </li>
  )
}

function RoleMenusDialog({
  role,
  onClose,
  onSaved,
}: {
  role: RoleDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [tree, setTree] = useState<MenuTreeReplyDTO | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [checked, setChecked] = useState<ReadonlySet<string>>(new Set())
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)

  // 取数：菜单树与当前有效授权并行加载；两者都成功才可保存（树未成功
  // 加载前没有完整可选集合、授权未加载前没有回显基线，贸然保存会误清空）。
  const requestSeq = useRef(0)
  useEffect(() => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setLoadError(null)
    setTree(null)
    Promise.all([fetchMenuTree(), listRoleMenus(role.id)])
      .then(([treeReply, menus]) => {
        if (requestSeq.current !== seq) {
          return
        }
        setTree(treeReply)
        setChecked(new Set(menus.menuIds))
      })
      .catch((err: unknown) => {
        if (requestSeq.current === seq) {
          setLoadError(err instanceof Error ? err.message : '加载失败')
        }
      })
  }, [role.id, reloadKey])

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // toggleNode 切换一个节点：勾选级联勾选子树内全部可授权（启用）菜单，
  // 取消则整棵子树一并移除。
  const toggleNode = useCallback((node: MenuTreeNodeDTO) => {
    setChecked((current) => {
      const ids = collectIds(node, true)
      const next = new Set(current)
      const willCheck = !next.has(node.menu.id)
      for (const id of ids) {
        if (willCheck) {
          next.add(id)
        } else {
          next.delete(id)
        }
      }
      return next
    })
  }, [])

  const submit = async () => {
    // 提交勾选后的完整有效菜单集合（数值升序，对齐回显集合口径）。
    const menuIds = [...checked].sort((a, b) => Number(a) - Number(b))
    setPending(true)
    setError(null)
    try {
      await setRoleMenus(role.id, menuIds)
      toast.success(`角色 ${role.name} 的菜单授权已更新（共 ${menuIds.length} 项）`)
      onSaved()
      onClose()
    } catch (err) {
      setError(roleErrorHint(err, 'set role menus failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="lg">
        <DialogHeader>
          <DialogTitle>菜单授权 — {role.name}</DialogTitle>
          <DialogDescription>
            勾选该角色可用的菜单与权限点，保存按勾选集合整组替换（清空全部勾选即收回全部授权）。
          </DialogDescription>
        </DialogHeader>

        {loadError !== null ? (
          <Alert variant="destructive">
            <AlertDescription className="flex items-center justify-between gap-3">
              <span>菜单树加载失败：{loadError}</span>
              <Button type="button" variant="outline" size="sm" onClick={reload}>
                重试
              </Button>
            </AlertDescription>
          </Alert>
        ) : tree === null ? (
          <div className="grid h-32 place-items-center">
            <Spinner aria-hidden className="size-5" />
          </div>
        ) : (
          <div aria-busy={pending} className="max-h-80 overflow-auto rounded-lg border p-2">
            {tree.roots.length === 0 ? (
              <p className="grid h-24 place-items-center text-sm text-muted-foreground">暂无可授权菜单</p>
            ) : (
              <ul aria-label="菜单授权树" className="space-y-0.5">
                {tree.roots.map((root) => (
                  <MenuGrantNode key={root.menu.id} node={root} depth={0} checked={checked} onToggle={toggleNode} />
                ))}
              </ul>
            )}
          </div>
        )}
        <FormError message={error} />
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="outline" disabled={pending}>
              取消
            </Button>
          </DialogClose>
          {/* 树未成功加载前禁用保存：没有回显基线就不能构造完整集合，防止误清空授权。 */}
          <Button type="button" disabled={pending || tree === null} onClick={() => { void submit() }}>
            保存授权
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 删除确认 ----------

function RoleDeleteDialog({
  role,
  onClose,
  onDeleted,
}: {
  role: RoleDTO
  onClose: () => void
  onDeleted: () => void
}) {
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      await deleteRole(role.id)
      toast.success(`角色 ${role.name} 已删除`)
      onDeleted()
      onClose()
    } catch (err) {
      // 系统预置角色（ROLE_PRESET_UNDELETABLE）等拒绝给映射文案。
      toast.error(`删除失败：${roleErrorHint(err, 'delete role failed')}`)
      onClose()
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除角色 {role.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            软删除后该角色立即失去分配资格与数据范围效力；编码 {role.code} 可被新角色复用。
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
  role,
  onOpen,
}: {
  role: RoleDTO
  onOpen: (state: RoleDialogState) => void
}) {
  return (
    <RowActionsMenu
      rowLabel={role.name}
      info={[
        { label: '角色名', value: role.name },
        { label: '编码', value: role.code },
      ]}
      actions={[
        { label: '编辑', onSelect: () => onOpen({ kind: 'edit', role }) },
        { label: '数据范围', onSelect: () => onOpen({ kind: 'data-scope', role }) },
        { label: '菜单授权', onSelect: () => onOpen({ kind: 'menus', role }) },
        { label: '删除', destructive: true, onSelect: () => onOpen({ kind: 'delete', role }) },
      ]}
    />
  )
}

// ---------- 页面 ----------

// RoleManagePage 角色管理：列表（筛选/列定义动态响应 GetListSchema 响应）
// + 行内动作（编辑/数据范围/菜单授权/删除）+ 新建。数据范围与菜单授权都
// 是整组替换语义，成功后重查列表。
export function RoleManagePage() {
  const [filters, setFilters] = useState<QueryCondition[]>([])
  const [reloadKey, setReloadKey] = useState(0)
  const [dialog, setDialog] = useState<RoleDialogState | null>(null)
  const { schema, error: schemaError, loading: schemaLoading, retry: retrySchema } = useListSchema('sys_role')

  const filterFields = useMemo(() => (schema === null ? [] : filterFieldsFromSchema(schema)), [schema])
  const defaultSort = useMemo<QuerySort[]>(() => (schema === null ? [] : defaultSortFromSchema(schema)), [schema])
  const pageSize = schema?.pagination?.defaultPageSize ?? 20

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // columns 动态响应 schema：页面仅覆写必要附加项——长 label 列头与徽标/
  // 规则名渲染；行操作列不对应查询字段，恒追加在末尾。
  const columns = useMemo<DataTableColumn<RoleDTO>[]>(
    () =>
      schema === null
        ? []
        : schemaColumns<RoleDTO>(
            schema,
            {
              roleType: { header: '类型', render: (role) => roleTypeBadge(role.roleType) },
              dataScope: { header: '数据范围', render: (role) => dataScopeLabel(role.dataScope) },
              status: { header: '状态', render: (role) => statusBadge(role.status) },
            },
            [
              {
                key: 'actions',
                header: '操作',
                className: 'w-16 text-right',
                sticky: 'right',
                render: (role) => <RowActions role={role} onOpen={setDialog} />,
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
          title="角色管理"
          description="租户内角色与数据范围、菜单授权的维护入口；系统预置角色不可删除。"
          filters={<QueryFilter fields={filterFields} value={filters} onChange={setFilters} onSearch={() => setReloadKey((key) => key + 1)} ariaLabel="角色筛选" />}
          rowKey={(role) => role.id}
          columns={columns}
          fetchPage={listRoles}
          where={filters}
          schema={schema}
          reloadKey={reloadKey}
          defaultSort={defaultSort}
          pageSize={pageSize}
          emptyText="没有符合条件的角色"
          toolbar={
            <ToolbarIconButton label="新建角色" onClick={() => setDialog({ kind: 'create' })}>
              <PlusIcon aria-hidden className="size-4" />
            </ToolbarIconButton>
          }
        />
      )}

      {dialog === null ? null : dialog.kind === 'create' ? (
        <RoleCreateDialog onClose={() => setDialog(null)} onCreated={reload} />
      ) : dialog.kind === 'edit' ? (
        <RoleEditDialog role={dialog.role} onClose={() => setDialog(null)} onSaved={reload} />
      ) : dialog.kind === 'data-scope' ? (
        <RoleDataScopeDialog role={dialog.role} onClose={() => setDialog(null)} onSaved={reload} />
      ) : dialog.kind === 'menus' ? (
        <RoleMenusDialog role={dialog.role} onClose={() => setDialog(null)} onSaved={reload} />
      ) : (
        <RoleDeleteDialog role={dialog.role} onClose={() => setDialog(null)} onDeleted={reload} />
      )}
    </div>
  )
}
