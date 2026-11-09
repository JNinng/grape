import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createLibrary,
  listLibraries,
  listLibraryDocuments,
  listModelOptions,
  updateLibrary,
  type LibraryDTO,
} from '@/api/knowledge'
import { Toaster, clearToasts } from '@/components/ui/toast'
import { KnowledgeManagePage } from './KnowledgeManagePage'

vi.mock('@/api/knowledge', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/api/knowledge')>()),
  listLibraries: vi.fn(),
  listLibraryDocuments: vi.fn(),
  listModelOptions: vi.fn(),
  createLibrary: vi.fn(),
  updateLibrary: vi.fn(),
}))

const mockListLibraries = vi.mocked(listLibraries)
const mockListLibraryDocuments = vi.mocked(listLibraryDocuments)
const mockListModelOptions = vi.mocked(listModelOptions)
const mockCreateLibrary = vi.mocked(createLibrary)
const mockUpdateLibrary = vi.mocked(updateLibrary)

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
    expect(mockCreateLibrary).toHaveBeenCalledWith(expect.objectContaining({ name: '财务', chunkDelimiter: '\n\n' }))
  })

  it('create_with_cleared_delimiter_submits_empty_string', async () => {
    const user = userEvent.setup()
    renderPage()
    const dialog = await openCreateDialog(user)
    await user.type(within(dialog).getByLabelText('名称'), '财务')
    await user.clear(within(dialog).getByLabelText('区分串'))
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(mockCreateLibrary).toHaveBeenCalledWith(expect.objectContaining({ chunkDelimiter: '' }))
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
    expect(mockUpdateLibrary).toHaveBeenCalledWith(expect.objectContaining({ id: '100', chunkDelimiter: '' }))
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

  it('custom_escaped_delimiter_is_decoded_on_submit', async () => {
    const user = userEvent.setup()
    renderPage()
    const dialog = await openCreateDialog(user)
    await user.type(within(dialog).getByLabelText('名称'), '财务')
    const field = within(dialog).getByLabelText('区分串')
    await user.clear(field)
    await user.type(field, '##\\t')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(mockCreateLibrary).toHaveBeenCalledWith(expect.objectContaining({ chunkDelimiter: '##\t' }))
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
