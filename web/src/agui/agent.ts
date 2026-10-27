import { HttpAgent } from '@ag-ui/client'
import { authFetch } from '../api/http'

// 对话页接入点：AG-UI 是 POST SSE；EventSource 无法携带 Authorization
// 头，而 @ag-ui/client 的 HttpAgent 支持注入 fetch。
//
// 这里把 authFetch 作为 HttpAgent 的 fetch 注入：每次运行发起时从
// auth/store 读取当前 access token（无令牌陈旧化问题），401 时走
// api/http.ts 的「single-flight 刷新 → 换新令牌重放一次」状态机，刷新
// 失败清空本地会话并通知上层回登录页。工厂本身不感知令牌。
//
// 服务端前缀 /agui（conf 的 agent.agui.base_path）：chat 在前缀根，
// /history、/cancel、/ack/*、/sessions 同前缀。开发期由 Vite 代理到
// 127.0.0.1:8000，生产期同源直连。
export function createChatAgent(options: { threadId: string }): HttpAgent {
  return new HttpAgent({
    url: '/agui',
    threadId: options.threadId,
    fetch: authFetch,
  })
}
