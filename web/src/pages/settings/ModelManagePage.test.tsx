import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ModelDTO } from '@/api/models'
import { createModel, listModels } from '@/api/models'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { SettingsLayout } from './SettingsLayout'
import { ModelManagePage } from './ModelManagePage'

vi.mock('@/api/models', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/models')>()),
  listModels: vi.fn(),
  createModel: vi.fn(),
}))

const mockListModels = vi.mocked(listModels)
const mockCreateModel = vi.mocked(createModel)

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
