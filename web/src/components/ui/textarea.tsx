import * as React from 'react'
import { cn } from 'cn'

// 本地改写：聚焦/校验环与 dark 分支换实色 token（ring-* / bg-input）。
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
