import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ThinkingLevel } from '../../agui/thinking'
import { ThinkingPicker } from './ThinkingPicker'

afterEach(cleanup)

function renderPicker(overrides: {
  value?: ThinkingLevel
  onChange?: (level: ThinkingLevel) => void
  disabled?: boolean
} = {}) {
  const onChange = overrides.onChange ?? vi.fn()
  render(<ThinkingPicker value={overrides.value ?? 'medium'} onChange={onChange} disabled={overrides.disabled} />)
  return { onChange }
}

describe('ThinkingPicker', () => {
  it('shows_only_the_icon_and_the_current_level_in_the_trigger', () => {
    renderPicker({ value: 'high' })

    expect(screen.getByRole('button', { name: '思考强度：高' })).toBeInTheDocument()
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
  })

  it('opens_the_level_list_on_hover', async () => {
    const user = userEvent.setup()
    renderPicker({ value: 'medium' })

    await user.hover(screen.getByRole('button', { name: '思考强度：中' }))

    expect(await screen.findByRole('radiogroup', { name: '思考强度' })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: '中' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: '高' })).toHaveAttribute('aria-checked', 'false')
  })

  it('reports_the_selected_level_and_closes_the_popover', async () => {
    const user = userEvent.setup()
    const { onChange } = renderPicker({ value: 'medium' })

    await user.hover(screen.getByRole('button', { name: '思考强度：中' }))
    await user.click(await screen.findByRole('radio', { name: '高' }))

    expect(onChange).toHaveBeenCalledWith('high')
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
  })

  it('opens_from_the_keyboard', async () => {
    const user = userEvent.setup()
    renderPicker({ value: 'off' })

    await user.tab()
    expect(screen.getByRole('button', { name: '思考强度：关闭' })).toHaveFocus()
    await user.keyboard('{Enter}')

    expect(await screen.findByRole('radiogroup', { name: '思考强度' })).toBeInTheDocument()
  })

  it('does_not_open_while_disabled', async () => {
    const user = userEvent.setup()
    renderPicker({ value: 'medium', disabled: true })

    await user.hover(screen.getByRole('button', { name: '思考强度：中' }))

    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
  })
})
