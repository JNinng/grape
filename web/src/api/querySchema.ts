import { authFetch } from './http'
import { toApiError } from './toApiError'

// QueryService.GetListSchema（api/query/v1/query.proto）的前端客户端：
// 动态 List 能力 schema 是确定性纯函数产出，前端据此驱动查询组件与
// 表格默认排序/页大小。序列化口径与 protojson 一致：字段小驼峰。

// ListSchemaFieldDTO 对齐 query.v1.ListSchemaField：ops 是该字段可用的
// 过滤操作符全集（空集省略，按空数组处理）；enum 的合法枚举名即选项；
// options 是 UI manifest 静态字典（value 按 dataType 定型 number/string，
// 与远程 api 选项源互斥）；visibility 为初始显隐（always/optional/hidden，
// 缺省 optional）。其余为字段能力位（sortable/nullable/desc）。
export interface ListSchemaFieldDTO {
  field: string
  label: string
  dataType: string
  nullable?: boolean
  sortable?: boolean
  visibility?: 'always' | 'optional' | 'hidden'
  ops?: string[]
  enum?: string[]
  desc?: string
  options?: { value: number | string; label: string }[]
  api?: {
    endpoint: string
    valueKey: string
    labelKey: string
    mode: string
  }
}

// ListSchemaDTO 对齐 query.v1.ListSchema：defaultSort 是查询未指定 sort
// 时的服务端回落；pagination 描述双模式分页与 total 计算能力。
export interface ListSchemaDTO {
  resource: string
  hash: string
  fields: ListSchemaFieldDTO[]
  defaultSort?: {
    field: string
    desc?: boolean
  }[]
  pagination?: {
    defaultPageSize: number
    maxPageSize: number
    modes: string[]
    includeTotal?: boolean
  }
}

// getListSchema 调用 GET /v1/query/schema?resource=<resource>：资源未收录
// 时服务端返回 INVALID_ARGUMENT（经 toApiError 抛出）。
export async function getListSchema(resource: string): Promise<ListSchemaDTO> {
  const res = await authFetch(`/v1/query/schema?resource=${encodeURIComponent(resource)}`)
  if (!res.ok) {
    throw await toApiError(res)
  }
  const schema = (await res.json()) as ListSchemaDTO
  return { ...schema, fields: schema.fields ?? [] }
}
