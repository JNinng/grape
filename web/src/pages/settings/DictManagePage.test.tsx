import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DictDataDTO, DictTypeDTO } from '@/api/dicts'
import { getListSchema, type ListSchemaDTO } from '@/api/querySchema'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { SettingsLayout } from './SettingsLayout'
import { DictManagePage } from './DictManagePage'
import {
  createDictData,
  createDictType,
  deleteDictData,
  deleteDictType,
  listDictDatas,
  listDictTypes,
  updateDictData,
  updateDictType,
} from '@/api/dicts'
import { fetchMyPermissions } from '@/api/permissions'

vi.mock('@/api/dicts', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/dicts')>()),
  listDictTypes: vi.fn(),
  createDictType: vi.fn(),
  updateDictType: vi.fn(),
  deleteDictType: vi.fn(),
  listDictDatas: vi.fn(),
  createDictData: vi.fn(),
  updateDictData: vi.fn(),
  deleteDictData: vi.fn(),
}))

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

vi.mock('@/api/querySchema', () => ({
  getListSchema: vi.fn(),
}))

const mockListTypes = vi.mocked(listDictTypes)
const mockCreateType = vi.mocked(createDictType)
const mockUpdateType = vi.mocked(updateDictType)
const mockDeleteType = vi.mocked(deleteDictType)
const mockListDatas = vi.mocked(listDictDatas)
const mockCreateData = vi.mocked(createDictData)
const mockUpdateData = vi.mocked(updateDictData)
const mockDeleteData = vi.mocked(deleteDictData)
const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)
const mockGetListSchema = vi.mocked(getListSchema)

// typeSchema 基线对齐 GetListSchema(sys_dict_type) 响应口径：visibility 驱动
// 列显隐，默认排序与页大小驱动表格。
function typeSchema(): ListSchemaDTO {
  return {
    resource: 'sys_dict_type',
    hash: 'test-hash',
    fields: [
      { field: 'id', label: '字典类型 ID', dataType: 'integer', sortable: true, visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'status', label: '数据状态，0：禁用、1：启用', dataType: 'integer', sortable: true, visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'name', label: '字典类型名', dataType: 'string', sortable: true, visibility: 'optional', ops: ['contains', 'prefix', 'suffix', 'eq', 'neq'] },
      { field: 'code', label: '字典类型编码', dataType: 'string', visibility: 'optional', ops: ['eq', 'prefix'] },
      { field: 'remark', label: '备注', dataType: 'string', nullable: true, visibility: 'optional', ops: ['contains', 'prefix', 'suffix'] },
      { field: 'createdAt', label: '创建时间', dataType: 'datetime', sortable: true, visibility: 'optional', ops: ['eq', 'neq', 'in', 'not_in', 'gt', 'gte', 'lt', 'lte', 'between'] },
      { field: 'updatedAt', label: '更新时间', dataType: 'datetime', sortable: true, visibility: 'optional', ops: ['eq', 'neq', 'in', 'not_in', 'gt', 'gte', 'lt', 'lte', 'between'] },
    ],
    defaultSort: [{ field: 'updatedAt', desc: true }],
    pagination: { defaultPageSize: 20, maxPageSize: 400, modes: ['offset', 'cursor'], includeTotal: true },
  }
}

// dictType 构造类型 DTO 基线：可选字段缺省即未设置（protojson 口径）。
function dictType(overrides: Partial<DictTypeDTO> = {}): DictTypeDTO {
  return {
    id: '5',
    status: 1,
    name: '数据状态',
    code: 'sys_data_status',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T08:00:00Z',
    ...overrides,
  }
}

function secondType(): DictTypeDTO {
  return dictType({ id: '6', name: '角色类型', code: 'sys_role_type' })
}

// dictData 构造数据 DTO 基线。
function dictData(overrides: Partial<DictDataDTO> = {}): DictDataDTO {
  return {
    id: '9',
    status: 1,
    dictTypeId: '5',
    label: '开启',
    value: '1',
    valueType: 'integer',
    sortOrder: 1,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T08:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  clearToasts()
  // 权限码拉取失败：SettingsLayout 导航回落静态全量展示（显式 mock，
  // 不依赖 jsdom fetch 对相对地址抛错）。
  mockFetchMyPermissions.mockRejectedValue(new Error('permission denied'))
  mockGetListSchema.mockResolvedValue(typeSchema())
  mockListTypes.mockResolvedValue({
    items: [dictType(), secondType()],
    total: '2',
    hasMore: false,
    nextCursor: '',
    page: 1,
  })
})

afterEach(() => {
  cleanup()
  clearToasts()
  vi.resetAllMocks()
})

// renderPage 以真实布局挂载字典页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/dicts']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="dicts" element={<DictManagePage />} />
        </Route>
      </Routes>
      <Toaster />
    </MemoryRouter>,
  )
}

async function openRowActions(user2: ReturnType<typeof userEvent.setup>, name: string) {
  await user2.click(await screen.findByRole('button', { name: `操作 ${name}` }))
  const menu = await screen.findByRole('menu')
  return within(menu)
}

describe('DictManagePage list', () => {
  it('loads_and_renders_types_with_the_schema_default_sort_on_mount', async () => {
    renderPage()

    expect(await screen.findByText('数据状态')).toBeInTheDocument()
    expect(screen.getByText('角色类型')).toBeInTheDocument()
    expect(mockListTypes).toHaveBeenCalledTimes(1)
    expect(mockListTypes.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'updatedAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
  })

  it('surfaces_a_retryable_alert_when_the_schema_fails', async () => {
    mockGetListSchema.mockRejectedValue(new Error('boom'))
    const user2 = userEvent.setup()
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('查询能力加载失败：boom')
    expect(mockListTypes).not.toHaveBeenCalled()

    mockGetListSchema.mockResolvedValue(typeSchema())
    await user2.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('数据状态')).toBeInTheDocument()
  })
})

describe('DictManagePage create type', () => {
  it('creates_a_type_from_the_dialog_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockCreateType.mockResolvedValue(dictType({ id: '12', name: '性别', code: 'sys_gender' }))
    renderPage()
    await screen.findByText('数据状态')

    await user2.click(screen.getByRole('button', { name: '新建字典类型' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('类型编码'), 'sys_gender')
    await user2.type(within(dialog).getByLabelText('类型名'), '性别')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateType).toHaveBeenCalledWith({ code: 'sys_gender', name: '性别', status: 1 }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('字典类型 性别 已创建')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListTypes).toHaveBeenCalledTimes(2))
  })

  it('rejects_the_submission_inline_when_code_or_name_is_missing', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('数据状态')

    await user2.click(screen.getByRole('button', { name: '新建字典类型' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '创建' }))

    expect(screen.getByRole('alert')).toHaveTextContent('code and name are required')
    expect(mockCreateType).not.toHaveBeenCalled()
  })

  it('surfaces_the_conflict_reason_as_a_readable_hint_and_keeps_the_dialog_open', async () => {
    const user2 = userEvent.setup()
    mockCreateType.mockRejectedValue(new Error('dict type already exists'))
    renderPage()
    await screen.findByText('数据状态')

    await user2.click(screen.getByRole('button', { name: '新建字典类型' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('类型编码'), 'sys_data_status')
    await user2.type(within(dialog).getByLabelText('类型名'), '重复')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a dict type with the same code already exists in the tenant',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockListTypes).toHaveBeenCalledTimes(1)
  })
})

describe('DictManagePage edit type', () => {
  it('prefills_the_row_values_and_saves_name_remark_and_status_only', async () => {
    const user2 = userEvent.setup()
    mockUpdateType.mockResolvedValue(dictType({ name: '数据状态', remark: 'rotated' }))
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('类型名')).toHaveValue('数据状态')
    // 编码只读展示（身份列不可变）。
    expect(within(dialog).getByLabelText('类型编码')).toBeDisabled()

    await user2.type(within(dialog).getByLabelText('备注'), 'rotated')
    await user2.click(within(dialog).getByRole('switch', { name: '状态' }))
    expect(within(dialog).getByText('禁用')).toBeInTheDocument()
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    // 更新面不上送 code（后端忽略，前端不发送）。
    await waitFor(() =>
      expect(mockUpdateType).toHaveBeenCalledWith({
        id: '5',
        status: 0,
        name: '数据状态',
        remark: 'rotated',
      }),
    )
    await waitFor(() => expect(mockListTypes).toHaveBeenCalledTimes(2))
  })
})

describe('DictManagePage delete type', () => {
  it('asks_for_confirmation_and_only_deletes_on_confirm', async () => {
    const user2 = userEvent.setup()
    mockDeleteType.mockResolvedValue(undefined)
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除字典类型 数据状态？')
    expect(mockDeleteType).not.toHaveBeenCalled()

    await user2.click(within(confirmDialog).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteType).toHaveBeenCalledWith('5'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('字典类型 数据状态 已删除')
    await waitFor(() => expect(mockListTypes).toHaveBeenCalledTimes(2))
  })

  it('surfaces_the_has_data_rejection_toast_with_the_reason_hint', async () => {
    const user2 = userEvent.setup()
    mockDeleteType.mockRejectedValue(new Error('dict type has active data'))
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '删除失败：the dict type still has active data; clear its data first',
    )
  })
})

describe('DictManagePage data panel', () => {
  it('loads_host_data_with_the_type_filter_and_stable_sort_on_open', async () => {
    const user2 = userEvent.setup()
    mockListDatas.mockResolvedValue({
      items: [dictData(), dictData({ id: '10', label: '禁用', value: '0', sortOrder: 2 })],
      total: '2',
      hasMore: false,
      nextCursor: '',
      page: 1,
    })
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '数据维护' }))
    const dialog = screen.getByRole('dialog')

    expect(await within(dialog).findByText('开启')).toBeInTheDocument()
    expect(mockListDatas).toHaveBeenCalledWith({
      where: [{ field: 'dictTypeId', op: 'eq', value: '5' }],
      sort: [
        { field: 'sortOrder', order: 'asc' },
        { field: 'id', order: 'asc' },
      ],
      pagination: { page: 1, pageSize: 50 },
    })
    expect(within(dialog).getByText('禁用')).toBeInTheDocument()
  })

  it('adds_a_data_item_with_the_default_string_value_type_and_reloads', async () => {
    const user2 = userEvent.setup()
    mockListDatas.mockResolvedValue({ items: [], total: '0', hasMore: false, nextCursor: '', page: 1 })
    mockCreateData.mockResolvedValue(dictData({ id: '11', label: '新增项', value: 'new', valueType: 'string' }))
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '数据维护' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('暂无数据项')

    await user2.type(within(dialog).getByLabelText('标签'), '新增项')
    await user2.type(within(dialog).getByLabelText('字典值'), 'new')
    await user2.type(within(dialog).getByLabelText('排序号'), '3')
    await user2.click(within(dialog).getByRole('button', { name: '添加数据' }))

    await waitFor(() =>
      expect(mockCreateData).toHaveBeenCalledWith({
        dictTypeId: '5',
        label: '新增项',
        value: 'new',
        valueType: 'string',
        sortOrder: 3,
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('字典数据 新增项 已添加')
    await waitFor(() => expect(mockListDatas).toHaveBeenCalledTimes(2))
  })

  it('rejects_blank_label_or_value_inline_without_calling_the_api', async () => {
    const user2 = userEvent.setup()
    mockListDatas.mockResolvedValue({ items: [], total: '0', hasMore: false, nextCursor: '', page: 1 })
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '数据维护' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('暂无数据项')

    await user2.click(within(dialog).getByRole('button', { name: '添加数据' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('label and value are required')
    expect(mockCreateData).not.toHaveBeenCalled()
  })

  it('rejects_a_negative_sort_order_inline_without_calling_the_api', async () => {
    const user2 = userEvent.setup()
    mockListDatas.mockResolvedValue({ items: [], total: '0', hasMore: false, nextCursor: '', page: 1 })
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '数据维护' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('暂无数据项')

    await user2.type(within(dialog).getByLabelText('标签'), 'l')
    await user2.type(within(dialog).getByLabelText('字典值'), 'v')
    await user2.type(within(dialog).getByLabelText('排序号'), '-1')
    await user2.click(within(dialog).getByRole('button', { name: '添加数据' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('sort order must be a non-negative integer')
    expect(mockCreateData).not.toHaveBeenCalled()
  })

  it('surfaces_the_duplicate_value_rejection_inline_and_keeps_the_panel_open', async () => {
    const user2 = userEvent.setup()
    mockListDatas.mockResolvedValue({ items: [dictData()], total: '1', hasMore: false, nextCursor: '', page: 1 })
    mockCreateData.mockRejectedValue(new Error('dict data already exists'))
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '数据维护' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('开启')

    await user2.type(within(dialog).getByLabelText('标签'), '重复')
    await user2.type(within(dialog).getByLabelText('字典值'), '1')
    await user2.click(within(dialog).getByRole('button', { name: '添加数据' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a dict data with the same value already exists under this type',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('edits_a_data_row_inline_and_saves_label_sort_order_and_status_only', async () => {
    const user2 = userEvent.setup()
    mockListDatas.mockResolvedValue({ items: [dictData()], total: '1', hasMore: false, nextCursor: '', page: 1 })
    mockUpdateData.mockResolvedValue(dictData({ label: '改标签', sortOrder: 9 }))
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '数据维护' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('开启')

    await user2.click(within(dialog).getByRole('button', { name: '编辑 开启' }))
    const form = within(dialog).getByRole('form', { name: '编辑 开启' })
    await user2.clear(within(form).getByLabelText('标签'))
    await user2.type(within(form).getByLabelText('标签'), '改标签')
    await user2.clear(within(form).getByLabelText('排序号'))
    await user2.type(within(form).getByLabelText('排序号'), '9')
    await user2.click(within(form).getByRole('button', { name: '保存' }))

    // 值语义列不上送：value/valueType 与宿主由后端忽略，前端不发送。
    await waitFor(() =>
      expect(mockUpdateData).toHaveBeenCalledWith({ id: '9', status: 1, label: '改标签', sortOrder: 9 }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('字典数据 改标签 已更新')
    await waitFor(() => expect(mockListDatas).toHaveBeenCalledTimes(2))
  })

  it('removes_a_data_item_and_reloads_the_current_page', async () => {
    const user2 = userEvent.setup()
    mockListDatas.mockResolvedValue({ items: [dictData()], total: '1', hasMore: false, nextCursor: '', page: 1 })
    mockDeleteData.mockResolvedValue(undefined)
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '数据维护' }))
    const dialog = screen.getByRole('dialog')
    await within(dialog).findByText('开启')

    await user2.click(within(dialog).getByRole('button', { name: '删除 开启' }))

    await waitFor(() => expect(mockDeleteData).toHaveBeenCalledWith('9'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('字典数据 开启 已删除')
    await waitFor(() => expect(mockListDatas).toHaveBeenCalledTimes(2))
  })

  it('shows_a_retryable_error_when_the_data_list_fails_to_load', async () => {
    const user2 = userEvent.setup()
    mockListDatas.mockRejectedValueOnce(new Error('invalid dict type argument'))
    mockListDatas.mockResolvedValueOnce({ items: [dictData()], total: '1', hasMore: false, nextCursor: '', page: 1 })
    renderPage()
    const menu = await openRowActions(user2, '数据状态')

    await user2.click(menu.getByRole('menuitem', { name: '数据维护' }))
    const dialog = screen.getByRole('dialog')
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('数据列表加载失败：invalid dict type argument')

    await user2.click(within(dialog).getByRole('button', { name: '重试' }))
    expect(await within(dialog).findByText('开启')).toBeInTheDocument()
  })
})
