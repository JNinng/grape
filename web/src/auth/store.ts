import type { LoginReply, SessionUser } from '../api/types'

// 令牌持久化策略（XSS 面评估）：
// - accessToken 只存模块内存：不写任何 Web 存储，页面刷新即失效，靠
//   refresh token 静默续期恢复；把脚本可窃取的窗口压到最短。
// - refreshToken 存 localStorage：刷新后无需重新登录，跨标签页共享同一
//   会话。XSS 的真正缓解是 CSP 与输出转义，存储介质只决定暴露时长与
//   范围；refresh token 本身单次有效、服务端轮换即作废，泄漏窗口有限。
const REFRESH_TOKEN_STORAGE_KEY = 'grape.refreshToken'

// 供路由守卫模拟「刷新后只有 refresh token 存活」的场景与调试使用。
export const refreshTokenStorageKey = REFRESH_TOKEN_STORAGE_KEY

let accessToken: string | null = null
let user: SessionUser | null = null

export function getAccessToken(): string | null {
  return accessToken
}

export function getUser(): SessionUser | null {
  return user
}

export function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_TOKEN_STORAGE_KEY)
}

// setSession 落地登录/刷新成功产出的一对令牌与用户摘要。
export function setSession(reply: LoginReply): void {
  accessToken = reply.accessToken
  user = reply.user ?? null
  localStorage.setItem(REFRESH_TOKEN_STORAGE_KEY, reply.refreshToken)
}

// clearSession 清空全部会话事实（内存与 localStorage）。
export function clearSession(): void {
  accessToken = null
  user = null
  localStorage.removeItem(REFRESH_TOKEN_STORAGE_KEY)
}
