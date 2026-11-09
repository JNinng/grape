import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createLibrary,
  getDocument,
  listLibraries,
  listLibraryDocuments,
  listModelOptions,
  listDocumentChunks,
  updateDocument,
  updateLibrary,
  type DocumentDTO,
  type LibraryDTO,
} from '@/api/knowledge'
import { ApiError } from '@/api/toApiError'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { KnowledgeManagePage } from './KnowledgeManagePage'

vi.mock('@/api/knowledge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/knowledge')>()),
  listLibraries: vi.fn(),
  listLibraryDocuments: vi.fn(),
  listModelOptions: vi.fn(),
  createLibrary: vi.fn(),
  updateLibrary: vi.fn(),
  getDocument: vi.fn(),
  updateDocument: vi.fn(),
  listDocumentChunks: vi.fn(),
}))

const mockListLibraries = vi.mocked(listLibraries)
const mockListLibraryDocuments = vi.mocked(listLibraryDocuments)
const mockListModelOptions = vi.mocked(listModelOptions)
const mockCreateLibrary = vi.mocked(createLibrary)
const mockUpdateLibrary = vi.mocked(updateLibrary)
const mockGetDocument = vi.mocked(getDocument)
const mockUpdateDocument = vi.mocked(updateDocument)
const mockListDocumentChunks = vi.mocked(listDocumentChunks)

function library(overrides: Partial<LibraryDTO> = {}): LibraryDTO {
  return {
    id: '100',
    status: 1,
    name: '人事',
    chunkSize: 800,
    chunkOverlap: 100,
    chunkDelimiter: '\n\n',
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function documentRow(overrides: Partial<DocumentDTO> = {}): DocumentDTO {
  return {
    id: '200',
    libraryId: '100',
    kind: 'text',
    title: '退款政策',
    status: 1,
    createdBy: '7',
    updatedBy: '7',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function documentDetail(overrides: Partial<DocumentDTO> = {}): DocumentDTO {
  return documentRow({ content: '30 天内全额退款', sourceName: '客服手册', ...overrides })
}

function mockLibraries(items: LibraryDTO[]) {
  mockListLibraries.mockResolvedValue({ items, total: String(items.length), hasMore: false, nextCursor: '', page: 1 })
}

beforeEach(() => {
  clearToasts()
  mockLibraries([library()])
  mockListLibraryDocuments.mockResolvedValue({ items: [], total: '0', hasMore: false, nextCursor: '', page: 1 })
  mockListModelOptions.mockResolvedValue({ items: [] })
  mockCreateLibrary.mockResolvedValue(library())
  mockUpdateLibrary.mockResolvedValue(library())
  mockGetDocument.mockResolvedValue(documentDetail())
  mockUpdateDocument.mockResolvedValue(documentDetail())
  mockListDocumentChunks.mockResolvedValue({
    items: [{ index: 0, chunkId: '200#0', anchor: 'runes 0-11', content: '30 天内全额退款', embedder: 'space@v1' }],
    total: 1,
  })
})

afterEach(() => {
  cleanup()
  clearToasts()
  vi.resetAllMocks()
})

function renderPage() {
  return render(
    <>
      <KnowledgeManagePage />
      <Toaster />
    </>,
  )
}

// createPayload / updatePayload 是库表单在未改动其他字段时的完整提交体。
function createPayload(overrides: Record<string, unknown>) {
  return { status: 1, name: '财务', chunkSize: 800, chunkOverlap: 100, ...overrides }
}

function updatePayload(overrides: Record<string, unknown>) {
  return { id: '100', status: 1, name: '人事', chunkSize: 800, chunkOverlap: 100, ...overrides }
}

async function openCreateDialog(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByText('人事', { selector: 'span' })
  await user.click(screen.getByRole('button', { name: '新建' }))
  return screen.findByRole('dialog')
}

describe('KnowledgeManagePage library chunk delimiter', () => {
  it('create_dialog_defaults_delimiter_to_escaped_blank_line', async () => {
    const user = userEvent.setup()
    renderPage()
    const dialog = await openCreateDialog(user)
    expect(within(dialog).getByLabelText('区分串')).toHaveValue('\\n\\n')
  })

  it('create_submits_decoded_default_delimiter', async () => {
    const user = userEvent.setup()
    renderPage()
    const dialog = await openCreateDialog(user)
    await user.type(within(dialog).getByLabelText('名称'), '财务')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(mockCreateLibrary).toHaveBeenCalledWith(createPayload({ chunkDelimiter: '\n\n' }))
  })

  it('create_with_cleared_delimiter_submits_empty_string', async () => {
    const user = userEvent.setup()
    renderPage()
    const dialog = await openCreateDialog(user)
    await user.type(within(dialog).getByLabelText('名称'), '财务')
    await user.clear(within(dialog).getByLabelText('区分串'))
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(mockCreateLibrary).toHaveBeenCalledWith(createPayload({ chunkDelimiter: '' }))
  })

  it('edit_dialog_shows_existing_empty_delimiter_and_submits_it_unchanged', async () => {
    mockLibraries([library({ chunkDelimiter: '' })])
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('人事', { selector: 'span' })
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByLabelText('区分串')).toHaveValue('')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(mockUpdateLibrary).toHaveBeenCalledWith(updatePayload({ chunkDelimiter: '' }))
  })

  it('edit_dialog_treats_missing_delimiter_as_default', async () => {
    mockLibraries([library({ chunkDelimiter: undefined })])
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('人事', { selector: 'span' })
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByLabelText('区分串')).toHaveValue('\\n\\n')
  })

  it('edit_with_missing_delimiter_submits_default', async () => {
    mockLibraries([library({ chunkDelimiter: undefined })])
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('人事', { selector: 'span' })
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(mockUpdateLibrary).toHaveBeenCalledWith(updatePayload({ chunkDelimiter: '\n\n' }))
  })

  it('custom_escaped_delimiter_is_decoded_on_submit', async () => {
    const user = userEvent.setup()
    renderPage()
    const dialog = await openCreateDialog(user)
    await user.type(within(dialog).getByLabelText('名称'), '财务')
    const field = within(dialog).getByLabelText('区分串')
    await user.clear(field)
    await user.type(field, '##\\t')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(mockCreateLibrary).toHaveBeenCalledWith(createPayload({ chunkDelimiter: '##\t' }))
  })

  it('library_summary_shows_disabled_delimiter', async () => {
    mockLibraries([library({ chunkDelimiter: '' })])
    renderPage()
    expect(await screen.findByText(/不按区分串/)).toBeInTheDocument()
  })
})

describe('KnowledgeManagePage search entry', () => {
  it('search_button_opens_search_dialog_with_loaded_libraries', async () => {
    const user = userEvent.setup()
    renderPage()
    await screen.findByText('人事', { selector: 'span' })
    await user.click(screen.getByRole('button', { name: '检索' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('检索知识库')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('combobox', { name: '知识库' }))
    expect(await screen.findByRole('option', { name: '人事' })).toBeInTheDocument()
  })
})

describe('KnowledgeManagePage edit document', () => {
  beforeEach(() => {
    mockListLibraryDocuments.mockResolvedValue({ items: [documentRow()], total: '1', hasMore: false, nextCursor: '', page: 1 })
  })

  // openEditDocument 打开条目行的编辑弹窗（列表行不含正文）。
  async function openEditDocument(user: ReturnType<typeof userEvent.setup>) {
    const row = (await screen.findByText('退款政策')).closest('tr')
    if (!row) {
      throw new Error('document row not found')
    }
    await user.click(within(row).getByRole('button', { name: '编辑' }))
    return screen.findByRole('dialog')
  }

  it('edit_document_loads_detail_by_id', async () => {
    const user = userEvent.setup()
    renderPage()
    await openEditDocument(user)
    expect(mockGetDocument).toHaveBeenCalledWith('200')
  })

  it('edit_document_fills_content_from_detail', async () => {
    const user = userEvent.setup()
    renderPage()
    const dialog = await openEditDocument(user)
    expect(await within(dialog).findByDisplayValue('30 天内全额退款')).toBeInTheDocument()
  })

  it('edit_document_while_detail_loading_disables_content_and_save', async () => {
    mockGetDocument.mockReturnValue(new Promise(() => {}))
    const user = userEvent.setup()
    renderPage()
    const dialog = await openEditDocument(user)
    expect(within(dialog).getByLabelText('正文')).toBeDisabled()
    expect(within(dialog).getByRole('button', { name: '保存' })).toBeDisabled()
  })

  it('edit_document_detail_failure_shows_error', async () => {
    mockGetDocument.mockRejectedValue(new ApiError('knowledge document not found', 'DOCUMENT_NOT_FOUND'))
    const user = userEvent.setup()
    renderPage()
    const dialog = await openEditDocument(user)
    expect(await within(dialog).findByText('加载条目正文失败：knowledge document not found')).toBeInTheDocument()
  })

  it('edit_document_detail_failure_keeps_save_disabled', async () => {
    mockGetDocument.mockRejectedValue(new ApiError('knowledge document not found', 'DOCUMENT_NOT_FOUND'))
    const user = userEvent.setup()
    renderPage()
    const dialog = await openEditDocument(user)
    await within(dialog).findByText(/加载条目正文失败/)
    expect(within(dialog).getByRole('button', { name: '保存' })).toBeDisabled()
  })

  it('edit_document_save_submits_loaded_content', async () => {
    const user = userEvent.setup()
    renderPage()
    const dialog = await openEditDocument(user)
    await within(dialog).findByDisplayValue('30 天内全额退款')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(mockUpdateDocument).toHaveBeenCalledWith({
      id: '200',
      status: 1,
      title: '退款政策',
      content: '30 天内全额退款',
      sourceName: '客服手册',
    })
  })

  it('edit_document_with_cleared_content_disables_save', async () => {
    const user = userEvent.setup()
    renderPage()
    const dialog = await openEditDocument(user)
    await user.clear(await within(dialog).findByDisplayValue('30 天内全额退款'))
    expect(within(dialog).getByRole('button', { name: '保存' })).toBeDisabled()
  })
})

describe('KnowledgeManagePage document detail', () => {
  beforeEach(() => {
    mockListLibraryDocuments.mockResolvedValue({ items: [documentRow()], total: '1', hasMore: false, nextCursor: '', page: 1 })
  })

  it('clicking_document_title_opens_detail_dialog', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: '退款政策' }))
    expect(await screen.findByText('条目详情与实际分片（切分结果以向量存储为准，改过切分参数需重建索引才会生效）。')).toBeInTheDocument()
  })

  it('opening_detail_loads_chunks_of_that_document', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: '退款政策' }))
    expect(mockListDocumentChunks).toHaveBeenCalledWith('200')
  })

  it('chunk_row_in_detail_expands_content', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: '退款政策' }))
    const row = await screen.findByRole('button', { name: /#0 · runes 0-11/ })
    await user.click(row)
    expect(screen.getByLabelText('分片 #0 正文')).toHaveTextContent('30 天内全额退款')
  })
})
