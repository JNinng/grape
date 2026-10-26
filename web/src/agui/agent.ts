import { HttpAgent } from '@ag-ui/client'

// A2 对话页接入点（本任务只留接缝，不实现 UI 与调用）：
// - AG-UI 是 POST SSE；EventSource 无法携带 Authorization 头，而
//   @ag-ui/client 的 HttpAgent 内部用 fetch，可带令牌（TODO 决策 2）。
// - 服务端前缀 /agui（conf 的 agent.agui.base_path）：chat 在前缀根，
//   /history、/cancel、/ack/confirmations 同前缀。开发期由 Vite 代理到
//   127.0.0.1:8000，生产期同源直连。
// - access token 从 auth/store 读取；发起运行前如已过期，先
//   refreshSession 续期再创建 agent，避免首连即 401。
// - 401 处理复用 api/http.ts 的状态机语义：续期失败清态回登录页。
export function createChatAgent(options: {
  accessToken: string
  threadId: string
}): HttpAgent {
  return new HttpAgent({
    url: '/agui',
    threadId: options.threadId,
    headers: { Authorization: `Bearer ${options.accessToken}` },
  })
}
