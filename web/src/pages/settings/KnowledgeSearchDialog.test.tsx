import { act, cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getDocument, searchDocuments, type LibraryDTO, type SearchHitDTO } from '@/api/knowledge'
import { ApiError } from '@/api/toApiError'
import { KnowledgeSearchDialog } from './KnowledgeSearchDialog'

vi.mock('@/api/knowledge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/knowledge')>()),
  searchDocuments: vi.fn(),
  getDocument: vi.fn(),
}))

const mockSearchDocuments = vi.mocked(searchDocuments)
const mockGetDocument = vi.mocked(getDocument)

function library(id: string, name: string): LibraryDTO {
  return {
    id,
    status: 1,
    name,
    chunkSize: 800,
    chunkOverlap: 100,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
  }
}

function hit(overrides: Partial<SearchHitDTO> = {}): SearchHitDTO {
  return {
    documentId: '200#0#a1b2c3d4',
    sourceId: '200',
    sourceName: '员工手册',
    anchor: 'runes 0-12',
    score: 0.91234,
    content: '年假 5 天',
    ...overrides,
  }
}

const libraries = [library('100', '人事'), library('101', '财务')]

function renderDialog() {
  const onClose = vi.fn()
  render(<KnowledgeSearchDialog libraries={libraries} onClose={onClose} />)
  return { onClose, dialog: screen.getByRole('dialog') }
}

// searchAndFindResults 以缺省条件检索一次并返回结果列表。
async function searchAndFindResults() {
  const user = userEvent.setup()
  const { dialog } = renderDialog()
  await user.type(within(dialog).getByLabelText('查询'), '年假')
  await user.click(within(dialog).getByRole('button', { name: '检索' }))
  return within(dialog).findByRole('list', { name: '检索结果' })
}

beforeEach(() => {
  mockSearchDocuments.mockResolvedValue({ items: [hit()], total: 1 })
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('KnowledgeSearchDialog', () => {
  it('blank_query_keeps_search_disabled', () => {
    const { dialog } = renderDialog()
    expect(within(dialog).getByRole('button', { name: '检索' })).toBeDisabled()
  })

  it('submitting_query_searches_all_libraries_with_default_top_k', async () => {
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '  年假  ')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    expect(mockSearchDocuments).toHaveBeenCalledWith({ query: '年假', libraryId: undefined, topK: 5 })
  })

  it('selected_library_and_top_k_are_sent', async () => {
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '报销')
    await user.click(within(dialog).getByRole('combobox', { name: '知识库' }))
    await user.click(await screen.findByRole('option', { name: '财务' }))
    const topK = within(dialog).getByLabelText('返回条数（最多 20）')
    await user.clear(topK)
    await user.type(topK, '12')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    expect(mockSearchDocuments).toHaveBeenCalledWith({ query: '报销', libraryId: '101', topK: 12 })
  })

  it.each(['0', '-3', '2.5'])('invalid_top_k_%s_blocks_search', async (value) => {
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    const topK = within(dialog).getByLabelText('返回条数（最多 20）')
    await user.clear(topK)
    await user.type(topK, value)
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    expect(mockSearchDocuments).not.toHaveBeenCalled()
  })

  it('cleared_top_k_is_omitted_for_server_default', async () => {
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.clear(within(dialog).getByLabelText('返回条数（最多 20）'))
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    expect(mockSearchDocuments).toHaveBeenCalledWith({ query: '年假', libraryId: undefined, topK: undefined })
  })

  it('hit_renders_source_name', async () => {
    const results = await searchAndFindResults()
    expect(within(results).getByText('员工手册')).toBeInTheDocument()
  })

  it('hit_renders_anchor', async () => {
    const results = await searchAndFindResults()
    expect(within(results).getByText(/runes 0-12/)).toBeInTheDocument()
  })

  it('hit_renders_score_with_three_decimals', async () => {
    const results = await searchAndFindResults()
    expect(within(results).getByText('相似度 0.912')).toBeInTheDocument()
  })

  it('hit_renders_content', async () => {
    const results = await searchAndFindResults()
    expect(within(results).getByText('年假 5 天')).toBeInTheDocument()
  })

  it('changing_library_after_search_clears_results', async () => {
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    await within(dialog).findByRole('list', { name: '检索结果' })
    await user.click(within(dialog).getByRole('combobox', { name: '知识库' }))
    await user.click(await screen.findByRole('option', { name: '财务' }))
    expect(within(dialog).queryByRole('list', { name: '检索结果' })).toBeNull()
  })

  it('changing_top_k_after_search_clears_results', async () => {
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    await within(dialog).findByRole('list', { name: '检索结果' })
    await user.type(within(dialog).getByLabelText('返回条数（最多 20）'), '0')
    expect(within(dialog).queryByRole('list', { name: '检索结果' })).toBeNull()
  })

  it('changing_query_after_search_clears_results', async () => {
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    await within(dialog).findByRole('list', { name: '检索结果' })
    await user.type(within(dialog).getByLabelText('查询'), '天数')
    expect(within(dialog).queryByRole('list', { name: '检索结果' })).toBeNull()
  })

  it('late_response_for_previous_conditions_is_discarded', async () => {
    let resolveSearch: (value: { items: SearchHitDTO[]; total: number }) => void = () => {}
    mockSearchDocuments.mockReturnValue(new Promise((resolve) => { resolveSearch = resolve }))
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    await user.click(within(dialog).getByRole('combobox', { name: '知识库' }))
    await user.click(await screen.findByRole('option', { name: '财务' }))
    await act(async () => {
      resolveSearch({ items: [hit()], total: 1 })
    })
    expect(within(dialog).queryByRole('list', { name: '检索结果' })).toBeNull()
  })

  it('markup_in_hit_content_renders_as_text', async () => {
    mockSearchDocuments.mockResolvedValue({ items: [hit({ content: '<img src=x onerror=alert(1)>' })], total: 1 })
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    const results = await within(dialog).findByRole('list', { name: '检索结果' })
    expect(within(results).getByText('<img src=x onerror=alert(1)>')).toBeInTheDocument()
    expect(results.querySelector('img')).toBeNull()
  })

  it('long_hit_content_is_truncated_to_summary', async () => {
    mockSearchDocuments.mockResolvedValue({ items: [hit({ content: '字'.repeat(300) })], total: 1 })
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    const results = await within(dialog).findByRole('list', { name: '检索结果' })
    expect(within(results).getByText(`${'字'.repeat(240)}…`)).toBeInTheDocument()
  })

  it('no_hits_shows_empty_hint', async () => {
    mockSearchDocuments.mockResolvedValue({ items: [], total: 0 })
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    expect(await within(dialog).findByText('无命中')).toBeInTheDocument()
  })

  it('search_failure_shows_error_message', async () => {
    mockSearchDocuments.mockRejectedValue(new ApiError('knowledge search is unavailable', 'SEARCH_UNAVAILABLE'))
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    expect(await within(dialog).findByText('knowledge search is unavailable')).toBeInTheDocument()
  })

  it('view_source_loads_document_by_source_id_and_shows_content', async () => {
    mockGetDocument.mockResolvedValue({
      id: '200',
      libraryId: '100',
      kind: 'text',
      title: '员工手册',
      content: '第一章 年假 5 天',
      createdBy: '7',
      updatedBy: '7',
      createdAt: '2026-01-01T00:00:00Z',
    })
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    await user.click(await within(dialog).findByRole('button', { name: '查看原文' }))
    expect(mockGetDocument).toHaveBeenCalledWith('200')
    expect(await within(dialog).findByLabelText('条目原文')).toHaveTextContent('第一章 年假 5 天')
  })

  it('hit_without_source_id_disables_view_source', async () => {
    mockSearchDocuments.mockResolvedValue({ items: [hit({ sourceId: '' })], total: 1 })
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    expect(await within(dialog).findByRole('button', { name: '查看原文' })).toBeDisabled()
  })

  it('view_source_failure_shows_error_and_keeps_results', async () => {
    mockGetDocument.mockRejectedValue(new ApiError('knowledge document not found', 'DOCUMENT_NOT_FOUND'))
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.type(within(dialog).getByLabelText('查询'), '年假')
    await user.click(within(dialog).getByRole('button', { name: '检索' }))
    await user.click(await within(dialog).findByRole('button', { name: '查看原文' }))
    expect(await within(dialog).findByText('knowledge document not found')).toBeInTheDocument()
    expect(within(dialog).getByRole('list', { name: '检索结果' })).toBeInTheDocument()
  })
})
