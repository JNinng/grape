import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { ChevronDownIcon, ChevronRightIcon, InfoIcon, PlusIcon } from 'lucide-react'
import {
  createMenu,
  deleteMenu,
  fetchMenuTree,
  moveMenu,
  updateMenu,
  MENU_TYPE_LABELS,
  type MenuDTO,
  type MenuTreeNodeDTO,
  type MenuTreeReplyDTO,
} from '@/api/menus'
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
import { RowActionsMenu } from '@/components/manage/row-actions'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import { toast } from '@/components/ui/toast'

// 菜单状态与存储层 sys_menu.status 取值一致。
const STATUS_DISABLED = 0
const STATUS_ENABLED = 1

// ROOT_PARENT 是「上级菜单」下拉里根菜单的哨兵值：菜单 id 均为正整数的
// 字符串形态，哨兵不会与之冲突，提交时翻译回 undefined（protojson 未设置）。
const ROOT_PARENT = '__root__'

// MenuDialogState 是当前打开的行内动作弹窗：create 的 parent 缺省即建根
// 菜单（从「新建子菜单」进入时预选父级）。
type MenuDialogState =
  | { kind: 'create'; parent?: MenuDTO }
  | { kind: 'edit'; menu: MenuDTO }
  | { kind: 'move'; menu: MenuDTO }
  | { kind: 'delete'; menu: MenuDTO }

// MENU_ERROR_HINTS 把后端菜单错误（perm/v1/error_reason.proto 各 reason 的
// 消息文案）映射为可读英文提示；词表外原样透传，不吞掉未知错误。
const MENU_ERROR_HINTS: Record<string, string> = {
  'menu not found': 'menu not found; it may have been deleted',
  'invalid menu argument': 'invalid argument; the parent may be missing or the move would create a cycle',
  'menu already exists': 'a menu with the same permission code already exists in the tenant',
  'menu has active children': 'menu has active children; delete its child menus first',
}

// menuErrorHint 归一动作失败的展示文案：命中 reason 词表给可读提示，
// 否则透传后端原始消息。
function menuErrorHint(err: unknown, fallback: string): string {
  const message = err instanceof Error ? err.message : fallback
  return MENU_ERROR_HINTS[message] ?? message
}

// flattenTree 把树按先序摊平（深度递增），供「上级菜单」下拉选用。
function flattenTree(nodes: MenuTreeNodeDTO[]): MenuDTO[] {
  const out: MenuDTO[] = []
  for (const node of nodes) {
    out.push(node.menu)
    out.push(...flattenTree(node.children))
  }
  return out
}

// flattenExcluding 先序摊平并跳过 excludeId 命中节点及其整棵子树：移动
// 弹窗用「排除自身及自身子树」在前端挡掉明显成环的选项（未知父级与其余
// 成环路径仍由后端拒绝兜底）。
function flattenExcluding(nodes: MenuTreeNodeDTO[], excludeId?: string): MenuDTO[] {
  const out: MenuDTO[] = []
  const walk = (list: MenuTreeNodeDTO[]) => {
    for (const node of list) {
      if (excludeId !== undefined && node.menu.id === excludeId) {
        continue
      }
      out.push(node.menu)
      walk(node.children)
    }
  }
  walk(nodes)
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

// menuTypeBadge 渲染类型列：1 目录、2 菜单、3 按钮；未知取值回退原值。
function menuTypeBadge(menuType: number): ReactNode {
  return <Badge variant="outline">{MENU_TYPE_LABELS[menuType] ?? String(menuType)}</Badge>
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

// ParentSelect 是「上级菜单」下拉：root 哨兵表示无父级（根菜单）；candidates
// 由调用方预过滤（移动弹窗排除自身及自身子树）。
function ParentSelect({
  id,
  value,
  menus,
  onValueChange,
}: {
  id: string
  value: string
  menus: MenuDTO[]
  onValueChange: (value: string) => void
}) {
  return (
    <Select value={value} onValueChange={onValueChange}>
      <SelectTrigger id={id} aria-label="上级菜单" className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={ROOT_PARENT}>无（根菜单）</SelectItem>
        {menus.map((menu) => (
          <SelectItem key={menu.id} value={menu.id}>
            {menu.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

// parseSortOrder 校验排序号为整数（树内同级按其升序），非法返回 null。
function parseSortOrder(raw: string): number | null {
  const trimmed = raw.trim()
  if (trimmed === '' || !/^-?\d+$/.test(trimmed)) {
    return null
  }
  const value = Number(trimmed)
  return Number.isSafeInteger(value) ? value : null
}

// ---------- 新建菜单 ----------

function MenuCreateDialog({
  parent,
  menus,
  onClose,
  onCreated,
}: {
  parent?: MenuDTO
  menus: MenuDTO[]
  onClose: () => void
  onCreated: () => void
}) {
  const [parentId, setParentId] = useState(parent?.id ?? ROOT_PARENT)
  const [name, setName] = useState('')
  const [permissionCode, setPermissionCode] = useState('')
  const [menuType, setMenuType] = useState(String(parent === undefined ? 1 : 2))
  const [sortOrder, setSortOrder] = useState('0')
  const [routePath, setRoutePath] = useState('')
  const [icon, setIcon] = useState('')
  const [component, setComponent] = useState('')
  const [permissionExpr, setPermissionExpr] = useState('')
  const [remark, setRemark] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const order = parseSortOrder(sortOrder)
    if (name.trim() === '' || permissionCode.trim() === '') {
      setError('name and permission code are required')
      return
    }
    if (order === null) {
      setError('sort order must be an integer')
      return
    }
    setPending(true)
    try {
      await createMenu({
        name: name.trim(),
        permissionCode: permissionCode.trim(),
        parentId: parentId === ROOT_PARENT ? undefined : parentId,
        menuType: Number(menuType),
        sortOrder: order,
        routePath: routePath.trim() === '' ? undefined : routePath.trim(),
        icon: icon.trim() === '' ? undefined : icon.trim(),
        component: component.trim() === '' ? undefined : component.trim(),
        permissionExpr: permissionExpr.trim() === '' ? undefined : permissionExpr.trim(),
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`菜单 ${name.trim()} 已创建`)
      onCreated()
      onClose()
    } catch (err) {
      setError(menuErrorHint(err, 'create menu failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建菜单</DialogTitle>
          <DialogDescription>
            上级菜单缺省为根菜单；权限编码与菜单类型创建后不可变，状态缺省启用。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="create-menu-parent" label="上级菜单">
            <ParentSelect id="create-menu-parent" value={parentId} menus={menus} onValueChange={setParentId} />
          </Field>
          <Field id="create-menu-name" label="名称">
            <Input id="create-menu-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-menu-code" label="权限编码">
            <Input id="create-menu-code" value={permissionCode} onChange={(e) => setPermissionCode(e.target.value)} autoComplete="off" placeholder="如：sys:user:create" />
          </Field>
          <Field id="create-menu-type" label="菜单类型">
            <Select value={menuType} onValueChange={setMenuType}>
              <SelectTrigger id="create-menu-type" aria-label="菜单类型" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="1">目录</SelectItem>
                <SelectItem value="2">菜单</SelectItem>
                <SelectItem value="3">按钮</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field id="create-menu-sort" label="排序号">
            <Input id="create-menu-sort" value={sortOrder} onChange={(e) => setSortOrder(e.target.value)} autoComplete="off" inputMode="numeric" />
          </Field>
          <Field id="create-menu-route" label="前端路由路径（可选）">
            <Input id="create-menu-route" value={routePath} onChange={(e) => setRoutePath(e.target.value)} autoComplete="off" placeholder="/system/menu" />
          </Field>
          <Field id="create-menu-icon" label="图标（可选）">
            <Input id="create-menu-icon" value={icon} onChange={(e) => setIcon(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-menu-component" label="前端组件路径（可选）">
            <Input id="create-menu-component" value={component} onChange={(e) => setComponent(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-menu-expr" label="权限表达式（可选）">
            <Input id="create-menu-expr" value={permissionExpr} onChange={(e) => setPermissionExpr(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="create-menu-remark" label="备注">
            <Input id="create-menu-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="创建" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 编辑菜单 ----------

function MenuEditDialog({
  menu,
  onClose,
  onSaved,
}: {
  menu: MenuDTO
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(menu.name)
  const [status, setStatus] = useState(menu.status === STATUS_DISABLED ? STATUS_DISABLED : STATUS_ENABLED)
  const [sortOrder, setSortOrder] = useState(String(menu.sortOrder))
  const [routePath, setRoutePath] = useState(menu.routePath ?? '')
  const [icon, setIcon] = useState(menu.icon ?? '')
  const [component, setComponent] = useState(menu.component ?? '')
  const [permissionExpr, setPermissionExpr] = useState(menu.permissionExpr ?? '')
  const [remark, setRemark] = useState(menu.remark ?? '')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const order = parseSortOrder(sortOrder)
    if (name.trim() === '') {
      setError('name is required')
      return
    }
    if (order === null) {
      setError('sort order must be an integer')
      return
    }
    setPending(true)
    try {
      // 更新只覆盖可变字段：权限编码与菜单类型是身份/类型列（创建后不可
      // 变），父子结构走「移动」。
      await updateMenu({
        id: menu.id,
        status,
        name: name.trim(),
        sortOrder: order,
        routePath: routePath.trim() === '' ? undefined : routePath.trim(),
        icon: icon.trim() === '' ? undefined : icon.trim(),
        component: component.trim() === '' ? undefined : component.trim(),
        permissionExpr: permissionExpr.trim() === '' ? undefined : permissionExpr.trim(),
        remark: remark.trim() === '' ? undefined : remark.trim(),
      })
      toast.success(`菜单 ${name.trim()} 已更新`)
      onSaved()
      onClose()
    } catch (err) {
      setError(menuErrorHint(err, 'update menu failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑菜单</DialogTitle>
          <DialogDescription>
            仅可变字段可改；权限编码 {menu.permissionCode} 与菜单类型创建后不可变，调整层级用「移动」。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="edit-menu-name" label="名称">
            <Input id="edit-menu-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
          </Field>
          <div className="flex items-center gap-2">
            {/* 开关的可访问名保持稳定，状态词另起展示位随开关联动。 */}
            <Switch id="edit-menu-status" checked={status === STATUS_ENABLED} onCheckedChange={(checked) => setStatus(checked ? STATUS_ENABLED : STATUS_DISABLED)} />
            <Label htmlFor="edit-menu-status">状态</Label>
            <span aria-live="polite" className="text-sm text-muted-foreground">
              {status === STATUS_ENABLED ? '启用' : '禁用'}
            </span>
          </div>
          <Field id="edit-menu-sort" label="排序号">
            <Input id="edit-menu-sort" value={sortOrder} onChange={(e) => setSortOrder(e.target.value)} autoComplete="off" inputMode="numeric" />
          </Field>
          <Field id="edit-menu-route" label="前端路由路径">
            <Input id="edit-menu-route" value={routePath} onChange={(e) => setRoutePath(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="edit-menu-icon" label="图标">
            <Input id="edit-menu-icon" value={icon} onChange={(e) => setIcon(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="edit-menu-component" label="前端组件路径">
            <Input id="edit-menu-component" value={component} onChange={(e) => setComponent(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="edit-menu-expr" label="权限表达式">
            <Input id="edit-menu-expr" value={permissionExpr} onChange={(e) => setPermissionExpr(e.target.value)} autoComplete="off" />
          </Field>
          <Field id="edit-menu-remark" label="备注">
            <Input id="edit-menu-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="保存" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 移动菜单 ----------

function MenuMoveDialog({
  menu,
  menus,
  onClose,
  onMoved,
}: {
  menu: MenuDTO
  menus: MenuDTO[]
  onClose: () => void
  onMoved: () => void
}) {
  const [parentId, setParentId] = useState(menu.parentId ?? ROOT_PARENT)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setPending(true)
    try {
      // 未知父级与其余成环路径由后端拒绝，前端透传错误提示。
      await moveMenu(menu.id, parentId === ROOT_PARENT ? undefined : parentId)
      toast.success(`菜单 ${menu.name} 已移动`)
      onMoved()
      onClose()
    } catch (err) {
      setError(menuErrorHint(err, 'move menu failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent size="sm">
        <DialogHeader>
          <DialogTitle>移动菜单</DialogTitle>
          <DialogDescription>
            选择 {menu.name} 的新上级菜单，选「无（根菜单）」移为根；候选已排除自身及其子树。
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(e) => { void submit(e) }}>
          <Field id="move-menu-parent" label="新上级菜单">
            <ParentSelect id="move-menu-parent" value={parentId} menus={menus} onValueChange={setParentId} />
          </Field>
          <FormError message={error} />
          <DialogActions pending={pending} submitLabel="移动" />
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---------- 删除确认 ----------

function MenuDeleteDialog({
  menu,
  onClose,
  onDeleted,
}: {
  menu: MenuDTO
  onClose: () => void
  onDeleted: () => void
}) {
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      await deleteMenu(menu.id)
      toast.success(`菜单 ${menu.name} 已删除`)
      onDeleted()
      onClose()
    } catch (err) {
      // 有未删除子菜单（MENU_HAS_CHILDREN）时提示先删子级。
      toast.error(`删除失败：${menuErrorHint(err, 'delete menu failed')}`)
      onClose()
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>确认删除菜单 {menu.name}？</AlertDialogTitle>
          <AlertDialogDescription>
            软删除后该菜单及其权限码立即失效；存在未删除子菜单时不可删除，需自叶向根逐层删除。
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

// MenuTreeNode 渲染一个菜单节点与其子树：缩进按深度递增，带子节点的行
// 可展开/收起（缺省全展开）。行内尾随类型、权限编码、排序号、状态与操作菜单。
function MenuTreeNodeView({
  node,
  depth,
  collapsedIds,
  onToggle,
  onOpen,
}: {
  node: MenuTreeNodeDTO
  depth: number
  collapsedIds: ReadonlySet<string>
  onToggle: (id: string) => void
  onOpen: (state: MenuDialogState) => void
}) {
  const { menu, children } = node
  const collapsed = collapsedIds.has(menu.id)
  return (
    <li>
      <div
        className="flex items-center gap-2 rounded-lg py-1.5 pr-2 hover:bg-soft"
        style={{ paddingLeft: depth * 20 + 8 }}
      >
        {children.length > 0 ? (
          <button
            type="button"
            aria-label={collapsed ? `展开 ${menu.name}` : `收起 ${menu.name}`}
            aria-expanded={!collapsed}
            onClick={() => onToggle(menu.id)}
            className="flex size-4 shrink-0 cursor-pointer items-center justify-center rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          >
            {collapsed ? <ChevronRightIcon aria-hidden className="size-4" /> : <ChevronDownIcon aria-hidden className="size-4" />}
          </button>
        ) : (
          <span aria-hidden className="size-4 shrink-0" />
        )}
        <span className="truncate text-sm font-medium text-foreground">{menu.name}</span>
        <span className="shrink-0 text-xs text-muted-foreground">#{menu.id}</span>
        <span className="shrink-0 text-xs text-muted-foreground">{menu.permissionCode}</span>
        <span className="ml-auto flex shrink-0 items-center gap-2">
          <span className="hidden text-xs text-muted-foreground sm:inline">
            排序 {menu.sortOrder} · {formatDateTime(menu.createdAt)}
          </span>
          {menuTypeBadge(menu.menuType)}
          {statusBadge(menu.status)}
          <RowActions menu={menu} onOpen={onOpen} />
        </span>
      </div>
      {children.length > 0 && !collapsed && (
        <ul aria-label={`${menu.name} 的子菜单`} className="space-y-0.5">
          {children.map((child) => (
            <MenuTreeNodeView
              key={child.menu.id}
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
  menu,
  onOpen,
}: {
  menu: MenuDTO
  onOpen: (state: MenuDialogState) => void
}) {
  return (
    <RowActionsMenu
      rowLabel={menu.name}
      info={[
        { label: '菜单名', value: menu.name },
        { label: '权限码', value: menu.permissionCode ?? '' },
      ]}
      actions={[
        { label: '编辑', onSelect: () => onOpen({ kind: 'edit', menu }) },
        { label: '移动', onSelect: () => onOpen({ kind: 'move', menu }) },
        { label: '新建子菜单', onSelect: () => onOpen({ kind: 'create', parent: menu }) },
        { label: '删除', destructive: true, onSelect: () => onOpen({ kind: 'delete', menu }) },
      ]}
    />
  )
}

// ---------- 页面 ----------

// MenuManagePage 菜单管理：功能权限码宿主树的维护入口（写面为受控写路
// 径）。树接口一次返回租户全量未删除菜单（无分页），主视图为可展开/收起
// 的树；增删改与移动成功后整树重查。
export function MenuManagePage() {
  const [tree, setTree] = useState<MenuTreeReplyDTO | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [collapsedIds, setCollapsedIds] = useState<ReadonlySet<string>>(new Set())
  const [dialog, setDialog] = useState<MenuDialogState | null>(null)

  // 取数：reloadKey 变化（动作成功后或手动重试）即重查全树；requestSeq 使
  // 迟到旧响应被丢弃，卸载后不再写状态。
  const requestSeq = useRef(0)
  useEffect(() => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setLoading(true)
    fetchMenuTree()
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

  // createMenus 是「新建菜单」弹窗的上级候选（全量摊平）；moveMenus 是
  // 移动弹窗的候选（排除被移动菜单自身及其子树，防环在前端挡掉明显项）。
  const allMenus = useMemo(() => (tree === null ? [] : flattenTree(tree.roots)), [tree])
  const moveMenus = useMemo(
    () => (tree === null || dialog === null || dialog.kind !== 'move' ? [] : flattenExcluding(tree.roots, dialog.menu.id)),
    [tree, dialog],
  )

  const totalNum = tree !== null ? Number(tree.total) : undefined

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 p-6">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex items-center gap-1">
          <h1 className="text-lg font-semibold text-foreground">菜单管理</h1>
          {/* 描述悬停标题弹出（与 DataTable 标题行同款）。 */}
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <span
                  tabIndex={0}
                  role="button"
                  aria-label="关于菜单管理"
                  className="cursor-help rounded-full p-0.5 text-muted-foreground hover:text-foreground"
                >
                  <InfoIcon aria-hidden className="size-4" />
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-72">功能权限码宿主树的维护入口。</TooltipContent>
            </Tooltip>
          </TooltipProvider>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <p aria-label="列表统计" className="text-sm text-muted-foreground">
            {totalNum !== undefined ? `共 ${totalNum} 个菜单` : ''}
          </p>
          <Button type="button" onClick={() => setDialog({ kind: 'create' })}>
            <PlusIcon aria-hidden className="size-4" />
            新建菜单
          </Button>
        </div>
      </div>

      {error !== null ? (
        <Alert variant="destructive">
          <AlertDescription className="flex items-center justify-between gap-3">
            <span>菜单树加载失败：{error}</span>
            <Button type="button" variant="outline" size="sm" onClick={reload}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        <div aria-busy={loading} className="min-h-0 flex-1 overflow-auto rounded-lg border p-2">
          {tree !== null && tree.roots.length === 0 ? (
            <p className="grid h-32 place-items-center text-sm text-muted-foreground">暂无菜单</p>
          ) : tree === null ? (
            <div className="grid h-32 place-items-center">
              <Spinner aria-hidden className="size-5" />
            </div>
          ) : (
            <ul aria-label="菜单树" className="space-y-0.5">
              {tree.roots.map((root) => (
                <MenuTreeNodeView
                  key={root.menu.id}
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
        <MenuCreateDialog parent={dialog.parent} menus={allMenus} onClose={() => setDialog(null)} onCreated={reload} />
      ) : dialog.kind === 'edit' ? (
        <MenuEditDialog menu={dialog.menu} onClose={() => setDialog(null)} onSaved={reload} />
      ) : dialog.kind === 'move' ? (
        <MenuMoveDialog menu={dialog.menu} menus={moveMenus} onClose={() => setDialog(null)} onMoved={reload} />
      ) : (
        <MenuDeleteDialog menu={dialog.menu} onClose={() => setDialog(null)} onDeleted={reload} />
      )}
    </div>
  )
}
