import type { LoginReply, SessionUser } from '../api/types'

// 令牌持久化：accessToken 只在模块内存（刷新即失效），refreshToken 在
// localStorage（跨标签共享）；存储策略与安全论证见 web/README.md「登录与令牌策略」。
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
