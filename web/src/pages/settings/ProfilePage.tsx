import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { KeyRoundIcon, ShieldCheckIcon, UserRoundIcon } from 'lucide-react'
import { ApiError } from '@/api/toApiError'
import { changeMyPassword, getMyProfile, updateMyProfile, type UserDTO } from '@/api/users'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Spinner } from '@/components/ui/spinner'
import { Textarea } from '@/components/ui/textarea'
import { toast } from '@/components/ui/toast'

// ProfileForm 是个人中心可编辑字段的本地草稿；用户名只读、不进入表单。
interface ProfileForm {
  nickname: string
  email: string
  phone: string
  remark: string
  locale: string
}

function formOf(profile: UserDTO): ProfileForm {
  return {
    nickname: profile.nickname,
    email: profile.email ?? '',
    phone: profile.phone ?? '',
    remark: profile.remark ?? '',
    locale: profile.locale,
  }
}

// initialOf 取昵称（缺省用用户名）首字符作头像占位。
function initialOf(profile: UserDTO): string {
  const label = profile.nickname.trim() === '' ? profile.name : profile.nickname
  return Array.from(label.trim())[0]?.toUpperCase() ?? '?'
}

// formatTimestamp 渲染只读时间；无法解析时原样回显服务端值。
function formatTimestamp(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

// Field 是表单字段布局：标签经 htmlFor 与控件关联。
function Field({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
    </div>
  )
}

// FormError 渲染表单内联错误（校验与后端拒绝共用一条展示位）。
function FormError({ message }: { message: string | null }) {
  if (message === null) {
    return null
  }
  return (
    <p role="alert" className="text-sm text-destructive">
      {message}
    </p>
  )
}

// CardTitleIcon 是卡片标题前的品牌色图标底座。
function CardTitleWithIcon({ icon, children }: { icon: ReactNode; children: ReactNode }) {
  return (
    <CardTitle className="flex items-center gap-2">
      <span className="flex size-8 items-center justify-center rounded-lg bg-brand-soft text-brand-text">
        {icon}
      </span>
      {children}
    </CardTitle>
  )
}

// ProfilePage 是个人中心：维护本人昵称/邮箱/手机号/备注/语言与登录口令。
// 目标恒取认证身份（服务端从会话取值），页面不携带用户 ID，也不依赖
// 管理面权限码。
export function ProfilePage() {
  const [profile, setProfile] = useState<UserDTO | null>(null)
  const [form, setForm] = useState<ProfileForm>({ nickname: '', email: '', phone: '', remark: '', locale: '' })
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [changing, setChanging] = useState(false)
  const [passwordError, setPasswordError] = useState<string | null>(null)

  useEffect(() => {
    let active = true
    setLoading(true)
    setLoadError(null)
    getMyProfile()
      .then((out) => {
        if (!active) {
          return
        }
        setProfile(out)
        setForm(formOf(out))
      })
      .catch((err) => {
        if (active) {
          setLoadError(err instanceof Error ? err.message : 'load profile failed')
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [reloadKey])

  const saveProfile = async (event: FormEvent) => {
    event.preventDefault()
    if (form.nickname.trim() === '') {
      setSaveError('nickname is required')
      return
    }
    setSaving(true)
    setSaveError(null)
    try {
      const updated = await updateMyProfile({
        nickname: form.nickname,
        email: form.email.trim() === '' ? undefined : form.email.trim(),
        phone: form.phone.trim() === '' ? undefined : form.phone.trim(),
        remark: form.remark.trim() === '' ? undefined : form.remark.trim(),
        locale: form.locale.trim(),
      })
      setProfile(updated)
      setForm(formOf(updated))
      toast.success('个人资料已更新')
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'update profile failed')
    } finally {
      setSaving(false)
    }
  }

  const submitPassword = async (event: FormEvent) => {
    event.preventDefault()
    if (currentPassword === '' || newPassword === '') {
      setPasswordError('current password and new password are required')
      return
    }
    if (newPassword !== confirmPassword) {
      setPasswordError('new passwords do not match')
      return
    }
    setChanging(true)
    setPasswordError(null)
    try {
      await changeMyPassword(currentPassword, newPassword)
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
      toast.success('密码已更新')
    } catch (err) {
      setPasswordError(
        err instanceof ApiError && err.reason === 'USER_PASSWORD_MISMATCH'
          ? 'current password is incorrect'
          : err instanceof Error
            ? err.message
            : 'change password failed',
      )
    } finally {
      setChanging(false)
    }
  }

  if (loading) {
    return (
      <div className="flex min-h-0 flex-1 flex-col p-6">
        <div className="flex flex-1 items-center justify-center" aria-label="个人资料加载中">
          <Spinner aria-hidden className="size-6" />
        </div>
      </div>
    )
  }

  if (loadError !== null || profile === null) {
    return (
      <div className="flex min-h-0 flex-1 flex-col p-6">
        <div className="mx-auto w-full max-w-5xl">
          <Alert variant="destructive">
            <AlertDescription className="flex items-center justify-between gap-3">
              <span>个人资料加载失败：{loadError ?? 'unknown error'}</span>
              <Button type="button" variant="outline" size="sm" onClick={() => setReloadKey((key) => key + 1)}>
                重试
              </Button>
            </AlertDescription>
          </Alert>
        </div>
      </div>
    )
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col p-6">
      <div className="mx-auto flex w-full max-w-5xl flex-col gap-6">
        <header className="overflow-hidden rounded-2xl border bg-card shadow-sm">
          <div className="h-24 bg-linear-to-r from-brand-soft-strong via-brand-soft to-background dark:from-brand-soft dark:via-background dark:to-background" />
          <div className="flex flex-wrap items-end justify-between gap-4 px-6 pb-6">
            <div className="-mt-9 flex items-end gap-4">
              <div
                aria-hidden
                className="flex size-16 shrink-0 items-center justify-center rounded-2xl border border-brand-soft-border bg-background text-2xl font-semibold text-brand-text shadow-sm"
              >
                {initialOf(profile)}
              </div>
              <div className="min-w-0 pb-1">
                <h1 className="truncate text-lg font-semibold text-foreground">{profile.nickname}</h1>
                <p className="truncate text-sm text-muted-foreground">@{profile.name}</p>
              </div>
            </div>
            <span className="pb-1 text-xs text-muted-foreground">个人资料与登录安全</span>
          </div>
        </header>

        <div className="grid gap-6 lg:grid-cols-[minmax(0,1.7fr)_minmax(0,1fr)]">
          <Card>
            <CardHeader>
              <CardTitleWithIcon icon={<UserRoundIcon aria-hidden className="size-4" />}>基本资料</CardTitleWithIcon>
              <CardDescription>用户名不可修改；邮箱、手机号、备注留空表示清空。</CardDescription>
            </CardHeader>
            <CardContent>
              <form className="grid gap-5" onSubmit={(e) => { void saveProfile(e) }}>
                <div className="grid gap-5 sm:grid-cols-2">
                  <Field id="profile-nickname" label="昵称">
                    <Input id="profile-nickname" value={form.nickname} onChange={(e) => setForm({ ...form, nickname: e.target.value })} autoComplete="off" />
                  </Field>
                  <Field id="profile-locale" label="语言（BCP-47）">
                    <Input id="profile-locale" value={form.locale} onChange={(e) => setForm({ ...form, locale: e.target.value })} placeholder="zh-CN" />
                  </Field>
                  <Field id="profile-email" label="邮箱">
                    <Input id="profile-email" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} autoComplete="off" />
                  </Field>
                  <Field id="profile-phone" label="手机号">
                    <Input id="profile-phone" value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} autoComplete="off" />
                  </Field>
                </div>
                <Field id="profile-remark" label="备注">
                  <Textarea id="profile-remark" className="min-h-24" value={form.remark} onChange={(e) => setForm({ ...form, remark: e.target.value })} />
                </Field>
                <FormError message={saveError} />
                <div className="flex justify-end border-t pt-4">
                  <Button type="submit" disabled={saving}>
                    保存资料
                  </Button>
                </div>
              </form>
            </CardContent>
          </Card>

          <div className="flex flex-col gap-6 self-start">
            <Card>
              <CardHeader>
                <CardTitleWithIcon icon={<KeyRoundIcon aria-hidden className="size-4" />}>修改密码</CardTitleWithIcon>
                <CardDescription>需要验证当前密码；修改后新密码立即生效。</CardDescription>
              </CardHeader>
              <CardContent>
                <form className="grid gap-4" onSubmit={(e) => { void submitPassword(e) }}>
                  <Field id="profile-old-password" label="当前密码">
                    <Input id="profile-old-password" type="password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} autoComplete="current-password" />
                  </Field>
                  <Field id="profile-new-password" label="新密码">
                    <Input id="profile-new-password" type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} autoComplete="new-password" />
                  </Field>
                  <Field id="profile-confirm-password" label="确认新密码">
                    <Input id="profile-confirm-password" type="password" value={confirmPassword} onChange={(e) => setConfirmPassword(e.target.value)} autoComplete="new-password" />
                  </Field>
                  <FormError message={passwordError} />
                  <Button type="submit" className="w-full" disabled={changing}>
                    修改密码
                  </Button>
                </form>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitleWithIcon icon={<ShieldCheckIcon aria-hidden className="size-4" />}>账号信息</CardTitleWithIcon>
              </CardHeader>
              <CardContent>
                <dl className="grid gap-3 text-sm">
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-muted-foreground">账号状态</dt>
                    <dd>
                      {profile.status === 0 ? <Badge variant="outline">禁用</Badge> : <Badge variant="secondary">启用</Badge>}
                    </dd>
                  </div>
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-muted-foreground">用户 ID</dt>
                    <dd className="truncate font-mono text-xs text-muted-foreground">{profile.id}</dd>
                  </div>
                  <div className="flex items-center justify-between gap-4">
                    <dt className="text-muted-foreground">创建时间</dt>
                    <dd className="text-muted-foreground">{formatTimestamp(profile.createdAt)}</dd>
                  </div>
                </dl>
              </CardContent>
            </Card>
          </div>
        </div>
      </div>
    </div>
  )
}
