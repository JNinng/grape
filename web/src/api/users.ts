import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// UserService 各端点（api/user/v1/user.proto）的前端客户端。序列化口径
// 与后端 jsoncodec（protojson 覆盖）一致：字段小驼峰、int64 为字符串、
// 时间为 RFC3339、optional 未设置时省略。

// UserDTO 对齐 user.v1.User：id 与归属三元组等 int64 均为字符串；
// status 为 proto3 optional（未设置省略）；email/phone/remark 可空。
export interface UserDTO {
  id: string
  status?: number
  name: string
  nickname: string
  email?: string
  phone?: string
  remark?: string
  locale: string
  ownerUserId?: string
  ownerDeptId?: string
  ownerUserGroupId?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

// UserSetDTO 对齐 user.v1.UserSet：total 为字符串型 int64，
// include_total=false 时缺省。
export interface UserSetDTO {
  items: UserDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// CreateUserInput 对齐 user.v1.CreateUserRequest.user 的可填字段：
// id 与时间戳由服务端管理；归属三元组可选（未指定归属人时服务端缺省为
// 创建人）；status 缺省启用；locale 缺省 zh-CN。
export interface CreateUserInput {
  status?: number
  name: string
  nickname: string
  email?: string
  phone?: string
  remark?: string
  locale?: string
  ownerUserId?: string
  ownerDeptId?: string
  ownerUserGroupId?: string
}

// UpdateUserInput 对齐 user.v1.UpdateUserRequest.user：id 标识目标记录，
// status 必填（更新语义下缺省无法表达禁用）；归属三元组不由此接口写入。
export interface UpdateUserInput {
  id: string
  status: number
  name: string
  nickname: string
  email?: string
  phone?: string
  remark?: string
  locale?: string
}

// OwnerTripleInput 对齐 user.v1.TransferUserOwnerRequest 的归属三元组：
// 转移是整组替换语义——字段缺省（或 null）表示清掉该归属，存储 NULL。
export interface OwnerTripleInput {
  ownerUserId?: string | null
  ownerDeptId?: string | null
  ownerUserGroupId?: string | null
}

// compact 序列化请求体：JSON.stringify 本就省略值为 undefined 的键，对
// protojson 的 optional 字段即未设置（与显式 null 同义，null 在解码时被跳过）。
function compact(body: object): string {
  return JSON.stringify(body)
}

// createUser 调用 POST /v1/users/create（body: "*"，整个请求消息即体）。
export async function createUser(user: CreateUserInput, password: string): Promise<UserDTO> {
  const res = await authFetch('/v1/users/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ user, password }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as UserDTO
}

// fetchUser 调用 GET /v1/users/{id}：数据范围不可见等同不存在（404 语义）。
export async function fetchUser(id: string): Promise<UserDTO> {
  const res = await authFetch(`/v1/users/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as UserDTO
}

// listUsers 调用 POST /v1/users/list（body: "query"，Query 消息即体）。
// 后端 protojson 省略未填充字段，零命中时响应没有 items 键；页面与表格
// 组件假定数组恒存在，此处补齐为空数组（对齐 agentAudit 系客户端的兜底）。
export async function listUsers(query?: DynamicQuery): Promise<UserSetDTO> {
  const res = await authFetch('/v1/users/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as UserSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateUser 调用 PUT /v1/users/update（body: "user"，User 消息即体）。
export async function updateUser(user: UpdateUserInput): Promise<UserDTO> {
  const res = await authFetch('/v1/users/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(user),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as UserDTO
}

// setUserPassword 调用 POST /v1/users/password；密码不出现在任何读接口。
export async function setUserPassword(id: string, password: string): Promise<void> {
  const res = await authFetch('/v1/users/password', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ id, password }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

// deleteUser 调用 DELETE /v1/users/{id}（软删除；该端点无 body 绑定）。
export async function deleteUser(id: string): Promise<void> {
  const res = await authFetch(`/v1/users/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

// AssignRolesReplyDTO 对齐 user.v1.AssignRolesReply：added 为实际新增的
// 映射条数（字符串型 int64）。
export interface AssignRolesReplyDTO {
  added: string
}

// assignRoles 调用 POST /v1/users/assign-roles：整批判定，任一角色不可
// 分配则整批拒绝；重复角色 ID 由调用方负责去重。
export async function assignRoles(userId: string, roleIds: string[]): Promise<AssignRolesReplyDTO> {
  const res = await authFetch('/v1/users/assign-roles', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ userId, roleIds }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as AssignRolesReplyDTO
}

// transferUserOwner 调用 POST /v1/users/transfer-owner：两头校验，新归属
// 不可达返回 403 语义错误；三元组整组替换，缺省字段即清空该归属。
export async function transferUserOwner(id: string, to: OwnerTripleInput): Promise<UserDTO> {
  const res = await authFetch('/v1/users/transfer-owner', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ id, ...to }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as UserDTO
}

// UpdateMyProfileInput 对齐 user.v1.UpdateMyProfileRequest：覆盖语义，
// email/phone/remark 缺省或空串表示清空。
export interface UpdateMyProfileInput {
  nickname: string
  email?: string
  phone?: string
  remark?: string
  locale?: string
}

// getMyProfile 调用 GET /v1/profile：目标恒取认证身份，不设权限码。
export async function getMyProfile(): Promise<UserDTO> {
  const res = await authFetch('/v1/profile')
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as UserDTO
}

// updateMyProfile 调用 PUT /v1/profile：覆盖当前用户可编辑资料。
export async function updateMyProfile(input: UpdateMyProfileInput): Promise<UserDTO> {
  const res = await authFetch('/v1/profile', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(input),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as UserDTO
}

// changeMyPassword 调用 POST /v1/profile/password：先校验当前口令；
// 不匹配返回 400 USER_PASSWORD_MISMATCH（不触发会话失效）。
export async function changeMyPassword(oldPassword: string, newPassword: string): Promise<void> {
  const res = await authFetch('/v1/profile/password', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ oldPassword, newPassword }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
}
