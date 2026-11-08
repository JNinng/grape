import { HttpAgent } from '@ag-ui/client'
import { authFetch } from '../api/http'

// 对话页的 AG-UI 接入点：AG-UI 走 POST SSE，EventSource 无法携带
// Authorization，故给 HttpAgent 注入 authFetch（401 时单飞刷新并重放一次）。
// 服务端前缀 /agui（conf 的 agent.agui.base_path），其下挂 chat、/history、
// /cancel、/ack/*、/sessions；工厂本身不感知令牌。
export function createChatAgent(options: { threadId: string }): HttpAgent {
  return new HttpAgent({
    url: '/agui',
    threadId: options.threadId,
    fetch: authFetch,
  })
}
