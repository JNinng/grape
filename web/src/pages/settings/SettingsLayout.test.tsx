import { cleanup, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it } from 'vitest'
import { SettingsLayout } from './SettingsLayout'

afterEach(() => {
  cleanup()
})

function renderLayout(initialEntry = '/settings/users') {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <SettingsLayout />
    </MemoryRouter>,
  )
}

describe('SettingsLayout navigation', () => {
  it('renders_the_full_static_module_list', () => {
    renderLayout()

    const nav = screen.getByLabelText('管理模块')
    for (const label of ['用户', '部门', '用户组', '角色', '菜单', '字典', '审计日志', '会话', '配置', '租户']) {
      expect(within(nav).getByText(label)).toBeInTheDocument()
    }
  })

  it('links_only_the_implemented_user_module_and_marks_the_rest_disabled', () => {
    renderLayout()

    const usersLink = screen.getByRole('link', { name: '用户' })
    expect(usersLink).toHaveAttribute('href', '/settings/users')
    for (const label of ['部门', '用户组', '角色', '菜单', '字典', '审计日志', '会话', '配置', '租户']) {
      const item = screen.getByText(label).closest('[aria-disabled]')
      expect(item).not.toBeNull()
      expect(item).toHaveAttribute('aria-disabled', 'true')
    }
  })

  it('marks_the_active_module_entry_with_the_page_marker', () => {
    renderLayout('/settings/users')

    expect(screen.getByRole('link', { name: '用户' })).toHaveAttribute('aria-current', 'page')
  })
})
