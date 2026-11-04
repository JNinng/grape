import { authFetch } from './http'
import { toApiError } from './toApiError'
import type { DynamicQuery } from './query'

// DictService 各端点（api/dict/v1/dict.proto）的前端客户端。
// 序列化口径与后端 jsoncodec（protojson 覆盖）一致：字段小驼峰、int64 为
// 字符串、时间为 RFC3339、optional 未设置时省略。

// DictTypeDTO 对齐 dict.v1.DictType：id 为字符串型 int64；status/remark 为
// proto3 optional（未设置省略，status 创建时可省略即缺省启用）；code 创建后
// 不可变。
export interface DictTypeDTO {
  id: string
  status?: number
  name: string
  code: string
  remark?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

// DictTypeSetDTO 对齐 dict.v1.DictTypeSet：ListDictTypes 平铺分页信封。
export interface DictTypeSetDTO {
  items: DictTypeDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// DictDataDTO 对齐 dict.v1.DictData：dictTypeId 为字符串型 int64；
// value/value_type/dict_type_id 创建后不可变。
export interface DictDataDTO {
  id: string
  status?: number
  dictTypeId: string
  label: string
  value: string
  valueType: string
  sortOrder: number
  remark?: string
  createdBy: string
  updatedBy: string
  createdAt: string
  updatedAt?: string
  deletedAt?: string
}

// DictDataSetDTO 对齐 dict.v1.DictDataSet：ListDictDatas 平铺分页信封。
export interface DictDataSetDTO {
  items: DictDataDTO[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}

// CreateDictTypeInput 对齐 dict.v1.CreateDictTypeRequest.dict_type 的可填
// 字段：id、created_by/updated_by 与时间戳由服务端管理；status 缺省启用。
// code 租户内唯一，冲突返回 409。
export interface CreateDictTypeInput {
  status?: number
  name: string
  code: string
  remark?: string
}

// UpdateDictTypeInput 对齐 dict.v1.UpdateDictTypeRequest.dict_type：id 标识
// 目标记录，仅 name/remark/status 可变（status 更新语义必填）；code 在输入
// 中被忽略，前端不上送。
export interface UpdateDictTypeInput {
  id: string
  status: number
  name: string
  remark?: string
}

// CreateDictDataInput 对齐 dict.v1.CreateDictDataRequest.dict_data 的可填
// 字段：宿主类型必须存在且未删除（未知 400）；同类型下 value 唯一（409）；
// valueType 限定 string/integer/boolean/float/json。status 缺省启用。
export interface CreateDictDataInput {
  status?: number
  dictTypeId: string
  label: string
  value: string
  valueType: string
  sortOrder?: number
  remark?: string
}

// UpdateDictDataInput 对齐 dict.v1.UpdateDictDataRequest.dict_data：id 标识
// 目标记录，仅 label/remark/status/sortOrder 可变；value/valueType/
// dictTypeId 在输入中被忽略，前端不上送。
export interface UpdateDictDataInput {
  id: string
  status: number
  label: string
  sortOrder: number
  remark?: string
}

// compact 序列化请求体：JSON.stringify 本就省略值为 undefined 的键，对
// protojson 的 optional 字段即未设置（与显式 null 同义，null 在解码时被跳过）。
function compact(body: object): string {
  return JSON.stringify(body)
}

// createDictType 调用 POST /v1/dict-types/create；code 冲突返回 409。
export async function createDictType(dictType: CreateDictTypeInput): Promise<DictTypeDTO> {
  const res = await authFetch('/v1/dict-types/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ dictType }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DictTypeDTO
}

// fetchDictType 调用 GET /v1/dict-types/{id}：跨租户不可见等同不存在
//（404 语义）。
export async function fetchDictType(id: string): Promise<DictTypeDTO> {
  const res = await authFetch(`/v1/dict-types/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DictTypeDTO
}

// listDictTypes 调用 POST /v1/dict-types/list（body: "query"，Query 消息即
// 体，平铺分页列表）。零命中时响应没有 items 键，补齐为空数组。
export async function listDictTypes(query?: DynamicQuery): Promise<DictTypeSetDTO> {
  const res = await authFetch('/v1/dict-types/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as DictTypeSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateDictType 调用 PUT /v1/dict-types/update（body: "dict_type"，覆盖
// name/remark/status）。
export async function updateDictType(dictType: UpdateDictTypeInput): Promise<DictTypeDTO> {
  const res = await authFetch('/v1/dict-types/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(dictType),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DictTypeDTO
}

// deleteDictType 调用 DELETE /v1/dict-types/{id}（软删除；存在未删除字典
// 数据时后端拒绝并返回 HAS_DATA 语义错误）。
export async function deleteDictType(id: string): Promise<void> {
  const res = await authFetch(`/v1/dict-types/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}

// createDictData 调用 POST /v1/dict-data/create；宿主未知返回 400、value
// 冲突返回 409。
export async function createDictData(dictData: CreateDictDataInput): Promise<DictDataDTO> {
  const res = await authFetch('/v1/dict-data/create', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact({ dictData }),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DictDataDTO
}

// fetchDictData 调用 GET /v1/dict-data/{id}。
export async function fetchDictData(id: string): Promise<DictDataDTO> {
  const res = await authFetch(`/v1/dict-data/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DictDataDTO
}

// listDictDatas 调用 POST /v1/dict-data/list；按 dictTypeId 过滤即可取某
// 类型下的全部数据项。
export async function listDictDatas(query?: DynamicQuery): Promise<DictDataSetDTO> {
  const res = await authFetch('/v1/dict-data/list', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: compact(query ?? {}),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  const set = (await res.json()) as DictDataSetDTO
  return { ...set, items: set.items ?? [] }
}

// updateDictData 调用 PUT /v1/dict-data/update（body: "dict_data"，覆盖
// label/remark/status/sort_order）。
export async function updateDictData(dictData: UpdateDictDataInput): Promise<DictDataDTO> {
  const res = await authFetch('/v1/dict-data/update', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: compact(dictData),
  })
  if (!res.ok) {
    throw await toApiError(res)
  }
  return (await res.json()) as DictDataDTO
}

// deleteDictData 调用 DELETE /v1/dict-data/{id}（软删除；宿主类型不受影响）。
export async function deleteDictData(id: string): Promise<void> {
  const res = await authFetch(`/v1/dict-data/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) {
    throw await toApiError(res)
  }
}
