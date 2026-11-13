import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ModelCallDTO } from '@/api/agentModelCalls'
import { listModelCalls } from '@/api/agentModelCalls'
import { fetchMyPermissions } from '@/api/permissions'
import { getListSchema, type ListSchemaDTO } from '@/api/querySchema'
import { ModelCallManagePage } from './ModelCallManagePage'
import { SettingsLayout } from './SettingsLayout'

vi.mock('@/api/agentModelCalls', () => ({
  listModelCalls: vi.fn(),
}))

vi.mock('@/api/permissions', () => ({
  fetchMyPermissions: vi.fn(),
}))

vi.mock('@/api/querySchema', () => ({
  getListSchema: vi.fn(),
}))

const mockListModelCalls = vi.mocked(listModelCalls)
const mockFetchMyPermissions = vi.mocked(fetchMyPermissions)
const mockGetListSchema = vi.mocked(getListSchema)

// modelCallSchema 基线对齐 GetListSchema(agent_model_call_log) 响应口径。
function modelCallSchema(): ListSchemaDTO {
  return {
    resource: 'agent_model_call_log',
    hash: 'test-hash-model-call',
    fields: [
      { field: 'id', label: '留痕 ID', dataType: 'integer', sortable: true, visibility: 'optional', ops: ['eq', 'in'] },
      {
        field: 'scenario',
        label: '调用场景',
        dataType: 'string',
        visibility: 'optional',
        ops: ['eq', 'in'],
        options: [
          { value: 'ingest_summary', label: '摄取摘要' },
          { value: 'embed_document', label: '摄取向量化' },
          { value: 'parse_document', label: '外部解析' },
        ],
      },
      { field: 'modelKey', label: '模型键', dataType: 'string', visibility: 'optional', ops: ['eq', 'prefix'] },
      { field: 'status', label: '状态', dataType: 'string', visibility: 'optional', ops: ['eq', 'in'] },
      { field: 'durationMs', label: '耗时（毫秒）', dataType: 'integer', sortable: true, visibility: 'optional', ops: ['eq', 'gte', 'lte'] },
      { field: 'createdAt', label: '写入时间', dataType: 'datetime', sortable: true, visibility: 'optional', ops: ['eq', 'gt', 'gte', 'lt', 'lte', 'between'] },
    ],
    defaultSort: [{ field: 'createdAt', desc: true }],
    pagination: { defaultPageSize: 20, maxPageSize: 200, modes: ['offset', 'cursor'], includeTotal: true },
  }
}

function modelCall(overrides: Partial<ModelCallDTO> = {}): ModelCallDTO {
  return {
    id: '31',
    userId: '7',
    traceId: '0123456789abcdef0123456789abcdef',
    scenario: 'embed_document',
    modelKey: 'bge-m1024',
    endpoint: 'http://embed.example/v1/embeddings',
    documentId: '42',
    inputSummary: 'chunk body',
    outputSummary: 'embedding vector of 1024 dimensions (vector not persisted)',
    inputTokens: 96,
    durationMs: 18,
    status: 'success',
    createdBy: '7',
    createdAt: '2026-01-01T00:00:03Z',
    ...overrides,
  }
}

beforeEach(() => {
  mockFetchMyPermissions.mockRejectedValue(new Error('permission denied'))
  mockGetListSchema.mockResolvedValue(modelCallSchema())
  mockListModelCalls.mockResolvedValue({
    items: [
      modelCall(),
      modelCall({
        id: '32',
        scenario: 'parse_document',
        modelKey: undefined,
        endpoint: 'http://ocr.example/parse',
        documentId: '7',
        inputSummary: 'mime=application/pdf filename=paper.pdf',
        outputSummary: undefined,
        inputTokens: undefined,
        durationMs: 1200,
        status: 'error',
        error: 'external parse service status 422: parse failed',
        createdAt: '2026-01-01T00:00:04Z',
      }),
    ],
    total: '2',
    hasMore: false,
    nextCursor: '',
    page: 1,
  })
})

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

// renderPage 以真实布局挂载模型调用页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/model-calls']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="model-calls" element={<ModelCallManagePage />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  )
}

describe('ModelCallManagePage dynamic list', () => {
  it('loads_model_calls_with_the_schema_default_sort_on_mount', async () => {
    renderPage()

    expect(await screen.findByRole('heading', { name: '模型调用' })).toBeInTheDocument()
    expect(mockGetListSchema).toHaveBeenCalledWith('agent_model_call_log')
    expect(mockListModelCalls).toHaveBeenCalledTimes(1)
    expect(mockListModelCalls.mock.calls[0][0]).toEqual({
      where: [],
      sort: [{ field: 'createdAt', order: 'desc' }],
      pagination: { page: 1, pageSize: 20 },
    })
  })

  it('renders_scenario_labels_and_status_badges', async () => {
    renderPage()

    expect(await screen.findByText('摄取向量化')).toBeInTheDocument()
    expect(screen.getByText('外部解析')).toBeInTheDocument()
    expect(screen.getAllByText('成功')).toHaveLength(1)
    expect(screen.getAllByText('失败')).toHaveLength(1)
  })

  it('opens_the_detail_dialog_with_redacted_summaries_and_error', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('摄取向量化')

    await user.click(screen.getByRole('button', { name: '操作 #32' }))
    const menu = await screen.findByRole('menu')
    await user.click(within(menu).getByRole('menuitem', { name: '详情' }))

    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByText('模型调用详情 #32')).toBeInTheDocument()
    expect(within(dialog).getByText('external parse service status 422: parse failed')).toBeInTheDocument()
    expect(within(dialog).getByText('mime=application/pdf filename=paper.pdf')).toBeInTheDocument()
    // 未上报字段出占位，不以 0 冒充。
    expect(within(dialog).getAllByText('—').length).toBeGreaterThanOrEqual(1)
  })

  it('surfaces_a_retryable_alert_when_the_schema_fails', async () => {
    mockGetListSchema.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    renderPage()

    expect(await screen.findByRole('alert')).toHaveTextContent('查询能力加载失败：boom')

    mockGetListSchema.mockResolvedValue(modelCallSchema())
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('摄取向量化')).toBeInTheDocument()
  })
})
