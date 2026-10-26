// 与 api/session/v1 契约对齐的会话 DTO：字段名按 protobuf JSON 映射
// （protojson）为 camelCase，与后端 jsoncodec 装载后的编解码一致。
export interface SessionUser {
  id: number
  tenantId: string
  name: string
  nickname: string
}

// LoginReply 是登录与刷新共用的响应（刷新后令牌整体轮换）。
// expiresAt 为访问令牌过期时间（RFC3339 字符串，存储介质时钟）。
export interface LoginReply {
  tokenType: string
  accessToken: string
  refreshToken: string
  expiresAt: string
  // protojson 省略未设置字段，运行时可能为 undefined。
  user?: SessionUser | null
}

// Kratos 错误响应形态：HTTP 状态码之外的业务事实在 reason / message。
export interface ApiErrorBody {
  code?: number
  reason?: string
  message?: string
}
