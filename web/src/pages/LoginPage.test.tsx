import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ThemeProvider } from '../components/theme-provider'
import { LoginPage } from './LoginPage'

vi.mock('../api/session', () => ({
  login: vi.fn(),
}))

import { login } from '../api/session'

const mockLogin = vi.mocked(login)

afterEach(cleanup)

function renderLogin() {
  return render(
    <MemoryRouter initialEntries={['/login']}>
      <ThemeProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<div>chat home</div>} />
        </Routes>
      </ThemeProvider>
    </MemoryRouter>,
  )
}

describe('LoginPage', () => {
  it('submits tenant, username and password then enters the chat page', async () => {
    const user = userEvent.setup()
    mockLogin.mockResolvedValue()
    renderLogin()

    await user.type(screen.getByLabelText('租户 ID（可选）'), 't-1')
    await user.type(screen.getByLabelText('用户名'), 'alice')
    await user.type(screen.getByLabelText('口令'), 'secret')
    await user.click(screen.getByRole('button', { name: '登录' }))

    await waitFor(() => expect(mockLogin).toHaveBeenCalledTimes(1))
    expect(mockLogin).toHaveBeenCalledWith({
      tenantId: 't-1',
      userName: 'alice',
      password: 'secret',
    })
    await waitFor(() => expect(screen.getByText('chat home')).toBeInTheDocument())
  })

  it('shows the backend error message when login fails', async () => {
    const user = userEvent.setup()
    mockLogin.mockRejectedValue(new Error('invalid credentials'))
    renderLogin()

    await user.type(screen.getByLabelText('用户名'), 'alice')
    await user.type(screen.getByLabelText('口令'), 'wrong')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('invalid credentials')
    expect(screen.queryByText('chat home')).not.toBeInTheDocument()
  })

  it('keeps the submit button disabled while submitting', async () => {
    const user = userEvent.setup()
    let release!: () => void
    mockLogin.mockReturnValue(
      new Promise<void>((resolve) => {
        release = resolve
      }),
    )
    renderLogin()

    await user.type(screen.getByLabelText('用户名'), 'alice')
    await user.type(screen.getByLabelText('口令'), 'secret')
    await user.click(screen.getByRole('button', { name: '登录' }))

    expect(screen.getByRole('button', { name: '登录中…' })).toBeDisabled()
    release()
    await waitFor(() => expect(screen.getByText('chat home')).toBeInTheDocument())
  })
})
