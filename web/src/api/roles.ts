import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// RoleService 各端点（api/perm/v1/role.proto）的前端客户端。序列化口径
// 与后端 jsoncodec（protojson 覆盖）一致：字段小驼峰、int64 为字符串、
// 时间为 RFC3339、optional 未设置时省略。

// RoleDTO 对齐 perm.v1.Role：id/createdBy 等 int64 均为字符串；
// status/scopeDeptId/remark 为 proto3 optional（未设置省略，status 创建时
// 可省略即缺省启用）。code 与 roleType 是身份列（创建后不可变），
// dataScope/scopeDeptId 归数据范围配置面（setRoleDataScope），均不经
// 创建/更新输入写入。
export interface RoleDTO {
  id: string
  status?: number
  name: string
  code: string
  roleType: number
  dataScope: number
  scopeDeptId?: string
  remark?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

// RoleSetDTO 对齐 perm.v1.RoleSet：ListRoles 平铺分页信封，total 为
// 字符串型 int64，include_total=false 时缺省。
export interface RoleSetDTO {
  items: RoleDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// RoleIDScopeEntryDTO 对齐 perm.v1.RoleIDScopeEntry：type 1 用户 ID /
// 2 部门 ID / 3 用户组 ID，仅在角色 data_scope 为 4/5/6 时分别被数据
// 范围推导消费；dataId 为字符串型 int64。
export interface RoleIDScopeEntryDTO {
  type: number
  dataId: string
}

// RoleDataScopeDTO 对齐 perm.v1.RoleDataScope：角色的数据范围配置视图。
// scopeDeptId 为组织可见范围（分配层级限定），NULL（未设置）=全租户可分配；
// allowedDeptIds 是分发部门 ID 集合（升序），只参与角色分配判定。
export interface RoleDataScopeDTO {
  roleId: string
  dataScope: number
  scopeDeptId?: string
  idScopes: RoleIDScopeEntryDTO[]
  allowedDeptIds: string[]
}

// RoleMenuSetDTO 对齐 perm.v1.RoleMenuSet：角色当前的有效授权菜单 ID
// 集合（升序）——停用/软删菜单的残留映射不出现在回显集合。
export interface RoleMenuSetDTO {
  menuIds: string[]
}

// CreateRoleInput 对齐 perm.v1.CreateRoleRequest.role 的可填字段：id、
// role_type、data_scope、scope_dept_id、created_by/updated_by 与时间戳
// 由服务端管理（data_scope 缺省 1 仅本人）；status 缺省启用。
export interface CreateRoleInput {
  status?: number
  name: string
  code: string
  remark?: string
}

// UpdateRoleInput 对齐 perm.v1.UpdateRoleRequest.role：id 标识目标记录，
// status 在更新语义下必填；code/role_type/data_scope/scope_dept_id 在
// 输入中被服务端忽略，前端不上送。
export interface UpdateRoleInput {
  id: string
  status: number
  name: string
  remark?: string
}

// SetRoleDataScopeInput 对齐 perm.v1.SetRoleDataScopeRequest 的可填字段：
// 整组替换语义——scopeDeptId 缺省（或 null）即置回 NULL（全租户可分配）；
// idScopes 与 allowedDeptIds 按传入集合差量同步，空集即清空对应集合。
export interface SetRoleDataScopeInput {
  dataScope: number
  scopeDeptId?: string | null
  idScopes: RoleIDScopeEntryDTO[]
  allowedDeptIds: string[]
}

// compact 序列化请求体：JSON.stringify 本就省略值为 undefined 的键，对
// protojson 的 optional 字段即未设置（与显式 null 同义，null 在解码时被
// 跳过）；scopeDeptId 置空正是靠这一点表达「清回全租户可分配」。
function compact(body: object): string {
  return JSON.stringify(body)
}

// createRole 调用 POST /v1/roles/create（body: "*"，整个请求消息即体）；
// 租户内 code 或 name 与未删除角色冲突返回 409 语义错误。
export async function createRole(role: CreateRoleInput): Promise<RoleDTO> {
  const res = await authFetch('/v1/roles/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ role }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as RoleDTO
}

// fetchRole 调用 GET /v1/roles/{id}：跨租户不可见等同不存在（404 语义）。
export async function fetchRole(id: string): Promise<RoleDTO> {
  const res = await authFetch(`/v1/roles/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as RoleDTO
}

// listRoles 调用 POST /v1/roles/list（body: "query"，Query 消息即体，
// 平铺分页列表）。零命中时响应没有 items 键（protojson 省略未填充字段），
// 页面与表格组件假定数组恒存在，此处补齐为空数组。
export async function listRoles(query?: DynamicQuery): Promise<RoleSetDTO> {
  const res = await authFetch('/v1/roles/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as RoleSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateRole 调用 PUT /v1/roles/update（body: "role"，覆盖 name/status/
// remark 可变字段）。
export async function updateRole(role: UpdateRoleInput): Promise<RoleDTO> {
  const res = await authFetch('/v1/roles/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(role),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as RoleDTO
}

// deleteRole 调用 DELETE /v1/roles/{id}（软删除；该端点无 body 绑定）；
// 系统预置角色（role_type=1）返回 403 语义错误（preset role cannot be
// deleted）。
export async function deleteRole(id: string): Promise<void> {
  const res = await authFetch(`/v1/roles/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

// fetchRoleDataScope 调用 GET /v1/roles/{id}/data-scope：读取角色的数据
// 范围配置（规则、分配层级限定、ID 范围明细与分发部门集合）。
export async function fetchRoleDataScope(id: string): Promise<RoleDataScopeDTO> {
  const res = await authFetch(`/v1/roles/${encodeURIComponent(id)}/data-scope`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as RoleDataScopeDTO
}

// setRoleDataScope 调用 PUT /v1/roles/{id}/data-scope（body: "*"）：整组
// 替换——明细引用的用户/部门/用户组与 scopeDeptId 引用的部门必须存在且
// 启用，任一未知返回 400 且整组不写入；返回归一化（去重升序）后的配置。
export async function setRoleDataScope(id: string, input: SetRoleDataScopeInput): Promise<RoleDataScopeDTO> {
  const res = await authFetch(`/v1/roles/${encodeURIComponent(id)}/data-scope`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ id, ...input }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as RoleDataScopeDTO
}

// listRoleMenus 调用 GET /v1/roles/{id}/menus：当前有效授权菜单 ID 升序
// 集合，可直接作 setRoleMenus 的回显输入（提交回显集合恒幂等）。无授权时
// 响应没有 menuIds 键（protojson 省略未填充字段），补齐为空数组。
export async function listRoleMenus(id: string): Promise<RoleMenuSetDTO> {
  const res = await authFetch(`/v1/roles/${encodeURIComponent(id)}/menus`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as RoleMenuSetDTO
  return { menuIds: set.menuIds ?? [] }
}

// setRoleMenus 调用 PUT /v1/roles/{id}/menus（body: "*"）：整组差量替换
// ——入参为勾选后的完整有效菜单集合，空集合合法（清空全部授权）；引用
// 的菜单必须存在且启用，任一未知返回 400 且整组不写入。
export async function setRoleMenus(id: string, menuIds: string[]): Promise<RoleMenuSetDTO> {
  const res = await authFetch(`/v1/roles/${encodeURIComponent(id)}/menus`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ id, menuIds }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as RoleMenuSetDTO
}
