// 运行侧 ID 生成：threadId（新会话）、runId（每次运行）与消息 ID 共用。
// crypto.randomUUID 在现代浏览器与 jsdom 均可用，保留回退只为极端环境
// 不致崩溃——ID 只要求进程内唯一，不承担安全语义。
export function newId(prefix: string): string {
  const c = globalThis.crypto
  if (c !== undefined && typeof c.randomUUID === 'function') {
    return `${prefix}-${c.randomUUID()}`
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}`
}
