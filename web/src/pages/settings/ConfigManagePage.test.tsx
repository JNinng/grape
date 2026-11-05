import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ConfigDTO, FeatureFlagDTO, TenantConfigDTO, TranslationDTO } from '@/api/configs'
import {
  createConfig,
  createFeatureFlag,
  createTenantConfig,
  createTranslation,
  deleteConfig,
  deleteFeatureFlag,
  deleteTenantConfig,
  deleteTranslation,
  listConfigs,
  listFeatureFlags,
  listTenantConfigs,
  listTranslations,
  updateConfig,
  updateFeatureFlag,
  updateTenantConfig,
  updateTranslation,
} from '@/api/configs'
import { getListSchema, type ListSchemaDTO } from '@/api/querySchema'
import { ApiError } from '@/api/toApiError'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { SettingsLayout } from './SettingsLayout'
import { ConfigManagePage } from './ConfigManagePage'
import { fetchMyPermissions } from '@/api/permissions'

vi.mock('@/api/configs', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/configs')>()),
  listConfigs: vi.fn(),
  createConfig: vi.fn(),
  updateConfig: vi.fn(),
  deleteConfig: vi.fn(),
  listTenantConfigs: vi.fn(),
  createTenantConfig: vi.fn(),
  updateTenantConfig: vi.fn(),
  deleteTenantConfig: vi.fn(),
  listFeatureFlags: vi.fn(),
  createFeatureFlag: vi.fn(),
  updateFeatureFlag: vi.fn(),
  deleteFeatureFlag: vi.fn(),
  listTranslations: vi.fn(),
  createTranslation: vi.fn(),
  updateTranslation: vi.fn(),
  deleteTranslation: vi.fn(),
}))

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

vi.mock('@/api/querySchema', () => ({
  getListSchema: vi.fn(),
}))

const mockListConfigs = vi.mocked(listConfigs)
const mockCreateConfig = vi.mocked(createConfig)
const mockUpdateConfig = vi.mocked(updateConfig)
const mockDeleteConfig = vi.mocked(deleteConfig)
const mockListTenantConfigs = vi.mocked(listTenantConfigs)
const mockCreateTenantConfig = vi.mocked(createTenantConfig)
const mockUpdateTenantConfig = vi.mocked(updateTenantConfig)
const mockDeleteTenantConfig = vi.mocked(deleteTenantConfig)
const mockListFeatureFlags = vi.mocked(listFeatureFlags)
const mockCreateFeatureFlag = vi.mocked(createFeatureFlag)
const mockUpdateFeatureFlag = vi.mocked(updateFeatureFlag)
const mockDeleteFeatureFlag = vi.mocked(deleteFeatureFlag)
const mockListTranslations = vi.mocked(listTranslations)
const mockCreateTranslation = vi.mocked(createTranslation)
const mockUpdateTranslation = vi.mocked(updateTranslation)
const mockDeleteTranslation = vi.mocked(deleteTranslation)
const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)
const mockGetListSchema = vi.mocked(getListSchema)

// configFields 对齐 sys_config 的 GetListSchema 口径：value 未声明 @hideable，
// 不在 schema 的可显隐字段集内（页面自行补齐该列）。
const CONFIG_FIELDS: ListSchemaDTO['fields'] = [
  { field: 'id', label: '配置 ID', dataType: 'integer', sortable: true, hideable: true, selectable: true, ops: ['eq', 'neq', 'in', 'not_in'] },
  { field: 'status', label: '数据状态，0：禁用、1：启用', dataType: 'integer', sortable: true, hideable: true, selectable: true, ops: ['eq', 'neq', 'in', 'not_in'] },
  { field: 'name', label: '配置名称', dataType: 'string', sortable: true, hideable: true, selectable: true, ops: ['contains', 'prefix', 'suffix', 'eq', 'neq'] },
  { field: 'configKey', label: '配置键（唯一），如 smtp.host', dataType: 'string', hideable: true, selectable: true, ops: ['eq', 'neq', 'prefix'] },
  { field: 'valueType', label: '值类型：string/integer/boolean/float/json', dataType: 'string', hideable: true, selectable: true, ops: ['eq', 'neq'] },
  { field: 'remark', label: '备注', dataType: 'string', nullable: true, hideable: true, selectable: true, ops: ['contains', 'prefix', 'suffix'] },
  { field: 'createdAt', label: '创建时间', dataType: 'datetime', sortable: true, hideable: true, selectable: true, ops: ['eq', 'neq', 'in', 'not_in', 'gt', 'gte', 'lt', 'lte', 'between'] },
  { field: 'updatedAt', label: '更新时间', dataType: 'datetime', sortable: true, hideable: true, selectable: true, ops: ['eq', 'neq', 'in', 'not_in', 'gt', 'gte', 'lt', 'lte', 'between'] },
]

function configSchema(resource: 'sys_config' | 'sys_tenant_config'): ListSchemaDTO {
  return {
    resource,
    hash: `test-hash-${resource}`,
    fields:
      resource === 'sys_tenant_config'
        ? [...CONFIG_FIELDS, { field: 'tenantId', label: '租户 ID', dataType: 'string', hideable: true, selectable: true, ops: ['eq', 'neq'] }]
        : CONFIG_FIELDS,
    defaultSort: [{ field: 'updatedAt', desc: true }],
    pagination: { defaultPageSize: 20, maxPageSize: 200, modes: ['offset', 'cursor'], includeTotal: true },
  }
}

function featureFlagSchema(): ListSchemaDTO {
  return {
    resource: 'sys_feature_flag',
    hash: 'test-hash-sys-feature-flag',
    fields: [
      { field: 'id', label: '特性 ID', dataType: 'integer', sortable: true, hideable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'status', label: '数据状态，0：禁用、1：启用', dataType: 'integer', sortable: true, hideable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'name', label: '特性名称', dataType: 'string', sortable: true, hideable: true, selectable: true, ops: ['contains', 'eq'] },
      { field: 'flagKey', label: '特性键（租户内唯一）', dataType: 'string', hideable: true, selectable: true, ops: ['eq', 'prefix'] },
      { field: 'description', label: '特性描述', dataType: 'string', nullable: true, hideable: true, selectable: true, ops: ['contains'] },
      { field: 'isEnabled', label: '是否启用：true-开启功能 false-关闭功能', dataType: 'boolean', hideable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'remark', label: '备注', dataType: 'string', nullable: true, hideable: true, selectable: true, ops: ['contains'] },
      { field: 'createdAt', label: '创建时间', dataType: 'datetime', sortable: true, hideable: true, selectable: true, ops: ['between'] },
      { field: 'updatedAt', label: '更新时间', dataType: 'datetime', sortable: true, hideable: true, selectable: true, ops: ['between'] },
    ],
    defaultSort: [{ field: 'updatedAt', desc: true }],
    pagination: { defaultPageSize: 20, maxPageSize: 200, modes: ['offset', 'cursor'], includeTotal: true },
  }
}

function translationSchema(): ListSchemaDTO {
  return {
    resource: 'sys_translation',
    hash: 'test-hash-sys-translation',
    fields: [
      { field: 'id', label: '翻译 ID', dataType: 'integer', sortable: true, hideable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'status', label: '数据状态，0：禁用、1：启用', dataType: 'integer', sortable: true, hideable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'tableName', label: '来源表名', dataType: 'string', hideable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'rowId', label: '来源行主键 ID', dataType: 'integer', hideable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'columnName', label: '来源列名', dataType: 'string', hideable: true, selectable: true, ops: ['eq', 'in'] },
      { field: 'locale', label: '语言代码（BCP-47）', dataType: 'string', hideable: true, selectable: true, ops: ['eq'] },
      { field: 'value', label: '翻译文本', dataType: 'string', sortable: true, hideable: true, selectable: true, ops: ['contains', 'eq'] },
      { field: 'remark', label: '备注', dataType: 'string', nullable: true, hideable: true, selectable: true, ops: ['contains'] },
      { field: 'createdAt', label: '创建时间', dataType: 'datetime', sortable: true, hideable: true, selectable: true, ops: ['between'] },
      { field: 'updatedAt', label: '更新时间', dataType: 'datetime', sortable: true, hideable: true, selectable: true, ops: ['between'] },
    ],
    defaultSort: [{ field: 'updatedAt', desc: true }],
    pagination: { defaultPageSize: 20, maxPageSize: 200, modes: ['offset', 'cursor'], includeTotal: true },
  }
}

function config(overrides: Partial<ConfigDTO> = {}): ConfigDTO {
  return {
    id: '11',
    status: 1,
    name: 'SMTP 主机',
    configKey: 'smtp.host',
    value: 'smtp.example.com',
    valueType: 'string',
    remark: '邮件服务器',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T08:00:00Z',
    ...overrides,
  }
}

function tenantConfig(overrides: Partial<TenantConfigDTO> = {}): TenantConfigDTO {
  return {
    ...config({ id: '21', name: '租户主题', configKey: 'ui.theme', value: 'dark' }),
    tenantId: 't-1',
    ...overrides,
  }
}

function featureFlag(overrides: Partial<FeatureFlagDTO> = {}): FeatureFlagDTO {
  return {
    id: '31',
    status: 1,
    name: '导出功能',
    flagKey: 'export_enabled',
    description: '允许导出报表',
    isEnabled: true,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T08:00:00Z',
    tenantId: 't-1',
    ...overrides,
  }
}

function translation(overrides: Partial<TranslationDTO> = {}): TranslationDTO {
  return {
    id: '41',
    status: 1,
    tableName: 'sys_menu',
    rowId: '42',
    columnName: 'name',
    locale: 'zh-CN',
    value: '系统设置',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T08:00:00Z',
    tenantId: 't-1',
    ...overrides,
  }
}

beforeEach(() => {
  clearToasts()
  // 权限码拉取失败：SettingsLayout 导航回落静态全量展示（显式 mock，
  // 不依赖 jsdom fetch 对相对地址抛错）。
  mockFetchMyPermissions.mockRejectedValue(new Error('permission denied'))
  mockGetListSchema.mockImplementation(async (resource: string) => {
    if (resource === 'sys_tenant_config') {
      return configSchema('sys_tenant_config')
    }
    if (resource === 'sys_feature_flag') {
      return featureFlagSchema()
    }
    if (resource === 'sys_translation') {
      return translationSchema()
    }
    return configSchema('sys_config')
  })
  mockListConfigs.mockResolvedValue({
    items: [config()],
    total: '1',
    hasMore: false,
    nextCursor: '',
    page: 1,
  })
  mockListTenantConfigs.mockResolvedValue({
    items: [tenantConfig()],
    total: '1',
    hasMore: false,
    nextCursor: '',
    page: 1,
  })
  mockListFeatureFlags.mockResolvedValue({
    items: [featureFlag()],
    total: '1',
    hasMore: false,
    nextCursor: '',
    page: 1,
  })
  mockListTranslations.mockResolvedValue({
    items: [translation()],
    total: '1',
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

// renderPage 以真实布局挂载配置页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/configs']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="configs" element={<ConfigManagePage />} />
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

// applyNameFilter 走渐进式筛选入口落一条「配置名称 包含 smtp」并等待重查：
// 字段按钮须在 Popover（role=dialog）内定位——表头排序按钮同名。
async function applyNameFilter(user2: ReturnType<typeof userEvent.setup>) {
  await user2.click(screen.getByRole('button', { name: '全局配置筛选' }))
  const popover = await screen.findByRole('dialog')
  await user2.click(within(popover).getByRole('button', { name: '配置名称' }))
  await user2.type(screen.getByLabelText('筛选值'), 'smtp')
  await user2.click(screen.getByRole('button', { name: '确定' }))
  await waitFor(() => expect(mockListConfigs).toHaveBeenCalledTimes(2))
}

describe('ConfigManagePage global config tab', () => {
  it('loads_global_configs_with_the_schema_default_sort_and_shows_the_value_column', async () => {
    renderPage()

    expect(await screen.findByText('SMTP 主机')).toBeInTheDocument()
    expect(screen.getByText('smtp.example.com')).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: '配置值' })).toBeInTheDocument()
    expect(mockListConfigs).toHaveBeenCalledTimes(1)
    expect(mockListConfigs.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'updatedAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
    expect(mockGetListSchema).toHaveBeenCalledWith('sys_config')
  })

  it('switches_tabs_lazily_and_fetches_each_tab_schema_and_list', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('SMTP 主机')
    // 挂载时只拉当前视图的 schema，其余视图不预取。
    expect(mockGetListSchema.mock.calls.map(([resource]) => resource)).toEqual(['sys_config'])
    expect(mockGetListSchema).not.toHaveBeenCalledWith('sys_tenant_config')
    expect(mockGetListSchema).not.toHaveBeenCalledWith('sys_feature_flag')
    expect(mockGetListSchema).not.toHaveBeenCalledWith('sys_translation')

    await user2.click(screen.getByRole('tab', { name: '租户配置' }))
    expect(await screen.findByText('租户主题')).toBeInTheDocument()
    expect(mockGetListSchema.mock.calls.map(([resource]) => resource)).toEqual(['sys_config', 'sys_tenant_config'])
    expect(mockGetListSchema).not.toHaveBeenCalledWith('sys_feature_flag')
    expect(mockGetListSchema).not.toHaveBeenCalledWith('sys_translation')
    expect(mockListTenantConfigs.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'updatedAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })

    await user2.click(screen.getByRole('tab', { name: '功能开关' }))
    expect(await screen.findByText('导出功能')).toBeInTheDocument()
    expect(mockGetListSchema.mock.calls.map(([resource]) => resource)).toEqual([
      'sys_config',
      'sys_tenant_config',
      'sys_feature_flag',
    ])
    expect(mockGetListSchema).not.toHaveBeenCalledWith('sys_translation')
    expect(mockListFeatureFlags).toHaveBeenCalledTimes(1)

    await user2.click(screen.getByRole('tab', { name: '翻译' }))
    expect(await screen.findByText('系统设置')).toBeInTheDocument()
    expect(mockGetListSchema.mock.calls.map(([resource]) => resource)).toEqual([
      'sys_config',
      'sys_tenant_config',
      'sys_feature_flag',
      'sys_translation',
    ])
    expect(mockListTranslations).toHaveBeenCalledTimes(1)
    // 尚未打开的视图不应提前拉取列表。
    expect(mockListConfigs).toHaveBeenCalledTimes(1)
    expect(mockListTenantConfigs).toHaveBeenCalledTimes(1)
    expect(mockListFeatureFlags).toHaveBeenCalledTimes(1)
  })

  it('applies_a_filter_condition_to_the_dynamic_query_where', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('SMTP 主机')

    await applyNameFilter(user2)

    expect(mockListConfigs.mock.calls[1][0]).toMatchObject({
      where: [{ field: 'name', op: 'contains', value: 'smtp' }],
      pagination: { page: 1 },
    })
  })

  it('applies_a_header_sort_to_the_dynamic_query_sort', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('SMTP 主机')

    // 仅 schema 声明 sortable 的列可点表头排序（创建时间）。
    await user2.click(screen.getByRole('button', { name: '创建时间' }))

    await waitFor(() => expect(mockListConfigs).toHaveBeenCalledTimes(2))
    expect(mockListConfigs.mock.calls[1][0]).toMatchObject({
      sort: [{ field: 'createdAt', order: 'asc' }],
    })
  })

  it('requests_the_next_page_with_the_updated_pagination', async () => {
    const user2 = userEvent.setup()
    mockListConfigs.mockResolvedValue({
      items: [config()],
      total: '40',
      hasMore: true,
      nextCursor: '',
      page: 1,
    })
    renderPage()
    await screen.findByText('SMTP 主机')

    await user2.click(screen.getByRole('button', { name: '下一页' }))

    await waitFor(() => expect(mockListConfigs).toHaveBeenCalledTimes(2))
    expect(mockListConfigs.mock.calls[1][0]).toMatchObject({
      pagination: { page: 2, pageSize: 20 },
    })
  })

  it('clears_the_where_and_resets_pagination_when_switching_tabs', async () => {
    const user2 = userEvent.setup()
    mockListConfigs.mockResolvedValue({
      items: [config()],
      total: '40',
      hasMore: true,
      nextCursor: '',
      page: 1,
    })
    renderPage()
    await screen.findByText('SMTP 主机')

    await applyNameFilter(user2)
    await user2.click(screen.getByRole('button', { name: '下一页' }))
    await waitFor(() => expect(mockListConfigs).toHaveBeenCalledTimes(3))
    expect(mockListConfigs.mock.calls[2][0]).toMatchObject({ pagination: { page: 2 } })

    await user2.click(screen.getByRole('tab', { name: '租户配置' }))

    await waitFor(() => expect(mockListTenantConfigs).toHaveBeenCalledTimes(1))
    // 切换视图清空筛选并回到第 1 页。
    expect(mockListTenantConfigs.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'updatedAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
  })

  it('creates_a_global_config_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockCreateConfig.mockResolvedValue(config({ id: '12', name: '缓存 TTL', configKey: 'cache.ttl', value: '60' }))
    renderPage()
    await screen.findByText('SMTP 主机')

    await user2.click(screen.getByRole('button', { name: '新建全局配置' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('配置名称'), '缓存 TTL')
    await user2.type(within(dialog).getByLabelText('配置键'), 'cache.ttl')
    await user2.type(within(dialog).getByLabelText('配置值'), '60')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateConfig).toHaveBeenCalledWith({
        status: 1,
        name: '缓存 TTL',
        configKey: 'cache.ttl',
        value: '60',
        valueType: 'string',
        remark: undefined,
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('全局配置 缓存 TTL 已创建')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListConfigs).toHaveBeenCalledTimes(2))
  })

  it('rejects_blank_name_or_key_inline_without_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('SMTP 主机')

    await user2.click(screen.getByRole('button', { name: '新建全局配置' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '创建' }))

    expect(within(screen.getByRole('dialog')).getByRole('alert')).toHaveTextContent('name and config key are required')
    expect(mockCreateConfig).not.toHaveBeenCalled()
  })

  it('surfaces_the_duplicate_key_reason_inline_and_keeps_the_dialog_open', async () => {
    const user2 = userEvent.setup()
    mockCreateConfig.mockRejectedValue(new Error('config already exists'))
    renderPage()
    await screen.findByText('SMTP 主机')

    await user2.click(screen.getByRole('button', { name: '新建全局配置' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('配置名称'), '重复')
    await user2.type(within(dialog).getByLabelText('配置键'), 'smtp.host')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a global config with the same key already exists',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockListConfigs).toHaveBeenCalledTimes(1)
  })

  it('falls_back_to_the_stable_reason_code_when_the_error_has_no_message', async () => {
    const user2 = userEvent.setup()
    // 仅含 reason 的错误：message 与 reason 同为 CONFIG_ALREADY_EXISTS，
    // 页面按 reason 码兜底出可读提示。
    mockCreateConfig.mockRejectedValue(new ApiError('CONFIG_ALREADY_EXISTS', 'CONFIG_ALREADY_EXISTS'))
    renderPage()
    await screen.findByText('SMTP 主机')

    await user2.click(screen.getByRole('button', { name: '新建全局配置' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('配置名称'), '重复')
    await user2.type(within(dialog).getByLabelText('配置键'), 'smtp.host')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a global config with the same key already exists',
    )
    expect(mockCreateConfig).toHaveBeenCalledTimes(1)
  })

  it('surfaces_the_platform_tenant_requirement_for_global_writes', async () => {
    const user2 = userEvent.setup()
    // 子租户即使持有权限码也写不了全局配置：reason 码需映射为可读提示。
    mockCreateConfig.mockRejectedValue(
      new ApiError('CONFIG_PLATFORM_REQUIRED', 'CONFIG_PLATFORM_REQUIRED'),
    )
    renderPage()
    await screen.findByText('SMTP 主机')

    await user2.click(screen.getByRole('button', { name: '新建全局配置' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('配置名称'), '越权')
    await user2.type(within(dialog).getByLabelText('配置键'), 'smtp.host')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'only the platform (root) tenant can modify global configs',
    )
    expect(mockCreateConfig).toHaveBeenCalledTimes(1)
  })

  it('prefills_the_edit_form_and_keeps_key_and_value_type_immutable', async () => {
    const user2 = userEvent.setup()
    mockUpdateConfig.mockResolvedValue(config({ name: 'SMTP 服务器', status: 0 }))
    renderPage()
    const menu = await openRowActions(user2, 'SMTP 主机')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('配置名称')).toHaveValue('SMTP 主机')
    expect(within(dialog).getByLabelText('配置键')).toBeDisabled()
    expect(within(dialog).getByLabelText('值类型')).toBeDisabled()

    await user2.clear(within(dialog).getByLabelText('配置名称'))
    await user2.type(within(dialog).getByLabelText('配置名称'), 'SMTP 服务器')
    await user2.click(within(dialog).getByRole('switch', { name: '状态' }))
    expect(within(dialog).getByText('禁用')).toBeInTheDocument()
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    // 更新不上送 configKey / valueType（后端忽略，前端不发送）。
    await waitFor(() =>
      expect(mockUpdateConfig).toHaveBeenCalledWith({
        id: '11',
        status: 0,
        name: 'SMTP 服务器',
        value: 'smtp.example.com',
        remark: '邮件服务器',
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('全局配置 SMTP 服务器 已更新')
    await waitFor(() => expect(mockListConfigs).toHaveBeenCalledTimes(2))
  })

  it('keeps_an_empty_value_as_an_empty_string_on_update', async () => {
    const user2 = userEvent.setup()
    mockListConfigs.mockResolvedValue({
      items: [config({ value: '' })],
      total: '1',
      hasMore: false,
      nextCursor: '',
      page: 1,
    })
    mockUpdateConfig.mockResolvedValue(config({ value: '' }))
    renderPage()
    await screen.findByText('SMTP 主机')
    const menu = await openRowActions(user2, 'SMTP 主机')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('配置值')).toHaveValue('')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    // 空串是合法取值：界面原样上送，不得清洗为 undefined（否则覆盖为 NULL）。
    await waitFor(() =>
      expect(mockUpdateConfig).toHaveBeenCalledWith({
        id: '11',
        status: 1,
        name: 'SMTP 主机',
        value: '',
        remark: '邮件服务器',
      }),
    )
  })

  it('keeps_a_null_value_unset_when_only_the_global_config_name_changes', async () => {
    const user2 = userEvent.setup()
    mockListConfigs.mockResolvedValue({
      items: [config({ value: undefined })],
      total: '1',
      hasMore: false,
      nextCursor: '',
      page: 1,
    })
    mockUpdateConfig.mockResolvedValue(config({ name: 'SMTP 服务器', value: undefined }))
    renderPage()
    await screen.findByText('SMTP 主机')
    const menu = await openRowActions(user2, 'SMTP 主机')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('配置值')).toHaveValue('')
    await user2.clear(within(dialog).getByLabelText('配置名称'))
    await user2.type(within(dialog).getByLabelText('配置名称'), 'SMTP 服务器')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    // 原值为 NULL 且配置值未改动：请求体不含 value，保持 NULL 而非写成空串。
    await waitFor(() => expect(mockUpdateConfig).toHaveBeenCalledTimes(1))
    const payload = mockUpdateConfig.mock.calls[0][0]
    expect(payload).not.toHaveProperty('value')
    expect(payload).toEqual({ id: '11', status: 1, name: 'SMTP 服务器', remark: '邮件服务器' })
  })

  it('submits_the_typed_value_when_the_original_global_config_value_is_null', async () => {
    const user2 = userEvent.setup()
    mockListConfigs.mockResolvedValue({
      items: [config({ value: undefined })],
      total: '1',
      hasMore: false,
      nextCursor: '',
      page: 1,
    })
    mockUpdateConfig.mockResolvedValue(config({ value: 'smtp.internal' }))
    renderPage()
    await screen.findByText('SMTP 主机')
    const menu = await openRowActions(user2, 'SMTP 主机')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    // 显式清空后再输入同样算改动过：按最终输入原样上送。
    await user2.type(within(dialog).getByLabelText('配置值'), '临时值')
    await user2.clear(within(dialog).getByLabelText('配置值'))
    await user2.type(within(dialog).getByLabelText('配置值'), 'smtp.internal')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(mockUpdateConfig).toHaveBeenCalledWith({
        id: '11',
        status: 1,
        name: 'SMTP 主机',
        value: 'smtp.internal',
        remark: '邮件服务器',
      }),
    )
  })

  it('deletes_only_after_confirmation_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockDeleteConfig.mockResolvedValue(undefined)
    renderPage()
    const menu = await openRowActions(user2, 'SMTP 主机')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除全局配置 SMTP 主机？')
    expect(mockDeleteConfig).not.toHaveBeenCalled()

    await user2.click(within(confirmDialog).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteConfig).toHaveBeenCalledWith('11'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('全局配置 SMTP 主机 已删除')
    await waitFor(() => expect(mockListConfigs).toHaveBeenCalledTimes(2))
  })
})

describe('ConfigManagePage tenant config tab', () => {
  it('creates_a_tenant_config_from_its_own_tab', async () => {
    const user2 = userEvent.setup()
    mockCreateTenantConfig.mockResolvedValue(tenantConfig({ id: '22', name: '租户标题', configKey: 'ui.title' }))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '租户配置' }))
    await screen.findByText('租户主题')

    await user2.click(screen.getByRole('button', { name: '新建租户配置' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('配置名称'), '租户标题')
    await user2.type(within(dialog).getByLabelText('配置键'), 'ui.title')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateTenantConfig).toHaveBeenCalledWith({
        status: 1,
        name: '租户标题',
        configKey: 'ui.title',
        value: '',
        valueType: 'string',
        remark: undefined,
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('租户配置 租户标题 已创建')
    await waitFor(() => expect(mockListTenantConfigs).toHaveBeenCalledTimes(2))
  })

  it('updates_a_tenant_config_without_sending_the_immutable_columns', async () => {
    const user2 = userEvent.setup()
    mockUpdateTenantConfig.mockResolvedValue(tenantConfig({ name: '租户主题改' }))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '租户配置' }))
    const menu = await openRowActions(user2, '租户主题')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('配置键')).toBeDisabled()
    expect(within(dialog).getByLabelText('值类型')).toBeDisabled()
    await user2.clear(within(dialog).getByLabelText('配置名称'))
    await user2.type(within(dialog).getByLabelText('配置名称'), '租户主题改')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(mockUpdateTenantConfig).toHaveBeenCalledWith({
        id: '21',
        status: 1,
        name: '租户主题改',
        value: 'dark',
        remark: '邮件服务器',
      }),
    )
  })

  it('keeps_a_null_value_unset_when_only_the_tenant_config_name_changes', async () => {
    const user2 = userEvent.setup()
    mockListTenantConfigs.mockResolvedValue({
      items: [tenantConfig({ value: undefined })],
      total: '1',
      hasMore: false,
      nextCursor: '',
      page: 1,
    })
    mockUpdateTenantConfig.mockResolvedValue(tenantConfig({ name: '租户主题改', value: undefined }))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '租户配置' }))
    await screen.findByText('租户主题')
    const menu = await openRowActions(user2, '租户主题')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('配置值')).toHaveValue('')
    await user2.clear(within(dialog).getByLabelText('配置名称'))
    await user2.type(within(dialog).getByLabelText('配置名称'), '租户主题改')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    // 租户配置同口径：原值 NULL 且未改动时不上送 value。
    await waitFor(() => expect(mockUpdateTenantConfig).toHaveBeenCalledTimes(1))
    const payload = mockUpdateTenantConfig.mock.calls[0][0]
    expect(payload).not.toHaveProperty('value')
    expect(payload).toEqual({ id: '21', status: 1, name: '租户主题改', remark: '邮件服务器' })
  })

  it('deletes_a_tenant_config_after_confirmation_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockDeleteTenantConfig.mockResolvedValue(undefined)
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '租户配置' }))
    const menu = await openRowActions(user2, '租户主题')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除租户配置 租户主题？')
    expect(mockDeleteTenantConfig).not.toHaveBeenCalled()

    await user2.click(within(confirmDialog).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteTenantConfig).toHaveBeenCalledWith('21'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('租户配置 租户主题 已删除')
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListTenantConfigs).toHaveBeenCalledTimes(2))
  })

  it('keeps_the_tenant_delete_dialog_open_when_the_backend_rejects', async () => {
    const user2 = userEvent.setup()
    mockDeleteTenantConfig.mockRejectedValue(new Error('tenant config not found'))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '租户配置' }))
    const menu = await openRowActions(user2, '租户主题')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    expect(await screen.findByRole('alert', { hidden: true })).toHaveTextContent(
      '删除失败：tenant config not found; it may have been deleted or belong to another tenant',
    )
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    expect(mockListTenantConfigs).toHaveBeenCalledTimes(1)
  })
})

describe('ConfigManagePage feature flag tab', () => {
  it('renders_the_enabled_badge_for_the_flag_value', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '功能开关' }))

    expect(await screen.findByText('导出功能')).toBeInTheDocument()
    expect(screen.getByText('开启')).toBeInTheDocument()
  })

  it('quick_toggles_the_flag_with_status_and_is_enabled_and_preserves_the_details', async () => {
    const user2 = userEvent.setup()
    mockUpdateFeatureFlag.mockResolvedValue(featureFlag({ isEnabled: false }))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '功能开关' }))
    const menu = await openRowActions(user2, '导出功能')

    await user2.click(menu.getByRole('menuitem', { name: '停用' }))

    await waitFor(() =>
      expect(mockUpdateFeatureFlag).toHaveBeenCalledWith({
        id: '31',
        status: 1,
        name: '导出功能',
        description: '允许导出报表',
        isEnabled: false,
        remark: undefined,
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('功能开关 导出功能 已关闭')
    await waitFor(() => expect(mockListFeatureFlags).toHaveBeenCalledTimes(2))
  })

  it('edits_the_flag_keeping_the_key_readonly_and_sending_both_status_and_is_enabled', async () => {
    const user2 = userEvent.setup()
    mockUpdateFeatureFlag.mockResolvedValue(featureFlag({ isEnabled: false, description: '改描述' }))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '功能开关' }))
    const menu = await openRowActions(user2, '导出功能')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('功能键')).toBeDisabled()
    await user2.clear(within(dialog).getByLabelText('功能描述'))
    await user2.type(within(dialog).getByLabelText('功能描述'), '改描述')
    await user2.click(within(dialog).getByRole('switch', { name: '功能开启' }))
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(mockUpdateFeatureFlag).toHaveBeenCalledWith({
        id: '31',
        status: 1,
        name: '导出功能',
        description: '改描述',
        isEnabled: false,
        remark: undefined,
      }),
    )
  })

  it('creates_a_feature_flag_from_the_dialog_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockCreateFeatureFlag.mockResolvedValue(featureFlag({ id: '32', name: '审计日志', flagKey: 'audit_log_enabled' }))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '功能开关' }))
    await screen.findByText('导出功能')

    await user2.click(screen.getByRole('button', { name: '新建功能开关' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('功能名称'), '审计日志')
    await user2.type(within(dialog).getByLabelText('功能键'), 'audit_log_enabled')
    await user2.type(within(dialog).getByLabelText('功能描述'), '记录审计')
    await user2.click(within(dialog).getByRole('switch', { name: '功能开启' }))
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateFeatureFlag).toHaveBeenCalledWith({
        status: 1,
        name: '审计日志',
        flagKey: 'audit_log_enabled',
        description: '记录审计',
        isEnabled: true,
        remark: undefined,
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('功能开关 审计日志 已创建')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListFeatureFlags).toHaveBeenCalledTimes(2))
  })

  it('rejects_a_flag_without_name_or_key_inline', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '功能开关' }))
    await screen.findByText('导出功能')

    await user2.click(screen.getByRole('button', { name: '新建功能开关' }))
    await user2.click(within(screen.getByRole('dialog')).getByRole('button', { name: '创建' }))

    expect(within(screen.getByRole('dialog')).getByRole('alert')).toHaveTextContent('name and flag key are required')
    expect(mockCreateFeatureFlag).not.toHaveBeenCalled()
  })

  it('surfaces_the_duplicate_flag_key_reason_inline_and_keeps_the_dialog_open', async () => {
    const user2 = userEvent.setup()
    mockCreateFeatureFlag.mockRejectedValue(new Error('feature flag already exists'))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '功能开关' }))
    await screen.findByText('导出功能')

    await user2.click(screen.getByRole('button', { name: '新建功能开关' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('功能名称'), '导入功能')
    await user2.type(within(dialog).getByLabelText('功能键'), 'export_enabled')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(
      'a feature flag with the same key already exists in this tenant',
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mockListFeatureFlags).toHaveBeenCalledTimes(1)
  })

  it('deletes_a_feature_flag_after_confirmation_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockDeleteFeatureFlag.mockResolvedValue(undefined)
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '功能开关' }))
    const menu = await openRowActions(user2, '导出功能')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除功能开关 导出功能？')
    expect(mockDeleteFeatureFlag).not.toHaveBeenCalled()

    await user2.click(within(confirmDialog).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteFeatureFlag).toHaveBeenCalledWith('31'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('功能开关 导出功能 已删除')
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListFeatureFlags).toHaveBeenCalledTimes(2))
  })

  it('keeps_the_feature_flag_delete_dialog_open_when_the_backend_rejects', async () => {
    const user2 = userEvent.setup()
    mockDeleteFeatureFlag.mockRejectedValue(new Error('feature flag not found'))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '功能开关' }))
    const menu = await openRowActions(user2, '导出功能')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    expect(await screen.findByRole('alert', { hidden: true })).toHaveTextContent(
      '删除失败：feature flag not found; it may have been deleted or belong to another tenant',
    )
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    expect(mockListFeatureFlags).toHaveBeenCalledTimes(1)
  })
})

describe('ConfigManagePage translation tab', () => {
  it('rejects_a_non_positive_row_id_before_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '翻译' }))
    await screen.findByText('系统设置')

    await user2.click(screen.getByRole('button', { name: '新建翻译' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('来源表'), 'sys_menu')
    await user2.type(within(dialog).getByLabelText('来源行 ID'), '0')
    await user2.type(within(dialog).getByLabelText('来源列'), 'name')
    await user2.type(within(dialog).getByLabelText('语言代码'), 'zh-CN')
    await user2.type(within(dialog).getByLabelText('翻译文本'), '系统设置')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('row id must be a positive integer')
    expect(mockCreateTranslation).not.toHaveBeenCalled()
  })

  it('rejects_an_invalid_locale_before_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '翻译' }))
    await screen.findByText('系统设置')

    await user2.click(screen.getByRole('button', { name: '新建翻译' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('来源表'), 'sys_menu')
    await user2.type(within(dialog).getByLabelText('来源行 ID'), '42')
    await user2.type(within(dialog).getByLabelText('来源列'), 'name')
    await user2.type(within(dialog).getByLabelText('语言代码'), 'zh_CN')
    await user2.type(within(dialog).getByLabelText('翻译文本'), '系统设置')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('locale must be a BCP-47 tag such as zh-CN')
    expect(mockCreateTranslation).not.toHaveBeenCalled()
  })

  it('creates_a_translation_with_the_int64_row_id_as_a_string', async () => {
    const user2 = userEvent.setup()
    mockCreateTranslation.mockResolvedValue(translation({ id: '42' }))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '翻译' }))
    await screen.findByText('系统设置')

    await user2.click(screen.getByRole('button', { name: '新建翻译' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('来源表'), 'sys_menu')
    await user2.type(within(dialog).getByLabelText('来源行 ID'), '42')
    await user2.type(within(dialog).getByLabelText('来源列'), 'name')
    await user2.type(within(dialog).getByLabelText('语言代码'), 'zh-CN')
    await user2.type(within(dialog).getByLabelText('翻译文本'), '系统设置')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(mockCreateTranslation).toHaveBeenCalledWith({
        status: 1,
        tableName: 'sys_menu',
        rowId: '42',
        columnName: 'name',
        locale: 'zh-CN',
        value: '系统设置',
        remark: undefined,
      }),
    )
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent('翻译 sys_menu/name (zh-CN) 已创建')
    await waitFor(() => expect(mockListTranslations).toHaveBeenCalledTimes(2))
  })

  it('keeps_the_identity_columns_readonly_and_updates_only_value_remark_and_status', async () => {
    const user2 = userEvent.setup()
    mockUpdateTranslation.mockResolvedValue(translation({ value: '系统配置' }))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '翻译' }))
    const menu = await openRowActions(user2, 'sys_menu/name zh-CN')

    await user2.click(menu.getByRole('menuitem', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('来源表')).toBeDisabled()
    expect(within(dialog).getByLabelText('来源行 ID')).toBeDisabled()
    expect(within(dialog).getByLabelText('来源列')).toBeDisabled()
    expect(within(dialog).getByLabelText('语言代码')).toBeDisabled()
    await user2.clear(within(dialog).getByLabelText('翻译文本'))
    await user2.type(within(dialog).getByLabelText('翻译文本'), '系统配置')
    await user2.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(mockUpdateTranslation).toHaveBeenCalledWith({
        id: '41',
        status: 1,
        value: '系统配置',
        remark: undefined,
      }),
    )
  })

  it('rejects_a_row_id_beyond_the_int64_range_before_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '翻译' }))
    await screen.findByText('系统设置')

    await user2.click(screen.getByRole('button', { name: '新建翻译' }))
    const dialog = screen.getByRole('dialog')
    await user2.type(within(dialog).getByLabelText('来源表'), 'sys_menu')
    await user2.type(within(dialog).getByLabelText('来源行 ID'), '9223372036854775808')
    await user2.type(within(dialog).getByLabelText('来源列'), 'name')
    await user2.type(within(dialog).getByLabelText('语言代码'), 'zh-CN')
    await user2.type(within(dialog).getByLabelText('翻译文本'), '系统设置')
    await user2.click(within(dialog).getByRole('button', { name: '创建' }))

    expect(within(dialog).getByRole('alert')).toHaveTextContent('row id exceeds the int64 range')
    expect(mockCreateTranslation).not.toHaveBeenCalled()
  })

  it('deletes_a_translation_after_confirmation_and_reloads_the_list', async () => {
    const user2 = userEvent.setup()
    mockDeleteTranslation.mockResolvedValue(undefined)
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '翻译' }))
    const menu = await openRowActions(user2, 'sys_menu/name zh-CN')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认删除翻译 zh-CN？')
    expect(mockDeleteTranslation).not.toHaveBeenCalled()

    await user2.click(within(confirmDialog).getByRole('button', { name: '删除' }))

    await waitFor(() => expect(mockDeleteTranslation).toHaveBeenCalledWith('41'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent(
      '翻译 sys_menu/name (zh-CN) 已删除',
    )
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await waitFor(() => expect(mockListTranslations).toHaveBeenCalledTimes(2))
  })

  it('keeps_the_translation_delete_dialog_open_when_the_backend_rejects', async () => {
    const user2 = userEvent.setup()
    mockDeleteTranslation.mockRejectedValue(new Error('translation not found'))
    renderPage()
    await user2.click(await screen.findByRole('tab', { name: '翻译' }))
    const menu = await openRowActions(user2, 'sys_menu/name zh-CN')

    await user2.click(menu.getByRole('menuitem', { name: '删除' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '删除' }))

    expect(await screen.findByRole('alert', { hidden: true })).toHaveTextContent(
      '删除失败：translation not found; it may have been deleted or belong to another tenant',
    )
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    expect(mockListTranslations).toHaveBeenCalledTimes(1)
  })
})

describe('ConfigManagePage load errors', () => {
  it('surfaces_a_retryable_list_error_and_recovers', async () => {
    const user2 = userEvent.setup()
    mockListConfigs.mockRejectedValueOnce(new Error('boom'))
    mockListConfigs.mockResolvedValueOnce({ items: [config()], total: '1', hasMore: false, nextCursor: '', page: 1 })
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('列表加载失败：boom')
    await user2.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('SMTP 主机')).toBeInTheDocument()
  })

  it('surfaces_a_retryable_schema_error_and_recovers', async () => {
    const user2 = userEvent.setup()
    mockGetListSchema.mockRejectedValue(new Error('schema down'))
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('查询能力加载失败：schema down')
    expect(mockListConfigs).not.toHaveBeenCalled()

    mockGetListSchema.mockImplementation(async () => configSchema('sys_config'))
    await user2.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('SMTP 主机')).toBeInTheDocument()
  })
})
