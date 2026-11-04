import { authFetch } from './http'
import { toApiError } from './toApiError'

// PermissionService 端点（api/perm/v1/permission.proto）的前端客户端。
// 本端点只受认证约束、不设权限码——权限码集合本身是界面的引导数据。

// fetchMyPermissions 调用 GET /v1/permissions/mine：返回当前认证用户持有
// 的全部功能权限码（后端保证去重升序恒为数组，用户无效时为空集合不报
// 错）。403（PERM_ACCESS_DENIED）或网络失败交由调用方回落处理。
export async function fetchMyPermissions(): Promise<string[]> {
  const res = await authFetch('/v1/permissions/mine')
  if (!res.ok) {
    throw await toApiError(res)
  }
  const reply = (await res.json()) as { codes?: string[] }
  return reply.codes ?? []
}
