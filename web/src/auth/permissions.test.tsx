import { cleanup, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchMyPermissions } from '@/api/permissions'
import { PermissionProvider, RequirePermission } from './permissions'

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)

beforeEach(() => {
  mockFetchMyPermissions.mockResolvedValue([])
})

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

// renderGuarded 挂载受守卫的路由：无权限时落到个人中心。
function renderGuarded() {
  return render(
    <MemoryRouter initialEntries={['/settings/users']}>
      <Routes>
        <Route path="/settings/profile" element={<div>profile-page</div>} />
        <Route
          path="/settings/users"
          element={
            <PermissionProvider>
              <RequirePermission code="sys:user">
                <div>users-page</div>
              </RequirePermission>
            </PermissionProvider>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe('RequirePermission', () => {
  it('renders_the_children_when_the_code_is_granted', async () => {
    mockFetchMyPermissions.mockResolvedValue(['sys:user'])
    renderGuarded()

    expect(await screen.findByText('users-page')).toBeInTheDocument()
    expect(mockFetchMyPermissions).toHaveBeenCalledTimes(1)
  })

  it('redirects_to_the_personal_center_when_the_code_is_missing', async () => {
    mockFetchMyPermissions.mockResolvedValue(['sys:role'])
    renderGuarded()

    expect(await screen.findByText('profile-page')).toBeInTheDocument()
    expect(screen.queryByText('users-page')).not.toBeInTheDocument()
  })

  it('falls_back_to_allowing_when_the_permission_fetch_fails', async () => {
    mockFetchMyPermissions.mockRejectedValue(new Error('permission denied'))
    renderGuarded()

    expect(await screen.findByText('users-page')).toBeInTheDocument()
  })

  it('shows_a_placeholder_while_permissions_are_loading', () => {
    mockFetchMyPermissions.mockReturnValue(new Promise<string[]>(() => undefined))
    renderGuarded()

    expect(screen.getByLabelText('权限校验中')).toBeInTheDocument()
    expect(screen.queryByText('users-page')).not.toBeInTheDocument()
  })
})

// renderRequirementGuarded 挂载带多码要求的守卫：required 全有 + anyOf 任一。
function renderRequirementGuarded() {
  return render(
    <MemoryRouter initialEntries={['/settings/users']}>
      <Routes>
        <Route path="/settings/profile" element={<div>profile-page</div>} />
        <Route
          path="/settings/users"
          element={
            <PermissionProvider>
              <RequirePermission
                requirement={{ required: ['sys:user'], anyOf: ['sys:user:query', 'sys:user:create'] }}
              >
                <div>users-page</div>
              </RequirePermission>
            </PermissionProvider>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe('RequirePermission multi-code requirement', () => {
  it('passes_when_required_and_one_anyOf_code_are_granted', async () => {
    mockFetchMyPermissions.mockResolvedValue(['sys:user', 'sys:user:create'])
    renderRequirementGuarded()

    expect(await screen.findByText('users-page')).toBeInTheDocument()
  })

  it('redirects_when_a_required_code_is_missing', async () => {
    mockFetchMyPermissions.mockResolvedValue(['sys:user:query', 'sys:user:create'])
    renderRequirementGuarded()

    expect(await screen.findByText('profile-page')).toBeInTheDocument()
    expect(screen.queryByText('users-page')).not.toBeInTheDocument()
  })

  it('redirects_when_none_of_the_anyOf_codes_is_granted', async () => {
    mockFetchMyPermissions.mockResolvedValue(['sys:user', 'sys:role:query'])
    renderRequirementGuarded()

    expect(await screen.findByText('profile-page')).toBeInTheDocument()
    expect(screen.queryByText('users-page')).not.toBeInTheDocument()
  })
})
