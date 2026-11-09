import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { listDocumentChunks, type DocumentDTO, type KnowledgeChunkDTO } from '@/api/knowledge'
import { ApiError } from '@/api/toApiError'
import { KnowledgeDocumentDetailDialog } from './KnowledgeDocumentDetailDialog'

vi.mock('@/api/knowledge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/knowledge')>()),
  listDocumentChunks: vi.fn(),
}))

const mockListDocumentChunks = vi.mocked(listDocumentChunks)

function documentDTO(overrides: Partial<DocumentDTO> = {}): DocumentDTO {
  return {
    id: '200',
    libraryId: '100',
    kind: 'text',
    title: '退款政策',
    status: 1,
    indexedAt: '2026-01-01T08:00:00Z',
    indexedEmbedder: 'qwen3-embedding:0.6b',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function chunk(overrides: Partial<KnowledgeChunkDTO> = {}): KnowledgeChunkDTO {
  return {
    index: 0,
    chunkId: '200#0',
    anchor: 'runes 0-12',
    content: '30 天内全额退款',
    embedder: 'qwen3-embedding:0.6b',
    ...overrides,
  }
}

function renderDialog(document: DocumentDTO = documentDTO()) {
  const onClose = vi.fn()
  render(<KnowledgeDocumentDetailDialog document={document} statusLabel="启用" onClose={onClose} />)
  return { onClose, dialog: screen.getByRole('dialog') }
}

beforeEach(() => {
  mockListDocumentChunks.mockResolvedValue({ items: [chunk()], total: 1 })
})

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('KnowledgeDocumentDetailDialog', () => {
  it('loads_chunks_for_the_document_on_open', async () => {
    renderDialog()
    expect(mockListDocumentChunks).toHaveBeenCalledWith('200')
  })

  it('renders_document_facts', async () => {
    const { dialog } = renderDialog()
    expect(within(dialog).getByRole('heading', { name: '退款政策' })).toBeInTheDocument()
    expect(within(dialog).getByText('启用')).toBeInTheDocument()
    expect(within(dialog).getByText('qwen3-embedding:0.6b')).toBeInTheDocument()
    expect(within(dialog).getByText('长期有效')).toBeInTheDocument()
  })

  it('missing_index_fields_render_placeholders', async () => {
    const { dialog } = renderDialog(documentDTO({ indexedAt: undefined, indexedEmbedder: undefined }))
    expect(within(dialog).getByText('无')).toBeInTheDocument()
    expect(within(dialog).getByText('未记录')).toBeInTheDocument()
  })

  it('renders_chunk_count_and_rows_in_order', async () => {
    mockListDocumentChunks.mockResolvedValue({
      items: [chunk(), chunk({ index: 1, chunkId: '200#1', anchor: 'runes 13-20', content: '第二段' })],
      total: 2,
    })
    const { dialog } = renderDialog()
    expect(await within(dialog).findByText('分片（2）')).toBeInTheDocument()
    const list = within(dialog).getByRole('list', { name: '分片列表' })
    const rows = within(list).getAllByRole('button')
    expect(rows[0]).toHaveTextContent('#0 · runes 0-12')
    expect(rows[1]).toHaveTextContent('#1 · runes 13-20')
  })

  it('chunk_content_is_hidden_until_clicked', async () => {
    const { dialog } = renderDialog()
    await within(dialog).findByRole('list', { name: '分片列表' })
    expect(within(dialog).queryByLabelText('分片 #0 正文')).toBeNull()
  })

  it('clicking_chunk_reveals_its_content', async () => {
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.click(await within(dialog).findByRole('button', { name: /#0 · runes 0-12/ }))
    expect(within(dialog).getByLabelText('分片 #0 正文')).toHaveTextContent('30 天内全额退款')
  })

  it('clicking_expanded_chunk_collapses_it', async () => {
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    const row = await within(dialog).findByRole('button', { name: /#0 · runes 0-12/ })
    await user.click(row)
    await user.click(row)
    expect(within(dialog).queryByLabelText('分片 #0 正文')).toBeNull()
  })

  it('expanded_chunk_is_marked_with_aria_expanded', async () => {
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    const row = await within(dialog).findByRole('button', { name: /#0/ })
    expect(row).toHaveAttribute('aria-expanded', 'false')
    await user.click(row)
    expect(row).toHaveAttribute('aria-expanded', 'true')
  })

  it('markup_in_chunk_content_renders_as_text', async () => {
    mockListDocumentChunks.mockResolvedValue({
      items: [chunk({ content: '<img src=x onerror=alert(1)>' })],
      total: 1,
    })
    const user = userEvent.setup()
    const { dialog } = renderDialog()
    await user.click(await within(dialog).findByRole('button', { name: /#0/ }))
    const content = within(dialog).getByLabelText('分片 #0 正文')
    expect(content).toHaveTextContent('<img src=x onerror=alert(1)>')
    expect(content.querySelector('img')).toBeNull()
  })

  it('no_chunks_shows_empty_state', async () => {
    mockListDocumentChunks.mockResolvedValue({ items: [], total: 0 })
    const { dialog } = renderDialog()
    expect(await within(dialog).findByText('暂无分片（尚未摄取或已被清理）。')).toBeInTheDocument()
    expect(within(dialog).queryByRole('list', { name: '分片列表' })).toBeNull()
  })

  it('chunk_load_failure_shows_error', async () => {
    mockListDocumentChunks.mockRejectedValue(
      new ApiError('knowledge document chunks unavailable', 'DOCUMENT_CHUNKS_UNAVAILABLE'),
    )
    const { dialog } = renderDialog()
    expect(await within(dialog).findByText(/加载分片失败/)).toBeInTheDocument()
  })

  it('chunk_load_failure_hides_empty_state', async () => {
    mockListDocumentChunks.mockRejectedValue(new ApiError('boom'))
    const { dialog } = renderDialog()
    await within(dialog).findByText(/加载分片失败/)
    expect(within(dialog).queryByText('暂无分片（尚未摄取或已被清理）。')).toBeNull()
  })

  it('chunk_without_anchor_renders_index_only', async () => {
    mockListDocumentChunks.mockResolvedValue({ items: [chunk({ anchor: '' })], total: 1 })
    const { dialog } = renderDialog()
    expect(await within(dialog).findByRole('button', { name: /#0/ })).toHaveTextContent('#0')
  })

  it('closing_the_dialog_calls_onClose', async () => {
    const user = userEvent.setup()
    const { onClose, dialog } = renderDialog()
    await user.keyboard('{Escape}')
    expect(within(dialog).queryByRole('button', { name: 'Close' })).toBeNull()
    expect(onClose).toHaveBeenCalled()
  })
})
