import { useCallback, useEffect, useRef, useState, type ReactNode, type UIEvent } from 'react'
import { CheckIcon, ChevronsUpDownIcon } from 'lucide-react'
import { cn } from 'cn'
import type { OptionDTO, OptionQueryInput } from '@/api/assignOptions'
import { Button } from '@/components/ui/button'
import {
  Command,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Spinner } from '@/components/ui/spinner'

// OptionPage 是候选来源的一页结果（与 query.v1.OptionSet 的前端形态
// 一致：items 已补齐为空数组）。
export interface OptionPage {
  items: OptionDTO[]
  hasMore: boolean
  page: number
}

// OptionLoader 拉取一页候选；keyword 为空即不过滤。
export type OptionLoader = (query: OptionQueryInput) => Promise<OptionPage>

// PAGE_SIZE / SEARCH_DEBOUNCE_MS：契约固定每页 20 条、远程搜索防抖 250ms。
const PAGE_SIZE = 20
const SEARCH_DEBOUNCE_MS = 250

// optionLabel 渲染已选值：优先已知 label，未知（不在候选且未提供回显
// 标签）时显示原始 ID 并标记不可见。
export function optionLabel(value: string, labels: Record<string, string>): string {
  const label = labels[value]
  return label !== undefined && label !== '' ? label : `ID ${value}（不可见）`
}

// OptionSelect 是远程搜索选项选择器（cmdk + Popover）：打开时加载首页，
// 输入关键字经 250ms 防抖重查，滚动到底或点「加载更多」续页；单选即选
// 即关，多选逐项增删。已选值不在候选时保留回显标签（无标签显示
// `ID {value}（不可见）`）。
export function OptionSelect({
  id,
  ariaLabel,
  mode,
  value,
  onChange,
  loadOptions,
  initialPage,
  selectedLabels,
  pinnedValues,
  placeholder = '请选择',
  disabled = false,
}: {
  id?: string
  ariaLabel: string
  mode: 'single' | 'multi'
  value: string[]
  onChange: (value: string[]) => void
  loadOptions: OptionLoader
  initialPage?: OptionPage
  selectedLabels?: Record<string, string>
  // pinnedValues 是远程来源可能不返回、但必须保持可选的当前值（如已拥有
  // 的角色、当前归属）：它们与已选值一起固定保留在列表里，清除选择后仍可
  // 重新勾选，避免「清掉即从列表消失、无法选回」。
  pinnedValues?: string[]
  placeholder?: string
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  const [items, setItems] = useState<OptionDTO[]>([])
  const [page, setPage] = useState(1)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pickedLabels, setPickedLabels] = useState<Record<string, string>>({})

  // loadOptions 可能是行内箭头函数；用 ref 固定回调身份，避免搜索 effect
  // 因父组件重渲染而反复重排。
  const loaderRef = useRef(loadOptions)
  loaderRef.current = loadOptions
  const requestSeqRef = useRef(0)
  const loadingRef = useRef(false)
  const loadedKeywordRef = useRef('')
  const suppressSearchRef = useRef(false)
  const initialConsumedRef = useRef(false)

  const loadPage = useCallback(async (nextPage: number, nextKeyword: string) => {
    const seq = ++requestSeqRef.current
    loadingRef.current = true
    setLoading(true)
    setError(null)
    try {
      const result = await loaderRef.current({
        keyword: nextKeyword,
        page: nextPage,
        pageSize: PAGE_SIZE,
      })
      if (seq !== requestSeqRef.current) {
        return
      }
      setItems((prev) => (nextPage === 1 ? result.items : [...prev, ...result.items]))
      setPage(result.page > 0 ? result.page : nextPage)
      setHasMore(result.hasMore)
    } catch (err) {
      if (seq === requestSeqRef.current) {
        setError(err instanceof Error ? err.message : '加载选项失败')
      }
    } finally {
      if (seq === requestSeqRef.current) {
        loadingRef.current = false
        setLoading(false)
      }
    }
  }, [])

  // 关键字变化防抖 250ms 后回到第一页；打开时的重置由 handleOpenChange
  // 直接加载，不经过防抖。
  useEffect(() => {
    if (!open) {
      return
    }
    if (suppressSearchRef.current) {
      suppressSearchRef.current = false
      return
    }
    if (keyword === loadedKeywordRef.current) {
      return
    }
    const timer = window.setTimeout(() => {
      loadedKeywordRef.current = keyword
      void loadPage(1, keyword)
    }, SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [keyword, open, loadPage])

  const handleOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      return
    }
    setError(null)
    setKeyword('')
    loadedKeywordRef.current = ''
    suppressSearchRef.current = true
    if (initialPage !== undefined && !initialConsumedRef.current) {
      initialConsumedRef.current = true
      setItems(initialPage.items)
      setPage(initialPage.page > 0 ? initialPage.page : 1)
      setHasMore(initialPage.hasMore)
      return
    }
    setItems([])
    setPage(1)
    setHasMore(false)
    void loadPage(1, '')
  }

  const loadMore = () => {
    if (loadingRef.current || !hasMore) {
      return
    }
    void loadPage(page + 1, loadedKeywordRef.current)
  }

  const handleScroll = (event: UIEvent<HTMLDivElement>) => {
    const el = event.currentTarget
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 24) {
      loadMore()
    }
  }

  const labels: Record<string, string> = { ...(selectedLabels ?? {}), ...pickedLabels }
  for (const item of items) {
    labels[item.value] = item.label
  }
  const selected = new Set(value)

  // 固定保留项 = 调用方声明的当前值 + 已选值：远程来源（候选接口会排除
  // 已拥有对象、搜索/翻页也只覆盖当前页）不返回它们时本地合成行，保证
  // 取消勾选或「清除选择」后仍能重新选回。关键字非空时按本地 label 过滤。
  const itemByValue = new Map(items.map((item) => [item.value, item]))
  const displayItems: OptionDTO[] = []
  const displaySeen = new Set<string>()
  const trimmedKeyword = keyword.trim()
  const loweredKeyword = trimmedKeyword.toLowerCase()
  for (const pinned of [...(pinnedValues ?? []), ...value]) {
    if (displaySeen.has(pinned)) {
      continue
    }
    const loaded = itemByValue.get(pinned)
    if (loaded !== undefined) {
      displaySeen.add(pinned)
      displayItems.push(loaded)
      continue
    }
    const label = optionLabel(pinned, labels)
    if (
      loweredKeyword !== '' &&
      !label.toLowerCase().includes(loweredKeyword) &&
      !pinned.includes(trimmedKeyword)
    ) {
      continue
    }
    displaySeen.add(pinned)
    displayItems.push({ value: pinned, label, enabled: true, reason: '', assigned: selected.has(pinned) })
  }
  for (const item of items) {
    if (displaySeen.has(item.value)) {
      continue
    }
    displaySeen.add(item.value)
    displayItems.push(item)
  }

  const toggle = (item: OptionDTO) => {
    if (!item.enabled) {
      return
    }
    setPickedLabels((prev) => ({ ...prev, [item.value]: item.label }))
    if (mode === 'single') {
      onChange([item.value])
      setOpen(false)
      return
    }
    onChange(selected.has(item.value) ? value.filter((v) => v !== item.value) : [...value, item.value])
  }

  let triggerContent: ReactNode
  if (value.length === 0) {
    triggerContent = <span className="text-muted-foreground">{placeholder}</span>
  } else if (mode === 'single') {
    triggerContent = <span className="truncate">{optionLabel(value[0], labels)}</span>
  } else {
    const shown = value.slice(0, 2).map((v) => optionLabel(v, labels))
    const rest = value.length - shown.length
    triggerContent = (
      <span className="truncate">
        {shown.join('、')}
        {rest > 0 ? ` 等 ${value.length} 项` : ''}
      </span>
    )
  }

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <Button
          id={id}
          type="button"
          variant="outline"
          role="combobox"
          aria-label={ariaLabel}
          aria-expanded={open}
          disabled={disabled}
          className="w-full justify-between font-normal"
        >
          <span className="flex min-w-0 flex-1 items-center">{triggerContent}</span>
          <ChevronsUpDownIcon aria-hidden className="size-4 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-0">
        <Command shouldFilter={false} label={`${ariaLabel}搜索`}>
          <CommandInput
            placeholder="输入关键字搜索"
            value={keyword}
            onValueChange={setKeyword}
          />
          <CommandList onScroll={handleScroll} className="max-h-64">
            {error !== null ? (
              <p role="alert" className="px-2 py-3 text-sm text-destructive">
                {error}
              </p>
            ) : null}
            {loading && displayItems.length === 0 && error === null ? (
              <div className="flex items-center justify-center py-6" aria-label="选项加载中">
                <Spinner aria-hidden className="size-5" />
              </div>
            ) : null}
            {!loading && displayItems.length === 0 && error === null ? (
              <p className="py-6 text-center text-sm text-muted-foreground">没有匹配的选项</p>
            ) : null}
            {displayItems.map((item) => {
              const checked = selected.has(item.value)
              return (
                <CommandItem
                  key={item.value}
                  value={item.value}
                  disabled={!item.enabled}
                  data-checked={checked ? 'true' : 'false'}
                  onSelect={() => toggle(item)}
                >
                  <span
                    aria-hidden
                    className={cn(
                      'flex size-4 shrink-0 items-center justify-center rounded-sm border',
                      checked ? 'border-primary bg-primary text-primary-foreground' : 'border-input',
                    )}
                  >
                    {checked ? <CheckIcon className="size-3" /> : null}
                  </span>
                  <span className="truncate">{item.label}</span>
                  {!item.enabled && item.reason !== '' ? (
                    <span className="ml-auto text-xs text-muted-foreground">{item.reason}</span>
                  ) : null}
                </CommandItem>
              )
            })}
          </CommandList>
          {hasMore ? (
            <div className="border-t p-2">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="w-full"
                disabled={loading}
                onClick={loadMore}
              >
                {loading ? '加载中…' : '加载更多'}
              </Button>
            </div>
          ) : null}
          {value.length > 0 ? (
            <div className="border-t p-2">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="w-full"
                onClick={() => onChange([])}
              >
                清除选择
              </Button>
            </div>
          ) : null}
        </Command>
      </PopoverContent>
    </Popover>
  )
}
