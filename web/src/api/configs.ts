import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// 配置族各端点（api/config/v1/config.proto）的前端客户端：全局配置
// （ConfigService）、租户配置（TenantConfigService）、功能开关
// （FeatureFlagService）与通用翻译（TranslationService）。
// 序列化口径与后端 jsoncodec（protojson 覆盖）一致：字段小驼峰、int64 为
// 字符串、时间为 RFC3339、optional 未设置时省略。

// ConfigDTO 对齐 config.v1.Config：全局配置不绑定租户。configKey 与
// valueType 是身份/值语义列，创建后不可变；value/remark/status 为可选
// 字段（未设置省略）。
export interface ConfigDTO {
  id: string
  status?: number
  name: string
  configKey: string
  value?: string
  valueType: string
  remark?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

// ConfigSetDTO 对齐 config.v1.ConfigSet：ListConfigs 平铺分页信封。
export interface ConfigSetDTO {
  items: ConfigDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// TenantConfigDTO 对齐 config.v1.TenantConfig：tenantId 只读，由服务端按
// 认证身份落值；configKey（租户内唯一）与 valueType 创建后不可变。
export interface TenantConfigDTO {
  id: string
  status?: number
  name: string
  configKey: string
  value?: string
  valueType: string
  remark?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
  tenantId: string
}

// TenantConfigSetDTO 对齐 config.v1.TenantConfigSet。
export interface TenantConfigSetDTO {
  items: TenantConfigDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// FeatureFlagDTO 对齐 config.v1.FeatureFlag：flagKey 租户内唯一且创建后
// 不可变；isEnabled 是功能取值（optional，未设置即 false），status 是数据
// 状态（0 禁用、1 启用）。
export interface FeatureFlagDTO {
  id: string
  status?: number
  name: string
  flagKey: string
  description?: string
  isEnabled?: boolean
  remark?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
  tenantId: string
}

// FeatureFlagSetDTO 对齐 config.v1.FeatureFlagSet。
export interface FeatureFlagSetDTO {
  items: FeatureFlagDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// TranslationDTO 对齐 config.v1.Translation：rowId 为字符串型 int64；
// tableName/rowId/columnName/locale 四列共同确定一条翻译，创建后不可变。
export interface TranslationDTO {
  id: string
  status?: number
  tableName: string
  rowId: string
  columnName: string
  locale: string
  value: string
  remark?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
  tenantId: string
}

// TranslationSetDTO 对齐 config.v1.TranslationSet。
export interface TranslationSetDTO {
  items: TranslationDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// CreateConfigInput 对齐 config.v1.CreateConfigSuperRequest.config 的可填
// 字段：configKey 未删除行间唯一（冲突 409）；valueType 限定
// string/integer/boolean/float/json；status 缺省启用。
export interface CreateConfigInput {
  status?: number
  name: string
  configKey: string
  value?: string
  valueType: string
  remark?: string
}

// UpdateConfigInput 对齐 config.v1.UpdateConfigSuperRequest.config：id 标识
// 目标记录，覆盖 name/status/value/remark；configKey 与 valueType 在输入中
// 被忽略，前端不上送。
export interface UpdateConfigInput {
  id: string
  status: number
  name: string
  value?: string
  remark?: string
}

// CreateTenantConfigInput 对齐 config.v1.CreateTenantConfigRequest.
// tenant_config 的可填字段：tenantId 由服务端落值，请求值被忽略。
export interface CreateTenantConfigInput {
  status?: number
  name: string
  configKey: string
  value?: string
  valueType: string
  remark?: string
}

// UpdateTenantConfigInput 对齐 config.v1.UpdateTenantConfigRequest.
// tenant_config：configKey/valueType/tenantId 创建后不可变，前端不上送。
export interface UpdateTenantConfigInput {
  id: string
  status: number
  name: string
  value?: string
  remark?: string
}

// CreateFeatureFlagInput 对齐 config.v1.CreateFeatureFlagRequest.feature_flag
// 的可填字段：flagKey 租户内唯一；isEnabled 缺省 false；status 缺省启用。
export interface CreateFeatureFlagInput {
  status?: number
  name: string
  flagKey: string
  description?: string
  isEnabled?: boolean
  remark?: string
}

// UpdateFeatureFlagInput 对齐 config.v1.UpdateFeatureFlagRequest.feature_flag：
// id 标识目标记录，覆盖 name/description/isEnabled/remark/status；flagKey 与
// tenantId 在输入中被忽略，前端不上送。isEnabled 为覆盖语义（未设置落
// false），调用方必须显式给出，避免省略导致意外关闭。
export interface UpdateFeatureFlagInput {
  id: string
  status: number
  name: string
  description?: string
  isEnabled: boolean
  remark?: string
}

// CreateTranslationInput 对齐 config.v1.CreateTranslationRequest.translation
// 的可填字段：rowId 为字符串型 int64 且必须为正；locale 为 BCP-47 基本形态
// （如 zh-CN）；身份四元组租户内唯一（冲突 409）。
export interface CreateTranslationInput {
  status?: number
  tableName: string
  rowId: string
  columnName: string
  locale: string
  value: string
  remark?: string
}

// UpdateTranslationInput 对齐 config.v1.UpdateTranslationRequest.translation：
// id 标识目标记录，仅 status/value/remark 可变；身份四元组与 tenantId 在输入
// 中被忽略，前端不上送。
export interface UpdateTranslationInput {
  id: string
  status: number
  value: string
  remark?: string
}

// compact 序列化请求体：JSON.stringify 本就省略值为 undefined 的键，对
// protojson 的 optional 字段即未设置（与显式 null 同义，null 在解码时被跳过）。
function compact(body: object): string {
  return JSON.stringify(body)
}

// ---------- 全局配置 ----------

// createConfig 调用 POST /v1/configs/create（body: "*"，请求消息外层字段
// 为 config）；config_key 冲突返回 409。
export async function createConfig(config: CreateConfigInput): Promise<ConfigDTO> {
  const res = await authFetch('/v1/configs/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ config }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as ConfigDTO
}

// fetchConfig 调用 GET /v1/configs/{id}：目标不存在或已软删返回 404。
export async function fetchConfig(id: string): Promise<ConfigDTO> {
  const res = await authFetch(`/v1/configs/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as ConfigDTO
}

// listConfigs 调用 POST /v1/configs/list（body: "query"，Query 消息即体，
// 平铺分页列表）。零命中时响应没有 items 键，补齐为空数组。
export async function listConfigs(query?: DynamicQuery): Promise<ConfigSetDTO> {
  const res = await authFetch('/v1/configs/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as ConfigSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateConfig 调用 PUT /v1/configs/update（body: "config"，Config 消息
// 即体，覆盖 name/status/value/remark）。
export async function updateConfig(config: UpdateConfigInput): Promise<ConfigDTO> {
  const res = await authFetch('/v1/configs/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(config),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as ConfigDTO
}

// deleteConfig 调用 DELETE /v1/configs/{id}（软删除；软删后 config_key 可
// 被新配置复用）。
export async function deleteConfig(id: string): Promise<void> {
  const res = await authFetch(`/v1/configs/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

// ---------- 租户配置 ----------

// createTenantConfig 调用 POST /v1/tenant-configs/create（body: "*"，请求
// 消息外层字段为 tenantConfig）；租户内 config_key 冲突返回 409。
export async function createTenantConfig(config: CreateTenantConfigInput): Promise<TenantConfigDTO> {
  const res = await authFetch('/v1/tenant-configs/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ tenantConfig: config }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TenantConfigDTO
}

// fetchTenantConfig 调用 GET /v1/tenant-configs/{id}：跨租户不可见等同
// 不存在（404 语义）。
export async function fetchTenantConfig(id: string): Promise<TenantConfigDTO> {
  const res = await authFetch(`/v1/tenant-configs/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TenantConfigDTO
}

// listTenantConfigs 调用 POST /v1/tenant-configs/list；items 缺失归一为 []。
export async function listTenantConfigs(query?: DynamicQuery): Promise<TenantConfigSetDTO> {
  const res = await authFetch('/v1/tenant-configs/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as TenantConfigSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateTenantConfig 调用 PUT /v1/tenant-configs/update（body:
// "tenant_config"，TenantConfig 消息即体，覆盖 name/status/value/remark）。
export async function updateTenantConfig(config: UpdateTenantConfigInput): Promise<TenantConfigDTO> {
  const res = await authFetch('/v1/tenant-configs/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(config),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TenantConfigDTO
}

// deleteTenantConfig 调用 DELETE /v1/tenant-configs/{id}（软删除；跨租户
// 目标由 RLS 天然不可见，等同不存在）。
export async function deleteTenantConfig(id: string): Promise<void> {
  const res = await authFetch(`/v1/tenant-configs/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

// ---------- 功能开关 ----------

// createFeatureFlag 调用 POST /v1/feature-flags/create（body: "*"，请求消息
// 外层字段为 featureFlag）；租户内 flag_key 冲突返回 409。
export async function createFeatureFlag(flag: CreateFeatureFlagInput): Promise<FeatureFlagDTO> {
  const res = await authFetch('/v1/feature-flags/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ featureFlag: flag }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as FeatureFlagDTO
}

// fetchFeatureFlag 调用 GET /v1/feature-flags/{id}。
export async function fetchFeatureFlag(id: string): Promise<FeatureFlagDTO> {
  const res = await authFetch(`/v1/feature-flags/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as FeatureFlagDTO
}

// listFeatureFlags 调用 POST /v1/feature-flags/list；items 缺失归一为 []。
export async function listFeatureFlags(query?: DynamicQuery): Promise<FeatureFlagSetDTO> {
  const res = await authFetch('/v1/feature-flags/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as FeatureFlagSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateFeatureFlag 调用 PUT /v1/feature-flags/update（body: "feature_flag"，
// FeatureFlag 消息即体，覆盖 name/description/isEnabled/remark/status）。
// isEnabled 是覆盖语义：调用方必须显式给出期望值（含快捷启停）。
export async function updateFeatureFlag(flag: UpdateFeatureFlagInput): Promise<FeatureFlagDTO> {
  const res = await authFetch('/v1/feature-flags/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(flag),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as FeatureFlagDTO
}

// deleteFeatureFlag 调用 DELETE /v1/feature-flags/{id}（软删除）。
export async function deleteFeatureFlag(id: string): Promise<void> {
  const res = await authFetch(`/v1/feature-flags/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

// ---------- 翻译 ----------

// createTranslation 调用 POST /v1/translations/create（body: "*"，请求消息
// 外层字段为 translation）；身份四元组冲突返回 409。
export async function createTranslation(translation: CreateTranslationInput): Promise<TranslationDTO> {
  const res = await authFetch('/v1/translations/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ translation }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TranslationDTO
}

// fetchTranslation 调用 GET /v1/translations/{id}。
export async function fetchTranslation(id: string): Promise<TranslationDTO> {
  const res = await authFetch(`/v1/translations/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TranslationDTO
}

// listTranslations 调用 POST /v1/translations/list；items 缺失归一为 []。
export async function listTranslations(query?: DynamicQuery): Promise<TranslationSetDTO> {
  const res = await authFetch('/v1/translations/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as TranslationSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateTranslation 调用 PUT /v1/translations/update（body: "translation"，
// Translation 消息即体，覆盖 status/value/remark）。
export async function updateTranslation(translation: UpdateTranslationInput): Promise<TranslationDTO> {
  const res = await authFetch('/v1/translations/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(translation),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as TranslationDTO
}

// deleteTranslation 调用 DELETE /v1/translations/{id}（软删除）。
export async function deleteTranslation(id: string): Promise<void> {
  const res = await authFetch(`/v1/translations/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}
