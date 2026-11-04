import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// UserGroupService 各端点（api/org/v1/user_group.proto）的前端客户端。
// 序列化口径与后端 jsoncodec（protojson 覆盖）一致：字段小驼峰、int64 为
// 字符串、时间为 RFC3339、optional 未设置时省略。

// UserGroupDTO 对齐 org.v1.UserGroup：id/ownerUserId 等 int64 均为字符串；
// status/remark/ownerUserId 为 proto3 optional（未设置省略，status 创建时可
// 省略即缺省启用）；code 创建后不可变。
export interface UserGroupDTO {
  id: string
  status?: number
  name: string
  code: string
  remark?: string
  ownerUserId?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

// UserGroupSetDTO 对齐 org.v1.UserGroupSet：ListUserGroups 平铺分页信封，
// total 为字符串型 int64，include_total=false 时缺省。
export interface UserGroupSetDTO {
  items: UserGroupDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// UserGroupMemberDTO 对齐 org.v1.UserGroupMember：映射行与成员用户展示
// 字段的联结视图，全部只读（成员增删走 add/removeMembers）。
export interface UserGroupMemberDTO {
  userId: string
  name: string
  nickname: string
  status: number
  addedBy: string
  addedAt: string
}

// UserGroupMemberSetDTO 对齐 org.v1.UserGroupMemberSet：固定 offset 分页，
// 按添加时间升序（同刻按 userId 升序）。
export interface UserGroupMemberSetDTO {
  items: UserGroupMemberDTO[]
  total: string
  hasMore: boolean
}

// CreateUserGroupInput 对齐 org.v1.CreateUserGroupRequest.group 的可填字段：
// id、created_by/updated_by 与时间戳由服务端管理；status 缺省启用；
// ownerUserId（组长）可省略。code/name 租户内唯一，冲突返回 409。
export interface CreateUserGroupInput {
  status?: number
  name: string
  code: string
  remark?: string
  ownerUserId?: string
}

// UpdateUserGroupInput 对齐 org.v1.UpdateUserGroupRequest.group：id 标识目标
// 记录，仅 name/remark/status 可变（status 更新语义必填）；code 与
// ownerUserId 在输入中被忽略，前端不上送。
export interface UpdateUserGroupInput {
  id: string
  status: number
  name: string
  remark?: string
}

// compact 序列化请求体：JSON.stringify 本就省略值为 undefined 的键，对
// protojson 的 optional 字段即未设置（与显式 null 同义，null 在解码时被跳过）。
function compact(body: object): string {
  return JSON.stringify(body)
}

// createUserGroup 调用 POST /v1/user-groups/create（body: "*"，整个请求
// 消息即体）；组编码/组名冲突返回 409 语义错误。
export async function createUserGroup(group: CreateUserGroupInput): Promise<UserGroupDTO> {
  const res = await authFetch('/v1/user-groups/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ group }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as UserGroupDTO
}

// fetchUserGroup 调用 GET /v1/user-groups/{id}：跨租户不可见等同不存在
//（404 语义）。
export async function fetchUserGroup(id: string): Promise<UserGroupDTO> {
  const res = await authFetch(`/v1/user-groups/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as UserGroupDTO
}

// listUserGroups 调用 POST /v1/user-groups/list（body: "query"，Query 消息
// 即体，平铺分页列表）。后端 protojson 省略未填充字段，零命中时响应没有
// items 键；页面与表格组件假定数组恒存在，此处补齐为空数组。
export async function listUserGroups(query?: DynamicQuery): Promise<UserGroupSetDTO> {
  const res = await authFetch('/v1/user-groups/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as UserGroupSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateUserGroup 调用 PUT /v1/user-groups/update（body: "group"，覆盖
// name/remark/status）。
export async function updateUserGroup(group: UpdateUserGroupInput): Promise<UserGroupDTO> {
  const res = await authFetch('/v1/user-groups/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(group),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as UserGroupDTO
}

// deleteUserGroup 调用 DELETE /v1/user-groups/{id}（软删除；该端点无 body
// 绑定）。
export async function deleteUserGroup(id: string): Promise<void> {
  const res = await authFetch(`/v1/user-groups/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

// listUserGroupMembers 调用 GET /v1/user-groups/{id}/members：固定 offset
// 分页（page 缺省 1；page_size 缺省 20、上限 200，越界由服务端归一）。
// 空组没有 items 键（protojson 省略未填充字段），补齐为空数组。
export async function listUserGroupMembers(id: string, page = 1, pageSize = 20): Promise<UserGroupMemberSetDTO> {
  const search = new URLSearchParams({ page: String(page), page_size: String(pageSize) })
  const res = await authFetch(`/v1/user-groups/${encodeURIComponent(id)}/members?${search}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as UserGroupMemberSetDTO
  return { ...set, items: set.items ?? [] }
}

// AddMembersReplyDTO 对齐 org.v1.AddUserGroupMembersReply：added 为实际
// 新增的映射条数（字符串型 int64；已是成员的用户幂等跳过）。
export interface AddMembersReplyDTO {
  added: string
}

// addMembers 调用 POST /v1/user-groups/add-members（body: "*"）：任一
// 用户未知/跨租户/已删除返回 400 且整批不写入；重复成员 ID 由调用方去重。
export async function addMembers(id: string, userIds: string[]): Promise<AddMembersReplyDTO> {
  const res = await authFetch('/v1/user-groups/add-members', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ id, userIds }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as AddMembersReplyDTO
}

// RemoveMembersReplyDTO 对齐 org.v1.RemoveUserGroupMembersReply：removed
// 为实际移除的映射条数（字符串型 int64；非成员幂等返回 0 不报错）。
export interface RemoveMembersReplyDTO {
  removed: string
}

// removeMembers 调用 POST /v1/user-groups/remove-members（body: "*"）：
// 物理删除映射行；目标组不存在返回 404。
export async function removeMembers(id: string, userIds: string[]): Promise<RemoveMembersReplyDTO> {
  const res = await authFetch('/v1/user-groups/remove-members', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ id, userIds }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as RemoveMembersReplyDTO
}
