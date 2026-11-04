import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DynamicQuery, QuerySet } from '@/api/query'
import type { ListSchemaDTO } from '@/api/querySchema'
import { DataTable, schemaColumns, type DataTableColumn } from './data-table'

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

  it('summarizes_the_total_in_the_bottom_bar_with_the_page_label_near_the_pager', async () => {
    const fetchPage = vi.fn().mockResolvedValue(pageSet([{ id: '1', name: 'alice' }], { total: '41', hasMore: true }))
    render(<DataTable rowKey={(row) => row.id} columns={columns} fetchPage={fetchPage} />)

    await screen.findByText('alice')
    expect(screen.getByLabelText('列表统计')).toHaveTextContent('共 41 条')
    expect(screen.getByText('第 1 / 3 页')).toBeInTheDocument()
  })

  it('changes_the_page_size_from_the_bottom_bar_selector_and_returns_to_page_one', async () => {
    const fetchPage = vi
      .fn()
      .mockResolvedValueOnce(pageSet([{ id: '1', name: 'alice' }], { total: '41', hasMore: true }))
      .mockResolvedValue(pageSet([{ id: '2', name: 'bob' }], { total: '41', hasMore: true }))
    const user = userEvent.setup()
    render(<DataTable rowKey={(row) => row.id} columns={columns} fetchPage={fetchPage} />)

    await screen.findByText('alice')
    await user.click(screen.getByRole('combobox', { name: '每页条数' }))
    await user.click(await screen.findByRole('option', { name: '10 条/页' }))

    await waitFor(() =>
      expect(fetchPage.mock.calls[1][0]).toMatchObject({ pagination: { page: 1, pageSize: 10 } }),
    )
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

describe('schemaColumns', () => {
  // schemaFixture 覆盖 hideable 门控、sortable 声明、可空 datetime 缺省
  // 渲染与 label 长文案。
  const schemaFixture: ListSchemaDTO = {
    resource: 'demo',
    hash: 'h',
    fields: [
      { field: 'id', label: 'ID', dataType: 'integer', sortable: true, hideable: true, ops: ['eq'] },
      { field: 'name', label: '名称', dataType: 'string', hideable: true, ops: ['contains'] },
      { field: 'deletedAt', label: '删除时间', dataType: 'datetime', nullable: true, sortable: true, hideable: true, ops: ['between'] },
      { field: 'active', label: '启用', dataType: 'boolean', hideable: true, ops: ['eq'] },
      { field: 'secret', label: '机密', dataType: 'string', ops: ['contains'] },
    ],
  }

  it('builds_columns_from_hideable_fields_in_schema_order_with_sortable_flags', () => {
    const cols = schemaColumns<Row>(schemaFixture)

    expect(cols.map((column) => column.key)).toEqual(['id', 'name', 'deletedAt', 'active'])
    expect(cols.map((column) => column.header)).toEqual(['ID', '名称', '删除时间', '启用'])
    expect(cols[0]?.sortable).toBe('id')
    expect(cols[2]?.sortable).toBe('deletedAt')
    expect(cols[1]?.sortable).toBeUndefined()
    expect(cols.every((column) => column.field === column.key)).toBe(true)
  })

  it('applies_header_and_render_overrides_and_appends_extra_columns', () => {
    const cols = schemaColumns<Row>(
      schemaFixture,
      {
        name: { header: '用户名', render: (row) => `named:${row.name}` },
        id: { className: 'w-16' },
      },
      [{ key: 'actions', header: '操作', render: () => 'ops' }],
    )

    expect(cols.map((column) => column.header)).toEqual(['ID', '用户名', '删除时间', '启用', '操作'])
    expect(cols[0]?.className).toBe('w-16')
    expect(cols[1]?.render({ id: '1', name: 'alice' })).toBe('named:alice')
    // 操作列不声明 field，恒可见且不参与显隐。
    expect(cols[4]?.field).toBeUndefined()
  })

  it('renders_default_values_with_dash_for_nullables_and_datetime_formatting', () => {
    const cols = schemaColumns<{ id: string; name: string; deletedAt?: string; active: boolean }>(schemaFixture)
    const deletedAtColumn = cols[2]!
    const activeColumn = cols[3]!

    // 可空缺省出破折号；datetime 合法值格式化本地时间、非法值回退原文；
    // boolean 出是/否。
    expect(deletedAtColumn.render({ id: '1', name: 'a', active: false })).toBe('—')
    expect(deletedAtColumn.render({ id: '1', name: 'a', active: false, deletedAt: '2026-01-01T08:00:00Z' })).toBe(
      new Date('2026-01-01T08:00:00Z').toLocaleString(),
    )
    expect(deletedAtColumn.render({ id: '1', name: 'a', active: false, deletedAt: 'not-a-date' })).toBe('not-a-date')
    expect(activeColumn.render({ id: '1', name: 'a', active: true })).toBe('是')
    expect(activeColumn.render({ id: '1', name: 'a', active: false })).toBe('否')
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

  it('offers_a_clear_sort_entry_only_while_sorting_deviates_from_the_default', async () => {
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
    // 未偏离默认排序时不出现清除入口。
    expect(screen.queryByRole('button', { name: '清除排序' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /名称/ }))
    expect(await screen.findByRole('button', { name: '清除排序' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '清除排序' }))

    await waitFor(() => expect(fetchPage.mock.calls[2][0]).toMatchObject({ sort: defaultSort }))
    expect(screen.queryByRole('button', { name: '清除排序' })).not.toBeInTheDocument()
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
