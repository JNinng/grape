import { useCallback, useEffect, useState, type FormEvent, type KeyboardEvent } from 'react'
import { EraserIcon, PlusIcon, SearchIcon, XIcon } from 'lucide-react'
import type { DateRange } from 'react-day-picker'
import { zhCN } from 'react-day-picker/locale'
import { cn } from 'cn'
import type { QueryCondition, QueryOp, QuerySort } from '@/api/query'
import { getListSchema, type ListSchemaDTO } from '@/api/querySchema'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Popover, PopoverAnchor, PopoverContent } from '@/components/ui/popover'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

// QueryFilter 渐进式筛选输入：观感为一个输入框，已落条件以 chip 串在框内
// 展示（AND 语义，与 query.v1 的 where 恒为 AND 对齐）；点击空框或 ＋ 弹出
// Popover 三段推进——选字段 → 默认操作符（可切换）→ 按值类型输入/选值，
// 完成即落 chip 并回调 onChange（落条即查，无查询按钮）。字段的 ops 子集
// 须与存储层 schema 列注释声明（@dynamic）对齐，服务端会按列校验。

// FilterValueType 是值编辑器类型：text/number 用输入框、select 用枚举
// 下拉（options 必填）、date 用日历（between 渲染为区间选择）。
export type FilterValueType = 'text' | 'number' | 'select' | 'date'

// FilterField 是页面声明的可筛字段：field 为查询字段名（小驼峰），ops 为
// 该列允许的操作符子集。
export interface FilterField {
  field: string
  label: string
  ops: QueryOp[]
  valueType: FilterValueType
  options?: { label: string; value: string | number }[]
}

export interface QueryFilterProps {
  fields: FilterField[]
  // value 受控持有当前条件（多条 AND）；onChange 在条件落条或移除时回调。
  value: QueryCondition[]
  onChange: (conditions: QueryCondition[]) => void
  // onSearch 是显式重查入口（框内搜索图标触发）：落条即查之外，供用户
  // 以当前条件手动再查一次（页面侧通常递增 reloadKey）。
  onSearch?: () => void
  ariaLabel?: string
  placeholder?: string
}

// OP_LABELS 是操作符的中文展示词表（与 QueryOp 全集一一对应）。
const OP_LABELS: Record<QueryOp, string> = {
  eq: '等于',
  neq: '不等于',
  in: '属于',
  not_in: '不属于',
  gt: '大于',
  gte: '大于等于',
  lt: '小于',
  lte: '小于等于',
  between: '介于',
  contains: '包含',
  prefix: '开头为',
  suffix: '结尾为',
  has: '含有',
  is_null: '为空',
  not_null: '不为空',
}

// DEFAULT_OPS 是各值类型的默认操作符优先序：选完字段落到第一个命中的 op，
// 值步顶部可随时切换。
const DEFAULT_OPS: Record<FilterValueType, QueryOp[]> = {
  text: ['contains', 'prefix', 'suffix', 'eq'],
  number: ['eq', 'gte', 'lte', 'between', 'gt', 'lt'],
  select: ['eq', 'in'],
  date: ['between', 'gte', 'lte', 'eq'],
}

// opNeedsValue 区分无值操作符：is_null/not_null 无需值。二者按渲染契约
// （dynq desc.md §10.2）为 API-only——操作符下拉不渲染，故仅在协议层保留。
function opNeedsValue(op: QueryOp): boolean {
  return op !== 'is_null' && op !== 'not_null'
}

// opRenderable 过滤 API-only 操作符：操作符下拉状态 = ops 中除
// is_null/not_null 的全部 op。
function opRenderable(op: QueryOp): boolean {
  return opNeedsValue(op)
}

// isMultiValueOp 标记多值操作符：值控件形态由 op 决定（渲染契约 §10.2），
// in/not_in 渲染多值输入，between 渲染双控件，其余单值。
function isMultiValueOp(op: QueryOp): boolean {
  return op === 'in' || op === 'not_in'
}

// SCHEMA_VALUE_TYPES 把 schema 的 dataType 映射为值编辑器类型：时间类出
// 日历、数值类出数字框，enum 出静态枚举下拉，其余（含 string/boolean）
// 出文本框。
const SCHEMA_VALUE_TYPES: Record<string, FilterValueType> = {
  datetime: 'date',
  date: 'date',
  integer: 'number',
  decimal: 'number',
  number: 'number',
  enum: 'select',
}

// filterFieldsFromSchema 把 ListSchema 响应映射为筛选字段配置：内容以
// 响应为主——凡声明了 ops 的字段皆可筛（label/ops 直取响应），值编辑器
// 类型由 dataType 推导，enum 的合法枚举名即选项；未知操作符剔除后无
// 可用 op 的字段一并丢弃。
export function filterFieldsFromSchema(schema: ListSchemaDTO): FilterField[] {
  return schema.fields
    .filter((field) => field.ops.length > 0)
    .map((field) => {
      const valueType = SCHEMA_VALUE_TYPES[field.dataType] ?? 'text'
      return {
        field: field.field,
        label: field.label,
        ops: field.ops.filter((op): op is QueryOp => OP_LABELS[op as QueryOp] !== undefined),
        valueType,
        ...(valueType === 'select'
          ? { options: (field.enum ?? []).map((name) => ({ label: name, value: name })) }
          : {}),
      }
    })
    .filter((field) => field.ops.length > 0)
}

// defaultSortFromSchema 把 schema 的 default_sort 换算为 QuerySort：desc
// 布尔映射为排序方向，缺省 asc。
export function defaultSortFromSchema(schema: ListSchemaDTO): QuerySort[] {
  return (schema.defaultSort ?? []).map((item) => ({
    field: item.field,
    order: item.desc === true ? 'desc' : 'asc',
  }))
}

// useListSchema 拉取资源的 List 能力 schema：加载中/失败/重试由各管理页
// 统一消费（失败不静默降级——筛选与列定义都依赖响应）。
export function useListSchema(resource: string): {
  schema: ListSchemaDTO | null
  error: string | null
  loading: boolean
  retry: () => void
} {
  const [schema, setSchema] = useState<ListSchemaDTO | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [retryCount, setRetryCount] = useState(0)

  useEffect(() => {
    let active = true
    setLoading(true)
    setError(null)
    getListSchema(resource)
      .then((reply) => {
        if (active) {
          setSchema(reply)
          setLoading(false)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setSchema(null)
          setError(err instanceof Error ? err.message : '加载失败')
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [resource, retryCount])

  const retry = useCallback(() => setRetryCount((count) => count + 1), [])
  return { schema, error, loading, retry }
}

function defaultOpFor(field: FilterField): QueryOp {
  for (const op of DEFAULT_OPS[field.valueType]) {
    if (field.ops.includes(op)) {
      return op
    }
  }
  return field.ops[0]
}

// formatDateISO 以本地年月日输出 YYYY-MM-DD（零填充），供日期边界换算与
// chip 展示共用。
function formatDateISO(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}

// localOffset 后缀输出本地时区偏移（如 +08:00），使日期边界落在用户视角
// 的当日；RFC3339 接受带偏移的时间戳。
function localOffsetSuffix(date: Date): string {
  const minutes = date.getTimezoneOffset()
  const sign = minutes <= 0 ? '+' : '-'
  const abs = Math.abs(minutes)
  const hours = String(Math.floor(abs / 60)).padStart(2, '0')
  const mins = String(abs % 60).padStart(2, '0')
  return `${sign}${hours}:${mins}`
}

// dateBoundaryISO 把选中的日期换算为当日边界时间戳：datetime 列（timestamptz）
// 服务端只收 RFC3339，故 gte/gt 型边界取当日 00:00:00，lte/lt 型取 23:59:59。
function dateBoundaryISO(date: Date, endOfDay: boolean): string {
  const time = endOfDay ? 'T23:59:59' : 'T00:00:00'
  return `${formatDateISO(date)}${time}${localOffsetSuffix(date)}`
}

// dateValueISO 按操作符方向选择日期边界：gte/lt 为「从该日起点算」语义
// 取 00:00:00，gt/lte 为「到该日终点算」语义取 23:59:59。
function dateValueISO(date: Date, op: QueryOp): string {
  return dateBoundaryISO(date, op === 'gt' || op === 'lte')
}

// displayValue 把条件值转为 chip 文案：select 选项映射为标签，日期取
// YYYY-MM-DD（边界时间戳只用于服务端），数组逐项连接，其余按字符串展示。
function displayValue(field: FilterField | undefined, val: unknown): string {
  if (field?.valueType === 'select') {
    return field.options?.find((option) => String(option.value) === String(val))?.label ?? String(val)
  }
  if (field?.valueType === 'date') {
    return String(val).slice(0, 10)
  }
  return String(val)
}

// conditionText 组装 chip 文案：`用户名 包含 ali`、`创建时间 介于 a ~ b`。
function conditionText(fields: FilterField[], cond: QueryCondition): string {
  const field = fields.find((item) => item.field === cond.field)
  const opLabel = OP_LABELS[cond.op] ?? cond.op
  if (!opNeedsValue(cond.op)) {
    return `${field?.label ?? cond.field} ${opLabel}`
  }
  if (cond.op === 'between') {
    return `${field?.label ?? cond.field} ${opLabel} ${displayValue(field, cond.value)} ~ ${displayValue(field, cond.rightValue)}`
  }
  if (cond.op === 'in' || cond.op === 'not_in') {
    const items = Array.isArray(cond.value) ? cond.value : [cond.value]
    return `${field?.label ?? cond.field} ${opLabel} ${items.map((item) => displayValue(field, item)).join('、')}`
  }
  return `${field?.label ?? cond.field} ${opLabel} ${displayValue(field, cond.value)}`
}

// Draft 是值编辑进行中的字段与操作符；null 表示处于选字段步。
interface Draft {
  field: FilterField
  op: QueryOp
}

export function QueryFilter({ fields, value, onChange, onSearch, ariaLabel = '筛选条件', placeholder = '点击添加筛选条件…' }: QueryFilterProps) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<Draft | null>(null)
  // editIndex 非空表示正在回编辑第几条已落条件：commit 变为按下标替换
  // 而非追加；关闭弹层即归位。
  const [editIndex, setEditIndex] = useState<number | null>(null)
  const [text, setText] = useState('')
  const [lowerText, setLowerText] = useState('')
  const [upperText, setUpperText] = useState('')
  // multiText 是 in/not_in 的多值输入（逗号/空白分隔）；selectedEnum 是
  // 枚举多选的勾选集（select 型多值操作符的取值）。
  const [multiText, setMultiText] = useState('')
  const [selectedEnum, setSelectedEnum] = useState<string[]>([])
  const [dateSingle, setDateSingle] = useState<Date | undefined>(undefined)
  const [dateRange, setDateRange] = useState<DateRange | undefined>(undefined)

  const resetEditor = () => {
    setText('')
    setLowerText('')
    setUpperText('')
    setMultiText('')
    setSelectedEnum([])
    setDateSingle(undefined)
    setDateRange(undefined)
  }

  // openDraft 打开选字段步（清空任何进行中的编辑状态）。
  const openDraft = () => {
    resetEditor()
    setEditIndex(null)
    setDraft(null)
    setOpen(true)
  }

  // editAt 点击已落 chip 回编辑：按条件预填编辑器（文本/数字回填原值，
  // 日期由 RFC3339 边界还原 Date；select 由用户重选），commit 时按下标替换。
  const editAt = (index: number) => {
    const cond = value[index]
    if (cond === undefined) {
      return
    }
    const field = fields.find((item) => item.field === cond.field)
    if (field === undefined) {
      openDraft()
      return
    }
    resetEditor()
    setEditIndex(index)
    setDraft({ field, op: cond.op })
    if (opNeedsValue(cond.op)) {
      if (field.valueType === 'select') {
        // 枚举多选回填勾选集；单选由用户重选（点选即落）。
        if (isMultiValueOp(cond.op) && Array.isArray(cond.value)) {
          setSelectedEnum(cond.value.map((item) => String(item)))
        }
      } else if (isMultiValueOp(cond.op)) {
        // 多值输入回填原数组（日期截到 YYYY-MM-DD 便于重编辑；展示用逗号
        // 连接，重新提交时再拆分）。
        setMultiText(
          Array.isArray(cond.value)
            ? cond.value.map((item) => (field.valueType === 'date' ? String(item).slice(0, 10) : String(item))).join(', ')
            : cond.value === undefined
              ? ''
              : String(cond.value),
        )
      } else if (field.valueType === 'text' || field.valueType === 'number') {
        if (cond.op === 'between') {
          setLowerText(cond.value === undefined ? '' : String(cond.value))
          setUpperText(cond.rightValue === undefined ? '' : String(cond.rightValue))
        } else {
          setText(cond.value === undefined ? '' : String(cond.value))
        }
      } else if (field.valueType === 'date') {
        if (cond.op === 'between') {
          const from = cond.value === undefined ? undefined : new Date(String(cond.value))
          const to = cond.rightValue === undefined ? undefined : new Date(String(cond.rightValue))
          if (from !== undefined && to !== undefined && !Number.isNaN(from.getTime()) && !Number.isNaN(to.getTime())) {
            setDateRange({ from, to })
          }
        } else if (cond.value !== undefined) {
          const date = new Date(String(cond.value))
          if (!Number.isNaN(date.getTime())) {
            setDateSingle(date)
          }
        }
      }
    }
    setOpen(true)
  }

  // handleOpenChange 关闭时归位编辑状态；radix 的 Esc/外点均经此收敛。
  const handleOpenChange = (next: boolean) => {
    if (!next) {
      resetEditor()
      setEditIndex(null)
      setDraft(null)
    }
    setOpen(next)
  }

  const removeAt = (index: number) => {
    onChange(value.filter((_, i) => i !== index))
  }

  // clearAll 一次性移除全部条件（框内「清空」入口）。
  const clearAll = () => {
    onChange([])
  }

  // commit 落条并收起：编辑态按下标替换，新增态追加。
  const commit = (condition: QueryCondition) => {
    onChange(editIndex === null ? [...value, condition] : value.map((cond, i) => (i === editIndex ? condition : cond)))
    handleOpenChange(false)
  }

  // pickField 选字段后进入值步；无值操作符直接落条。
  const pickField = (field: FilterField) => {
    const op = defaultOpFor(field)
    if (!opNeedsValue(op)) {
      commit({ field: field.field, op })
      return
    }
    resetEditor()
    setDraft({ field, op })
  }

  // changeOp 切换操作符并清空已填值；切到无值操作符立即落条。
  const changeOp = (op: QueryOp) => {
    if (draft === null) {
      return
    }
    if (!opNeedsValue(op)) {
      commit({ field: draft.field.field, op })
      return
    }
    resetEditor()
    setDraft({ ...draft, op })
  }

  // commitTextValue 提交文本/数字/多值编辑器的取值：in/not_in 走多值拆分，
  // between 走双端，其余单值。date 的多值输入也经此路径（逐项校验
  // YYYY-MM-DD 后换算当日边界）；select 编辑器单选即落、多选走确定按钮。
  const commitTextValue = (kind: FilterValueType) => {
    if (draft === null || kind === 'select') {
      return
    }
    const parse = (raw: string): unknown | null => {
      if (raw.trim() === '') {
        return null
      }
      if (kind === 'number') {
        const parsed = Number(raw)
        return Number.isFinite(parsed) ? parsed : null
      }
      return raw.trim()
    }
    if (isMultiValueOp(draft.op)) {
      const parsed = parseMultiTokens(multiText, kind)
      if (!parsed.ok) {
        return
      }
      commit({ field: draft.field.field, op: draft.op, value: parsed.values })
      return
    }
    if (draft.op === 'between') {
      const lower = parse(lowerText)
      const upper = parse(upperText)
      if (lower === null || upper === null) {
        return
      }
      commit({ field: draft.field.field, op: draft.op, value: lower, rightValue: upper })
      return
    }
    const single = parse(text)
    if (single === null) {
      return
    }
    commit({ field: draft.field.field, op: draft.op, value: single })
  }

  // parseMultiTokens 把多值输入按逗号/空白拆分并逐项按值类型解析：数字逐项
  // Number、日期逐项校验后取当日起点，任一项非法即整体不可提交。
  const parseMultiTokens = (raw: string, kind: FilterValueType): { ok: boolean; values: unknown[] } => {
    const tokens = raw
      .split(/[\s,，;；]+/)
      .map((token) => token.trim())
      .filter((token) => token !== '')
    if (tokens.length === 0) {
      return { ok: false, values: [] }
    }
    const values: unknown[] = []
    for (const token of tokens) {
      if (kind === 'number') {
        const parsed = Number(token)
        if (!Number.isFinite(parsed)) {
          return { ok: false, values: [] }
        }
        values.push(parsed)
      } else if (kind === 'date') {
        if (!/^\d{4}-\d{2}-\d{2}$/.test(token)) {
          return { ok: false, values: [] }
        }
        const date = new Date(`${token}T00:00:00`)
        if (Number.isNaN(date.getTime())) {
          return { ok: false, values: [] }
        }
        values.push(dateBoundaryISO(date, false))
      } else {
        values.push(token)
      }
    }
    return { ok: true, values }
  }

  // commitSelectMulti 提交枚举多选：勾选项的 value 数组整体落条。
  const commitSelectMulti = () => {
    if (draft === null || selectedEnum.length === 0) {
      return
    }
    const values = selectedEnum.map(
      (optionValue) => draft.field.options?.find((item) => String(item.value) === optionValue)?.value ?? optionValue,
    )
    commit({ field: draft.field.field, op: draft.op, value: values })
  }

  // commitSelectValue 提交枚举单选取值（单值操作符点选即落条）。
  const commitSelectValue = (optionValue: string) => {
    if (draft === null) {
      return
    }
    const option = draft.field.options?.find((item) => String(item.value) === optionValue)
    if (option === undefined) {
      return
    }
    commit({ field: draft.field.field, op: draft.op, value: option.value })
  }

  // submitText 是带确定按钮的编辑器表单提交入口（输入框内回车同按钮）。
  const submitText = (event: FormEvent) => {
    event.preventDefault()
    if (draft === null) {
      return
    }
    if (draft.field.valueType === 'select') {
      commitSelectMulti()
      return
    }
    commitTextValue(draft.field.valueType)
  }

  const boxKeydown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      openDraft()
    }
  }

  // textReady 控制确定按钮的可用性：多值需逐项合法，between 需双端，
  // 数字单值需有限数值。
  const textReady =
    draft !== null &&
    (isMultiValueOp(draft.op)
      ? draft.field.valueType === 'select'
        ? selectedEnum.length > 0
        : parseMultiTokens(multiText, draft.field.valueType).ok
      : draft.op === 'between'
        ? lowerText.trim() !== '' && upperText.trim() !== '' && (draft.field.valueType !== 'number' || (Number.isFinite(Number(lowerText)) && Number.isFinite(Number(upperText))))
        : text.trim() !== '' && (draft.field.valueType !== 'number' || Number.isFinite(Number(text))))

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverAnchor asChild>
        <div
          role="button"
          tabIndex={0}
          aria-label={ariaLabel}
          aria-haspopup="dialog"
          aria-expanded={open}
          className={cn(
            'flex min-h-9 min-w-100 max-w-2xl flex-1 cursor-text flex-wrap items-center gap-1.5 rounded-md border border-input bg-transparent px-2.5 py-1 text-sm shadow-xs outline-none',
            'focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring',
          )}
          onClick={openDraft}
          onKeyDown={boxKeydown}
        >
          {/* ＋ 图标——打开筛选弹层。 */}
            <span
              role="button"
              tabIndex={-1}
              aria-label="添加筛选条件"
              className="flex cursor-pointer items-center"
              onClick={(event) => {
                event.stopPropagation()
                openDraft()
              }}
              onKeyDown={(event) => event.stopPropagation()}
            >
              <PlusIcon aria-hidden className="size-3.5" />
            </span>
          {value.length === 0 ? (
            <span className="py-0.5 text-muted-foreground">{placeholder}</span>
          ) : (
            value.map((cond, index) => {
              const chip = conditionText(fields, cond)
              return (
                <span key={`${index}-${chip}`} className="flex items-center gap-1">
                  {index > 0 && <span className="text-xs text-muted-foreground">且</span>}
                  <span className="flex max-w-full items-center gap-0.5 rounded-sm border border-brand-soft-border bg-brand-soft text-brand-text">
                    <button
                      type="button"
                      aria-label={`编辑条件：${chip}`}
                      className="max-w-56 cursor-pointer truncate px-1.5 py-0.5 text-left"
                      onClick={(event) => {
                        event.stopPropagation()
                        editAt(index)
                      }}
                      onKeyDown={(event) => event.stopPropagation()}
                    >
                      {chip}
                    </button>
                    <button
                      type="button"
                      aria-label={`移除条件：${chip}`}
                      className="cursor-pointer rounded-sm px-1 py-0.5 text-brand-text-soft hover:text-foreground"
                      onClick={(event) => {
                        event.stopPropagation()
                        removeAt(index)
                      }}
                      onKeyDown={(event) => event.stopPropagation()}
                    >
                      <XIcon aria-hidden className="size-3.5" />
                    </button>
                  </span>
                </span>
              )
            })
          )}
          <span className="ml-auto flex items-center gap-1.5 text-muted-foreground">
            {value.length > 0 && (
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <button
                      type="button"
                      aria-label="清空筛选条件"
                      className="cursor-pointer rounded-sm hover:text-foreground"
                      onClick={(event) => {
                        event.stopPropagation()
                        clearAll()
                      }}
                      onKeyDown={(event) => event.stopPropagation()}
                    >
                      <EraserIcon aria-hidden className="size-3.5" />
                    </button>
                  </TooltipTrigger>
                  <TooltipContent>清空</TooltipContent>
                </Tooltip>
              </TooltipProvider>
            )}
            <span
              role="button"
              tabIndex={-1}
              aria-label="执行搜索"
              className="flex cursor-pointer items-center"
              onClick={(event) => {
                event.stopPropagation()
                onSearch?.()
              }}
              onKeyDown={(event) => event.stopPropagation()}
            >
              <SearchIcon aria-hidden className="ml-1 size-4" />
            </span>
          </span>
        </div>
      </PopoverAnchor>

      <PopoverContent align="start" className="w-80 p-3">
        {draft === null ? (
          <div className="grid gap-1">
            <p className="px-1 pb-1 text-xs text-muted-foreground">选择字段</p>
            {fields.map((field) => (
              <Button key={field.field} type="button" variant="ghost" size="sm" className="justify-start" onClick={() => pickField(field)}>
                {field.label}
              </Button>
            ))}
          </div>
        ) : (
          <form className="grid gap-3" onSubmit={submitText}>
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium">{draft.field.label}</span>
              <Select value={draft.op} onValueChange={(op) => changeOp(op as QueryOp)}>
                <SelectTrigger aria-label="操作符" size="sm" className="ml-auto w-32">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {/* 渲染契约（dynq desc.md §10.2）：操作符下拉 = ops 去除
                   * is_null/not_null（API-only 不渲染）。 */}
                  {draft.field.ops.filter((op) => opRenderable(op)).map((op) => (
                    <SelectItem key={op} value={op}>
                      {OP_LABELS[op]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {/* 值控件形态由 op 决定：in/not_in 多值、between 双控件、
             * 其余单值；select 型多值走勾选列表。 */}
            {draft.field.valueType === 'select' ? (
              isMultiValueOp(draft.op) ? (
                <div aria-label="筛选值" className="grid max-h-48 gap-1 overflow-auto" role="group">
                  {(draft.field.options ?? []).map((option) => {
                    const optionKey = String(option.value)
                    return (
                      <label key={optionKey} className="flex cursor-pointer items-center gap-2 rounded-sm px-1 py-0.5 text-sm hover:bg-accent">
                        <Checkbox
                          checked={selectedEnum.includes(optionKey)}
                          onCheckedChange={(checked) => {
                            setSelectedEnum((current) =>
                              checked === true ? [...current, optionKey] : current.filter((item) => item !== optionKey),
                            )
                          }}
                        />
                        {option.label}
                      </label>
                    )
                  })}
                </div>
              ) : (
                <Select onValueChange={commitSelectValue}>
                  <SelectTrigger aria-label="筛选值" size="sm" className="w-full">
                    <SelectValue placeholder={`选择${draft.field.label}…`} />
                  </SelectTrigger>
                  <SelectContent>
                    {(draft.field.options ?? []).map((option) => (
                      <SelectItem key={String(option.value)} value={String(option.value)}>
                        {option.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )
            ) : isMultiValueOp(draft.op) ? (
              <Input
                aria-label="筛选值"
                placeholder="多值：逗号或空白分隔"
                value={multiText}
                onChange={(event) => setMultiText(event.target.value)}
                autoFocus
              />
            ) : draft.field.valueType === 'text' ? (
              draft.op === 'between' ? (
                <div className="flex items-center gap-2">
                  <Input aria-label="下界" placeholder="下界" value={lowerText} onChange={(event) => setLowerText(event.target.value)} autoFocus />
                  <span className="text-muted-foreground">~</span>
                  <Input aria-label="上界" placeholder="上界" value={upperText} onChange={(event) => setUpperText(event.target.value)} />
                </div>
              ) : (
                <Input aria-label="筛选值" placeholder={`输入${draft.field.label}…`} value={text} onChange={(event) => setText(event.target.value)} autoFocus />
              )
            ) : draft.field.valueType === 'number' ? (
              draft.op === 'between' ? (
                <div className="flex items-center gap-2">
                  <Input aria-label="下界" type="number" placeholder="下界" value={lowerText} onChange={(event) => setLowerText(event.target.value)} autoFocus />
                  <span className="text-muted-foreground">~</span>
                  <Input aria-label="上界" type="number" placeholder="上界" value={upperText} onChange={(event) => setUpperText(event.target.value)} />
                </div>
              ) : (
                <Input aria-label="筛选值" type="number" placeholder={`输入${draft.field.label}…`} value={text} onChange={(event) => setText(event.target.value)} autoFocus />
              )
            ) : (
              draft.op === 'between' ? (
                <Calendar
                  mode="range"
                  locale={zhCN}
                  selected={dateRange}
                  onSelect={(range) => {
                    setDateRange(range)
                    // rdp 首次点击返回 {from: d, to: d}（同日单点区间），
                    // 须待第二次点出不同日期才落条；改点更早日期视为重选
                    // 起点（rdp 自行收敛为新的单点区间）。
                    if (
                      range?.from !== undefined &&
                      range?.to !== undefined &&
                      range.from.getTime() !== range.to.getTime()
                    ) {
                      commit({
                        field: draft.field.field,
                        op: draft.op,
                        value: dateBoundaryISO(range.from, false),
                        rightValue: dateBoundaryISO(range.to, true),
                      })
                    }
                  }}
                />
              ) : (
                <Calendar
                  mode="single"
                  locale={zhCN}
                  selected={dateSingle}
                  onSelect={(date) => {
                    setDateSingle(date)
                    if (date !== undefined) {
                      commit({ field: draft.field.field, op: draft.op, value: dateValueISO(date, draft.op) })
                    }
                  }}
                />
              )
            )}

            {/* 单值枚举点选即落条，不出确定按钮；其余编辑器均需确认。 */}
            {(draft.field.valueType !== 'select' || isMultiValueOp(draft.op)) && (
              <Button type="submit" size="sm" disabled={!textReady}>
                确定
              </Button>
            )}
          </form>
        )}
      </PopoverContent>
    </Popover>
  )
}
