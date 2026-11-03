// 与 query.v1 契约对齐的动态 List 查询协议类型（protojson 口径）：
// 字段键小驼峰，多条 where 恒为 AND 组合。各管理模块的 List 端点共用。

// QueryOp 是条件操作符全集，每列可用的子集由存储层 schema 注释声明
// 并在服务端校验；前端只能从目标列声明的子集中取值。
export type QueryOp =
  | 'eq'
  | 'neq'
  | 'in'
  | 'not_in'
  | 'gt'
  | 'gte'
  | 'lt'
  | 'lte'
  | 'between'
  | 'contains'
  | 'prefix'
  | 'suffix'
  | 'has'
  | 'is_null'
  | 'not_null'

// QueryCondition 是单个过滤条件：between 用 value 放下界、rightValue
// 放上界；in / not_in 的 value 为数组；is_null / not_null 不带值。
export interface QueryCondition {
  field: string
  op: QueryOp
  value?: unknown
  rightValue?: unknown
}

// QuerySort 是单个排序项；order 缺省 asc，仅 @sortable 列被接受。
export interface QuerySort {
  field: string
  order?: 'asc' | 'desc'
}

// QueryPagination 是分页输入：page 从 1 开始（offset 模式）；after 非空
// 切换游标模式且与 page 互斥；includeTotal 缺省 true。
export interface QueryPagination {
  page?: number
  pageSize?: number
  after?: string
  includeTotal?: boolean
}

// DynamicQuery 是 List 请求体（body: "query" 端点的直接消息体）。
export interface DynamicQuery {
  fields?: string[]
  where?: QueryCondition[]
  sort?: QuerySort[]
  pagination?: QueryPagination
}

// QuerySet 是动态 List 端点分页信封的通用形态：total 是 protojson 下的
// 字符串型 int64（includeTotal=false 或未计算时缺省）。
export interface QuerySet<T> {
  items: T[]
  total?: string
  hasMore: boolean
  nextCursor: string
  page: number
}
