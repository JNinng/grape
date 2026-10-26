// 会话失效回调：网络层（401 刷新失败）与路由层（跳转登录页）解耦的
// 唯一接缝。App 挂载时注册 navigate，网络层只负责触发，不感知路由。
export type SessionExpiredHandler = () => void

let handler: SessionExpiredHandler | null = null

export function setSessionExpiredHandler(next: SessionExpiredHandler): void {
  handler = next
}

export function notifySessionExpired(): void {
  handler?.()
}
