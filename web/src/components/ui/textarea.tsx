import * as React from 'react'
import { cn } from 'cn'

// 源自 shadcn/ui new-york-v4 textarea，按 hex 兼容规则改写：ring-ring/50 与
// aria-invalid 的 alpha 环 → ring-ring / ring-destructive-soft；dark:bg-input/30 删除。
function Textarea({ className, ...props }: React.ComponentProps<'textarea'>) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        'flex field-sizing-content min-h-16 w-full rounded-md border border-input bg-transparent px-3 py-2 text-base shadow-xs transition-[color,box-shadow] outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive-soft md:text-sm',
        className
      )}
      {...props}
    />
  )
}

export { Textarea }
