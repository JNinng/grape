import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LoginLogDTO, OperationLogDTO } from '@/api/auditLogs'
import { fetchOperationLog, listLoginLogs, listOperationLogs } from '@/api/auditLogs'
import { getListSchema, type ListSchemaDTO } from '@/api/querySchema'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { SettingsLayout } from './SettingsLayout'
import { AuditLogManagePage } from './AuditLogManagePage'
import { fetchMyPermissions } from '@/api/permissions'

vi.mock('@/api/auditLogs', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/auditLogs')>()),
  listOperationLogs: vi.fn(),
  listLoginLogs: vi.fn(),
  fetchOperationLog: vi.fn(),
}))

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

vi.mock('@/api/querySchema', () => ({
  getListSchema: vi.fn(),
}))

const mockListOperationLogs = vi.mocked(listOperationLogs)
const mockListLoginLogs = vi.mocked(listLoginLogs)
const mockFetchOperationLog = vi.mocked(fetchOperationLog)
const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)
const mockGetListSchema = vi.mocked(getListSchema)

// operationSchema 基线对齐 GetListSchema(sys_operation_log) 响应口径。
function operationSchema(): ListSchemaDTO {
  return {
    resource: 'sys_operation_log',
    hash: 'test-hash-op',
    fields: [
      { field: 'id', label: '日志 ID', dataType: 'integer', sortable: true, visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'username', label: '用户名（冗余）', dataType: 'string', sortable: true, visibility: 'optional', ops: ['contains', 'prefix', 'suffix', 'eq', 'neq'] },
      { field: 'operationType', label: '操作类型', dataType: 'string', visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'operatedAt', label: '操作时间', dataType: 'datetime', sortable: true, visibility: 'optional', ops: ['eq', 'neq', 'in', 'not_in', 'gt', 'gte', 'lt', 'lte', 'between'] },
    ],
    defaultSort: [{ field: 'operatedAt', desc: true }],
    pagination: { defaultPageSize: 20, maxPageSize: 200, modes: ['offset', 'cursor'], includeTotal: true },
  }
}

// loginSchema 基线对齐 GetListSchema(sys_login_log) 响应口径。
function loginSchema(): ListSchemaDTO {
  return {
    resource: 'sys_login_log',
    hash: 'test-hash-login',
    fields: [
      { field: 'id', label: '日志 ID', dataType: 'integer', sortable: true, visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'username', label: '用户名（冗余，便于快速查看）', dataType: 'string', sortable: true, visibility: 'optional', ops: ['contains', 'prefix', 'suffix', 'eq', 'neq'] },
      { field: 'isSuccess', label: '是否登录成功', dataType: 'boolean', visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'loginAt', label: '登录时间', dataType: 'datetime', sortable: true, visibility: 'optional', ops: ['eq', 'neq', 'in', 'not_in', 'gt', 'gte', 'lt', 'lte', 'between'] },
    ],
    defaultSort: [{ field: 'loginAt', desc: true }],
    pagination: { defaultPageSize: 20, maxPageSize: 200, modes: ['offset', 'cursor'], includeTotal: true },
  }
}

function operationLog(overrides: Partial<OperationLogDTO> = {}): OperationLogDTO {
  return {
    id: '2',
    userId: '1',
    username: 'admin',
    operationType: 'UPDATE',
    targetTable: 'sys_role',
    targetId: '103',
    oldValue: { data_scope: 1 },
    newValue: { data_scope: 1, remark: '默认角色' },
    durationMs: 30,
    resultCode: '200',
    requestId: 'req-002',
    operatedAt: '2026-06-01T10:10:00Z',
    createdBy: '1',
    createdAt: '2026-06-01T10:10:00Z',
    ...overrides,
  }
}

function loginLog(overrides: Partial<LoginLogDTO> = {}): LoginLogDTO {
  return {
    id: '3',
    userId: '3',
    username: 'lisi',
    loginType: 'password',
    isSuccess: false,
    failureReason: '密码错误',
    loginAt: '2026-06-30T09:00:00Z',
    createdBy: '3',
    createdAt: '2026-06-30T09:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  clearToasts()
  mockFetchMyPermissions.mockRejectedValue(new Error('permission denied'))
  mockGetListSchema.mockImplementation(async (resource: string) => {
    if (resource === 'sys_login_log') {
      return loginSchema()
    }
    return operationSchema()
  })
  mockListOperationLogs.mockResolvedValue({
    items: [operationLog(), operationLog({ id: '5', operationType: 'CREATE', targetTable: 'sys_user', targetId: '2', oldValue: undefined, username: 'admin' })],
    total: '5',
    hasMore: false,
    nextCursor: '',
    page: 1,
  })
  mockListLoginLogs.mockResolvedValue({
    items: [loginLog()],
    total: '4',
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

// renderPage 以真实布局挂载审计日志页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/audit-logs']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="audit-logs" element={<AuditLogManagePage />} />
        </Route>
      </Routes>
      <Toaster />
    </MemoryRouter>,
  )
}

describe('AuditLogManagePage operation tab', () => {
  it('loads_operation_logs_with_the_schema_default_sort_on_mount', async () => {
    renderPage()

    expect(await screen.findByRole('heading', { name: '操作日志' })).toBeInTheDocument()
    expect(mockListOperationLogs).toHaveBeenCalledTimes(1)
    expect(mockListOperationLogs.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'operatedAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
    expect(mockGetListSchema).toHaveBeenCalledWith('sys_operation_log')
  })

  it('switches_to_the_login_tab_and_requeries_with_a_clean_filter_state', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('UPDATE')

    await user2.click(screen.getByRole('tab', { name: '登录日志' }))

    expect(await screen.findByText('lisi')).toBeInTheDocument()
    expect(mockListLoginLogs).toHaveBeenCalledTimes(1)
    expect(mockListLoginLogs.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'loginAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
    expect(mockGetListSchema).toHaveBeenCalledWith('sys_login_log')
    // 登录视图无行操作列（只读），仅结果徽标。
    expect(screen.queryByRole('button', { name: '操作 #3' })).not.toBeInTheDocument()
  })

  it('renders_the_failure_badge_for_failed_logins', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByText('UPDATE')

    await user2.click(screen.getByRole('tab', { name: '登录日志' }))
    expect(await screen.findByText('失败')).toBeInTheDocument()
  })
})

describe('AuditLogManagePage operation detail', () => {
  it('opens_the_detail_dialog_and_renders_json_snapshots', async () => {
    const user2 = userEvent.setup()
    mockFetchOperationLog.mockResolvedValue(operationLog())
    renderPage()
    await screen.findByText('UPDATE')

    await user2.click(screen.getByRole('button', { name: '操作 #2' }))
    const menu = await screen.findByRole('menu')
    await user2.click(within(menu).getByRole('menuitem', { name: '详情' }))

    const dialog = screen.getByRole('dialog')
    expect(mockFetchOperationLog).toHaveBeenCalledWith('2')
    expect(await within(dialog).findByText('操作日志详情')).toBeInTheDocument()
    // 快照以格式化 JSON 展示。
    expect(within(dialog).getByLabelText('操作前数据')).toHaveTextContent('"data_scope": 1')
    expect(within(dialog).getByLabelText('操作后数据')).toHaveTextContent('"remark": "默认角色"')
  })

  it('shows_a_retryable_error_when_the_detail_fails_to_load', async () => {
    const user2 = userEvent.setup()
    mockFetchOperationLog.mockRejectedValueOnce(new Error('audit log not found'))
    renderPage()
    await screen.findByText('UPDATE')

    await user2.click(screen.getByRole('button', { name: '操作 #2' }))
    const menu = await screen.findByRole('menu')
    await user2.click(within(menu).getByRole('menuitem', { name: '详情' }))

    const dialog = screen.getByRole('dialog')
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('详情加载失败：audit log not found')

    mockFetchOperationLog.mockResolvedValue(operationLog())
    await user2.click(within(dialog).getByRole('button', { name: '重试' }))
    expect(await within(dialog).findByText('操作日志详情')).toBeInTheDocument()
  })

  it('surfaces_a_retryable_alert_when_the_schema_fails', async () => {
    mockGetListSchema.mockRejectedValue(new Error('boom'))
    const user2 = userEvent.setup()
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('查询能力加载失败：boom')

    mockGetListSchema.mockResolvedValue(operationSchema())
    await user2.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('UPDATE')).toBeInTheDocument()
  })
})
