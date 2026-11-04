import { cleanup, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchMyPermissions } from '@/api/permissions'
import { SettingsLayout } from './SettingsLayout'

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)

beforeEach(() => {
  mockFetchMyPermissions.mockResolvedValue([
    'sys:user',
    'sys:dept',
    'sys:user-group',
    'sys:role',
    'sys:menu',
    'sys:dict',
    'sys:audit',
    'sys:session',
  ])
})

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

function renderLayout(initialEntry = '/settings/users') {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <SettingsLayout />
    </MemoryRouter>,
  )
}

// ALL_LABELS 是静态全量模块标签（与 SETTINGS_MODULES 对齐）。
const ALL_LABELS = ['用户', '部门', '用户组', '角色', '菜单', '字典', '审计日志', '会话', '配置', '租户']

describe('SettingsLayout navigation filtering', () => {
  it('keeps_only_the_modules_whose_page_code_is_granted_after_loading', async () => {
    renderLayout()

    // 授权集合仅含八个已实现模块的页面码：其余模块（含未实现的禁用占位
    // 项）一并隐藏。
    expect(await screen.findByRole('link', { name: '用户' })).toBeInTheDocument()
    const nav = screen.getByLabelText('管理模块')
    for (const label of ['用户', '部门', '用户组', '角色', '菜单', '字典', '审计日志', '会话']) {
      expect(within(nav).getByText(label)).toBeInTheDocument()
    }
    for (const label of ['配置', '租户']) {
      expect(within(nav).queryByText(label)).not.toBeInTheDocument()
    }
    expect(mockFetchMyPermissions).toHaveBeenCalledTimes(1)
  })

  it('falls_back_to_the_full_static_list_when_the_permissions_fetch_fails', async () => {
    mockFetchMyPermissions.mockRejectedValue(new Error('permission denied'))
    renderLayout()

    expect(await screen.findByRole('link', { name: '用户' })).toBeInTheDocument()
    const nav = screen.getByLabelText('管理模块')
    for (const label of ALL_LABELS) {
      expect(within(nav).getByText(label)).toBeInTheDocument()
    }
  })

  it('shows_no_nav_items_while_the_permissions_fetch_is_pending', async () => {
    let resolveFetch: ((codes: string[]) => void) | undefined
    mockFetchMyPermissions.mockReturnValue(
      new Promise<string[]>((resolve) => {
        resolveFetch = resolve
      }),
    )
    renderLayout()

    const nav = screen.getByRole('navigation')
    expect(within(nav).queryByText('用户')).not.toBeInTheDocument()
    expect(nav).toHaveAttribute('aria-busy', 'true')

    resolveFetch?.(['sys:user'])
    expect(await within(nav).findByText('用户')).toBeInTheDocument()
    expect(nav).toHaveAttribute('aria-busy', 'false')
  })
})

describe('SettingsLayout navigation links', () => {
  it('links_the_implemented_modules_and_marks_the_rest_disabled', async () => {
    // 全量授权：断言五个已实现模块的链接与其余模块的禁用占位并存。
    mockFetchMyPermissions.mockResolvedValue(['sys:user', 'sys:dept', 'sys:user-group', 'sys:role', 'sys:menu', 'sys:dict', 'sys:audit', 'sys:session', 'sys:config', 'sys:tenant'])
    renderLayout()

    expect(await screen.findByRole('link', { name: '用户' })).toHaveAttribute('href', '/settings/users')
    expect(screen.getByRole('link', { name: '部门' })).toHaveAttribute('href', '/settings/depts')
    expect(screen.getByRole('link', { name: '用户组' })).toHaveAttribute('href', '/settings/user-groups')
    expect(screen.getByRole('link', { name: '角色' })).toHaveAttribute('href', '/settings/roles')
    expect(screen.getByRole('link', { name: '菜单' })).toHaveAttribute('href', '/settings/menus')
    expect(screen.getByRole('link', { name: '字典' })).toHaveAttribute('href', '/settings/dicts')
    expect(screen.getByRole('link', { name: '审计日志' })).toHaveAttribute('href', '/settings/audit-logs')
    expect(screen.getByRole('link', { name: '会话' })).toHaveAttribute('href', '/settings/sessions')
    for (const label of ['配置', '租户']) {
      const item = screen.getByText(label).closest('[aria-disabled]')
      expect(item).not.toBeNull()
      expect(item).toHaveAttribute('aria-disabled', 'true')
    }
  })

  it('marks_the_active_module_entry_with_the_page_marker', async () => {
    renderLayout('/settings/users')

    expect(await screen.findByRole('link', { name: '用户' })).toHaveAttribute('aria-current', 'page')
  })

  it('marks_the_role_module_active_on_the_role_page', async () => {
    renderLayout('/settings/roles')

    expect(await screen.findByRole('link', { name: '角色' })).toHaveAttribute('aria-current', 'page')
  })

  it('marks_the_menu_module_active_on_the_menu_page', async () => {
    renderLayout('/settings/menus')

    expect(await screen.findByRole('link', { name: '菜单' })).toHaveAttribute('aria-current', 'page')
  })

  it('marks_the_dept_module_active_on_the_dept_page', async () => {
    renderLayout('/settings/depts')

    expect(await screen.findByRole('link', { name: '部门' })).toHaveAttribute('aria-current', 'page')
  })

  it('marks_the_user_group_module_active_on_the_user_group_page', async () => {
    renderLayout('/settings/user-groups')

    expect(await screen.findByRole('link', { name: '用户组' })).toHaveAttribute('aria-current', 'page')
  })
})
