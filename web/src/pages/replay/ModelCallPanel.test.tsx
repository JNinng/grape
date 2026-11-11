import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../api/agentModelCalls', () => ({
  fetchModelCalls: vi.fn(),
}))

import { fetchModelCalls, type ModelCall } from '../../api/agentModelCalls'
import { ModelCallPanel } from './ModelCallPanel'

const mockFetchModelCalls = vi.mocked(fetchModelCalls)

// modelCallOf 是合法留痕行的样板：按用例展开覆盖。
function modelCallOf(overrides: Partial<ModelCall> = {}): ModelCall {
  return {
    id: 31,
    scenario: 'embed_document',
    status: 'success',
    modelKey: 'bge-m1024',
    endpoint: 'http://embed.example/v1/embeddings',
    documentId: 42,
    inputSummary: 'chunk body',
    outputSummary: 'embedding vector of 1024 dimensions (vector not persisted)',
    inputTokens: 96,
    outputTokens: null,
    durationMs: 18,
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  mockFetchModelCalls.mockResolvedValue([modelCallOf()])
})

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

describe('ModelCallPanel', () => {
  it('renders_rows_with_facts_and_expands_to_show_input_output_verbatim', async () => {
    const user = userEvent.setup()
    render(<ModelCallPanel />)

    const region = screen.getByRole('region', { name: '模型调用' })
    expect(await within(region).findByText('bge-m1024')).toBeInTheDocument()
    expect(within(region).getByText('embed_document/success')).toBeInTheDocument()
    expect(within(region).getByText('tokens 96 / —')).toBeInTheDocument()
    expect(within(region).getByText('文档 42')).toBeInTheDocument()

    // 摘要与产物折叠在行内：展开后原样渲染（落库即所显）。
    expect(within(region).queryByText('chunk body')).not.toBeInTheDocument()
    await user.click(within(region).getByText('embed_document/success'))
    expect(within(region).getByText('chunk body')).toBeInTheDocument()
    expect(within(region).getByText('embedding vector of 1024 dimensions (vector not persisted)')).toBeInTheDocument()
    expect(within(region).getByText('端点：http://embed.example/v1/embeddings')).toBeInTheDocument()
  })

  it('renders_the_error_summary_of_failed_calls', async () => {
    mockFetchModelCalls.mockResolvedValue([
      modelCallOf({
        id: 32,
        scenario: 'parse_document',
        status: 'error',
        modelKey: '',
        outputSummary: '',
        inputTokens: null,
        error: 'external parse service status 422: parse failed',
      }),
    ])
    const user = userEvent.setup()
    render(<ModelCallPanel />)

    const region = screen.getByRole('region', { name: '模型调用' })
    expect(await within(region).findByText('parse_document/error')).toBeInTheDocument()
    await user.click(within(region).getByText('parse_document/error'))
    expect(within(region).getByText('external parse service status 422: parse failed')).toBeInTheDocument()
    // 无模型键给占位，不伪造。
    expect(within(region).getByText('（无模型键）')).toBeInTheDocument()
  })

  it('omits_titled_blocks_when_summaries_are_absent_from_the_reply', async () => {
    // 后端 omitempty：空产出等空文本字段在响应中缺省（undefined）。真值
    // 判定下不得渲染带标题的空 pre 块，非空字段照常渲染。
    mockFetchModelCalls.mockResolvedValue([
      modelCallOf({ id: 33, inputSummary: undefined, outputSummary: undefined, error: undefined }),
    ])
    const user = userEvent.setup()
    render(<ModelCallPanel />)

    const region = screen.getByRole('region', { name: '模型调用' })
    expect(await within(region).findByText('embed_document/success')).toBeInTheDocument()
    await user.click(within(region).getByText('embed_document/success'))
    expect(within(region).queryByText('输出')).not.toBeInTheDocument()
    expect(within(region).queryByText('输入')).not.toBeInTheDocument()
    expect(within(region).queryByText('错误')).not.toBeInTheDocument()
    // 端点字段在场时仍渲染，证明是按真值过滤而非整块省略。
    expect(within(region).getByText('端点：http://embed.example/v1/embeddings')).toBeInTheDocument()
  })

  it('refetches_with_scenario_and_status_filters_on_change', async () => {
    const user = userEvent.setup()
    render(<ModelCallPanel />)

    await waitFor(() => {
      expect(mockFetchModelCalls).toHaveBeenCalledWith({ scenario: '', modelKey: '', status: '' })
    })

    await user.selectOptions(screen.getByRole('combobox', { name: '场景过滤' }), 'parse_document')
    await user.selectOptions(screen.getByRole('combobox', { name: '状态过滤' }), 'error')
    await waitFor(() => {
      expect(mockFetchModelCalls).toHaveBeenLastCalledWith({ scenario: 'parse_document', modelKey: '', status: 'error' })
    })

    await user.type(screen.getByRole('textbox', { name: '模型键过滤' }), 'glm-4')
    await waitFor(() => {
      expect(mockFetchModelCalls).toHaveBeenLastCalledWith({ scenario: 'parse_document', modelKey: 'glm-4', status: 'error' })
    })
  })

  it('shows_empty_note_when_no_rows_and_error_alert_on_failure', async () => {
    mockFetchModelCalls.mockResolvedValue([])
    render(<ModelCallPanel />)
    expect(await screen.findByText('暂无模型调用留痕。')).toBeInTheDocument()

    mockFetchModelCalls.mockRejectedValue(new Error('HTTP 500'))
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /刷新/ }))
    expect(await screen.findByText(/加载失败：HTTP 500/)).toBeInTheDocument()
  })
})
