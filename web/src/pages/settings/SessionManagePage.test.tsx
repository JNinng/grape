import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SessionAdminDTO } from '@/api/sessionAdmin'
import { listSessions, revokeSession } from '@/api/sessionAdmin'
import { getListSchema, type ListSchemaDTO } from '@/api/querySchema'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { SettingsLayout } from './SettingsLayout'
import { SessionManagePage } from './SessionManagePage'
import { fetchMyPermissions } from '@/api/permissions'

vi.mock('@/api/sessionAdmin', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/sessionAdmin')>()),
  listSessions: vi.fn(),
  revokeSession: vi.fn(),
}))

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

vi.mock('@/api/querySchema', () => ({
  getListSchema: vi.fn(),
}))

const mockListSessions = vi.mocked(listSessions)
const mockRevokeSession = vi.mocked(revokeSession)
const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)
const mockGetListSchema = vi.mocked(getListSchema)

// sessionSchema 基线对齐 GetListSchema(sys_session) 响应口径（令牌哈希
// @internal 不在 fields 中）。
function sessionSchema(): ListSchemaDTO {
  return {
    resource: 'sys_session',
    hash: 'test-hash',
    fields: [
      { field: 'id', label: '会话 ID', dataType: 'integer', sortable: true, visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'status', label: '数据状态，0：禁用、1：启用', dataType: 'integer', sortable: true, visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'userId', label: '用户 ID', dataType: 'integer', visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'isRevoked', label: '是否已主动撤销：true 后即使令牌未过期也不可用', dataType: 'boolean', visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'updatedAt', label: '更新时间', dataType: 'datetime', sortable: true, visibility: 'optional', ops: ['eq', 'neq', 'in', 'not_in', 'gt', 'gte', 'lt', 'lte', 'between'] },
    ],
    defaultSort: [{ field: 'updatedAt', desc: true }],
    pagination: { defaultPageSize: 20, maxPageSize: 200, modes: ['offset', 'cursor'], includeTotal: true },
  }
}

function session(overrides: Partial<SessionAdminDTO> = {}): SessionAdminDTO {
  return {
    id: '5',
    status: 1,
    userId: '7',
    expiresAt: '2026-01-01T00:30:00Z',
    refreshExpiresAt: '2026-01-08T00:00:00Z',
    deviceInfo: 'Windows Chrome',
    ipAddress: '192.168.1.100',
    isRevoked: false,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  clearToasts()
  mockFetchMyPermissions.mockRejectedValue(new Error('permission denied'))
  mockGetListSchema.mockResolvedValue(sessionSchema())
  mockListSessions.mockResolvedValue({
    items: [session(), session({ id: '6', userId: '8', isRevoked: true })],
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

// renderPage 以真实布局挂载会话页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/sessions']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="sessions" element={<SessionManagePage />} />
        </Route>
      </Routes>
      <Toaster />
    </MemoryRouter>,
  )
}

async function openRowActions(user2: ReturnType<typeof userEvent.setup>, label: string) {
  await user2.click(await screen.findByRole('button', { name: `操作 ${label}` }))
  const menu = await screen.findByRole('menu')
  return within(menu)
}

describe('SessionManagePage list', () => {
  it('loads_sessions_with_the_schema_default_sort_on_mount', async () => {
    renderPage()

    expect(await screen.findByText('已撤销')).toBeInTheDocument()
    expect(mockListSessions).toHaveBeenCalledTimes(1)
    expect(mockListSessions.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'updatedAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
  })

  it('renders_the_active_badge_for_live_sessions', async () => {
    renderPage()

    expect(await screen.findByText('活跃')).toBeInTheDocument()
    // 已撤销与活跃并存（两条种子形态）。
    expect(screen.getByText('已撤销')).toBeInTheDocument()
  })

  it('surfaces_a_retryable_alert_when_the_schema_fails', async () => {
    mockGetListSchema.mockRejectedValue(new Error('boom'))
    const user2 = userEvent.setup()
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('查询能力加载失败：boom')
    expect(mockListSessions).not.toHaveBeenCalled()

    mockGetListSchema.mockResolvedValue(sessionSchema())
    await user2.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('活跃')).toBeInTheDocument()
  })
})

describe('SessionManagePage revoke', () => {
  it('asks_for_confirmation_and_reloads_the_list_on_confirm', async () => {
    const user2 = userEvent.setup()
    mockRevokeSession.mockResolvedValue(undefined)
    renderPage()
    const menu = await openRowActions(user2, '#5')

    await user2.click(menu.getByRole('menuitem', { name: '撤销' }))
    const confirmDialog = screen.getByRole('alertdialog')
    expect(confirmDialog).toHaveTextContent('确认撤销会话 #5？')
    expect(mockRevokeSession).not.toHaveBeenCalled()

    // 取消：关框且不撤销。
    await user2.click(within(confirmDialog).getByRole('button', { name: '取消' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(mockRevokeSession).not.toHaveBeenCalled()

    // 再次打开并确认：撤销并重查列表。
    const menuAgain = await openRowActions(user2, '#5')
    await user2.click(menuAgain.getByRole('menuitem', { name: '撤销' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '撤销' }))

    await waitFor(() => expect(mockRevokeSession).toHaveBeenCalledWith('5'))
    expect(await screen.findByRole('status', { hidden: true })).toHaveTextContent(
      '会话 #5 已撤销，其访问与刷新令牌一并失效',
    )
    await waitFor(() => expect(mockListSessions).toHaveBeenCalledTimes(2))
  })

  it('surfaces_the_revoke_failure_toast_and_keeps_the_page_usable', async () => {
    const user2 = userEvent.setup()
    mockRevokeSession.mockRejectedValue(new Error('invalid session argument'))
    renderPage()
    const menu = await openRowActions(user2, '#5')

    await user2.click(menu.getByRole('menuitem', { name: '撤销' }))
    await user2.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '撤销' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('撤销失败：invalid session argument')
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })
})
