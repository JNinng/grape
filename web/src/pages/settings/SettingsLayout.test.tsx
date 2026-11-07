import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchMyPermissions } from '@/api/permissions'
import { SettingsIndexRedirect, SettingsLayout } from './SettingsLayout'

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)

beforeEach(() => {
  mockFetchMyPermissions.mockResolvedValue([
    'sys:user',
    'sys:user:query',
    'sys:dept',
    'sys:dept:query',
    'sys:user-group',
    'sys:user-group:query',
    'sys:role',
    'sys:role:query',
    'sys:menu',
    'sys:menu:query',
    'sys:dict',
    'sys:dict:query',
    'sys:audit',
    'sys:audit:operation-log',
    'sys:session',
    'sys:session:query',
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
const ALL_LABELS = ['个人中心', '用户', '部门', '用户组', '角色', '菜单', '字典', '审计日志', '会话', '配置', '租户']

describe('SettingsLayout navigation filtering', () => {
  it('keeps_only_the_modules_whose_permission_requirement_is_met_after_loading', async () => {
    renderLayout()

    // 授权集合不含 sys:config / sys:tenant：配置与租户导航项一并隐藏。
    expect(await screen.findByRole('link', { name: '用户' })).toBeInTheDocument()
    const nav = screen.getByLabelText('管理模块')
    for (const label of ['个人中心', '用户', '部门', '用户组', '角色', '菜单', '字典', '审计日志', '会话']) {
      expect(within(nav).getByText(label)).toBeInTheDocument()
    }
    for (const label of ['配置', '租户']) {
      expect(within(nav).queryByText(label)).not.toBeInTheDocument()
    }
    expect(mockFetchMyPermissions).toHaveBeenCalledTimes(1)
  })

  it('always_shows_the_personal_center_without_any_permission_code', async () => {
    mockFetchMyPermissions.mockResolvedValue([])
    renderLayout()

    const nav = screen.getByLabelText('管理模块')
    expect(await within(nav).findByRole('link', { name: '个人中心' })).toHaveAttribute('href', '/settings/profile')
    expect(within(nav).queryByText('用户')).not.toBeInTheDocument()
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

  it('hides_a_list_module_when_the_query_code_is_missing', async () => {
    // 只有页面码、没有 List 端点要求的 Query 码：模块不展示，避免进入后 403。
    mockFetchMyPermissions.mockResolvedValue(['sys:user'])
    renderLayout()

    const nav = screen.getByLabelText('管理模块')
    expect(await within(nav).findByRole('link', { name: '个人中心' })).toBeInTheDocument()
    expect(within(nav).queryByText('用户')).not.toBeInTheDocument()
  })

  it('shows_a_multi_view_module_when_page_and_one_view_code_are_granted', async () => {
    mockFetchMyPermissions.mockResolvedValue(['sys:audit', 'sys:audit:login-log'])
    renderLayout()

    expect(await screen.findByRole('link', { name: '审计日志' })).toBeInTheDocument()
  })

  it('hides_a_multi_view_module_when_no_view_code_is_granted', async () => {
    mockFetchMyPermissions.mockResolvedValue(['sys:audit'])
    renderLayout()

    const nav = screen.getByLabelText('管理模块')
    expect(await within(nav).findByRole('link', { name: '个人中心' })).toBeInTheDocument()
    expect(within(nav).queryByText('审计日志')).not.toBeInTheDocument()
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

    resolveFetch?.(['sys:user', 'sys:user:query'])
    expect(await within(nav).findByText('用户')).toBeInTheDocument()
    expect(nav).toHaveAttribute('aria-busy', 'false')
  })
})

describe('SettingsLayout navigation links', () => {
  it('links_all_implemented_modules', async () => {
    // 全量授权：已实现模块全部渲染为可点击链接。
    mockFetchMyPermissions.mockResolvedValue([
      'sys:user', 'sys:user:query',
      'sys:dept', 'sys:dept:query',
      'sys:user-group', 'sys:user-group:query',
      'sys:role', 'sys:role:query',
      'sys:menu', 'sys:menu:query',
      'sys:dict', 'sys:dict:query',
      'sys:audit', 'sys:audit:operation-log',
      'sys:session', 'sys:session:query',
      'sys:config', 'sys:config:global:query',
      'sys:tenant', 'sys:tenant:query',
    ])
    renderLayout()

    expect(await screen.findByRole('link', { name: '个人中心' })).toHaveAttribute('href', '/settings/profile')
    expect(screen.getByRole('link', { name: '用户' })).toHaveAttribute('href', '/settings/users')
    expect(screen.getByRole('link', { name: '部门' })).toHaveAttribute('href', '/settings/depts')
    expect(screen.getByRole('link', { name: '用户组' })).toHaveAttribute('href', '/settings/user-groups')
    expect(screen.getByRole('link', { name: '角色' })).toHaveAttribute('href', '/settings/roles')
    expect(screen.getByRole('link', { name: '菜单' })).toHaveAttribute('href', '/settings/menus')
    expect(screen.getByRole('link', { name: '字典' })).toHaveAttribute('href', '/settings/dicts')
    expect(screen.getByRole('link', { name: '审计日志' })).toHaveAttribute('href', '/settings/audit-logs')
    expect(screen.getByRole('link', { name: '会话' })).toHaveAttribute('href', '/settings/sessions')
    expect(screen.getByRole('link', { name: '配置' })).toHaveAttribute('href', '/settings/configs')
    expect(screen.getByRole('link', { name: '租户' })).toHaveAttribute('href', '/settings/tenants')
  })

  it('links_back_to_the_workspace', async () => {
    const user2 = userEvent.setup()
    mockFetchMyPermissions.mockResolvedValue(['sys:user', 'sys:user:query'])
    render(
      <MemoryRouter initialEntries={['/settings/users']}>
        <Routes>
          <Route path="/" element={<div>workspace-page</div>} />
          <Route path="/settings/*" element={<SettingsLayout />} />
        </Routes>
      </MemoryRouter>,
    )

    const back = await screen.findByRole('link', { name: '返回工作台' })
    expect(back).toHaveAttribute('href', '/')
    await user2.click(back)
    expect(await screen.findByText('workspace-page')).toBeInTheDocument()
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

// renderIndex 挂载索引路由：只注册少量目标页，断言索引重定向落到哪一页。
function renderIndex() {
  return render(
    <MemoryRouter initialEntries={['/settings']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route index element={<SettingsIndexRedirect />} />
          <Route path="profile" element={<div>profile-page</div>} />
          <Route path="users" element={<div>users-page</div>} />
          <Route path="dicts" element={<div>dicts-page</div>} />
          <Route path="audit-logs" element={<div>audit-page</div>} />
          <Route path="roles" element={<div>roles-page</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  )
}

describe('SettingsLayout index redirect', () => {
  it('redirects_to_the_first_permitted_module_in_declaration_order', async () => {
    // 字典声明序在审计日志之前：两者都有权限时跳到字典。
    mockFetchMyPermissions.mockResolvedValue([
      'sys:dict', 'sys:dict:query',
      'sys:audit', 'sys:audit:operation-log',
    ])
    renderIndex()

    expect(await screen.findByText('dicts-page')).toBeInTheDocument()
    expect(screen.queryByText('audit-page')).not.toBeInTheDocument()
  })

  it('skips_modules_without_the_full_permission_requirement', async () => {
    mockFetchMyPermissions.mockResolvedValue([
      'sys:role', 'sys:role:query',
      'sys:audit', 'sys:audit:operation-log',
    ])
    renderIndex()

    expect(await screen.findByText('roles-page')).toBeInTheDocument()
    expect(screen.queryByText('audit-page')).not.toBeInTheDocument()
  })

  it('falls_back_to_the_first_module_when_the_permissions_fetch_fails', async () => {
    mockFetchMyPermissions.mockRejectedValue(new Error('permission denied'))
    renderIndex()

    expect(await screen.findByText('users-page')).toBeInTheDocument()
  })

  it('falls_back_to_the_personal_center_when_no_management_module_is_permitted', async () => {
    mockFetchMyPermissions.mockResolvedValue([])
    renderIndex()

    expect(await screen.findByText('profile-page')).toBeInTheDocument()
    expect(screen.queryByText('users-page')).not.toBeInTheDocument()
  })

  it('waits_for_the_permissions_before_redirecting', async () => {
    let resolveFetch: ((codes: string[]) => void) | undefined
    mockFetchMyPermissions.mockReturnValue(
      new Promise<string[]>((resolve) => {
        resolveFetch = resolve
      }),
    )
    renderIndex()

    expect(screen.getByLabelText('管理模块加载中')).toBeInTheDocument()
    expect(screen.queryByText('users-page')).not.toBeInTheDocument()

    resolveFetch?.(['sys:role', 'sys:role:query'])
    expect(await screen.findByText('roles-page')).toBeInTheDocument()
  })
})
