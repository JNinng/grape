import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { changeMyPassword, getMyProfile, updateMyProfile, type UserDTO } from '@/api/users'
import { ApiError } from '@/api/toApiError'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { ProfilePage } from './ProfilePage'

vi.mock('@/api/users', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/users')>()),
  getMyProfile: vi.fn(),
  updateMyProfile: vi.fn(),
  changeMyPassword: vi.fn(),
}))

const mockGetMyProfile = vi.mocked(getMyProfile)
const mockUpdateMyProfile = vi.mocked(updateMyProfile)
const mockChangeMyPassword = vi.mocked(changeMyPassword)

function profile(overrides: Partial<UserDTO> = {}): UserDTO {
  return {
    id: '11',
    name: 'alice',
    nickname: 'Alice',
    email: 'alice@example.com',
    phone: '13800000000',
    remark: 'hello',
    locale: 'zh-CN',
    createdBy: '11',
    updatedBy: '11',
    createdAt: '2026-01-01T08:00:00Z',
    ...overrides,
  }
}

function renderPage() {
  return render(
    <>
      <ProfilePage />
      <Toaster />
    </>,
  )
}

beforeEach(() => {
  clearToasts()
  mockGetMyProfile.mockResolvedValue(profile())
  mockUpdateMyProfile.mockImplementation(async (input) => profile({
    nickname: input.nickname,
    email: input.email,
    phone: input.phone,
    remark: input.remark,
    locale: input.locale ?? 'zh-CN',
  }))
})

afterEach(() => {
  cleanup()
  clearToasts()
  vi.resetAllMocks()
})

describe('ProfilePage load', () => {
  it('prefills_the_editable_fields_and_shows_the_username_readonly', async () => {
    renderPage()

    expect(await screen.findByDisplayValue('Alice')).toBeInTheDocument()
    expect(screen.getByText('@alice')).toBeInTheDocument()
    expect(screen.queryByLabelText('用户名')).not.toBeInTheDocument()
    expect(screen.getByLabelText('邮箱')).toHaveValue('alice@example.com')
    expect(screen.getByLabelText('手机号')).toHaveValue('13800000000')
    expect(screen.getByLabelText('语言（BCP-47）')).toHaveValue('zh-CN')
  })

  it('offers_a_retry_when_the_profile_fails_to_load', async () => {
    const user2 = userEvent.setup()
    mockGetMyProfile.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce(profile())
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('个人资料加载失败：boom')
    await user2.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByDisplayValue('Alice')).toBeInTheDocument()
  })
})

describe('ProfilePage save', () => {
  it('sends_the_editable_fields_and_clears_blank_optionals', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByDisplayValue('Alice')

    await user2.clear(screen.getByLabelText('昵称'))
    await user2.type(screen.getByLabelText('昵称'), 'Alice2')
    await user2.clear(screen.getByLabelText('邮箱'))
    await user2.clear(screen.getByLabelText('手机号'))
    await user2.clear(screen.getByLabelText('备注'))
    await user2.type(screen.getByLabelText('备注'), 'note')
    await user2.clear(screen.getByLabelText('语言（BCP-47）'))
    await user2.type(screen.getByLabelText('语言（BCP-47）'), 'en-US')
    await user2.click(screen.getByRole('button', { name: '保存资料' }))

    await waitFor(() =>
      expect(mockUpdateMyProfile).toHaveBeenCalledWith({
        nickname: 'Alice2',
        email: undefined,
        phone: undefined,
        remark: 'note',
        locale: 'en-US',
      }),
    )
    expect(await screen.findByRole('status')).toHaveTextContent('个人资料已更新')
  })

  it('rejects_a_blank_nickname_before_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByDisplayValue('Alice')

    await user2.clear(screen.getByLabelText('昵称'))
    await user2.click(screen.getByRole('button', { name: '保存资料' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('nickname is required')
    expect(mockUpdateMyProfile).not.toHaveBeenCalled()
  })
})

describe('ProfilePage password', () => {
  it('requires_matching_new_passwords_before_calling_the_api', async () => {
    const user2 = userEvent.setup()
    renderPage()
    await screen.findByDisplayValue('Alice')

    await user2.type(screen.getByLabelText('当前密码'), 'old-pass')
    await user2.type(screen.getByLabelText('新密码'), 'new-pass')
    await user2.type(screen.getByLabelText('确认新密码'), 'other-pass')
    await user2.click(screen.getByRole('button', { name: '修改密码' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('new passwords do not match')
    expect(mockChangeMyPassword).not.toHaveBeenCalled()
  })

  it('shows_the_current_password_error_without_leaving_the_page', async () => {
    const user2 = userEvent.setup()
    mockChangeMyPassword.mockRejectedValue(new ApiError('password mismatch', 'USER_PASSWORD_MISMATCH'))
    renderPage()
    await screen.findByDisplayValue('Alice')

    await user2.type(screen.getByLabelText('当前密码'), 'wrong-pass')
    await user2.type(screen.getByLabelText('新密码'), 'new-pass')
    await user2.type(screen.getByLabelText('确认新密码'), 'new-pass')
    await user2.click(screen.getByRole('button', { name: '修改密码' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('current password is incorrect')
    expect(mockChangeMyPassword).toHaveBeenCalledWith('wrong-pass', 'new-pass')
  })

  it('clears_the_password_fields_after_a_successful_change', async () => {
    const user2 = userEvent.setup()
    mockChangeMyPassword.mockResolvedValue(undefined)
    renderPage()
    await screen.findByDisplayValue('Alice')

    await user2.type(screen.getByLabelText('当前密码'), 'old-pass')
    await user2.type(screen.getByLabelText('新密码'), 'new-pass')
    await user2.type(screen.getByLabelText('确认新密码'), 'new-pass')
    await user2.click(screen.getByRole('button', { name: '修改密码' }))

    await waitFor(() => expect(mockChangeMyPassword).toHaveBeenCalledWith('old-pass', 'new-pass'))
    expect(await screen.findByRole('status')).toHaveTextContent('密码已更新')
    expect(screen.getByLabelText('当前密码')).toHaveValue('')
    expect(screen.getByLabelText('新密码')).toHaveValue('')
    expect(screen.getByLabelText('确认新密码')).toHaveValue('')
  })
})
