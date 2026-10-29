import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { login } from '../api/session'
import { AlertCircleIcon, GrapeMark, SpinnerIcon } from '../ui/icons'

// LoginPage 登录页：租户可空（留空由服务端回落默认租户），用户名口令
// 必填。登录成功进入对话页，失败原地展示错误。
export function LoginPage() {
  const navigate = useNavigate()
  const [tenantId, setTenantId] = useState('')
  const [userName, setUserName] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) {
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      await login({ tenantId, userName, password })
      navigate('/', { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败，请稍后重试')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="flex min-h-full items-center justify-center bg-linear-to-br from-violet-100 via-slate-50 to-emerald-50 px-4 py-10">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex flex-col items-center gap-3">
          <GrapeMark className="h-14 w-14" />
          <div className="text-center">
            <h1 className="text-xl font-semibold text-slate-900">登录 Grape</h1>
            <p className="mt-1 text-sm text-slate-400">AI 对话与运维复现工作台</p>
          </div>
        </div>
        <form onSubmit={handleSubmit} className="card space-y-4 p-6">
          <div className="space-y-1.5">
            <label htmlFor="tenantId" className="block text-sm font-medium text-slate-600">
              租户 ID（可选）
            </label>
            <input
              id="tenantId"
              name="tenantId"
              className="field"
              placeholder="留空使用默认租户"
              value={tenantId}
              onChange={(e) => setTenantId(e.target.value)}
              autoComplete="off"
            />
          </div>
          <div className="space-y-1.5">
            <label htmlFor="userName" className="block text-sm font-medium text-slate-600">
              用户名
            </label>
            <input
              id="userName"
              name="userName"
              className="field"
              placeholder="输入用户名"
              required
              value={userName}
              onChange={(e) => setUserName(e.target.value)}
              autoComplete="username"
            />
          </div>
          <div className="space-y-1.5">
            <label htmlFor="password" className="block text-sm font-medium text-slate-600">
              口令
            </label>
            <input
              id="password"
              name="password"
              type="password"
              className="field"
              placeholder="输入口令"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
            />
          </div>
          {error !== null && (
            <p role="alert" className="alert">
              <AlertCircleIcon aria-hidden className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{error}</span>
            </p>
          )}
          <button type="submit" disabled={submitting} className="btn btn-primary w-full">
            {submitting && <SpinnerIcon aria-hidden className="h-4 w-4" />}
            {submitting ? '登录中…' : '登录'}
          </button>
        </form>
      </div>
    </main>
  )
}
