import type { ApiErrorBody } from './types'

// ApiError 是非 2xx 响应的归一错误：message 是展示文案（Kratos 错误体的
// message，缺省回落 reason），reason 保留业务原因码（如 CONFIG_NOT_FOUND）
// 供调用方按稳定的码表兜底映射；既有调用方只依赖 Error 的 message，不受影响。
export class ApiError extends Error {
  readonly reason: string | undefined

  constructor(message: string, reason?: string) {
    super(message)
    this.name = 'ApiError'
    this.reason = reason
  }
}

// toApiError 把非 2xx 响应归一为 ApiError：优先 Kratos 错误体的 message，
// 其次 reason；非 JSON 响应保留状态码文案。reason 始终透出（存在时）。
export async function toApiError(res: Response): Promise<ApiError> {
  let detail = `HTTP ${res.status}`
  let reason: string | undefined
  try {
    const body = (await res.json()) as ApiErrorBody
    reason = body.reason
    if (body.message) {
      detail = body.message
    } else if (body.reason) {
      detail = body.reason
    }
  } catch {
    // 响应体非 JSON：保留状态码文案
  }
  return new ApiError(detail, reason)
}
