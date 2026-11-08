import { useCallback, useEffect, useState } from 'react'
import { CpuIcon, PencilIcon, PlusIcon, Trash2Icon } from 'lucide-react'
import {
  createModel,
  createModelSuper,
  deleteModel,
  deleteModelSuper,
  listModels,
  updateModel,
  updateModelSuper,
  type ModelDTO,
} from '@/api/models'
import { usePermissions } from '@/auth/permissions'
import { PERM } from '@/auth/permissionCodes'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { toast } from '@/components/ui/toast'

// ModelManagePage 是模型目录管理页：租户行可维护，平台行只读（持有
// sys:model:platform:* 时可编辑）。密钥只写不读：响应仅展示「已配置」，
// 更新时留空表示不变，清除开关表示删除密文。

const EMBEDDING_DIMENSION = 1024

const PROVIDERS = ['openai', 'deepseek', 'qwen', 'glm', 'minimax', 'kimi', 'hunyuan'] as const

function errorHint(err: unknown, fallback: string): string {
  if (err instanceof Error && err.message) {
    return err.message
  }
  return fallback
}

export function ModelManagePage() {
  const { can } = usePermissions()
  const canCreate = can(PERM.ModelCreate)
  const canUpdate = can(PERM.ModelUpdate)
  const canDelete = can(PERM.ModelDelete)
  const canPlatformCreate = can(PERM.ModelPlatformCreate)
  const canPlatformUpdate = can(PERM.ModelPlatformUpdate)
  const canPlatformDelete = can(PERM.ModelPlatformDelete)

  const [kind, setKind] = useState<string>('all')
  const [models, setModels] = useState<ModelDTO[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [dialog, setDialog] = useState<{ mode: 'create'; platform: boolean } | { mode: 'edit'; model: ModelDTO } | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<ModelDTO | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const set = await listModels(kind === 'all' ? undefined : (kind as 'text' | 'embedding'))
      setModels(set.items)
    } catch (err) {
      setError(errorHint(err, '加载模型失败'))
    } finally {
      setLoading(false)
    }
  }, [kind])

  useEffect(() => {
    void load()
  }, [load, reloadKey])

  const performDelete = async () => {
    if (!confirmDelete) {
      return
    }
    try {
      if (confirmDelete.platform) {
        await deleteModelSuper(confirmDelete.id)
      } else {
        await deleteModel(confirmDelete.id)
      }
      toast.success(`模型 ${confirmDelete.name} 已删除`)
      setReloadKey((key) => key + 1)
    } catch (err) {
      toast.error(`删除失败：${errorHint(err, 'delete failed')}`)
    } finally {
      setConfirmDelete(null)
    }
  }

  return (
    <div className="flex h-full flex-col gap-4 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-base font-semibold">
            <CpuIcon aria-hidden className="size-4" />
            模型管理
          </h1>
          <p className="text-xs text-muted-foreground">
            平台模型对所有租户可见；租户模型覆盖同名平台模型。嵌入模型本期固定 {EMBEDDING_DIMENSION} 维。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select value={kind} onValueChange={setKind}>
            <SelectTrigger className="w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部类别</SelectItem>
              <SelectItem value="text">文本模型</SelectItem>
              <SelectItem value="embedding">嵌入模型</SelectItem>
            </SelectContent>
          </Select>
          {canCreate ? (
            <Button size="sm" onClick={() => setDialog({ mode: 'create', platform: false })}>
              <PlusIcon aria-hidden className="size-4" />
              新建租户模型
            </Button>
          ) : null}
          {canPlatformCreate ? (
            <Button variant="outline" size="sm" onClick={() => setDialog({ mode: 'create', platform: true })}>
              <PlusIcon aria-hidden className="size-4" />
              新建平台模型
            </Button>
          ) : null}
        </div>
      </div>

      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      <div className="min-h-0 flex-1 overflow-auto rounded-xl border border-border bg-card p-4">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>名称</TableHead>
              <TableHead>级别</TableHead>
              <TableHead>类别</TableHead>
              <TableHead>模型键</TableHead>
              <TableHead>Provider</TableHead>
              <TableHead>模型名</TableHead>
              <TableHead>维度</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>密钥</TableHead>
              <TableHead className="w-40">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              <TableRow>
                <TableCell colSpan={10} className="text-sm text-muted-foreground">
                  加载中…
                </TableCell>
              </TableRow>
            ) : null}
            {!loading && models.length === 0 ? (
              <TableRow>
                <TableCell colSpan={10} className="text-sm text-muted-foreground">
                  暂无模型
                </TableCell>
              </TableRow>
            ) : null}
            {models.map((model) => {
              const editable = model.platform ? canPlatformUpdate : canUpdate
              const deletable = model.platform ? canPlatformDelete : canDelete
              return (
                <TableRow key={model.id}>
                  <TableCell className="font-medium">{model.name}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {model.platform ? '平台' : '租户'}
                  </TableCell>
                  <TableCell>{model.kind === 'embedding' ? '嵌入' : '文本'}</TableCell>
                  <TableCell className="font-mono text-xs">{model.modelKey}</TableCell>
                  <TableCell>{model.provider}</TableCell>
                  <TableCell className="font-mono text-xs">{model.model}</TableCell>
                  <TableCell>{model.dimension ?? '—'}</TableCell>
                  <TableCell className={model.status === 1 ? 'text-emerald-700' : 'text-muted-foreground'}>
                    {model.status === 1 ? '启用' : '停用'}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {model.hasApiKey ? '已配置' : '未配置'}
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {editable ? (
                        <Button variant="ghost" size="sm" onClick={() => setDialog({ mode: 'edit', model })}>
                          <PencilIcon aria-hidden className="size-3.5" />
                          编辑
                        </Button>
                      ) : null}
                      {deletable ? (
                        <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(model)}>
                          <Trash2Icon aria-hidden className="size-3.5" />
                          删除
                        </Button>
                      ) : null}
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      {dialog ? (
        <ModelDialog
          model={dialog.mode === 'edit' ? dialog.model : undefined}
          platform={dialog.mode === 'create' ? dialog.platform : dialog.model.platform}
          onClose={() => setDialog(null)}
          onSaved={() => {
            setDialog(null)
            setReloadKey((key) => key + 1)
          }}
        />
      ) : null}

      {confirmDelete ? (
        <AlertDialog open onOpenChange={(open) => { if (!open) setConfirmDelete(null) }}>
          <AlertDialogContent size="sm">
            <AlertDialogHeader>
              <AlertDialogTitle>确认删除模型 {confirmDelete.name}？</AlertDialogTitle>
              <AlertDialogDescription>
                删除后引用该模型键的租户配置会解析失败（fail-closed），需重新选择模型。
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>取消</AlertDialogCancel>
              <AlertDialogAction variant="destructive" onClick={(e) => { e.preventDefault(); void performDelete() }}>
                删除
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      ) : null}
    </div>
  )
}

function ModelDialog({
  model,
  platform,
  onClose,
  onSaved,
}: {
  model?: ModelDTO
  platform: boolean
  onClose: () => void
  onSaved: () => void
}) {
  const [kind, setKind] = useState(model?.kind ?? 'text')
  const [name, setName] = useState(model?.name ?? '')
  const [modelKey, setModelKey] = useState(model?.modelKey ?? '')
  const [provider, setProvider] = useState(model?.provider ?? 'openai')
  const [baseUrl, setBaseUrl] = useState(model?.baseUrl ?? '')
  const [modelName, setModelName] = useState(model?.model ?? '')
  const [timeoutMs, setTimeoutMs] = useState(model?.timeoutMs ? String(model.timeoutMs) : '')
  const [apiKeyEnv, setApiKeyEnv] = useState(model?.apiKeyEnv ?? '')
  const [apiKey, setApiKey] = useState('')
  const [clearApiKey, setClearApiKey] = useState(false)
  const [remark, setRemark] = useState(model?.remark ?? '')
  const [status, setStatus] = useState(model ? model.status === 1 : true)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    setPending(true)
    setError(null)
    try {
      const timeout = timeoutMs.trim() ? Number(timeoutMs) : undefined
      const dimension = kind === 'embedding' ? EMBEDDING_DIMENSION : undefined
      if (model) {
        const input = {
          id: model.id,
          status: status ? 1 : 0,
          name: name.trim(),
          provider,
          baseUrl: baseUrl.trim() || undefined,
          model: modelName.trim(),
          dimension,
          timeoutMs: timeout,
          apiKeyEnv: apiKeyEnv.trim() || undefined,
          remark: remark.trim() || undefined,
        }
        if (platform) {
          await updateModelSuper(input, apiKey.trim() || undefined, clearApiKey)
        } else {
          await updateModel(input, apiKey.trim() || undefined, clearApiKey)
        }
        toast.success(`模型 ${name.trim()} 已更新`)
      } else {
        const input = {
          status: status ? 1 : 0,
          kind,
          name: name.trim(),
          modelKey: modelKey.trim(),
          provider,
          baseUrl: baseUrl.trim() || undefined,
          model: modelName.trim(),
          dimension,
          timeoutMs: timeout,
          apiKeyEnv: apiKeyEnv.trim() || undefined,
          remark: remark.trim() || undefined,
        }
        if (platform) {
          await createModelSuper(input, apiKey.trim() || undefined)
        } else {
          await createModel(input, apiKey.trim() || undefined)
        }
        toast.success(`模型 ${name.trim()} 已创建`)
      }
      onSaved()
    } catch (err) {
      setError(errorHint(err, 'save failed'))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {model ? '编辑模型' : '新建模型'}
            {platform ? '（平台）' : ''}
          </DialogTitle>
          <DialogDescription>
            密钥只写不读：留空表示不变，打开清除开关表示删除已存密文。
          </DialogDescription>
        </DialogHeader>
        <div className="grid max-h-[70vh] gap-3 overflow-y-auto pr-1">
          <div className="grid grid-cols-2 gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="model-kind">类别</Label>
              <Select value={kind} onValueChange={setKind} disabled={model !== undefined}>
                <SelectTrigger id="model-kind">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="text">文本模型</SelectItem>
                  <SelectItem value="embedding">嵌入模型</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="model-provider">Provider</Label>
              <Select value={provider} onValueChange={setProvider}>
                <SelectTrigger id="model-provider">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PROVIDERS.map((value) => (
                    <SelectItem key={value} value={value}>
                      {value}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="model-name">展示名</Label>
            <Input id="model-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="model-key">模型键（选择键与向量空间键）</Label>
            <Input
              id="model-key"
              value={modelKey}
              disabled={model !== undefined}
              onChange={(e) => setModelKey(e.target.value)}
              placeholder="如 qwen3:8b"
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="model-base">端点根地址</Label>
            <Input
              id="model-base"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder="如 http://127.0.0.1:11434/v1"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="model-model">Provider 模型名</Label>
              <Input id="model-model" value={modelName} onChange={(e) => setModelName(e.target.value)} />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="model-timeout">超时（毫秒）</Label>
              <Input id="model-timeout" value={timeoutMs} onChange={(e) => setTimeoutMs(e.target.value)} />
            </div>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="model-env">密钥环境变量名</Label>
            <Input id="model-env" value={apiKeyEnv} onChange={(e) => setApiKeyEnv(e.target.value)} />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="model-apikey">API key{model?.hasApiKey ? '（已配置，留空不变）' : ''}</Label>
            <Input
              id="model-apikey"
              type="password"
              autoComplete="new-password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
            />
          </div>
          {model ? (
            <div className="flex items-center gap-2">
              <Switch id="model-clear" checked={clearApiKey} onCheckedChange={setClearApiKey} />
              <Label htmlFor="model-clear">清除已存密钥</Label>
            </div>
          ) : null}
          <div className="grid gap-1.5">
            <Label htmlFor="model-remark">备注</Label>
            <Textarea id="model-remark" value={remark} onChange={(e) => setRemark(e.target.value)} />
          </div>
          <div className="flex items-center gap-2">
            <Switch id="model-status" checked={status} onCheckedChange={setStatus} />
            <Label htmlFor="model-status">启用</Label>
          </div>
          {error ? <p className="text-sm text-destructive">{error}</p> : null}
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={pending} onClick={onClose}>
            取消
          </Button>
          <Button
            disabled={pending || !name.trim() || !modelName.trim() || (!model && !modelKey.trim())}
            onClick={() => void submit()}
          >
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
