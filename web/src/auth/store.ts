import type { LoginReply, SessionUser } from '../api/types'

// 令牌持久化策略（XSS 面评估）：
// - accessToken 只存模块内存：不写任何 Web 存储，页面刷新即失效，靠
//   refresh token 静默续期恢复；把脚本可窃取的窗口压到最短。
// - refreshToken 存 sessionStorage：暴露范围限定单标签页会话，关页即失
//   效，不跨标签共享、不跨浏览器重启残留。XSS 的真正缓解是 CSP 与输出
//   转义，存储介质只决定暴露时长与范围——localStorage
//   持久化且全标签共享，暴露面显著更大，禁止使用。
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
  return sessionStorage.getItem(REFRESH_TOKEN_STORAGE_KEY)
}

// setSession 落地登录/刷新成功产出的一对令牌与用户摘要。
export function setSession(reply: LoginReply): void {
  accessToken = reply.accessToken
  user = reply.user ?? null
  sessionStorage.setItem(REFRESH_TOKEN_STORAGE_KEY, reply.refreshToken)
}

// clearSession 清空全部会话事实（内存与 sessionStorage）。
export function clearSession(): void {
  accessToken = null
  user = null
  sessionStorage.removeItem(REFRESH_TOKEN_STORAGE_KEY)
}
