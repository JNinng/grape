import { authFetch } from './http'
import { toApiError } from './toApiError'
import { clearSession, getAccessToken, setSession } from '../auth/store'
import type { LoginReply } from './types'

const loginPath = '/v1/auth/login'
const logoutPath = '/v1/auth/logout'

export interface LoginInput {
  // 租户 ID，可空：留空由服务端回落默认租户（auth.default_tenant_id）。
  tenantId: string
  userName: string
  password: string
}

// login 调用登录端点并落地会话态：access token 只进内存，refresh token
// 进 sessionStorage（理由见 auth/store.ts）。失败时抛错且不改任何状态。
export async function login(input: LoginInput): Promise<void> {
  const body: Record<string, string> = {
    userName: input.userName.trim(),
    password: input.password,
  }
  const tenantId = input.tenantId.trim()
  if (tenantId !== '') {
    body.tenantId = tenantId
  }
  const res = await fetch(loginPath, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  setSession((await res.json()) as LoginReply)
}

// logout 通知后端撤销当前登录会话。本地登出语义不依赖远端结果：无论
// 后端成败（含令牌已失效与网络失败）都清空本地态并正常返回——令牌撤
// 销失败时会话至多存活到自然过期，不能因此把用户困在已不可用的页面里。
export async function logout(): Promise<void> {
  try {
    if (getAccessToken() !== null) {
      // 经 authFetch：令牌恰已过期时先续期再撤销，服务端尽量不残留
      // 活跃会话；401 且续期失败时 authFetch 已清态并通知跳转。
      // 契约的 LogoutRequest 无字段，但生成端点仍按 JSON 解码请求体，
      // 必须显式携带 Content-Type 与空对象体，否则 400 CODEC。
      await authFetch(logoutPath, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}',
      })
    }
  } catch {
    // 后端不可达等异常：按本地登出语义吞掉，保证调用方总能继续跳转。
  } finally {
    clearSession()
  }
}
