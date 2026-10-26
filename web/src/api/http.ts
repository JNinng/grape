import { clearSession, getAccessToken } from '../auth/store'
import { notifySessionExpired } from '../auth/callbacks'
import { refreshSession } from '../auth/refresh'

// authFetch 是带会话事实的 fetch：自动附 Authorization 头；受保护请求
// 收到 401 时走「single-flight 刷新 → 换新令牌重放一次」的状态机。刷新
// 失败则清空本地会话、通知上层回登录页，并把原始 401 响应交还调用方
// 按非 2xx 处理。登录 / 刷新端点不走本函数（身份成立前或正在轮换），
// 因此不存在 401 递归刷新。
export async function authFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const token = getAccessToken()
  const headers = new Headers(init.headers)
  if (token !== null) {
    headers.set('Authorization', `Bearer ${token}`)
  }
  const res = await fetch(path, { ...init, headers })
  if (res.status !== 401 || token === null) {
    return res
  }
  try {
    await refreshSession()
  } catch {
    clearSession()
    notifySessionExpired()
    return res
  }
  headers.set('Authorization', `Bearer ${getAccessToken() ?? ''}`)
  return fetch(path, { ...init, headers })
}
