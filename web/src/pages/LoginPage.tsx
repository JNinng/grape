import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { login } from '../api/session'

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
    <main>
      <h1>登录 Grape</h1>
      <form onSubmit={handleSubmit}>
        <div>
          <label htmlFor="tenantId">租户 ID（可选）</label>
          <input
            id="tenantId"
            name="tenantId"
            placeholder="留空使用默认租户"
            value={tenantId}
            onChange={(e) => setTenantId(e.target.value)}
            autoComplete="off"
          />
        </div>
        <div>
          <label htmlFor="userName">用户名</label>
          <input
            id="userName"
            name="userName"
            required
            value={userName}
            onChange={(e) => setUserName(e.target.value)}
            autoComplete="username"
          />
        </div>
        <div>
          <label htmlFor="password">口令</label>
          <input
            id="password"
            name="password"
            type="password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
          />
        </div>
        {error !== null && <p role="alert">{error}</p>}
        <button type="submit" disabled={submitting}>
          {submitting ? '登录中…' : '登录'}
        </button>
      </form>
    </main>
  )
}
