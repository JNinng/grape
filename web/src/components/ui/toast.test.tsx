import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { TOAST_DURATION_MS, Toaster, clearToasts, dismissToast, toast } from './toast'

// toast 命令式入口在 React 事件之外改 store：测试里包 act 促使提交渲染。
function push(kind: 'success' | 'error' | 'info', description: string) {
  act(() => {
    toast[kind](description)
  })
}

beforeEach(() => {
  clearToasts()
})

afterEach(() => {
  cleanup()
  clearToasts()
})

describe('Toaster', () => {
  it('renders_a_success_toast_and_removes_it_via_the_close_button', () => {
    render(<Toaster />)
    push('success', 'saved')

    expect(screen.getByLabelText('通知')).toHaveTextContent('saved')
    // 成功类为非警报播报（role=status），关闭按钮独立于点击移除。
    const statusToast = screen.getByRole('status')
    expect(statusToast).toHaveTextContent('saved')

    fireEvent.click(screen.getByRole('button', { name: '关闭通知' }))
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('renders_an_error_toast_with_the_alert_role', () => {
    render(<Toaster />)
    push('error', 'boom')

    expect(screen.getByRole('alert')).toHaveTextContent('boom')
  })

  it('dismisses_by_clicking_the_toast_body', () => {
    render(<Toaster />)
    push('info', 'FYI')

    fireEvent.click(screen.getByRole('status'))
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('auto_dismisses_after_the_configured_duration', () => {
    vi.useFakeTimers()
    try {
      render(<Toaster />)
      push('success', 'temporary')

      expect(screen.getByRole('status')).toHaveTextContent('temporary')
      act(() => {
        vi.advanceTimersByTime(TOAST_DURATION_MS - 1)
      })
      expect(screen.getByRole('status')).toBeInTheDocument()
      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(screen.queryByRole('status')).not.toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('only_removes_the_targeted_toast_when_dismissed_by_id', () => {
    render(<Toaster />)
    push('success', 'first')
    push('success', 'second')

    const first = screen.getAllByRole('status')[0]
    const firstId = Number(first.getAttribute('data-id') ?? '')
    act(() => {
      dismissToast(firstId)
    })

    expect(screen.queryByText('first')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('second')
  })
})
