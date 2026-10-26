import type { ApiErrorBody } from './types'

// toApiError 把非 2xx 响应归一为 Error：优先 Kratos 错误体的 message，
// 其次 reason；非 JSON 响应保留状态码文案。
export async function toApiError(res: Response): Promise<Error> {
  let detail = `HTTP ${res.status}`
  try {
    const body = (await res.json()) as ApiErrorBody
    if (body.message) {
      detail = body.message
    } else if (body.reason) {
      detail = body.reason
    }
  } catch {
    // 响应体非 JSON：保留状态码文案
  }
  return new Error(detail)
}
