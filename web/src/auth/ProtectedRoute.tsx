import { useEffect, useState, type ReactNode } from 'react'
import { Navigate } from 'react-router'
import { getAccessToken } from './store'
import { refreshSession } from './refresh'

type RestoreState = 'pending' | 'ready' | 'denied'

// ProtectedRoute 守卫受保护路由：持有 access token 直接放行；没有则用
// refresh token 静默续期一次（覆盖页面刷新后的会话恢复），续期失败回
// 登录页。等待期渲染占位，避免闪跳登录页。
export function ProtectedRoute({ children }: { children: ReactNode }) {
  const [state, setState] = useState<RestoreState>(() =>
    getAccessToken() !== null ? 'ready' : 'pending',
  )

  useEffect(() => {
    if (state !== 'pending') {
      return
    }
    let cancelled = false
    refreshSession()
      .then(() => {
        if (!cancelled) setState('ready')
      })
      .catch(() => {
        if (!cancelled) setState('denied')
      })
    return () => {
      cancelled = true
    }
  }, [state])

  if (state === 'pending') {
    return (
      <div className="flex min-h-full items-center justify-center">
        <p role="status" className="text-sm text-slate-400">
          正在恢复会话…
        </p>
      </div>
    )
  }
  if (state === 'denied') {
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}
