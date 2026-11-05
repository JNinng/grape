import { authFetch } from './http'
import { toApiError } from './toApiError'

// 用户分配选择器与角色整组替换各端点（api/user/v1/user.proto 的
// ListAssignableRoles / ListUserRoles / SetUserRoles 与三个归属候选）的
// 前端客户端。序列化口径与后端 jsoncodec（protojson 覆盖）一致：字段
// 小驼峰、int64 为字符串、optional 未设置时省略。

// OptionDTO 对齐 query.v1.Option：value 是 int64 ID 的十进制字符串；
// enabled/reason 为前向兼容字段（v1 恒 true/空串）；assigned 只在角色
// 场景有意义。
export interface OptionDTO {
  value: string
  label: string
  enabled: boolean
  reason: string
  assigned: boolean
}

// OptionSetDTO 对齐 query.v1.OptionSet：total 为字符串型 int64 且 v1
// 不返回；零命中时响应省略 items 键。
export interface OptionSetDTO {
  items: OptionDTO[]
  hasMore: boolean
  total?: string
  page: number
}

// OptionQueryInput 对齐 query.v1.OptionQuery：关键字 + offset 分页。
export interface OptionQueryInput {
  keyword?: string
  page?: number
  pageSize?: number
}

// UserRoleSetDTO 对齐 user.v1.UserRoleSet：items 是可管理角色（assigned
// 恒 true），unmanagedCount 是 locked 数量（字符串型 int64），version
// 提交 SetUserRoles 时原样回传。
export interface UserRoleSetDTO {
  items: OptionDTO[]
  unmanagedCount: string
  version: string
}

function compact(body: object): string {
  return JSON.stringify(body)
}

// normalizeOptionSet 补齐 items 为空数组（protojson 零命中时省略键）。
function normalizeOptionSet(set: OptionSetDTO): OptionSetDTO {
  return { ...set, items: set.items ?? [] }
}

// normalizeUserRoleSet 补齐 items 为空数组并兜底字符串计数缺省。
function normalizeUserRoleSet(set: UserRoleSetDTO): UserRoleSetDTO {
  return {
    ...set,
    items: set.items ?? [],
    unmanagedCount: set.unmanagedCount ?? '0',
  }
}

async function postOptionSet(path: string, body: object): Promise<OptionSetDTO> {
  const res = await authFetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(body),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return normalizeOptionSet((await res.json()) as OptionSetDTO)
}

// listAssignableRoles 调用 POST /v1/users/assignable-roles：目标用户可
// 分配且尚未拥有的活跃角色（目标不可见 404）。
export function listAssignableRoles(
  targetUserId: string,
  query?: OptionQueryInput,
): Promise<OptionSetDTO> {
  return postOptionSet('/v1/users/assignable-roles', { targetUserId, query })
}

// listUserRoles 调用 GET /v1/users/{user_id}/roles：当前可管理角色集合、
// locked 计数与版本。
export async function listUserRoles(userId: string): Promise<UserRoleSetDTO> {
  const res = await authFetch(`/v1/users/${encodeURIComponent(userId)}/roles`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return normalizeUserRoleSet((await res.json()) as UserRoleSetDTO)
}

// setUserRoles 调用 POST /v1/users/set-roles：管理范围内整组替换；版本
// 不一致返回 409（reason USER_ROLE_SET_CHANGED），调用方应重载而非重试。
export async function setUserRoles(
  userId: string,
  roleIds: string[],
  expectedVersion: string,
): Promise<UserRoleSetDTO> {
  const res = await authFetch('/v1/users/set-roles', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ userId, roleIds, expectedVersion }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return normalizeUserRoleSet((await res.json()) as UserRoleSetDTO)
}

// listOwnerUserOptions 调用 POST /v1/users/owner-user-options：操作者可见
// 范围内的活跃用户候选。
export function listOwnerUserOptions(query?: OptionQueryInput): Promise<OptionSetDTO> {
  return postOptionSet('/v1/users/owner-user-options', { query })
}

// listOwnerDeptOptions 调用 POST /v1/users/owner-dept-options：操作者可见
// 范围内的活跃部门候选。
export function listOwnerDeptOptions(query?: OptionQueryInput): Promise<OptionSetDTO> {
  return postOptionSet('/v1/users/owner-dept-options', { query })
}

// listOwnerGroupOptions 调用 POST /v1/users/owner-group-options：操作者
// 可见范围内的活跃用户组候选。
export function listOwnerGroupOptions(query?: OptionQueryInput): Promise<OptionSetDTO> {
  return postOptionSet('/v1/users/owner-group-options', { query })
}
