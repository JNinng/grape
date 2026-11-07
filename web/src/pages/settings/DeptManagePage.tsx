import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { ChevronDownIcon, ChevronRightIcon, InfoIcon, PlusIcon } from 'lucide-react'
import {
  createDept,
  deleteDept,
  fetchDeptTree,
  moveDept,
  updateDept,
  type DeptDTO,
  type DeptTreeNodeDTO,
  type DeptTreeReplyDTO,
} from '@/api/depts'
import { listOwnerGroupOptions, listOwnerUserOptions } from '@/api/assignOptions'
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
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { OptionSelect } from '@/components/manage/option-select'
import { RowActionsMenu, type RowActionItem } from '@/components/manage/row-actions'
import { PERM } from '@/auth/permissionCodes'
import { usePermissions } from '@/auth/permissions'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import { toast } from '@/components/ui/toast'

// 部门状态与存储层 sys_dept.status 取值一致。
const STATUS_DISABLED = 0
const STATUS_ENABLED = 1

// ROOT_PARENT 是「上级部门」下拉里根部门的哨兵值：部门 id 均为正整数的
// 字符串形态，'root' 不会与之冲突，提交时翻译回 undefined（protojson 未设置）。
const ROOT_PARENT = '__root__'

// DeptDialogState 是当前打开的行内动作弹窗：create 的 parent 缺省即建根
// 部门（从「新建子部门」进入时预选父级）。
type DeptDialogState =
  | { kind: 'create'; parent?: DeptDTO }
  | { kind: 'edit'; dept: DeptDTO }
  | { kind: 'move'; dept: DeptDTO }
  | { kind: 'delete'; dept: DeptDTO }

// DEPT_ERROR_HINTS 把后端部门错误（org/v1/error_reason.proto 各 reason 的
// 消息文案）映射为可读英文提示；词表外原样透传，不吞掉未知错误。
const DEPT_ERROR_HINTS: Record<string, string> = {
  'dept not found': 'dept not found; it may have been deleted',
  'invalid dept argument': 'invalid argument; the parent may be missing or the move would create a cycle',
  'dept already exists': 'a dept with the same name already exists in the tenant',
  'dept has active children': 'dept has active children; delete its child depts first',
}

// deptErrorHint 归一动作失败的展示文案：命中 reason 词表给可读提示，
// 否则透传后端原始消息。
function deptErrorHint(err: unknown, fallback: string): string {
  const message = err instanceof Error ? err.message : fallback
  return DEPT_ERROR_HINTS[message] ?? message
}

// flattenTree 把树按先序摊平（深度递增），供「上级部门」下拉选用。
function flattenTree(nodes: DeptTreeNodeDTO[]): DeptDTO[] {
  const out: DeptDTO[] = []
  for (const node of nodes) {
    out.push(node.dept)
    out.push(...flattenTree(node.children))
  }
  return out
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

// subtreeIDs 返回 rootId 及其全部后代部门 ID（按 parentId 关系遍历）：
// 编辑/移动时把整棵子树从新父级选项中排除，避免提交后才被后端拒绝。
function subtreeIDs(depts: DeptDTO[], rootId: string): ReadonlySet<string> {
  const childrenByParent = new Map<string, string[]>()
  for (const dept of depts) {
    if (dept.parentId === undefined) {
      continue
    }
    const siblings = childrenByParent.get(dept.parentId) ?? []
    siblings.push(dept.id)
    childrenByParent.set(dept.parentId, siblings)
  }
  const out = new Set<string>()
  const stack = [rootId]
  while (stack.length > 0) {
    const id = stack.pop() as string
    if (out.has(id)) {
      continue
    }
    out.add(id)
    stack.push(...(childrenByParent.get(id) ?? []))
  }
  return out
}

// ParentSelect 是「上级部门」下拉：root 哨兵表示无父级（根部门）；
// excludeIds 供编辑/移动弹窗排除目标部门自身与其子树。
function ParentSelect({
  id,
  value,
  depts,
  excludeIds,
  onValueChange,
}: {
  id: string
  value: string
  depts: DeptDTO[]
  excludeIds?: ReadonlySet<string>
  onValueChange: (value: string) => void
}) {
  return (
    <Select value={value} onValueChange={onValueChange}>
      <SelectTrigger id={id} aria-label="上级部门" className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={ROOT_PARENT}>无（根部门）</SelectItem>
        {depts
          .filter((dept) => !(excludeIds?.has(dept.id) ?? false))
          .map((dept) => (
            <SelectItem key={dept.id} value={dept.id}>
              {dept.name}
            </SelectItem>
          ))}
      </SelectContent>
    </Select>
  )
}

// ---------- 新建部门 ----------

function DeptCreateDialog({
  parent,
  depts,
  onClose,
  onCreated,
}: {
  parent?: DeptDTO
  depts: DeptDTO[]
  onClose: () => void
  onCreated: () => void
}) {
  const [parentId, setParentId] = useState(parent?.id ?? ROOT_PARENT)
  const [name, setName] = useState('')
  const [ownerUserId, setOwnerUserId] = useState<string[]>([])
  const [ownerUserGroupId, setOwnerUserGroupId] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const trimmedName = name.trim()
    const owner = ownerUserId[0]
    if (trimmedName === '' || owner === undefined) {
      setError('name and dept admin are required')
      return
    }
    setPending(true)
    try {
      await createDept({
        name: trimmedName,
        parentId: parentId === ROOT_PARENT ? undefined : parentId,
        ownerUserId: owner,
        ownerUserGroupId: ownerUserGroupId[0],
      })
      toast.success(`部门 ${trimmedName} 已创建`)
      onCreated()
      onClose()
    } catch (err) {
      setError(deptErrorHint(err, 'create dept failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建部门</DialogTitle>
          <DialogDescription>
            上级部门缺省为根部门；部门管理员必选，归属用户组可选（候选均为当前可见范围内的活跃对象），状态缺省启用。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="create-dept-parent" label="上级部门">
            <ParentSelect id="create-dept-parent" value={parentId} depts={depts} onValueChange={setParentId} />
          </Field>
          <Field id="create-dept-name" label="部门名称">
            <Input id="create-dept-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-dept-owner-user" label="部门管理员">
            <OptionSelect
              id="create-dept-owner-user"
              ariaLabel="部门管理员"
              mode="single"
              value={ownerUserId}
              onChange={setOwnerUserId}
              loadOptions={listOwnerUserOptions}
              placeholder="选择部门管理员"
            />
          </Field>
          <Field id="create-dept-owner-group" label="归属用户组（可选）">
            <OptionSelect
              id="create-dept-owner-group"
              ariaLabel="归属用户组"
              mode="single"
              value={ownerUserGroupId}
              onChange={setOwnerUserGroupId}
              loadOptions={listOwnerGroupOptions}
              placeholder="不设归属用户组"
            />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="创建" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 编辑部门 ----------

function DeptEditDialog({
  dept,
  depts,
  onClose,
  onSaved,
}: {
  dept: DeptDTO
  depts: DeptDTO[]
  onClose: () => void
  onSaved: () => void
}) {
  const [parentId, setParentId] = useState(dept.parentId ?? ROOT_PARENT)
  const [name, setName] = useState(dept.name)
  const [ownerUserId, setOwnerUserId] = useState<string[]>([dept.ownerUserId])
  const [ownerUserGroupId, setOwnerUserGroupId] = useState<string[]>(
    dept.ownerUserGroupId === undefined ? [] : [dept.ownerUserGroupId],
  )
  const [status, setStatus] = useState(dept.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const owner = ownerUserId[0]
    if (name.trim() === '' || owner === undefined) {
      setError('name and dept admin are required')
      return
    }
    setPending(true)
    try {
      // PUT 覆盖语义：携带完整可变字段集合；parentId 缺省即移为根，
      // ownerUserGroupId 缺省即清空归属用户组。
      await updateDept({
        id: dept.id,
        status,
        name: name.trim(),
        parentId: parentId === ROOT_PARENT ? undefined : parentId,
        ownerUserId: owner,
        ownerUserGroupId: ownerUserGroupId[0],
      })
      toast.success(`部门 ${name.trim()} 已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(deptErrorHint(err, 'update dept failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑部门</DialogTitle>
          <DialogDescription>
            覆盖上级部门、名称、部门管理员、归属用户组与状态；上级部门变更即移动，防环由后端校验。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="edit-dept-parent" label="上级部门">
            <ParentSelect
              id="edit-dept-parent"
              value={parentId}
              depts={depts}
              excludeIds={subtreeIDs(depts, dept.id)}
              onValueChange={setParentId}
            />
          </Field>
          <Field id="edit-dept-name" label="部门名称">
            <Input id="edit-dept-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="edit-dept-owner-user" label="部门管理员">
            <OptionSelect
              id="edit-dept-owner-user"
              ariaLabel="部门管理员"
              mode="single"
              value={ownerUserId}
              onChange={setOwnerUserId}
              loadOptions={listOwnerUserOptions}
              pinnedValues={[dept.ownerUserId]}
              placeholder="选择部门管理员"
            />
          </Field>
          <Field id="edit-dept-owner-group" label="归属用户组（可选）">
            <OptionSelect
              id="edit-dept-owner-group"
              ariaLabel="归属用户组"
              mode="single"
              value={ownerUserGroupId}
              onChange={setOwnerUserGroupId}
              loadOptions={listOwnerGroupOptions}
              pinnedValues={dept.ownerUserGroupId === undefined ? [] : [dept.ownerUserGroupId]}
              placeholder="不设归属用户组"
            />
          </Field>
          <div className="flex items-center gap-2">
            {/* 开关的可访问名保持稳定，状态词另起展示位随开关联动。 */}
            <Switch id="edit-dept-status" checked={status === STATUS_ENABLED} onCheckedChange={(checked) => setStatus(checked ? STATUS_ENABLED : STATUS_DISABLED)} />
            <Label htmlFor="edit-dept-status">状态</Label>
            <span aria-live="polite" className="text-sm text-muted-foreground">
              {status === STATUS_ENABLED ? '启用' : '禁用'}
            </span>
          </div>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="保存" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 移动部门 ----------

function DeptMoveDialog({
  dept,
  depts,
  onClose,
  onMoved,
}: {
  dept: DeptDTO
  depts: DeptDTO[]
  onClose: () => void
  onMoved: () => void
}) {
  const [parentId, setParentId] = useState(dept.parentId ?? ROOT_PARENT)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setPending(true)
    try {
      // 成环（移入自身子树）与未知父级由后端拒绝，前端透传错误提示。
      await moveDept(dept.id, parentId === ROOT_PARENT ? undefined : parentId)
      toast.success(`部门 ${dept.name} 已移动`)
      onMoved()
      onClose()
    } catch (err) {
      setError(deptErrorHint(err, 'move dept failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>移动部门</DialogTitle>
          <DialogDescription>
            选择 {dept.name} 的新上级部门，选「无（根部门）」移为根；移入其自身子树会被后端拒绝。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="move-dept-parent" label="新上级部门">
            <ParentSelect
              id="move-dept-parent"
              value={parentId}
              depts={depts}
              excludeIds={subtreeIDs(depts, dept.id)}
              onValueChange={setParentId}
            />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="移动" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 删除确认 ----------

function DeptDeleteDialog({
  dept,
  onClose,
  onDeleted,
}: {
  dept: DeptDTO
  onClose: () => void
  onDeleted: () => void
}) {
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      await deleteDept(dept.id)
      toast.success(`部门 ${dept.name} 已删除`)
      onDeleted()
      onClose()
    } catch (err) {
      // 有未删除子部门（DEPT_HAS_CHILDREN）时提示先删子级。
      toast.error(`删除失败：${deptErrorHint(err, 'delete dept failed')}`)
      onClose()
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除部门 {dept.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            软删除后该部门不再可见；存在未删除子部门时不可删除，需自叶向根逐层删除。
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

// ---------- 树节点 ----------

// DeptTreeNode 渲染一个部门节点与其子树：缩进按深度递增，带子节点的行
// 可展开/收起（缺省全展开）。行内尾随该部门的管理员、状态与操作菜单。
function DeptTreeNode({
  node,
  depth,
  collapsedIds,
  onToggle,
  onOpen,
}: {
  node: DeptTreeNodeDTO
  depth: number
  collapsedIds: ReadonlySet<string>
  onToggle: (id: string) => void
  onOpen: (state: DeptDialogState) => void
}) {
  const { dept, children } = node
  const collapsed = collapsedIds.has(dept.id)
  return (
    <li>
      <div
        className="flex items-center gap-2 rounded-lg py-1.5 pr-2 hover:bg-soft"
        style={{ paddingLeft: depth * 20 + 8 }}
      >
        {children.length > 0 ? (
          <button
            type="button"
            aria-label={collapsed ? `展开 ${dept.name}` : `收起 ${dept.name}`}
            aria-expanded={!collapsed}
            onClick={() => onToggle(dept.id)}
            className="flex size-4 shrink-0 cursor-pointer items-center justify-center rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          >
            {collapsed ? <ChevronRightIcon aria-hidden className="size-4" /> : <ChevronDownIcon aria-hidden className="size-4" />}
          </button>
        ) : (
          <span aria-hidden className="size-4 shrink-0" />
        )}
        <span className="truncate text-sm font-medium text-foreground">{dept.name}</span>
        <span className="shrink-0 text-xs text-muted-foreground">#{dept.id}</span>
        <span className="ml-auto flex shrink-0 items-center gap-2">
          <span className="hidden text-xs text-muted-foreground sm:inline">
            管理员 {dept.ownerUserId} · {formatDateTime(dept.createdAt)}
          </span>
          {statusBadge(dept.status)}
          <RowActions dept={dept} onOpen={onOpen} />
        </span>
      </div>
      {children.length > 0 && !collapsed && (
        <ul aria-label={`${dept.name} 的子部门`} className="space-y-0.5">
          {children.map((child) => (
            <DeptTreeNode
              key={child.dept.id}
              node={child}
              depth={depth + 1}
              collapsedIds={collapsedIds}
              onToggle={onToggle}
              onOpen={onOpen}
            />
          ))}
        </ul>
      )}
    </li>
  )
}

function RowActions({
  dept,
  onOpen,
}: {
  dept: DeptDTO
  onOpen: (state: DeptDialogState) => void
}) {
  const { can } = usePermissions()
  const actions: RowActionItem[] = []
  if (can(PERM.DeptUpdate)) {
    actions.push({ label: '编辑', onSelect: () => onOpen({ kind: 'edit', dept }) })
    actions.push({ label: '移动', onSelect: () => onOpen({ kind: 'move', dept }) })
  }
  if (can(PERM.DeptCreate)) {
    actions.push({ label: '新建子部门', onSelect: () => onOpen({ kind: 'create', parent: dept }) })
  }
  if (can(PERM.DeptDelete)) {
    actions.push({ label: '删除', destructive: true, onSelect: () => onOpen({ kind: 'delete', dept }) })
  }
  if (actions.length === 0) {
    return null
  }
  return (
    <RowActionsMenu
      rowLabel={dept.name}
      info={[
        { label: '部门名', value: dept.name },
        { label: '组长 ID', value: dept.ownerUserId },
      ]}
      actions={actions}
    />
  )
}

// ---------- 页面 ----------

// DeptManagePage 部门管理：租户组织树的维护入口。树接口一次返回租户全量
// 部门（无分页），主视图为可展开/收起的树；增删改与移动成功后整树重查。
export function DeptManagePage() {
  const [tree, setTree] = useState<DeptTreeReplyDTO | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [collapsedIds, setCollapsedIds] = useState<ReadonlySet<string>>(new Set())
  const [dialog, setDialog] = useState<DeptDialogState | null>(null)
  const { can } = usePermissions()

  // 取数：reloadKey 变化（动作成功后或手动重试）即重查全树；requestSeq 使
  // 迟到旧响应被丢弃，卸载后不再写状态。
  const requestSeq = useRef(0)
  useEffect(() => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setLoading(true)
    fetchDeptTree()
      .then((reply) => {
        if (requestSeq.current !== seq) {
          return
        }
        setTree(reply)
        setError(null)
      })
      .catch((err: unknown) => {
        if (requestSeq.current === seq) {
          setError(err instanceof Error ? err.message : '加载失败')
        }
      })
      .finally(() => {
        if (requestSeq.current === seq) {
          setLoading(false)
        }
      })
  }, [reloadKey])

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  const toggle = useCallback((id: string) => {
    setCollapsedIds((current) => {
      const next = new Set(current)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }, [])

  // depts 是「上级部门」下拉的候选（先序摊平的全量部门）。
  const depts = useMemo(() => (tree === null ? [] : flattenTree(tree.roots)), [tree])

  const totalNum = tree !== null ? Number(tree.total) : undefined

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 p-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex items-center gap-1">
          <h1 className="text-lg font-semibold text-foreground">部门管理</h1>
          {/* 信息图标悬停/聚焦显示页面说明（与 DataTable 标题行同款）。 */}
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <span
                  tabIndex={0}
                  role="button"
                  aria-label="关于部门管理"
                  className="cursor-help rounded-full p-0.5 text-muted-foreground hover:text-foreground"
                >
                  <InfoIcon aria-hidden className="size-4" />
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-72">租户组织树的维护入口；树一次返回租户内全部未删除部门，无分页。</TooltipContent>
            </Tooltip>
          </TooltipProvider>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <p aria-label="列表统计" className="text-sm text-muted-foreground">
            {totalNum !== undefined ? `共 ${totalNum} 个部门` : ''}
          </p>
          {can(PERM.DeptCreate) && (
            <Button type="button" onClick={() => setDialog({ kind: 'create' })}>
              <PlusIcon aria-hidden className="size-4" />
              新建部门
            </Button>
          )}
        </div>
      </div>

      {error !== null ? (
        <Alert variant="destructive">
          <AlertDescription className="flex items-center justify-between gap-3">
            <span>组织树加载失败：{error}</span>
            <Button type="button" variant="outline" size="sm" onClick={reload}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        <div aria-busy={loading} className="min-h-0 flex-1 overflow-auto rounded-lg border p-2">
          {tree !== null && tree.roots.length === 0 ? (
            <p className="grid h-32 place-items-center text-sm text-muted-foreground">暂无部门</p>
          ) : tree === null ? (
            <div className="grid h-32 place-items-center">
              <Spinner aria-hidden className="size-5" />
            </div>
          ) : (
            <ul aria-label="部门树" className="space-y-0.5">
              {tree.roots.map((root) => (
                <DeptTreeNode
                  key={root.dept.id}
                  node={root}
                  depth={0}
                  collapsedIds={collapsedIds}
                  onToggle={toggle}
                  onOpen={setDialog}
                />
              ))}
            </ul>
          )}
        </div>
      )}

      {dialog === null ? null : dialog.kind === 'create' ? (
        <DeptCreateDialog parent={dialog.parent} depts={depts} onClose={() => setDialog(null)} onCreated={reload} />
      ) : dialog.kind === 'edit' ? (
        <DeptEditDialog dept={dialog.dept} depts={depts} onClose={() => setDialog(null)} onSaved={reload} />
      ) : dialog.kind === 'move' ? (
        <DeptMoveDialog dept={dialog.dept} depts={depts} onClose={() => setDialog(null)} onMoved={reload} />
      ) : (
        <DeptDeleteDialog dept={dialog.dept} onClose={() => setDialog(null)} onDeleted={reload} />
      )}
    </div>
  )
}
