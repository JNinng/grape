import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { QueryCondition } from '@/api/query'
import type { ListSchemaDTO } from '@/api/querySchema'
import { QueryFilter, defaultSortFromSchema, filterFieldsFromSchema, type FilterField } from './query-filter'

// fields 覆盖四种值编辑器与默认/切换操作符路径：文本默认 contains、数字
// 默认 eq（可切 in/between）、枚举单选点选即落（可切多选勾选）、日期
// 默认 between；最后登录含 API-only 的 is_null/not_null 验证下拉剔除。
const fields: FilterField[] = [
  { field: 'name', label: '用户名', valueType: 'text', ops: ['contains', 'prefix', 'eq'] },
  { field: 'count', label: '数量', valueType: 'number', ops: ['eq', 'in', 'between'] },
  { field: 'status', label: '状态', valueType: 'select', ops: ['eq'], options: [{ label: '启用', value: 1 }, { label: '禁用', value: 0 }] },
  { field: 'level', label: '等级', valueType: 'select', ops: ['eq', 'in', 'not_in'], options: [{ label: '普通', value: 1 }, { label: '管理员', value: 9 }] },
  { field: 'kind', label: '类型', valueType: 'select', ops: ['eq', 'in'], options: [{ label: '基础', value: 'basic' }, { label: '代理', value: 'agent' }] },
  { field: 'createdAt', label: '创建时间', valueType: 'date', ops: ['between', 'eq'] },
  { field: 'lastLoginAt', label: '最后登录', valueType: 'date', ops: ['between', 'is_null', 'not_null'] },
]

// Harness 受控持有条件串，转接 onChange 供断言（组件本身要求受控使用）。
function Harness({ onConditions }: { onConditions?: (conditions: QueryCondition[]) => void }) {
  const [value, setValue] = useState<QueryCondition[]>([])
  return (
    <QueryFilter
      fields={fields}
      value={value}
      onChange={(next) => {
        onConditions?.(next)
        setValue(next)
      }}
    />
  )
}

// openField 点筛选框进入选字段步，再选目标字段进入值编辑步。
async function openField(user2: ReturnType<typeof userEvent.setup>, fieldLabel: string) {
  await user2.click(screen.getByRole('button', { name: '筛选条件' }))
  await user2.click(await screen.findByRole('button', { name: fieldLabel }))
}

afterEach(() => {
  cleanup()
})

describe('filterFieldsFromSchema', () => {
  it('maps_schema_fields_by_ops_and_data_type_and_drops_op_less_entries', () => {
    const schema: ListSchemaDTO = {
      resource: 'sys_user',
      hash: 'h',
      fields: [
        { field: 'name', label: '用户名', dataType: 'string', ops: ['contains', 'prefix', 'suffix', 'eq', 'neq'] },
        { field: 'status', label: '状态', dataType: 'integer', ops: ['eq', 'in'], options: [{ value: 0, label: '禁用' }, { value: 1, label: '启用' }] },
        { field: 'count', label: '数量', dataType: 'integer', ops: ['eq', 'in', 'between'] },
        { field: 'createdAt', label: '创建时间', dataType: 'datetime', ops: ['eq', 'between', 'gte', 'lte'] },
        { field: 'kind', label: '类型', dataType: 'enum', ops: ['eq'], enum: ['basic', 'agent'] },
        // protojson 省略空 repeated：ops 缺键等价于不可筛。
        { field: 'password', label: '密码', dataType: 'string' },
        { field: 'future', label: '未知操作符', dataType: 'string', ops: ['regex'] },
      ],
    }

    const fields = filterFieldsFromSchema(schema)
    expect(fields.map((field) => field.field)).toEqual(['name', 'status', 'count', 'createdAt', 'kind'])
    expect(fields[0]).toMatchObject({ valueType: 'text', ops: ['contains', 'prefix', 'suffix', 'eq', 'neq'] })
    // 带 @options 的 integer 列出下拉字典（不再按 dataType 出数字输入框），
    // 单选/多选形态由 op 决定（in/not_in 多选勾选）。
    expect(fields[1]).toMatchObject({
      valueType: 'select',
      ops: ['eq', 'in'],
      options: [
        { label: '禁用', value: 0 },
        { label: '启用', value: 1 },
      ],
    })
    expect(fields[2]).toMatchObject({ valueType: 'number', ops: ['eq', 'in', 'between'] })
    expect(fields[3]).toMatchObject({ valueType: 'date' })
    expect(fields[4]).toMatchObject({
      valueType: 'select',
      options: [
        { label: 'basic', value: 'basic' },
        { label: 'agent', value: 'agent' },
      ],
    })
  })

  it('falls_back_to_the_data_type_editor_when_options_is_empty', () => {
    const schema: ListSchemaDTO = {
      resource: 'sys_user',
      hash: 'h',
      fields: [{ field: 'status', label: '状态', dataType: 'integer', ops: ['eq'], options: [] }],
    }

    const fields = filterFieldsFromSchema(schema)
    expect(fields).toHaveLength(1)
    expect(fields[0]).toMatchObject({ valueType: 'number', ops: ['eq'] })
    expect(fields[0]?.options).toBeUndefined()
  })

  it('maps_default_sort_desc_flags_to_query_sorts', () => {
    const schema: ListSchemaDTO = {
      resource: 'sys_user',
      hash: 'h',
      fields: [],
      defaultSort: [
        { field: 'createdAt', desc: true },
        { field: 'name' },
      ],
    }

    expect(defaultSortFromSchema(schema)).toEqual([
      { field: 'createdAt', order: 'desc' },
      { field: 'name', order: 'asc' },
    ])
  })
})

describe('QueryFilter progressive flow', () => {
  it('commits_a_text_condition_with_the_default_contains_op_and_renders_the_chip', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '用户名')
    await user2.type(screen.getByLabelText('筛选值'), 'ali')
    await user2.click(screen.getByRole('button', { name: '确定' }))

    expect(onConditions).toHaveBeenCalledWith([{ field: 'name', op: 'contains', value: 'ali' }])
    expect(screen.getByText('用户名 包含 ali')).toBeInTheDocument()
    expect(screen.queryByLabelText('筛选值')).not.toBeInTheDocument()
  })

  it('stacks_conditions_across_fields_with_the_and_connector', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '用户名')
    await user2.type(screen.getByLabelText('筛选值'), 'ali')
    await user2.click(screen.getByRole('button', { name: '确定' }))

    await openField(user2, '状态')
    // 单选选项内联展示，点选即落条。
    await user2.click(await screen.findByRole('option', { name: '禁用' }))

    expect(onConditions).toHaveBeenLastCalledWith([
      { field: 'name', op: 'contains', value: 'ali' },
      { field: 'status', op: 'eq', value: 0 },
    ])
    expect(screen.getByText('用户名 包含 ali')).toBeInTheDocument()
    expect(screen.getByText('状态 等于 禁用')).toBeInTheDocument()
    expect(screen.getByText('且')).toBeInTheDocument()
  })

  it('excludes_the_api_only_operators_from_the_op_dropdown', async () => {
    const user2 = userEvent.setup()
    render(<Harness />)

    // is_null/not_null 为 API-only，操作符下拉不渲染。
    await openField(user2, '最后登录')
    await user2.click(screen.getByRole('combobox', { name: '操作符' }))
    expect(await screen.findByRole('option', { name: '介于' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: '为空' })).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: '不为空' })).not.toBeInTheDocument()
  })

  it('commits_in_as_a_number_array_from_the_multi_value_input', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '数量')
    await user2.click(screen.getByRole('combobox', { name: '操作符' }))
    await user2.click(await screen.findByRole('option', { name: '属于' }))
    await user2.type(screen.getByLabelText('筛选值'), '1, 2')
    await user2.click(screen.getByRole('button', { name: '确定' }))

    expect(onConditions).toHaveBeenCalledWith([{ field: 'count', op: 'in', value: [1, 2] }])
    expect(screen.getByText('数量 属于 1、2')).toBeInTheDocument()
  })

  it('keeps_the_multi_value_confirm_disabled_until_every_token_is_valid', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '数量')
    await user2.click(screen.getByRole('combobox', { name: '操作符' }))
    await user2.click(await screen.findByRole('option', { name: '属于' }))
    await user2.type(screen.getByLabelText('筛选值'), '1, oops')
    expect(screen.getByRole('button', { name: '确定' })).toBeDisabled()
    expect(onConditions).not.toHaveBeenCalled()
  })

  it('commits_a_multi_select_enum_array_from_checkboxes', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '类型')
    await user2.click(screen.getByRole('combobox', { name: '操作符' }))
    await user2.click(await screen.findByRole('option', { name: '属于' }))
    await user2.click(screen.getByRole('checkbox', { name: '基础' }))
    await user2.click(screen.getByRole('checkbox', { name: '代理' }))
    await user2.click(screen.getByRole('button', { name: '确定' }))

    expect(onConditions).toHaveBeenCalledWith([{ field: 'kind', op: 'in', value: ['basic', 'agent'] }])
    expect(screen.getByText('类型 属于 基础、代理')).toBeInTheDocument()
  })

  it('commits_a_multi_select_numeric_options_array_from_checkboxes', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    // 数值字典（@options）同样按 op 出多选勾选：落条值为数字数组，chip
    // 以字典标签展示。
    await openField(user2, '等级')
    await user2.click(screen.getByRole('combobox', { name: '操作符' }))
    await user2.click(await screen.findByRole('option', { name: '属于' }))
    await user2.click(screen.getByRole('checkbox', { name: '普通' }))
    await user2.click(screen.getByRole('checkbox', { name: '管理员' }))
    await user2.click(screen.getByRole('button', { name: '确定' }))

    expect(onConditions).toHaveBeenCalledWith([{ field: 'level', op: 'in', value: [1, 9] }])
    expect(screen.getByText('等级 属于 普通、管理员')).toBeInTheDocument()
  })

  it('commits_a_string_single_select_condition_with_the_option_value', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '类型')
    await user2.click(await screen.findByRole('option', { name: '基础' }))

    expect(onConditions).toHaveBeenCalledWith([{ field: 'kind', op: 'eq', value: 'basic' }])
    expect(screen.getByText('类型 等于 基础')).toBeInTheDocument()
  })

  it('commits_a_not_in_multi_select_condition_as_an_array', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '等级')
    await user2.click(screen.getByRole('combobox', { name: '操作符' }))
    await user2.click(await screen.findByRole('option', { name: '不属于' }))
    await user2.click(screen.getByRole('checkbox', { name: '普通' }))
    await user2.click(screen.getByRole('checkbox', { name: '管理员' }))
    await user2.click(screen.getByRole('button', { name: '确定' }))

    expect(onConditions).toHaveBeenCalledWith([{ field: 'level', op: 'not_in', value: [1, 9] }])
    expect(screen.getByText('等级 不属于 普通、管理员')).toBeInTheDocument()
  })

  it('backfills_checked_options_when_editing_a_multi_select_condition', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '等级')
    await user2.click(screen.getByRole('combobox', { name: '操作符' }))
    await user2.click(await screen.findByRole('option', { name: '属于' }))
    await user2.click(screen.getByRole('checkbox', { name: '普通' }))
    await user2.click(screen.getByRole('checkbox', { name: '管理员' }))
    await user2.click(screen.getByRole('button', { name: '确定' }))

    await user2.click(screen.getByRole('button', { name: '编辑条件：等级 属于 普通、管理员' }))
    expect(screen.getByRole('checkbox', { name: '普通' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: '管理员' })).toBeChecked()
    await user2.click(screen.getByRole('checkbox', { name: '管理员' }))
    await user2.click(screen.getByRole('button', { name: '确定' }))

    expect(onConditions).toHaveBeenLastCalledWith([{ field: 'level', op: 'in', value: [1] }])
    expect(screen.getByText('等级 属于 普通')).toBeInTheDocument()
    expect(screen.queryByText('等级 属于 普通、管理员')).not.toBeInTheDocument()
  })

  it('commits_a_number_between_only_when_both_bounds_are_valid', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '数量')
    await user2.click(screen.getByRole('combobox', { name: '操作符' }))
    await user2.click(await screen.findByRole('option', { name: '介于' }))

    // 只填下界时不可提交；补齐上界后落条为 value/rightValue 双端。
    await user2.type(screen.getByLabelText('下界'), '1')
    expect(screen.getByRole('button', { name: '确定' })).toBeDisabled()
    await user2.type(screen.getByLabelText('上界'), '9')
    await user2.click(screen.getByRole('button', { name: '确定' }))

    expect(onConditions).toHaveBeenCalledWith([{ field: 'count', op: 'between', value: 1, rightValue: 9 }])
    expect(screen.getByText('数量 介于 1 ~ 9')).toBeInTheDocument()
  })

  it('commits_a_date_between_only_after_two_distinct_day_clicks', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '创建时间')
    // 日历网格内只含日期格子按钮：rdp 首次点击返回同日单点区间
    // {from: d, to: d}，不应落条；点出第二个不同日期才提交。
    const grid = screen.getByRole('grid')
    const days = within(grid).getAllByRole('button')
    await user2.click(days[10]!)
    expect(onConditions).not.toHaveBeenCalled()
    await user2.click(days[12]!)

    expect(onConditions).toHaveBeenCalledTimes(1)
    const condition = onConditions.mock.calls[0][0][0] as QueryCondition
    expect(condition.field).toBe('createdAt')
    expect(condition.op).toBe('between')
    // datetime 列服务端只收 RFC3339：下界取起点 00:00:00、上界取终点
    // 23:59:59，均带本地时区偏移。
    expect(String(condition.value)).toMatch(/^\d{4}-\d{2}-\d{2}T00:00:00[+-]\d{2}:\d{2}$/)
    expect(String(condition.rightValue)).toMatch(/^\d{4}-\d{2}-\d{2}T23:59:59[+-]\d{2}:\d{2}$/)
  })

  it('removes_a_chip_and_emits_the_remaining_conditions', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '用户名')
    await user2.type(screen.getByLabelText('筛选值'), 'ali')
    await user2.click(screen.getByRole('button', { name: '确定' }))
    await openField(user2, '状态')
    await user2.click(await screen.findByRole('option', { name: '禁用' }))

    await user2.click(screen.getByRole('button', { name: '移除条件：用户名 包含 ali' }))

    expect(onConditions).toHaveBeenLastCalledWith([{ field: 'status', op: 'eq', value: 0 }])
    expect(screen.queryByText('用户名 包含 ali')).not.toBeInTheDocument()
  })

  it('edits_an_existing_condition_in_place_when_its_chip_is_clicked', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '用户名')
    await user2.type(screen.getByLabelText('筛选值'), 'ali')
    await user2.click(screen.getByRole('button', { name: '确定' }))

    // 点 chip 回编辑：编辑器预填原值，改动后按下标替换而非追加。
    await user2.click(screen.getByRole('button', { name: '编辑条件：用户名 包含 ali' }))
    expect(screen.getByLabelText('筛选值')).toHaveValue('ali')
    await user2.type(screen.getByLabelText('筛选值'), 'bob')
    await user2.click(screen.getByRole('button', { name: '确定' }))

    expect(onConditions).toHaveBeenLastCalledWith([{ field: 'name', op: 'contains', value: 'alibob' }])
    expect(screen.getByText('用户名 包含 alibob')).toBeInTheDocument()
    expect(screen.queryByText('用户名 包含 ali')).not.toBeInTheDocument()
  })

  it('replaces_an_existing_condition_when_a_new_option_is_picked_in_edit_mode', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '状态')
    await user2.click(await screen.findByRole('option', { name: '禁用' }))

    await user2.click(screen.getByRole('button', { name: '编辑条件：状态 等于 禁用' }))
    // 编辑态回显当前值（原选项高亮），点新值即替换落条。
    expect(screen.getByRole('option', { name: '禁用' })).toHaveAttribute('aria-selected', 'true')
    await user2.click(screen.getByRole('option', { name: '启用' }))

    expect(onConditions).toHaveBeenLastCalledWith([{ field: 'status', op: 'eq', value: 1 }])
    expect(screen.getByText('状态 等于 启用')).toBeInTheDocument()
    expect(screen.queryByText('状态 等于 禁用')).not.toBeInTheDocument()
  })

  it('clears_all_conditions_at_once_from_the_box_entry', async () => {
    const user2 = userEvent.setup()
    const onConditions = vi.fn()
    render(<Harness onConditions={onConditions} />)

    await openField(user2, '用户名')
    await user2.type(screen.getByLabelText('筛选值'), 'ali')
    await user2.click(screen.getByRole('button', { name: '确定' }))
    await openField(user2, '状态')
    await user2.click(await screen.findByRole('option', { name: '禁用' }))

    await user2.click(screen.getByRole('button', { name: '清空筛选条件' }))

    expect(onConditions).toHaveBeenLastCalledWith([])
    expect(screen.getByText('点击添加筛选条件…')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '清空筛选条件' })).not.toBeInTheDocument()
  })

  it('reopens_into_the_field_step_after_closing_and_keeps_dropped_chips', async () => {
    const user2 = userEvent.setup()
    render(<Harness />)

    await openField(user2, '用户名')
    await user2.type(screen.getByLabelText('筛选值'), 'ali')
    await user2.click(screen.getByRole('button', { name: '确定' }))

    await user2.click(screen.getByRole('button', { name: '筛选条件' }))
    expect(await screen.findByText('选择字段')).toBeInTheDocument()
    expect(screen.getByText('用户名 包含 ali')).toBeInTheDocument()
  })
})
