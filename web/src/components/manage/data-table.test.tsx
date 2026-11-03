import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DynamicQuery, QuerySet } from '@/api/query'
import { DataTable, type DataTableColumn } from './data-table'

interface Row {
  id: string
  name: string
}

function pageSet(items: Row[], extra?: Partial<QuerySet<Row>>): QuerySet<Row> {
  return { items, hasMore: false, nextCursor: '', page: 1, ...extra }
}

const columns: DataTableColumn<Row>[] = [
  { key: 'name', header: '名称', sortable: 'name', render: (row) => row.name },
  { key: 'id', header: 'ID', render: (row) => row.id },
]

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  cleanup()
})

// deferred 构造手动放行的取数承诺：用于时序与竞态用例的确定性控制。
function deferred(): { promise: Promise<QuerySet<Row>>; resolve: (v: QuerySet<Row>) => void; reject: (e: Error) => void } {
  let resolve!: (v: QuerySet<Row>) => void
  let reject!: (e: Error) => void
  const promise = new Promise<QuerySet<Row>>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('DataTable rendering', () => {
  it('fetches_the_first_page_on_mount_and_renders_rows_by_columns', async () => {
    const fetchPage = vi.fn().mockResolvedValue(pageSet([{ id: '1', name: 'alice' }]))
    render(
      <DataTable
        rowKey={(row) => row.id}
        columns={columns}
        fetchPage={fetchPage}
      />,
    )

    await screen.findByText('alice')
    expect(fetchPage).toHaveBeenCalledTimes(1)
    expect(fetchPage.mock.calls[0][0]).toEqual({
      where: [],
      sort: undefined,
      pagination: { page: 1, pageSize: 20 },
    })
    // 表头渲染列定义；非排序列不渲染排序按钮。
    expect(screen.getByRole('columnheader', { name: 'ID' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /名称/ })).toBeInTheDocument()
  })

  it('shows_the_empty_state_when_the_page_has_no_rows', async () => {
    const fetchPage = vi.fn().mockResolvedValue(pageSet([]))
    render(
      <DataTable rowKey={(row) => row.id} columns={columns} fetchPage={fetchPage} emptyText="nothing here" />,
    )

    await screen.findByText('nothing here')
    expect(screen.queryByText('alice')).not.toBeInTheDocument()
  })

  it('shows_a_retryable_alert_when_the_fetch_fails', async () => {
    const fetchPage = vi
      .fn()
      .mockRejectedValueOnce(new Error('backend down'))
      .mockResolvedValue(pageSet([{ id: '1', name: 'alice' }]))
    const user = userEvent.setup()
    render(<DataTable rowKey={(row) => row.id} columns={columns} fetchPage={fetchPage} />)

    expect(await screen.findByRole('alert')).toHaveTextContent('列表加载失败：backend down')

    await user.click(screen.getByRole('button', { name: '重试' }))
    await screen.findByText('alice')
    expect(fetchPage).toHaveBeenCalledTimes(2)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows_the_generic_fallback_message_when_the_fetch_rejects_a_non_error_value', async () => {
    const fetchPage = vi.fn().mockRejectedValue('raw backend failure')
    render(<DataTable rowKey={(row) => row.id} columns={columns} fetchPage={fetchPage} />)

    // 非 Error 拒绝值不读 message 属性，落到通用兜底文案。
    expect(await screen.findByRole('alert')).toHaveTextContent('列表加载失败：加载失败')
  })

  it('summarizes_the_total_when_the_envelope_carries_one', async () => {
    const fetchPage = vi.fn().mockResolvedValue(pageSet([{ id: '1', name: 'alice' }], { total: '41', hasMore: true }))
    render(<DataTable rowKey={(row) => row.id} columns={columns} fetchPage={fetchPage} />)

    await screen.findByText('alice')
    expect(screen.getByLabelText('列表统计')).toHaveTextContent('共 41 条')
    expect(screen.getByText('第 1 页 / 3 页')).toBeInTheDocument()
  })
})

describe('DataTable pagination', () => {
  it('flips_pages_only_within_available_range_and_passes_the_page_number', async () => {
    const fetchPage = vi.fn()
    fetchPage.mockImplementation(async (query: DynamicQuery) =>
      pageSet([{ id: String(query.pagination?.page), name: `row-${query.pagination?.page}` }], {
        hasMore: (query.pagination?.page ?? 1) < 2,
        total: '40',
      }),
    )
    const user = userEvent.setup()
    render(<DataTable rowKey={(row) => row.id} columns={columns} fetchPage={fetchPage} />)

    await screen.findByText('row-1')
    // 首页禁用上一页。
    expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: '下一页' }))
    await screen.findByText('row-2')
    const secondCall = fetchPage.mock.calls[1][0] as DynamicQuery
    expect(secondCall.pagination).toEqual({ page: 2, pageSize: 20 })
    // 末页禁用下一页（hasMore=false）。
    expect(screen.getByRole('button', { name: '下一页' })).toBeDisabled()
  })
})

describe('DataTable sorting', () => {
  it('cycles_asc_desc_and_back_to_default_on_header_clicks', async () => {
    const defaultSort = [{ field: 'createdAt', order: 'desc' as const }]
    const fetchPage = vi.fn().mockResolvedValue(pageSet([{ id: '1', name: 'alice' }]))
    const user = userEvent.setup()
    render(
      <DataTable
        rowKey={(row) => row.id}
        columns={columns}
        fetchPage={fetchPage}
        defaultSort={defaultSort}
      />,
    )
    await screen.findByText('alice')
    expect(fetchPage.mock.calls[0][0]).toMatchObject({ sort: defaultSort })

    await user.click(screen.getByRole('button', { name: /名称/ }))
    await waitFor(() =>
      expect(fetchPage.mock.calls[1][0]).toMatchObject({ sort: [{ field: 'name', order: 'asc' }] }),
    )

    await user.click(screen.getByRole('button', { name: /名称/ }))
    await waitFor(() =>
      expect(fetchPage.mock.calls[2][0]).toMatchObject({ sort: [{ field: 'name', order: 'desc' }] }),
    )

    await user.click(screen.getByRole('button', { name: /名称/ }))
    await waitFor(() => expect(fetchPage.mock.calls[3][0]).toMatchObject({ sort: defaultSort }))
    expect(screen.getByRole('columnheader', { name: /名称/ })).not.toHaveAttribute('aria-sort')
  })
})

describe('DataTable where / reload', () => {
  it('restarts_from_page_one_when_the_where_conditions_change', async () => {
    const fetchPage = vi.fn().mockResolvedValue(pageSet([{ id: '1', name: 'alice' }], { hasMore: true }))
    const user = userEvent.setup()
    const { rerender } = render(
      <DataTable
        rowKey={(row) => row.id}
        columns={columns}
        fetchPage={fetchPage}
        where={[{ field: 'status', op: 'eq', value: 1 }]}
      />,
    )
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: '下一页' }))
    await waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(2))

    rerender(
      <DataTable
        rowKey={(row) => row.id}
        columns={columns}
        fetchPage={fetchPage}
        where={[{ field: 'status', op: 'eq', value: 0 }]}
      />,
    )

    await waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(3))
    expect(fetchPage.mock.calls[2][0]).toMatchObject({
      where: [{ field: 'status', op: 'eq', value: 0 }],
      pagination: { page: 1 },
    })
    // 相同条件引用不变：不因数组身份变化而重查。
    rerender(
      <DataTable
        rowKey={(row) => row.id}
        columns={columns}
        fetchPage={fetchPage}
        where={[{ field: 'status', op: 'eq', value: 0 }]}
      />,
    )
    await waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(3))
  })

  it('returns_to_page_one_and_refetches_when_the_reload_key_increases', async () => {
    const fetchPage = vi.fn().mockResolvedValue(pageSet([{ id: '1', name: 'alice' }], { hasMore: true }))
    const user = userEvent.setup()
    const { rerender } = render(
      <DataTable rowKey={(row) => row.id} columns={columns} fetchPage={fetchPage} />,
    )
    await screen.findByText('alice')
    await user.click(screen.getByRole('button', { name: '下一页' }))
    await waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(2))

    rerender(<DataTable rowKey={(row) => row.id} columns={columns} fetchPage={fetchPage} reloadKey={1} />)

    await waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(3))
    expect((fetchPage.mock.calls[2][0] as DynamicQuery).pagination?.page).toBe(1)
  })
})

describe('DataTable response races', () => {
  it('ignores_a_stale_page_response_that_lands_after_the_latest_request', async () => {
    const first = deferred()
    const second = deferred()
    const fetchPage = vi
      .fn()
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise)
    const user = userEvent.setup()
    render(
      <DataTable
        rowKey={(row) => row.id}
        columns={columns}
        fetchPage={fetchPage}
        defaultSort={[{ field: 'name', order: 'asc' }]}
      />,
    )

    await waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(1))
    await user.click(screen.getByRole('button', { name: /名称/ }))
    await waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(2))

    // 旧响应后到：内容与统计均被丢弃，不覆盖新结果。
    first.resolve(pageSet([{ id: 'stale', name: 'stale-row' }], { total: '999' }))
    second.resolve(pageSet([{ id: 'fresh', name: 'fresh-row' }]))
    await screen.findByText('fresh-row')
    expect(screen.queryByText('stale-row')).not.toBeInTheDocument()
    expect(screen.getByLabelText('列表统计')).toHaveTextContent('已加载 1 条')
  })
})
