// singleFlight 把异步操作包成并发收敛的单飞调用：并发期间只有第一次
// 调用真正执行，其余调用共享同一结果（含失败）；全部结算后状态复位，
// 下一次调用重新执行。刷新令牌轮换依赖该语义：并发 401 只发一次
// refresh，避免旧刷新令牌被并发轮换互相作废。
export function singleFlight<T>(fn: () => Promise<T>): () => Promise<T> {
  let inflight: Promise<T> | null = null
  return () => {
    if (inflight === null) {
      // 外面 async 包一层：fn 的同步异常也归一为 rejection，保证所有
      // 并发调用方共享同一结局，而不是只有首个调用方感知失败。
      inflight = (async () => fn())().finally(() => {
        inflight = null
      })
    }
    return inflight
  }
}
