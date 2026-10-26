import { describe, expect, it, vi } from 'vitest'
import { singleFlight } from './singleFlight'

// deferred 构造可手工放行的 promise，用于确定性控制并发时序。
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('singleFlight', () => {
  it('shares one in-flight invocation when called concurrently', async () => {
    const flight = deferred<number>()
    const fn = vi.fn(() => flight.promise)
    const run = singleFlight(fn)

    const first = run()
    const second = run()
    flight.resolve(42)

    await expect(first).resolves.toBe(42)
    await expect(second).resolves.toBe(42)
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('propagates rejection to every concurrent caller', async () => {
    const flight = deferred<never>()
    const fn = vi.fn(() => flight.promise)
    const run = singleFlight(fn)

    const first = run()
    const second = run()
    flight.reject(new Error('boom'))

    await expect(first).rejects.toThrow('boom')
    await expect(second).rejects.toThrow('boom')
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('invokes the operation again after the previous flight settles', async () => {
    let value = 1
    const run = singleFlight(() => Promise.resolve(value))

    await expect(run()).resolves.toBe(1)
    value = 2
    await expect(run()).resolves.toBe(2)
  })

  it('converts a synchronous throw into a shared rejection', async () => {
    const fn = vi.fn((): Promise<number> => {
      throw new Error('sync failure')
    })
    const run = singleFlight(fn)

    const first = run()
    const second = run()

    await expect(first).rejects.toThrow('sync failure')
    await expect(second).rejects.toThrow('sync failure')
    expect(fn).toHaveBeenCalledTimes(1)
  })
})
