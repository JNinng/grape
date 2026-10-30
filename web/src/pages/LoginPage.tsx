import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { login } from '../api/session'
import { GrapeMark } from '../ui/icons'
import { AccentPicker } from '../components/accent-picker'
import { ModeToggle } from '../components/mode-toggle'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Spinner } from '@/components/ui/spinner'
import { CircleAlertIcon } from 'lucide-react'

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
    <main className="relative flex min-h-full items-center justify-center bg-linear-to-br from-brand-soft-strong via-slate-50 to-emerald-50 px-4 py-10 dark:from-brand-soft dark:via-background dark:to-emerald-950">
      <div className="absolute top-4 right-4 flex items-center gap-1">
        <AccentPicker />
        <ModeToggle />
      </div>
      <div className="w-full max-w-sm">
        <div className="mb-6 flex flex-col items-center gap-3">
          <GrapeMark className="h-14 w-14" />
          <div className="text-center">
            <h1 className="text-xl font-semibold text-foreground">登录 Grape</h1>
            <p className="mt-1 text-sm text-muted-foreground">AI 对话与运维复现工作台</p>
          </div>
        </div>
        <Card>
          <CardHeader>
            <CardTitle className="sr-only">登录</CardTitle>
            <CardDescription className="sr-only">输入租户与凭据登录 Grape</CardDescription>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleSubmit} className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="tenantId">租户 ID（可选）</Label>
                <Input
                  id="tenantId"
                  name="tenantId"
                  placeholder="留空使用默认租户"
                  value={tenantId}
                  onChange={(e) => setTenantId(e.target.value)}
                  autoComplete="off"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="userName">用户名</Label>
                <Input
                  id="userName"
                  name="userName"
                  placeholder="输入用户名"
                  required
                  value={userName}
                  onChange={(e) => setUserName(e.target.value)}
                  autoComplete="username"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="password">口令</Label>
                <Input
                  id="password"
                  name="password"
                  type="password"
                  placeholder="输入口令"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="current-password"
                />
              </div>
              {error !== null && (
                <Alert variant="destructive">
                  <CircleAlertIcon aria-hidden />
                  <AlertDescription>
                    <span>{error}</span>
                  </AlertDescription>
                </Alert>
              )}
              <Button type="submit" disabled={submitting} className="w-full">
                {submitting && <Spinner aria-hidden className="size-4" />}
                {submitting ? '登录中…' : '登录'}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </main>
  )
}
