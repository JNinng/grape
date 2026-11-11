import { useCallback, useEffect, useState } from 'react'
import { fetchModelCalls, type ModelCall } from '../../api/agentModelCalls'
import { ChevronDownIcon, CircleAlertIcon, RotateCwIcon } from 'lucide-react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Spinner } from '@/components/ui/spinner'

// formatTime 渲染 UTC RFC3339 为本地时间；非法时间原样呈现。
function formatTime(iso: string): string {
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString()
}

// statusChipClass 按执行状态区分：success 走 success 软色对，其余 danger
//（与复现页其他证据行同约定，词表外 fail-closed 显红）。
function statusChipClass(status: string): string {
  return status === 'success'
    ? 'bg-success-soft text-success-text'
    : 'bg-danger-soft text-danger-text'
}

// scenarioOptions 是场景过滤的封闭选项（与后端 biz 词表对齐）；空值即
// 不过滤。词表扩展（如 rerank）随 biz 常量演进在此追加。
const scenarioOptions: Array<{ value: string; label: string }> = [
  { value: '', label: '全部场景' },
  { value: 'ingest_summary', label: '摄取摘要' },
  { value: 'ingest_prefix', label: '块前缀' },
  { value: 'embed_document', label: '摄取向量化' },
  { value: 'embed_query', label: '检索向量化' },
  { value: 'parse_document', label: '外部解析' },
]

// tokensText 渲染 token 用量：未上报（null）不以 0 冒充，给占位。
function tokensText(row: ModelCall): string {
  const input = row.inputTokens ?? '—'
  const output = row.outputTokens ?? '—'
  return `tokens ${input} / ${output}`
}

// ModelCallRow 是单条留痕的折叠行：摘要行始终可见（场景/状态/模型键/
// 耗时/用量/时间），展开后原样渲染落库的输入/输出摘要与错误（落库即
// 所显，纯文本渲染不走 innerHTML——web/README.md 约束）。
function ModelCallRow({ row }: { row: ModelCall }) {
  return (
    <Collapsible className="w-full overflow-hidden rounded-xl border bg-card shadow-sm">
      <CollapsibleTrigger className="group flex w-full cursor-pointer flex-wrap items-center gap-x-2 gap-y-1 px-3.5 py-2.5 text-left text-sm text-muted-foreground transition-colors select-none hover:bg-soft">
        <Badge className={statusChipClass(row.status)}>
          {row.scenario}/{row.status}
        </Badge>
        <span className="font-mono text-xs text-foreground">{row.modelKey || '（无模型键）'}</span>
        <span className="text-xs tabular-nums">{tokensText(row)}</span>
        <span className="text-xs tabular-nums">{row.durationMs}ms</span>
        {row.documentId !== null && (
          <span className="text-xs tabular-nums">文档 {row.documentId}</span>
        )}
        <span className="text-xs tabular-nums">{formatTime(row.createdAt)}</span>
        <ChevronDownIcon
          aria-hidden
          className="ml-auto size-4 shrink-0 text-muted-foreground transition-transform duration-200 group-data-[state=open]:rotate-180"
        />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="space-y-2.5 border-t px-3.5 py-3 text-xs text-secondary-foreground">
          {row.endpoint && <p className="break-all font-mono">端点：{row.endpoint}</p>}
          {row.traceId && <p className="break-all font-mono">trace {row.traceId}</p>}
          {row.inputSummary && (
            <>
              <h4 className="text-xs font-medium text-muted-foreground">输入</h4>
              <pre className="code-block">{row.inputSummary}</pre>
            </>
          )}
          {row.outputSummary && (
            <>
              <h4 className="text-xs font-medium text-muted-foreground">输出</h4>
              <pre className="code-block">{row.outputSummary}</pre>
            </>
          )}
          {row.error && (
            <>
              <h4 className="text-xs font-medium text-muted-foreground">错误</h4>
              <pre className="code-block">{row.error}</pre>
            </>
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

// ModelCallPanel 是模型调用留痕面板：管线侧模型/类模型调用的证据面
//（摄取摘要/块前缀/向量化/外部解析），与检索留痕面板同形态——列表 +
// 过滤器（场景/模型键/状态），行展开原样渲染 input/output。数据按租户
// 查询（服务端认证 + 行级），不随会话选择联动：管线调用没有会话事实。
export function ModelCallPanel() {
  const [rows, setRows] = useState<ModelCall[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [scenario, setScenario] = useState('')
  const [modelKey, setModelKey] = useState('')
  const [status, setStatus] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      setRows(await fetchModelCalls({ scenario, modelKey, status }))
    } catch (err) {
      setError(err instanceof Error ? err.message : '模型调用留痕加载失败')
    } finally {
      setLoading(false)
    }
  }, [scenario, modelKey, status])

  useEffect(() => {
    void load()
  }, [load])

  return (
    <section aria-label="模型调用">
      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-semibold">模型调用</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <select
              aria-label="场景过滤"
              value={scenario}
              onChange={(e) => setScenario(e.target.value)}
              className="h-8 rounded-md border border-input bg-transparent px-2 text-xs"
            >
              {scenarioOptions.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            <input
              aria-label="模型键过滤"
              value={modelKey}
              onChange={(e) => setModelKey(e.target.value)}
              placeholder="模型键（精确匹配）"
              className="h-8 w-44 rounded-md border border-input bg-transparent px-2 text-xs placeholder:text-muted-foreground"
            />
            <select
              aria-label="状态过滤"
              value={status}
              onChange={(e) => setStatus(e.target.value)}
              className="h-8 rounded-md border border-input bg-transparent px-2 text-xs"
            >
              <option value="">全部状态</option>
              <option value="success">成功</option>
              <option value="error">失败</option>
            </select>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={loading}
              onClick={() => void load()}
            >
              <RotateCwIcon aria-hidden className="size-3.5" />
              刷新
            </Button>
            {loading && (
              <Badge role="status" variant="secondary">
                <Spinner aria-hidden className="size-3" />
                加载中…
              </Badge>
            )}
          </div>
          {error !== null && (
            <Alert variant="destructive">
              <CircleAlertIcon aria-hidden className="mt-0.5 shrink-0" />
              <AlertDescription>
                <span>加载失败：{error}</span>
              </AlertDescription>
            </Alert>
          )}
          {rows.length === 0 && !error && (
            <p className="rounded-xl border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
              暂无模型调用留痕。
            </p>
          )}
          <ol className="space-y-2">
            {rows.map((row) => (
              <li key={row.id}>
                <ModelCallRow row={row} />
              </li>
            ))}
          </ol>
        </CardContent>
      </Card>
    </section>
  )
}
