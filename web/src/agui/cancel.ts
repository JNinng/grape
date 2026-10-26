import { authFetch } from '../api/http'

// /agui/cancel 的请求体与 AG-UI RunAgentInput 同形（框架按 runAgentInput
// 解析），运行定位只需要 threadId + runId，其余集合置空。
const cancelPath = '/agui/cancel'

export type CancelResult = 'cancelled' | 'not-running'

// cancelRun 请求服务端取消一次运行：200 表示取消已受理（运行经原流
// 收敛，前端靠 RUN_FINISHED/RUN_ERROR 事件更新状态）；404 表示运行
// 已不存在（正常竞态，不视为错误）；其余状态抛错展示。
export async function cancelRun(threadId: string, runId: string): Promise<CancelResult> {
  const res = await authFetch(cancelPath, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ threadId, runId, messages: [], tools: [], context: [] }),
  })
  if (res.ok) {
    return 'cancelled'
  }
  if (res.status === 404) {
    return 'not-running'
  }
  throw new Error(`cancel failed: HTTP ${res.status}`)
}
