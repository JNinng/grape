import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { RowActionsMenu } from './row-actions'

afterEach(() => {
  cleanup()
})

// renderMenu 挂载带摘要字段与动作的行操作弹框。
function renderMenu(onEdit: () => void, onDelete: () => void) {
  return render(
    <RowActionsMenu
      rowLabel="alice"
      info={[
        { label: '用户名', value: 'alice' },
        { label: '备注', value: '' },
      ]}
      actions={[
        { label: '编辑', onSelect: onEdit },
        { label: '删除', destructive: true, onSelect: onDelete },
      ]}
    />,
  )
}

describe('RowActionsMenu', () => {
  it('opens_on_hover_and_shows_the_configured_info_fields', async () => {
    const user2 = userEvent.setup()
    renderMenu(vi.fn(), vi.fn())

    // 悬停即弹出：顶部按 info 配置展示行摘要（空值出占位横线）。
    await user2.hover(screen.getByRole('button', { name: '操作 alice' }))
    const menu = await screen.findByRole('menu')
    expect(within(menu).getByText('用户名')).toBeInTheDocument()
    expect(within(menu).getByText('alice')).toBeInTheDocument()
    expect(within(menu).getByText('—')).toBeInTheDocument()
  })

  it('runs_actions_from_the_hover_opened_menu', async () => {
    const onEdit = vi.fn()
    const onDelete = vi.fn()
    const user2 = userEvent.setup()
    renderMenu(onEdit, onDelete)

    // 悬停打开后指针移入弹框（取消收起计时），点动作即执行并收起弹框。
    await user2.hover(screen.getByRole('button', { name: '操作 alice' }))
    const menu = await screen.findByRole('menu')
    await user2.hover(menu)
    await user2.click(within(menu).getByRole('menuitem', { name: '编辑' }))
    expect(onEdit).toHaveBeenCalledTimes(1)
    expect(onDelete).not.toHaveBeenCalled()
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  })
})
