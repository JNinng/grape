import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { LoginReply } from '../api/types'
import { clearSession, refreshTokenStorageKey, setSession } from './store'

vi.mock('./refresh', () => ({
  refreshSession: vi.fn(),
}))

import { refreshSession } from './refresh'

const mockRefresh = vi.mocked(refreshSession)

afterEach(cleanup)

function loginReply(): LoginReply {
  return {
    tokenType: 'Bearer',
    accessToken: 'access-1',
    refreshToken: 'refresh-1',
    expiresAt: '2026-01-01T00:00:00Z',
    user: { id: 7, tenantId: 't-1', name: 'alice', nickname: 'Alice' },
  }
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route
          path="/"
          element={
            <ProtectedRoute>
              <div>chat home</div>
            </ProtectedRoute>
          }
        />
        <Route path="/login" element={<div>login page</div>} />
      </Routes>
    </MemoryRouter>,
  )
}

import { ProtectedRoute } from './ProtectedRoute'

describe('ProtectedRoute', () => {
  it('renders children when an access token is present', () => {
    setSession(loginReply())

    renderAt('/')

    expect(screen.getByText('chat home')).toBeInTheDocument()
    expect(mockRefresh).not.toHaveBeenCalled()
  })

  it('redirects to login when no tokens are available', async () => {
    clearSession()
    mockRefresh.mockRejectedValue(new Error('no refresh token'))

    renderAt('/')

    await waitFor(() => expect(screen.getByText('login page')).toBeInTheDocument())
  })

  it('restores the session when only a refresh token survives a reload', async () => {
    clearSession()
    sessionStorage.setItem(refreshTokenStorageKey, 'refresh-1')
    mockRefresh.mockResolvedValue()

    renderAt('/')

    await waitFor(() => expect(screen.getByText('chat home')).toBeInTheDocument())
    expect(screen.queryByText('login page')).not.toBeInTheDocument()
  })

  it('redirects to login when refresh fails', async () => {
    clearSession()
    sessionStorage.setItem(refreshTokenStorageKey, 'refresh-stale')
    mockRefresh.mockRejectedValue(new Error('expired'))

    renderAt('/')

    await waitFor(() => expect(screen.getByText('login page')).toBeInTheDocument())
  })
})
