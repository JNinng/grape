import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// DeptService 各端点（api/org/v1/dept.proto）的前端客户端。序列化口径
// 与后端 jsoncodec（protojson 覆盖）一致：字段小驼峰、int64 为字符串、
// 时间为 RFC3339、optional 未设置时省略。

// DeptDTO 对齐 org.v1.Dept：id/parentId/owner_* 等 int64 均为字符串；
// status/parentId/ownerUserGroupId 为 proto3 optional（未设置省略，
// parentId 缺省即根部门）。
export interface DeptDTO {
  id: string
  status?: number
  name: string
  parentId?: string
  ownerUserId: string
  ownerUserGroupId?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

// DeptTreeNodeDTO 对齐 org.v1.DeptTreeNode：部门与其直接子节点，
// children 按部门创建时间升序。
export interface DeptTreeNodeDTO {
  dept: DeptDTO
  children: DeptTreeNodeDTO[]
}

// DeptTreeReplyDTO 对齐 org.v1.DeptTreeReply：租户全量树（无分页），
// total 为字符串型 int64，等于租户内未删除部门数。
export interface DeptTreeReplyDTO {
  roots: DeptTreeNodeDTO[]
  total: string
}

// DeptSetDTO 对齐 org.v1.DeptSet：ListDepts 平铺分页信封。
export interface DeptSetDTO {
  items: DeptDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// CreateDeptInput 对齐 org.v1.CreateDeptRequest.dept 的可填字段：
// id、created_by/updated_by 与时间戳由服务端管理；status 缺省启用；
// parentId 缺省表示创建根部门；ownerUserId 必填。
export interface CreateDeptInput {
  status?: number
  name: string
  parentId?: string
  ownerUserId: string
  ownerUserGroupId?: string
}

// UpdateDeptInput 对齐 org.v1.UpdateDeptRequest.dept：仅 name/status
// 可改（status 在更新语义下必填）；parentId 与 owner_* 在输入中被忽略
//（移动走 moveDept）。
export interface UpdateDeptInput {
  id: string
  status: number
  name: string
}

// compact 序列化请求体：JSON.stringify 本就省略值为 undefined 的键，对
// protojson 的 optional 字段即未设置（与显式 null 同义，null 在解码时被跳过）。
function compact(body: object): string {
  return JSON.stringify(body)
}

// normalizeDeptNode 递归归一化树节点：后端 protojson 默认编组省略一切未
// 填充字段——叶子节点的 children 为空即没有该键，而页面摊平/遍历子树时
// 假定数组恒存在（缺键直接崩溃）。逐节点把缺失的 children 补为空数组。
function normalizeDeptNode(node: DeptTreeNodeDTO): DeptTreeNodeDTO {
  return { dept: node.dept, children: (node.children ?? []).map(normalizeDeptNode) }
}

// normalizeDeptTreeReply 归一化树响应：空树没有 roots 键，同样补为空数组。
function normalizeDeptTreeReply(reply: DeptTreeReplyDTO): DeptTreeReplyDTO {
  return { roots: (reply.roots ?? []).map(normalizeDeptNode), total: reply.total }
}

// fetchDeptTree 调用 GET /v1/depts/tree：租户全量树（roots 嵌套 + total）。
export async function fetchDeptTree(): Promise<DeptTreeReplyDTO> {
  const res = await authFetch('/v1/depts/tree')
  if (!res.ok) {
    throw await toApiError(res)
  }
  return normalizeDeptTreeReply((await res.json()) as DeptTreeReplyDTO)
}

// createDept 调用 POST /v1/depts/create（body: "*"，整个请求消息即体）；
// 父部门无效返回 400 语义错误，租户内部门名冲突返回 409。
export async function createDept(dept: CreateDeptInput): Promise<DeptDTO> {
  const res = await authFetch('/v1/depts/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ dept }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DeptDTO
}

// fetchDept 调用 GET /v1/depts/{id}：跨租户不可见等同不存在（404 语义）。
export async function fetchDept(id: string): Promise<DeptDTO> {
  const res = await authFetch(`/v1/depts/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DeptDTO
}

// listDepts 调用 POST /v1/depts/list（body: "query"，Query 消息即体，
// 平铺分页列表）；树形展示请用 fetchDeptTree。零命中时响应没有 items 键
// （protojson 省略未填充字段），补齐为空数组。
export async function listDepts(query?: DynamicQuery): Promise<DeptSetDTO> {
  const res = await authFetch('/v1/depts/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as DeptSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateDept 调用 PUT /v1/depts/update（body: "dept"，User 同款覆盖语义）。
export async function updateDept(dept: UpdateDeptInput): Promise<DeptDTO> {
  const res = await authFetch('/v1/depts/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(dept),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DeptDTO
}

// moveDept 调用 POST /v1/depts/move（body: "*"）；parentId 缺省表示移为
// 根部门，父级未知或移入自身子树（成环）返回 400 语义错误。
export async function moveDept(id: string, parentId?: string): Promise<DeptDTO> {
  const res = await authFetch('/v1/depts/move', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ id, parentId }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DeptDTO
}

// deleteDept 调用 DELETE /v1/depts/{id}（软删除）；存在未删除直接子部门
// 返回 409 语义错误（DEPT_HAS_CHILDREN），需自叶向根逐层删除。
export async function deleteDept(id: string): Promise<void> {
  const res = await authFetch(`/v1/depts/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}
