import { describe, expect, it, vi } from 'vitest'
import { createDetailCache } from './detailCache'

// 实体详情缓存的去重与失效纪律：同一 ID 只请求一次、并发共享在途请求、
// 失败不缓存（可重试）、clear 后重新请求。

describe('createDetailCache', () => {
  it('returns_the_cached_result_for_the_same_id_after_the_first_load', async () => {
    const cache = createDetailCache<string>()
    const fetcher = vi.fn().mockResolvedValue('first')

    await expect(cache.load('id-1', fetcher)).resolves.toBe('first')
    await expect(cache.load('id-1', fetcher)).resolves.toBe('first')
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher).toHaveBeenCalledWith('id-1')
  })

  it('shares_one_in_flight_request_between_concurrent_loads', async () => {
    const cache = createDetailCache<string>()
    let release: (value: string) => void = () => {}
    const fetcher = vi.fn(
      () =>
        new Promise<string>((resolve) => {
          release = resolve
        }),
    )

    const first = cache.load('id-1', fetcher)
    const second = cache.load('id-1', fetcher)
    release('shared')
    await expect(first).resolves.toBe('shared')
    await expect(second).resolves.toBe('shared')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('does_not_cache_failures_so_the_next_load_can_retry', async () => {
    const cache = createDetailCache<string>()
    const fetcher = vi
      .fn()
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce('recovered')

    await expect(cache.load('id-1', fetcher)).rejects.toThrow('boom')
    await expect(cache.load('id-1', fetcher)).resolves.toBe('recovered')
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('refetches_after_clear', async () => {
    const cache = createDetailCache<string>()
    const fetcher = vi.fn().mockResolvedValueOnce('first').mockResolvedValueOnce('second')

    await expect(cache.load('id-1', fetcher)).resolves.toBe('first')
    cache.clear()
    await expect(cache.load('id-1', fetcher)).resolves.toBe('second')
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('clear_id_lets_a_retry_escape_a_hung_request', async () => {
    // 请求悬挂时同 ID 会一直复用同一个 Promise；clearID 后重试必须发起
    // 新请求，不被悬挂项挡住。
    const cache = createDetailCache<string>()
    const hung = vi.fn(() => new Promise<string>(() => {}))
    void cache.load('id-1', hung)
    cache.clearID('id-1')

    const retry = vi.fn().mockResolvedValue('fresh')
    await expect(cache.load('id-1', retry)).resolves.toBe('fresh')
    expect(hung).toHaveBeenCalledTimes(1)
    expect(retry).toHaveBeenCalledTimes(1)
  })

  it('does_not_repopulate_a_response_that_was_cleared_in_flight', async () => {
    // 刷新/切会话清空后，旧在途响应既不回填缓存，也不影响下一次请求。
    const cache = createDetailCache<string>()
    let releaseOld: (value: string) => void = () => {}
    const oldFetcher = vi.fn(
      () =>
        new Promise<string>((resolve) => {
          releaseOld = resolve
        }),
    )
    const oldLoad = cache.load('id-1', oldFetcher)
    cache.clear()
    releaseOld('stale')
    await expect(oldLoad).resolves.toBe('stale')

    const newFetcher = vi.fn().mockResolvedValue('fresh')
    await expect(cache.load('id-1', newFetcher)).resolves.toBe('fresh')
    expect(newFetcher).toHaveBeenCalledTimes(1)
  })
})
