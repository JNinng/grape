import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { TenantDTO } from '@/api/tenants'
import {
  createTenant,
  deleteTenant,
  disableTenant,
  listTenants,
  restoreTenant,
  updateTenant,
} from '@/api/tenants'
import { getListSchema, type ListSchemaDTO } from '@/api/querySchema'
import { ApiError } from '@/api/toApiError'
import { clearSession, setSession } from '@/auth/store'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { fetchMyPermissions } from '@/api/permissions'
import { SettingsLayout } from './SettingsLayout'
import { TenantManagePage } from './TenantManagePage'

vi.mock('@/api/tenants', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/tenants')>()),
  listTenants: vi.fn(),
  createTenant: vi.fn(),
  updateTenant: vi.fn(),
  deleteTenant: vi.fn(),
  disableTenant: vi.fn(),
  restoreTenant: vi.fn(),
}))

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

vi.mock('@/api/querySchema', () => ({
  getListSchema: vi.fn(),
}))

const mockListTenants = vi.mocked(listTenants)
const mockCreateTenant = vi.mocked(createTenant)
const mockUpdateTenant = vi.mocked(updateTenant)
const mockDeleteTenant = vi.mocked(deleteTenant)
const mockDisableTenant = vi.mocked(disableTenant)
const mockRestoreTenant = vi.mocked(restoreTenant)
const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)
const mockGetListSchema = vi.mocked(getListSchema)

// tenantSchema 基线对齐 GetListSchema(sys_tenant) 响应口径：hideable 驱动
// 列显隐，sortable 门控表头排序，默认排序与页大小驱动表格。
function tenantSchema(): ListSchemaDTO {
  return {
    resource: 'sys_tenant',
    hash: 'test-hash',
    fields: [
      { field: 'id', label: '租户 ID', dataType: 'string', sortable: true, hideable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'status', label: '数据状态，0：停用、1：启用', dataType: 'integer', sortable: true, hideable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'code', label: '租户编码', dataType: 'string', hideable: true, selectable: true, ops: ['eq', 'prefix'] },
      { field: 'name', label: '租户名', dataType: 'string', sortable: true, hideable: true, selectable: true, ops: ['contains', 'prefix', 'suffix', 'eq', 'neq'] },
      { field: 'parentId', label: '父级租户 ID', dataType: 'string', selectable: true, ops: ['eq', 'in'] },
      { field: 'contactName', label: '联系人', dataType: 'string', nullable: true, hideable: true, selectable: true, ops: ['contains'] },
      { field: 'contactPhone', label: '联系电话', dataType: 'string', nullable: true, hideable: true, selectable: true, ops: ['eq', 'prefix'] },
      { field: 'expiredAt', label: '到期时间', dataType: 'datetime', sortable: true, selectable: true, ops: ['between'] },
      { field: 'maxUserCount', label: '最大用户数', dataType: 'integer', sortable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'defaultLocale', label: '租户默认语言', dataType: 'string', hideable: true, selectable: true, ops: ['eq'] },
      { field: 'createdAt', label: '创建时间', dataType: 'datetime', sortable: true, hideable: true, selectable: true, ops: ['between'] },
      { field: 'updatedAt', label: '更新时间', dataType: 'datetime', sortable: true, hideable: true, selectable: true, ops: ['between'] },
      { field: 'tenantId', label: '租户 ID', dataType: 'string', hideable: true, selectable: true, ops: ['eq'] },
    ],
    defaultSort: [{ field: 'updatedAt', desc: true }],
    pagination: { defaultPageSize: 20, maxPageSize: 400, modes: ['offset', 'cursor'], includeTotal: true },
  }
}

// tenant 构造租户 DTO 基线：可选字段缺省即未设置（protojson 口径）。
function tenant(overrides: Partial<TenantDTO> = {}): TenantDTO {
  return {
    id: 't-2',
    status: 1,
    code: 'demo',
    name: '演示租户',
    parentId: 't-1',
    contactName: '张三',
    contactPhone: '13800000000',
    expiredAt: '2026-12-31T00:00:00Z',
    maxUserCount: 100,
    defaultLocale: 'en-US',
    tenantId: 't-1',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T08:00:00Z',
    updatedAt: '2026-01-02T08:00:00Z',
    ...overrides,
  }
}

// selfTenant 是当前登录租户（getUser().tenantId = t-1）：根行无父级、
// 无到期时间与最大用户数，用于空值占位与自身动作禁用断言。
function selfTenant(overrides: Partial<TenantDTO> = {}): TenantDTO {
  return tenant({
    id: 't-1',
    code: 'default',
    name: '默认租户',
    parentId: undefined,
    contactName: undefined,
    contactPhone: undefined,
    expiredAt: undefined,
    maxUserCount: undefined,
    defaultLocale: 'zh-CN',
    ...overrides,
  })
}

// deadTenant 是已停用的子租户：行内动作呈「恢复」。
function deadTenant(): TenantDTO {
  return tenant({
    id: 't-3',
    code: 'archive',
    name: '归档租户',
    status: 0,
    defaultLocale: 'zh-CN',
    maxUserCount: undefined,
    expiredAt: undefined,
  })
}

function page(items: TenantDTO[], overrides: Record<string, unknown> = {}) {
  return {
    items,
    total: String(items.length),
    hasMore: false,
    nextCursor: '',
    page: 1,
    ...overrides,
  }
}

beforeEach(() => {
  clearToasts()
  setSession({
    tokenType: 'Bearer',
    accessToken: 'access-1',
    refreshToken: 'refresh-1',
    expiresAt: '2026-01-01T00:00:00Z',
    user: { id: 7, tenantId: 't-1', name: 'alice', nickname: 'Alice' },
  })
  // 权限码拉取失败：SettingsLayout 导航回落静态全量展示（显式 mock，
  // 不依赖 jsdom fetch 对相对地址抛错）。
  mockFetchMyPermissions.mockRejectedValue(new Error('permission denied'))
  mockGetListSchema.mockResolvedValue(tenantSchema())
  mockListTenants.mockResolvedValue(page([selfTenant(), tenant(), deadTenant()]))
})

afterEach(() => {
  cleanup()
  clearToasts()
  clearSession()
  vi.resetAllMocks()
})

// renderPage 以真实布局挂载租户页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/tenants']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="tenants" element={<TenantManagePage />} />
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

describe('TenantManagePage list', () => {
  it('loads_tenants_with_the_schema_default_sort_and_renders_badges_and_placeholders', async () => {
    renderPage()

    expect(await screen.findByText('演示租户')).toBeInTheDocument()
    expect(screen.getByText('默认租户')).toBeInTheDocument()
    expect(screen.getByText('归档租户')).toBeInTheDocument()
    expect(screen.getByText('en-US')).toBeInTheDocument()
    expect(screen.getByText('100')).toBeInTheDocument()
    expect(screen.getByText('2026-12-31')).toBeInTheDocument()
    // 状态徽标：两个启用 + 一个停用。
    expect(screen.getAllByText('启用')).toHaveLength(2)
    expect(screen.getAllByText('停用')).toHaveLength(1)
    expect(mockListTenants).toHaveBeenCalledTimes(1)
    expect(mockListTenants.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'updatedAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
    expect(mockGetListSchema).toHaveBeenCalledWith('sys_tenant')
  })

  it('applies_a_filter_condition_to_the_dynamic_query_where', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '租户筛选' }))
    const popover = await screen.findByRole('dialog')
    await user2.click(within(popover).getByRole('button', { name: '租户编码' }))
    await user2.type(screen.getByLabelText('筛选值'), 'demo')
    await user2.click(screen.getByRole('button', { name: '确定' }))

    await waitFor(() => expect(mockListTenants).toHaveBeenCalledTimes(2))
    expect(mockListTenants.mock.calls[1][0]).toMatchObject({
      where: [{ field: 'code', op: 'prefix', value: 'demo' }],
      pagination: { page: 1 },
    })
  })

  it('applies_a_header_sort_to_the_dynamic_query_sort', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '租户名' }))

    await waitFor(() => expect(mockListTenants).toHaveBeenCalledTimes(2))
    expect(mockListTenants.mock.calls[1][0]).toMatchObject({
      sort: [{ field: 'name', order: 'asc' }],
    })
  })

  it('sorts_by_the_max_user_count_supplemental_column', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '最大用户数' }))

    await waitFor(() => expect(mockListTenants).toHaveBeenCalledTimes(2))
    expect(mockListTenants.mock.calls[1][0]).toMatchObject({
      sort: [{ field: 'maxUserCount', order: 'asc' }],
    })
  })

  it('sorts_by_the_expiry_date_supplemental_column', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '到期时间' }))

    await waitFor(() => expect(mockListTenants).toHaveBeenCalledTimes(2))
    expect(mockListTenants.mock.calls[1][0]).toMatchObject({
      sort: [{ field: 'expiredAt', order: 'asc' }],
    })
  })

  it('requests_the_next_page_with_the_updated_pagination', async () => {
    const user2 = userEvent.setup()
    mockListTenants.mockResolvedValue(page([tenant()], { total: '40', hasMore: true }))
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '下一页' }))

    await waitFor(() => expect(mockListTenants).toHaveBeenCalledTimes(2))
    expect(mockListTenants.mock.calls[1][0]).toMatchObject({
      pagination: { page: 2, pageSize: 20 },
    })
  })
})

describe('TenantManagePage create', () => {
  it('creates_a_tenant_with_the_form_values_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockCreateTenant.mockResolvedValue(tenant({ id: 't-9', code: 'demo2', name: '新租户' }))
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '新建租户' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('租户编码'), 'demo2')
    await user2.type(within(dialog).getByLabelText('租户名'), '新租户')
    await user2.type(within(dialog).getByLabelText('联系人'), '李四')
    await user2.type(within(dialog).getByLabelText('联系电话'), '13900000000')
    fireEvent.change(within(dialog).getByLabelText('到期时间'), { target: { value: '2026-06-30' } })
    await user2.type(within(dialog).getByLabelText('最大用户数'), '50')
    await user2.clear(within(dialog).getByLabelText('默认语言'))
    await user2.type(within(dialog).getByLabelText('默认语言'), 'ja-JP')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateTenant).toHaveBeenCalledWith({
        status: 1,
        code: 'demo2',
        name: '新租户',
        contactName: '李四',
        contactPhone: '13900000000',
        expiredAt: '2026-06-30T00:00:00Z',
        maxUserCount: 50,
        defaultLocale: 'ja-JP',
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('租户 新租户 已创建')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListTenants).toHaveBeenCalledTimes(2))
  })

  it('rejects_blank_code_and_name_inline_without_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '新建租户' }))
    const dialog = screen.getByRole('dialog')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('code and name are required')
    expect(mockCreateTenant).not.toHaveBeenCalled()
  })

  it.each(['abc', '0', '-2', '2147483648'])(
    'rejects_the_invalid_max_user_count_%s_before_calling_the_api',
    async (value) => {
      const user2 = userEvent.setup()
      renderPage()
      await screen.findByText('演示租户')

      await user2.click(screen.getByRole('button', { name: '新建租户' }))
      const dialog = screen.getByRole('dialog')
      await user2.type(within(dialog).getByLabelText('租户编码'), 'demo3')
      await user2.type(within(dialog).getByLabelText('租户名'), '新租户')
      await user2.type(within(dialog).getByLabelText('最大用户数'), value)
      await user2.click(within(dialog).getByRole('button', { name: '创建' }))

      expect(within(dialog).getByRole('alert')).toHaveTextContent(
        'max user count must be a positive integer',
      )
      expect(mockCreateTenant).not.toHaveBeenCalled()
    },
  )

  it('rejects_an_invalid_locale_before_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '新建租户' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('租户编码'), 'demo3')
    await user2.type(within(dialog).getByLabelText('租户名'), '新租户')
    await user2.clear(within(dialog).getByLabelText('默认语言'))
    await user2.type(within(dialog).getByLabelText('默认语言'), 'zh_CN')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('locale must be a BCP-47 tag such as zh-CN')
    expect(mockCreateTenant).not.toHaveBeenCalled()
  })

  it('surfaces_the_duplicate_tenant_reason_inline_and_keeps_the_dialog_open', async () => {
    const user2 = userEvent.setup()
    mockCreateTenant.mockRejectedValue(new ApiError('tenant already exists', 'TENANT_ALREADY_EXISTS'))
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '新建租户' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('租户编码'), 'demo')
    await user2.type(within(dialog).getByLabelText('租户名'), '重复租户')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a tenant with the same code or name already exists',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockListTenants).toHaveBeenCalledTimes(1)
  })

  it('prefers_the_specific_caller_missing_message_over_the_generic_invalid_reason', async () => {
    const user2 = userEvent.setup()
    mockCreateTenant.mockRejectedValue(
      new ApiError('caller tenant not found or deleted', 'TENANT_INVALID_ARGUMENT'),
    )
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '新建租户' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('租户编码'), 'demo3')
    await user2.type(within(dialog).getByLabelText('租户名'), '新租户')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'the caller tenant is missing or deleted; sign in again and retry',
    )
    expect(mockCreateTenant).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['code', '租户编码', 'a'.repeat(33), 'code must be at most 32 characters'],
    ['name', '租户名', 'a'.repeat(257), 'name must be at most 256 characters'],
    ['contactName', '联系人', 'a'.repeat(65), 'contact name must be at most 64 characters'],
    ['contactPhone', '联系电话', '1'.repeat(33), 'contact phone must be at most 32 characters'],
    ['locale', '默认语言', 'abcdefghijk', 'locale must be a BCP-47 tag such as zh-CN'],
  ])('rejects_the_%s_column_width_boundary_before_calling_the_api', async (field, label, value, message) => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('演示租户')

    await user2.click(screen.getByRole('button', { name: '新建租户' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('租户编码'), field === 'code' ? value : 'demo3')
    await user2.type(within(dialog).getByLabelText('租户名'), field === 'name' ? value : '新租户')
    if (field !== 'code' && field !== 'name') {
      if (field === 'locale') {
        await user2.clear(within(dialog).getByLabelText(label))
      }
      await user2.type(within(dialog).getByLabelText(label), value)
    }
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent(message)
    expect(mockCreateTenant).not.toHaveBeenCalled()
  })
})

describe('TenantManagePage edit', () => {
  it('keeps_the_code_readonly_and_updates_only_the_mutable_fields', async () => {
    const user2 = userEvent.setup()
    mockUpdateTenant.mockResolvedValue(tenant({ name: '演示租户改' }))
    renderPage()
    await screen.findByText('演示租户')
    const menu = await openRowActions(user2, '演示租户')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('租户编码')).toBeDisabled()
    expect(within(dialog).getByLabelText('租户编码')).toHaveValue('demo')
    expect(within(dialog).getByLabelText('到期时间')).toHaveValue('2026-12-31')
    expect(within(dialog).getByLabelText('最大用户数')).toHaveValue('100')

    await user2.clear(within(dialog).getByLabelText('租户名'))
    await user2.type(within(dialog).getByLabelText('租户名'), '演示租户改')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(mockUpdateTenant).toHaveBeenCalledWith({
        id: 't-2',
        status: 1,
        name: '演示租户改',
        contactName: '张三',
        contactPhone: '13800000000',
        expiredAt: '2026-12-31T00:00:00Z',
        maxUserCount: 100,
        defaultLocale: 'en-US',
      }),
    )
    const payload = mockUpdateTenant.mock.calls[0][0]
    // 身份/结构/归属列不上送（后端也会忽略，前端不依赖该兜底）。
    expect(payload).not.toHaveProperty('code')
    expect(payload).not.toHaveProperty('parentId')
    expect(payload).not.toHaveProperty('tenantId')
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('租户 演示租户改 已更新')
    await waitFor(() => expect(mockListTenants).toHaveBeenCalledTimes(2))
  })

  it('keeps_the_current_tenant_status_readonly_and_submits_the_same_value', async () => {
    const user2 = userEvent.setup()
    mockUpdateTenant.mockResolvedValue(selfTenant())
    renderPage()
    await screen.findByText('默认租户')
    const menu = await openRowActions(user2, '默认租户')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('switch', { name: '状态' })).toBeDisabled()
    expect(within(dialog).getByText('当前登录租户不能停用/恢复自身；状态保持不变。')).toBeInTheDocument()

    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(mockUpdateTenant).toHaveBeenCalledWith({
        id: 't-1',
        status: 1,
        name: '默认租户',
        contactName: undefined,
        contactPhone: undefined,
        expiredAt: undefined,
        maxUserCount: undefined,
        defaultLocale: 'zh-CN',
      }),
    )
  })

  it('clears_expiry_and_max_user_count_by_omitting_them_from_the_update_payload', async () => {
    const user2 = userEvent.setup()
    mockUpdateTenant.mockResolvedValue(tenant({ expiredAt: undefined, maxUserCount: undefined }))
    renderPage()
    await screen.findByText('演示租户')
    const menu = await openRowActions(user2, '演示租户')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('到期时间'), { target: { value: '' } })
    await user2.clear(within(dialog).getByLabelText('最大用户数'))
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() => expect(mockUpdateTenant).toHaveBeenCalledTimes(1))
    const payload = mockUpdateTenant.mock.calls[0][0]
    expect(payload.expiredAt).toBeUndefined()
    expect(payload.maxUserCount).toBeUndefined()
    // undefined 键经 JSON.stringify 即从请求体省略（protojson 覆盖语义）。
    expect(JSON.stringify(payload)).not.toContain('expiredAt')
    expect(JSON.stringify(payload)).not.toContain('maxUserCount')
  })

  it('keeps_the_edit_dialog_open_and_shows_an_inline_error_when_the_backend_rejects', async () => {
    const user2 = userEvent.setup()
    mockUpdateTenant.mockRejectedValue(
      new ApiError('tenant already exists', 'TENANT_ALREADY_EXISTS'),
    )
    renderPage()
    await screen.findByText('演示租户')
    const menu = await openRowActions(user2, '演示租户')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    await user2.clear(within(dialog).getByLabelText('租户名'))
    await user2.type(within(dialog).getByLabelText('租户名'), '重名租户')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a tenant with the same code or name already exists',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockListTenants).toHaveBeenCalledTimes(1)
  })

  it('prefers_the_specific_own_status_message_over_the_generic_invalid_reason', async () => {
    const user2 = userEvent.setup()
    mockUpdateTenant.mockRejectedValue(
      new ApiError('cannot change own tenant status; use disable or restore', 'TENANT_INVALID_ARGUMENT'),
    )
    renderPage()
    await screen.findByText('演示租户')
    const menu = await openRowActions(user2, '演示租户')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'the current tenant status cannot be changed here; use disable or restore',
    )
  })

  it('rejects_an_invalid_expiry_date_before_calling_the_api', async () => {
    const user2 = userEvent.setup()
    // 库中不应出现非法日期；构造畸形 DTO 使编辑表单持有非法值，验证提交前
    // 的日期合法性校验（date 控件本身会拒绝直接输入非法 YYYY-MM-DD）。
    mockListTenants.mockResolvedValue(page([tenant({ expiredAt: '2026-02-30T00:00:00Z' })]))
    renderPage()
    await screen.findByText('演示租户')
    const menu = await openRowActions(user2, '演示租户')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('expiry date must be a valid date')
    expect(mockUpdateTenant).not.toHaveBeenCalled()
  })
})

describe('TenantManagePage status actions', () => {
  it('disables_an_active_tenant_after_confirmation_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockDisableTenant.mockResolvedValue(tenant({ status: 0 }))
    renderPage()
    await screen.findByText('演示租户')
    const menu = await openRowActions(user2, '演示租户')

    await user2.click(menu.getByRole('menuitem', { name: '停用' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认停用租户 演示租户？')
    expect(mockDisableTenant).not.toHaveBeenCalled()

    await user2.click(within(confirmDialog).getByRole('button', { name: '停用' }))

    await waitFor(() => expect(mockDisableTenant).toHaveBeenCalledWith('t-2'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('租户 演示租户 已停用')
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListTenants).toHaveBeenCalledTimes(2))
  })

  it('restores_a_disabled_tenant_after_confirmation_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockRestoreTenant.mockResolvedValue(deadTenant())
    renderPage()
    await screen.findByText('归档租户')
    const menu = await openRowActions(user2, '归档租户')

    await user2.click(menu.getByRole('menuitem', { name: '恢复' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认恢复租户 归档租户？')

    await user2.click(within(confirmDialog).getByRole('button', { name: '恢复' }))

    await waitFor(() => expect(mockRestoreTenant).toHaveBeenCalledWith('t-3'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('租户 归档租户 已恢复')
    await waitFor(() => expect(mockListTenants).toHaveBeenCalledTimes(2))
  })

  it('states_the_factual_disable_effect_without_promising_access_blocking', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('演示租户')
    const menu = await openRowActions(user2, '演示租户')

    await user2.click(menu.getByRole('menuitem', { name: '停用' }))
    expect(screen.getByRole('alertdialog')).toHaveTextContent(
      '停用仅把数据状态置为停用；当前版本不会阻断该租户用户的登录与访问，可随时恢复。',
    )
  })

  it('keeps_the_disable_dialog_open_with_an_inline_error_when_the_backend_rejects', async () => {
    const user2 = userEvent.setup()
    mockDisableTenant.mockRejectedValue(new ApiError('tenant not found', 'TENANT_NOT_FOUND'))
    renderPage()
    await screen.findByText('演示租户')
    const menu = await openRowActions(user2, '演示租户')

    await user2.click(menu.getByRole('menuitem', { name: '停用' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '停用' }))

    const confirmDialog = screen.getByRole('alertdialog')
    expect(within(confirmDialog).getByRole('alert')).toHaveTextContent(
      '停用失败：tenant not found; it may have been deleted or be outside the current tenant tree',
    )
    expect(confirmDialog).toBeInTheDocument()
    expect(mockListTenants).toHaveBeenCalledTimes(1)
  })

  it('keeps_the_restore_dialog_open_with_an_inline_error_when_the_backend_rejects', async () => {
    const user2 = userEvent.setup()
    mockRestoreTenant.mockRejectedValue(new ApiError('tenant not found', 'TENANT_NOT_FOUND'))
    renderPage()
    await screen.findByText('归档租户')
    const menu = await openRowActions(user2, '归档租户')

    await user2.click(menu.getByRole('menuitem', { name: '恢复' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '恢复' }))

    const confirmDialog = screen.getByRole('alertdialog')
    expect(within(confirmDialog).getByRole('alert')).toHaveTextContent(
      '恢复失败：tenant not found; it may have been deleted or be outside the current tenant tree',
    )
    expect(confirmDialog).toBeInTheDocument()
    expect(mockListTenants).toHaveBeenCalledTimes(1)
  })

  it('shows_the_current_tenant_guard_note_and_disables_disable_and_delete_with_visible_labels', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('默认租户')
    expect(screen.getByText(/当前登录租户不可停用、恢复或删除自身/)).toBeInTheDocument()

    const menu = await openRowActions(user2, '默认租户')
    expect(menu.getByRole('menuitem', { name: '编辑' })).not.toHaveAttribute('aria-disabled')
    const disableItem = menu.getByRole('menuitem', { name: '停用（当前登录租户）' })
    expect(disableItem).toHaveAttribute('aria-disabled', 'true')
    const deleteItem = menu.getByRole('menuitem', { name: '删除（当前登录租户）' })
    expect(deleteItem).toHaveAttribute('aria-disabled', 'true')

    // 禁用项是行为禁用：点击不打开确认弹窗、不调用 API。
    await user2.click(disableItem)
    await user2.click(deleteItem)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(mockDisableTenant).not.toHaveBeenCalled()
    expect(mockDeleteTenant).not.toHaveBeenCalled()
  })

  it('disables_restore_for_the_current_tenant_with_a_visible_label_and_no_action', async () => {
    const user2 = userEvent.setup()
    mockListTenants.mockResolvedValue(page([selfTenant({ status: 0 })]))
    renderPage()
    await screen.findByText('默认租户')
    const menu = await openRowActions(user2, '默认租户')

    const restoreItem = menu.getByRole('menuitem', { name: '恢复（当前登录租户）' })
    expect(restoreItem).toHaveAttribute('aria-disabled', 'true')
    await user2.click(restoreItem)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(mockRestoreTenant).not.toHaveBeenCalled()
  })
})

describe('TenantManagePage delete', () => {
  it('deletes_a_tenant_after_confirmation_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockDeleteTenant.mockResolvedValue(undefined)
    renderPage()
    await screen.findByText('演示租户')
    const menu = await openRowActions(user2, '演示租户')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除租户 演示租户？')
    expect(mockDeleteTenant).not.toHaveBeenCalled()

    await user2.click(within(confirmDialog).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteTenant).toHaveBeenCalledWith('t-2'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('租户 演示租户 已删除')
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListTenants).toHaveBeenCalledTimes(2))
  })

  it('keeps_the_delete_dialog_open_and_hints_to_delete_children_first', async () => {
    const user2 = userEvent.setup()
    mockDeleteTenant.mockRejectedValue(new ApiError('tenant has active children', 'TENANT_HAS_CHILDREN'))
    renderPage()
    await screen.findByText('演示租户')
    const menu = await openRowActions(user2, '演示租户')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    const confirmDialog = screen.getByRole('alertdialog')
    expect(within(confirmDialog).getByRole('alert')).toHaveTextContent(
      '删除失败：the tenant still has active child tenants; delete them first',
    )
    expect(confirmDialog).toBeInTheDocument()
    expect(mockListTenants).toHaveBeenCalledTimes(1)
  })
})

describe('TenantManagePage load states', () => {
  it('surfaces_a_retryable_list_error_and_recovers', async () => {
    const user2 = userEvent.setup()
    mockListTenants.mockRejectedValueOnce(new Error('boom'))
    mockListTenants.mockResolvedValueOnce(page([tenant()]))
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('列表加载失败：boom')
    await user2.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('演示租户')).toBeInTheDocument()
  })

  it('surfaces_a_retryable_schema_error_and_recovers', async () => {
    const user2 = userEvent.setup()
    mockGetListSchema.mockRejectedValueOnce(new Error('schema down'))
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('查询能力加载失败：schema down')
    expect(mockListTenants).not.toHaveBeenCalled()

    await user2.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('演示租户')).toBeInTheDocument()
  })

  it('renders_the_empty_state_when_no_tenant_matches', async () => {
    mockListTenants.mockResolvedValue(page([]))
    renderPage()

    expect(await screen.findByText('没有符合条件的租户')).toBeInTheDocument()
  })
})
