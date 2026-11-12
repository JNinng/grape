import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../api/toApiError'
import { ChunkIdPopover } from './EntityPopover'
import { forgetChunkDetail, loadChunkDetail } from './detailCache'

vi.mock('./detailCache', () => ({
  loadChunkDetail: vi.fn(),
  loadDocumentDetail: vi.fn(),
  forgetChunkDetail: vi.fn(),
  forgetDocumentDetail: vi.fn(),
  clearReplayDetailCache: vi.fn(),
}))

const mockLoadChunkDetail = vi.mocked(loadChunkDetail)
const mockForgetChunkDetail = vi.mocked(forgetChunkDetail)

function chunkDetail() {
  return {
    chunk: { index: 0, chunkId: 'doc-1', anchor: 'runes 0-10', content: '切片正文ABC', embedder: 'space@v1' },
    document: {
      id: '2',
      libraryId: '1',
      kind: 'text',
      title: '员工手册',
      status: 1,
      createdBy: '7',
      updatedBy: '7',
      createdAt: '2026-01-01T00:00:00Z',
    },
    supersededAt: '',
  }
}

beforeEach(() => {
  mockLoadChunkDetail.mockResolvedValue(chunkDetail())
})

afterEach(() => {
  cleanup()
  vi.resetAllMocks()
})

// EntityPopover 的浮层状态：加载成功/权限拒绝/重试清缓存。渲染走
// components/ui 的 Popover，正文按节点渲染。

describe('EntityPopover', () => {
  it('renders_chunk_content_and_document_facts_on_success', async () => {
    const user = userEvent.setup()
    render(<ChunkIdPopover chunkId="doc-1" />)
    await user.click(screen.getByRole('button', { name: '分片 doc-1' }))
    expect(await screen.findByText('切片正文ABC')).toBeInTheDocument()
    expect(screen.getByText('员工手册')).toBeInTheDocument()
  })

  it('maps_the_backend_perm_access_denied_reason_to_a_friendly_message', async () => {
    mockLoadChunkDetail.mockRejectedValue(new ApiError('permission denied', 'PERM_ACCESS_DENIED'))
    const user = userEvent.setup()
    render(<ChunkIdPopover chunkId="doc-1" />)
    await user.click(screen.getByRole('button', { name: '分片 doc-1' }))
    expect(await screen.findByText(/无权限查看该内容/)).toBeInTheDocument()
  })

  it('retry_forgets_the_cached_id_before_reloading', async () => {
    mockLoadChunkDetail.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce(chunkDetail())
    const user = userEvent.setup()
    render(<ChunkIdPopover chunkId="doc-1" />)
    await user.click(screen.getByRole('button', { name: '分片 doc-1' }))
    await screen.findByText('加载失败：boom')
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('切片正文ABC')).toBeInTheDocument()
    expect(mockForgetChunkDetail).toHaveBeenCalledWith('doc-1')
    expect(mockLoadChunkDetail).toHaveBeenCalledTimes(2)
  })
})
