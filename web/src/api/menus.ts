import { authFetch } from './http'
import { toApiError } from './toApiError'

// MenuService 各端点（api/perm/v1/menu.proto）的前端客户端。序列化口径
// 与后端 jsoncodec（protojson 覆盖）一致：字段小驼峰、int64 为字符串、
// 时间为 RFC3339、optional 未设置时省略。写端点是受控写路径（SECURITY
// DEFINER 函数，方法名带 super），前端不经由其他面写菜单。

// MenuDTO 对齐 perm.v1.Menu：id/parentId 等 int64 均为字符串；
// status/parentId/icon/routePath/component/permissionExpr/remark 为
// proto3 optional（未设置省略，parentId 缺省即根菜单）。permissionCode
// 与 menuType 创建后不可变（更新输入中被服务端忽略），结构变更走
// moveMenu。
export interface MenuDTO {
  id: string
  status?: number
  name: string
  permissionCode: string
  parentId?: string
  menuType: number
  icon?: string
  routePath?: string
  component?: string
  permissionExpr?: string
  sortOrder: number
  remark?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

// MENU_TYPE_* 与 sys_menu.menu_type 取值一致：1 目录、2 菜单、3 按钮。
export const MENU_TYPE_DIRECTORY = 1
export const MENU_TYPE_PAGE = 2
export const MENU_TYPE_BUTTON = 3

// MENU_TYPE_LABELS 渲染菜单类型列与下拉。
export const MENU_TYPE_LABELS: Record<number, string> = {
  [MENU_TYPE_DIRECTORY]: '目录',
  [MENU_TYPE_PAGE]: '菜单',
  [MENU_TYPE_BUTTON]: '按钮',
}

// MenuTreeNodeDTO 对齐 perm.v1.MenuTreeNode：菜单与其直接子节点，子节点
// 顺序 sort_order 升序（同序按 id 升序）。
export interface MenuTreeNodeDTO {
  menu: MenuDTO
  children: MenuTreeNodeDTO[]
}

// MenuTreeReplyDTO 对齐 perm.v1.MenuTreeReply：租户全量未删除菜单树
// （无分页），total 为字符串型 int64，等于节点总数。
export interface MenuTreeReplyDTO {
  roots: MenuTreeNodeDTO[]
  total: string
}

// CreateMenuInput 对齐 perm.v1.CreateMenuSuperRequest.menu 的可填字段：
// id、created_by/updated_by 与时间戳由服务端管理；status 缺省启用；
// parentId 缺省表示创建根菜单；menuType 必填（1-3，创建后不可变）。
export interface CreateMenuInput {
  status?: number
  name: string
  permissionCode: string
  parentId?: string
  menuType: number
  icon?: string
  routePath?: string
  component?: string
  permissionExpr?: string
  sortOrder: number
  remark?: string
}

// UpdateMenuInput 对齐 perm.v1.UpdateMenuSuperRequest.menu：仅可变字段
// （name、status、icon、routePath、component、permissionExpr、sortOrder、
// remark；status 在更新语义下必填）。permissionCode/menuType/parentId 是
// 身份/类型/结构列，服务端忽略，前端不上送。
export interface UpdateMenuInput {
  id: string
  status: number
  name: string
  icon?: string
  routePath?: string
  component?: string
  permissionExpr?: string
  sortOrder: number
  remark?: string
}

// compact 序列化请求体：JSON.stringify 本就省略值为 undefined 的键，对
// protojson 的 optional 字段即未设置（与显式 null 同义，null 在解码时被
// 跳过）；moveMenu 的 parentId 缺省即「移为根菜单」。
function compact(body: object): string {
  return JSON.stringify(body)
}

// fetchMenuTree 调用 GET /v1/menus/tree：租户内全部未删除菜单的树
// （roots 嵌套 + total；父引用缺失的行由后端按根兜底挂载）。
export async function fetchMenuTree(): Promise<MenuTreeReplyDTO> {
  const res = await authFetch('/v1/menus/tree')
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as MenuTreeReplyDTO
}

// createMenu 调用 POST /v1/menus/create（body: "*"，整个请求消息即体）；
// 父菜单不存在/已删除/跨租户返回 400 语义错误，permission_code 在租户内
// 未删除菜单间冲突返回 409。
export async function createMenu(menu: CreateMenuInput): Promise<MenuDTO> {
  const res = await authFetch('/v1/menus/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ menu }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as MenuDTO
}

// fetchMenu 调用 GET /v1/menus/{id}：跨租户不可见等同不存在（404 语义）。
export async function fetchMenu(id: string): Promise<MenuDTO> {
  const res = await authFetch(`/v1/menus/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as MenuDTO
}

// updateMenu 调用 PUT /v1/menus/update（body: "menu"，覆盖可变字段）。
export async function updateMenu(menu: UpdateMenuInput): Promise<MenuDTO> {
  const res = await authFetch('/v1/menus/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(menu),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as MenuDTO
}

// moveMenu 调用 PUT /v1/menus/move（body: "*"）；parentId 缺省表示移为
// 根菜单，父级未知或移入自身子树（成环）返回 400 语义错误。
export async function moveMenu(id: string, parentId?: string): Promise<MenuDTO> {
  const res = await authFetch('/v1/menus/move', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ id, parentId }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as MenuDTO
}

// deleteMenu 调用 DELETE /v1/menus/{id}（软删除）；存在未删除直接子菜单
// 返回 409 语义错误（MENU_HAS_CHILDREN），需自叶向根逐层删除。
export async function deleteMenu(id: string): Promise<void> {
  const res = await authFetch(`/v1/menus/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}
