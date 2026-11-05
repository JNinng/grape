import { useState } from 'react'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { OptionDTO, OptionQueryInput } from '@/api/assignOptions'
import { OptionSelect, optionLabel, type OptionPage } from './option-select'

function option(value: string, label: string, overrides: Partial<OptionDTO> = {}): OptionDTO {
  return { value, label, enabled: true, reason: '', assigned: false, ...overrides }
}

function page(items: OptionDTO[], hasMore = false, pageNo = 1): OptionPage {
  return { items, hasMore, page: pageNo }
}

// Harness 把选择值提升为受控状态，模拟弹窗内表单的用法。
function Harness({
  mode = 'multi',
  initial = [],
  loader,
  initialPage,
  selectedLabels,
  pinnedValues,
}: {
  mode?: 'single' | 'multi'
  initial?: string[]
  loader: (query: OptionQueryInput) => Promise<OptionPage>
  initialPage?: OptionPage
  selectedLabels?: Record<string, string>
  pinnedValues?: string[]
}) {
  const [value, setValue] = useState(initial)
  return (
    <div>
      <OptionSelect
        ariaLabel="角色"
        mode={mode}
        value={value}
        onChange={setValue}
        loadOptions={loader}
        initialPage={initialPage}
        selectedLabels={selectedLabels}
        pinnedValues={pinnedValues}
        placeholder="选择角色"
      />
      <div data-testid="value">{value.join(',')}</div>
    </div>
  )
}

afterEach(() => {
  cleanup()
})

describe('OptionSelect', () => {
  it('loads_the_first_page_only_after_the_popover_opens', async () => {
    const user = userEvent.setup()
    const loader = vi.fn().mockResolvedValue(page([option('103', '普通用户')]))

    render(<Harness loader={loader} />)
    expect(loader).not.toHaveBeenCalled()
    expect(screen.getByText('选择角色')).toBeInTheDocument()

    await user.click(screen.getByRole('combobox', { name: '角色' }))

    expect(await screen.findByRole('option', { name: /普通用户/ })).toBeInTheDocument()
    expect(loader).toHaveBeenCalledWith({ keyword: '', page: 1, pageSize: 20 })
  })

  it('uses_the_prefetched_first_page_without_a_second_request', async () => {
    const user = userEvent.setup()
    const loader = vi.fn()

    render(<Harness loader={loader} initialPage={page([option('104', '审计员')])} />)
    await user.click(screen.getByRole('combobox', { name: '角色' }))

    expect(await screen.findByRole('option', { name: /审计员/ })).toBeInTheDocument()
    expect(loader).not.toHaveBeenCalled()
  })

  it('debounces_remote_search_by_keyword', async () => {
    const user = userEvent.setup()
    const loader = vi
      .fn()
      .mockResolvedValueOnce(page([option('103', '普通用户')]))
      .mockResolvedValueOnce(page([option('110', '测试管理员')]))

    render(<Harness loader={loader} />)
    await user.click(screen.getByRole('combobox', { name: '角色' }))
    expect(await screen.findByRole('option', { name: /普通用户/ })).toBeInTheDocument()

    await user.type(screen.getByRole('combobox', { name: '角色搜索' }), '管理')

    // 防抖窗口内不重查；250ms 后按关键字回到第一页。
    expect(loader).toHaveBeenCalledTimes(1)
    await waitFor(
      () => expect(loader).toHaveBeenLastCalledWith({ keyword: '管理', page: 1, pageSize: 20 }),
      { timeout: 1500 },
    )
    expect(await screen.findByRole('option', { name: /测试管理员/ })).toBeInTheDocument()
  })

  it('appends_the_next_page_when_more_is_requested', async () => {
    const user = userEvent.setup()
    const loader = vi.fn().mockResolvedValueOnce(page([option('103', '普通用户')], true, 1))
    loader.mockResolvedValueOnce(page([option('104', '审计员')], false, 2))

    render(<Harness loader={loader} />)
    await user.click(screen.getByRole('combobox', { name: '角色' }))
    expect(await screen.findByRole('option', { name: /普通用户/ })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '加载更多' }))

    expect(await screen.findByRole('option', { name: /审计员/ })).toBeInTheDocument()
    expect(loader).toHaveBeenLastCalledWith({ keyword: '', page: 2, pageSize: 20 })
    expect(screen.getByRole('option', { name: /普通用户/ })).toBeInTheDocument()
  })

  it('toggles_multi_selection_and_keeps_the_popover_open', async () => {
    const user = userEvent.setup()
    const loader = vi.fn().mockResolvedValue(
      page([option('103', '普通用户'), option('104', '审计员')]),
    )

    render(<Harness loader={loader} />)
    await user.click(screen.getByRole('combobox', { name: '角色' }))
    const normal = await screen.findByRole('option', { name: /普通用户/ })
    await user.click(normal)

    expect(screen.getByTestId('value')).toHaveTextContent('103')
    expect(normal).toHaveAttribute('data-checked', 'true')
    expect(screen.getByRole('option', { name: /审计员/ })).toBeInTheDocument()

    await user.click(screen.getByRole('option', { name: /普通用户/ }))
    expect(screen.getByTestId('value')).toHaveTextContent('')
  })

  it('selects_a_single_value_and_closes_the_popover', async () => {
    const user = userEvent.setup()
    const loader = vi.fn().mockResolvedValue(
      page([option('103', '普通用户'), option('104', '审计员')]),
    )

    render(<Harness mode="single" loader={loader} />)
    await user.click(screen.getByRole('combobox', { name: '角色' }))
    await user.click(await screen.findByRole('option', { name: /审计员/ }))

    expect(screen.getByTestId('value')).toHaveTextContent('104')
    await waitFor(() => expect(screen.queryByRole('option')).not.toBeInTheDocument())
    expect(screen.getByRole('combobox', { name: '角色' })).toHaveTextContent('审计员')
  })

  it('clears_the_selection_from_the_popover', async () => {
    const user = userEvent.setup()
    const loader = vi.fn().mockResolvedValue(page([]))

    render(<Harness mode="single" initial={['8']} selectedLabels={{ '8': '张三' }} loader={loader} />)
    expect(screen.getByRole('combobox', { name: '角色' })).toHaveTextContent('张三')

    await user.click(screen.getByRole('combobox', { name: '角色' }))
    await user.click(await screen.findByRole('button', { name: '清除选择' }))

    expect(screen.getByTestId('value')).toHaveTextContent('')
  })

  it('keeps_pinned_values_listed_after_clear_and_allows_reselecting', async () => {
    const user = userEvent.setup()
    const loader = vi.fn().mockResolvedValue(page([]))

    render(
      <Harness
        mode="single"
        initial={['8']}
        pinnedValues={['8']}
        selectedLabels={{ '8': '张三' }}
        loader={loader}
      />,
    )
    const trigger = screen.getByRole('combobox', { name: '角色' })
    expect(trigger).toHaveTextContent('张三')

    await user.click(trigger)
    const pinned = await screen.findByRole('option', { name: /张三/ })
    expect(pinned).toHaveAttribute('data-checked', 'true')

    await user.click(screen.getByRole('button', { name: '清除选择' }))
    expect(screen.getByTestId('value')).toHaveTextContent('')
    // 清除后固定项仍在列表里，可再次勾选。
    expect(pinned).toHaveAttribute('data-checked', 'false')
    await user.click(pinned)
    expect(screen.getByTestId('value')).toHaveTextContent('8')
  })

  it('filters_pinned_values_locally_while_searching', async () => {
    const user = userEvent.setup()
    const loader = vi.fn(async (q: OptionQueryInput) =>
      q.keyword === '' ? page([option('103', '普通用户')]) : page([]),
    )

    render(<Harness pinnedValues={['110']} selectedLabels={{ '110': '测试管理员' }} loader={loader} />)
    await user.click(screen.getByRole('combobox', { name: '角色' }))
    expect(await screen.findByRole('option', { name: /测试管理员/ })).toBeInTheDocument()

    await user.type(screen.getByRole('combobox', { name: '角色搜索' }), '测试')
    await waitFor(() => expect(loader).toHaveBeenLastCalledWith({ keyword: '测试', page: 1, pageSize: 20 }))
    // 远程来源不返回固定项时，本地按 label 匹配继续展示。
    expect(await screen.findByRole('option', { name: /测试管理员/ })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /普通用户/ })).not.toBeInTheDocument()
  })

  it('falls_back_to_the_raw_id_when_a_selected_value_has_no_label', () => {
    expect(optionLabel('99', {})).toBe('ID 99（不可见）')
    expect(optionLabel('99', { '99': '张三' })).toBe('张三')
    expect(optionLabel('99', { '99': '' })).toBe('ID 99（不可见）')
  })

  it('keeps_a_disabled_option_visible_with_its_reason_but_not_selectable', async () => {
    const user = userEvent.setup()
    const loader = vi.fn().mockResolvedValue(
      page([option('120', '受限角色', { enabled: false, reason: '目标不满足分发条件' })]),
    )

    render(<Harness loader={loader} />)
    await user.click(screen.getByRole('combobox', { name: '角色' }))
    const item = await screen.findByRole('option', { name: /受限角色/ })
    expect(screen.getByText('目标不满足分发条件')).toBeInTheDocument()

    await user.click(item)
    expect(screen.getByTestId('value')).toHaveTextContent('')
  })

  it('surfaces_load_errors_inline', async () => {
    const user = userEvent.setup()
    const loader = vi.fn().mockRejectedValue(new Error('load failed'))

    render(<Harness loader={loader} />)
    await user.click(screen.getByRole('combobox', { name: '角色' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('load failed')
  })

  it('shows_the_empty_state_when_the_source_returns_nothing', async () => {
    const user = userEvent.setup()
    const loader = vi.fn().mockResolvedValue(page([]))

    render(<Harness loader={loader} />)
    await user.click(screen.getByRole('combobox', { name: '角色' }))

    expect(await screen.findByText('没有匹配的选项')).toBeInTheDocument()
  })
})
