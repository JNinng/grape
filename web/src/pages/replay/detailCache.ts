import { getDocument, getDocumentChunk, type DocumentChunkDetailDTO, type DocumentDTO } from '../../api/knowledge'

// 复现页的实体详情缓存：同一 ID 只请求一次，并发点击共享同一在途请求；
// 失败结果不缓存（允许重试）。清空时机由页面挂在「重新装载复现数据」
//（刷新快照）与「切换复现会话」两个入口——历史留痕的分片可能已被重摄
// 淘汰，刷新后必须重新取事实。
export interface DetailCache<T> {
  load(id: string, fetcher: (id: string) => Promise<T>): Promise<T>
  // clearID 删除单个在途/已完成项：请求悬挂时按「重试」先删表项再取，
  // 否则同 ID 会永远等同一个悬挂 Promise。
  clearID(id: string): void
  clear(): void
}

export function createDetailCache<T>(): DetailCache<T> {
  const entries = new Map<string, Promise<T>>()
  return {
    load(id, fetcher) {
      const cached = entries.get(id)
      if (cached !== undefined) {
        return cached
      }
      const pending = fetcher(id).catch((err: unknown) => {
        // 失败不缓存：清掉在途项，下次点击可重试。清空后的旧响应同样
        // 不会回填（清空只删表，成功不再写回）。
        if (entries.get(id) === pending) {
          entries.delete(id)
        }
        throw err
      })
      entries.set(id, pending)
      return pending
    },
    clearID(id) {
      entries.delete(id)
    },
    clear() {
      entries.clear()
    },
  }
}

const chunkDetails = createDetailCache<DocumentChunkDetailDTO>()
const documentDetails = createDetailCache<DocumentDTO>()

// loadChunkDetail 取分片 + 所属条目摘要（命中缓存即不发请求）。
export function loadChunkDetail(chunkId: string): Promise<DocumentChunkDetailDTO> {
  return chunkDetails.load(chunkId, getDocumentChunk)
}

// loadDocumentDetail 只取文档摘要（单元的 source_id 来源只展示文档信息）。
export function loadDocumentDetail(documentId: string): Promise<DocumentDTO> {
  return documentDetails.load(documentId, getDocument)
}

// forgetChunkDetail / forgetDocumentDetail 删除单个 ID 的缓存项，供浮层
// 「重试」在请求悬挂时重新发起（清空后 load 才会新建请求）。
export function forgetChunkDetail(chunkId: string): void {
  chunkDetails.clearID(chunkId)
}

export function forgetDocumentDetail(documentId: string): void {
  documentDetails.clearID(documentId)
}

// clearReplayDetailCache 使全部缓存失效（刷新快照与切换会话时调用）。
export function clearReplayDetailCache(): void {
  chunkDetails.clear()
  documentDetails.clear()
}
