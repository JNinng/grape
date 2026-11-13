import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ModelDTO } from '@/api/models'
import { createModel, listModels, updateModel, updateModelSuper } from '@/api/models'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { SettingsLayout } from './SettingsLayout'
import { ModelManagePage } from './ModelManagePage'

vi.mock('@/api/models', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/models')>()),
  listModels: vi.fn(),
  createModel: vi.fn(),
  createModelSuper: vi.fn(),
  updateModel: vi.fn(),
  updateModelSuper: vi.fn(),
}))

const mockListModels = vi.mocked(listModels)
const mockCreateModel = vi.mocked(createModel)
const mockUpdateModel = vi.mocked(updateModel)
const mockUpdateModelSuper = vi.mocked(updateModelSuper)

// model 构造模型 DTO 基线：可选字段缺省即未设置（protojson 口径）。
function model(overrides: Partial<ModelDTO> = {}): ModelDTO {
  return {
    id: '5',
    platform: false,
    status: 1,
    kind: 'text',
    name: 'Qwen3 8B',
    modelKey: 'qwen3:8b',
    provider: 'openai',
    model: 'qwen3:8b',
    hasApiKey: false,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  clearToasts()
  mockListModels.mockResolvedValue({ items: [model()] })
  mockCreateModel.mockResolvedValue(model({ id: '6' }))
  mockUpdateModel.mockResolvedValue(model())
  mockUpdateModelSuper.mockResolvedValue(model({ platform: true }))
})

afterEach(() => {
  cleanup()
  clearToasts()
  vi.resetAllMocks()
})

// renderPage 以真实布局挂载模型页：顺带覆盖子路由经 Outlet 的装配。
function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/settings/models']}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="models" element={<ModelManagePage />} />
        </Route>
      </Routes>
      <Toaster />
    </MemoryRouter>,
  )
}

describe('ModelManagePage rerank kind', () => {
  it('filter_selects_rerank_and_passes_it_to_list_models', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('Qwen3 8B')

    await user.click(screen.getByRole('combobox', { name: '模型类别' }))
    await user.click(screen.getByRole('option', { name: '重排模型' }))
    await waitForListCall('rerank')

    expect(mockListModels).toHaveBeenLastCalledWith('rerank')
  })

  it('table_renders_rerank_kind_label', async () => {
    mockListModels.mockResolvedValue({
      items: [model({ id: '9', kind: 'rerank', name: 'BGE Reranker', modelKey: 'bge-reranker:0.6b' })],
    })
    renderPage()

    const row = await screen.findByText('BGE Reranker')
    expect(row.closest('tr')).toHaveTextContent('重排模型')
  })

  it('create_dialog_submits_rerank_kind_without_dimension', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('Qwen3 8B')

    await user.click(screen.getByRole('button', { name: '新建租户模型' }))
    const dialog = screen.getByRole('dialog')

    // 类别下拉提供重排选项；选中后提交体不带 dimension（rerank 不携带）。
    await user.click(within(dialog).getByRole('combobox', { name: '类别' }))
    await user.click(await screen.findByRole('option', { name: '重排模型' }))

    await user.type(within(dialog).getByLabelText('展示名'), 'BGE Reranker')
    await user.type(within(dialog).getByLabelText('模型键（选择键与向量空间键）'), 'bge-reranker:0.6b')
    await user.type(within(dialog).getByLabelText('Provider 模型名'), 'bge-reranker:0.6b')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitForCreateCall()
    expect(mockCreateModel).toHaveBeenCalledTimes(1)
    const payload = mockCreateModel.mock.calls[0][0]
    expect(payload.kind).toBe('rerank')
    expect(payload.dimension).toBeUndefined()
  })
})

// MUTABLE_UPDATE_FIELDS 是更新协议允许的变更集字段（ModelUpdateSpec）：
// kind / modelKey / id 是创建后不可变的身份列，不得出现在更新体中。
const MUTABLE_UPDATE_FIELDS = [
  'apiKeyEnv',
  'baseUrl',
  'contextWindow',
  'dimension',
  'maxOutputTokens',
  'model',
  'name',
  'provider',
  'remark',
  'status',
  'timeoutMs',
]

describe('ModelManagePage edit dialog', () => {
  it('edit_tenant_row_updates_only_mutable_fields', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('Qwen3 8B')

    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitForUpdateCall(mockUpdateModel)
    expect(mockUpdateModel).toHaveBeenCalledTimes(1)
    expect(mockUpdateModelSuper).not.toHaveBeenCalled()

    const payload = mockUpdateModel.mock.calls[0][0]
    expect(payload.id).toBe('5')
    expect(payload.apiKey).toBeUndefined()
    expect(payload.clearApiKey).toBe(false)
    // 变更集只有可变字段：身份列不随更新协议下发。
    expect(Object.keys(payload.model).sort()).toEqual(MUTABLE_UPDATE_FIELDS)
    expect(payload.model).toEqual({
      status: 1,
      name: 'Qwen3 8B',
      provider: 'openai',
      baseUrl: undefined,
      model: 'qwen3:8b',
      dimension: undefined,
      timeoutMs: undefined,
      contextWindow: undefined,
      maxOutputTokens: undefined,
      apiKeyEnv: undefined,
      remark: undefined,
    })
  })

  it('edit_text_row_submits_generation_budget', async () => {
    const user = userEvent.setup()
    mockListModels.mockResolvedValue({
      items: [model({ contextWindow: 1048576, maxOutputTokens: 393216 })],
    })
    renderPage()
    await screen.findByText('Qwen3 8B')

    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    // 默认值回填后直接保存：生成预算随更新协议下发。
    expect(within(dialog).getByLabelText('上下文窗口（token）')).toHaveValue('1048576')
    expect(within(dialog).getByLabelText('最大输出（token，含思考）')).toHaveValue('393216')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitForUpdateCall(mockUpdateModel)
    const payload = mockUpdateModel.mock.calls[0][0]
    expect(payload.model.contextWindow).toBe(1048576)
    expect(payload.model.maxOutputTokens).toBe(393216)
  })

  it('edit_platform_row_updates_via_super_route', async () => {
    const user = userEvent.setup()
    mockListModels.mockResolvedValue({ items: [model({ id: '11', platform: true })] })
    renderPage()
    await screen.findByText('Qwen3 8B')

    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitForUpdateCall(mockUpdateModelSuper)
    expect(mockUpdateModelSuper).toHaveBeenCalledTimes(1)
    expect(mockUpdateModel).not.toHaveBeenCalled()

    const payload = mockUpdateModelSuper.mock.calls[0][0]
    expect(payload.id).toBe('11')
    expect(Object.keys(payload.model).sort()).toEqual(MUTABLE_UPDATE_FIELDS)
    expect(payload.model).not.toHaveProperty('kind')
    expect(payload.model).not.toHaveProperty('modelKey')
    expect(payload.model).not.toHaveProperty('id')
  })
})

// waitForListCall / waitForCreateCall 等待 mock 被调用（act 环境下的
// 异步 setState 收敛）。
async function waitForListCall(kind: string) {
  for (let i = 0; i < 50; i++) {
    if (mockListModels.mock.calls.some((call) => call[0] === kind)) {
      return
    }
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  throw new Error(`listModels was not called with ${kind}`)
}

async function waitForCreateCall() {
  for (let i = 0; i < 50; i++) {
    if (mockCreateModel.mock.calls.length > 0) {
      return
    }
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  throw new Error('createModel was not called')
}

async function waitForUpdateCall(mock: { mock: { calls: unknown[][] } }) {
  for (let i = 0; i < 50; i++) {
    if (mock.mock.calls.length > 0) {
      return
    }
    await new Promise((resolve) => setTimeout(resolve, 10))
  }
  throw new Error('update mock was not called')
}
