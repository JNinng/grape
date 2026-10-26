import type { LoginReply } from '../api/types'
import { singleFlight } from './singleFlight'
import { getRefreshToken, setSession } from './store'

const refreshPath = '/v1/auth/refresh'

// doRefresh 用当前刷新令牌轮换一对新令牌。无令牌、后端拒绝或网络失败
// 都按失败处理，由调用方（401 状态机 / 路由守卫）决定清态与跳转；本
// 函数不改状态（成功写态在 setSession，失败清态归调用方）。
async function doRefresh(): Promise<void> {
  const token = getRefreshToken()
  if (token === null) {
    throw new Error('no refresh token')
  }
  const res = await fetch(refreshPath, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ refreshToken: token }),
  })
  if (!res.ok) {
    throw new Error(`refresh failed: HTTP ${res.status}`)
  }
  setSession((await res.json()) as LoginReply)
}

// refreshSession 是单飞化的刷新入口：并发 401 只发一次轮换请求，其余
// 调用共享同一结果（成功或失败）。
export const refreshSession = singleFlight(doRefresh)
