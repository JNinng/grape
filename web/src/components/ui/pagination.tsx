import * as React from 'react'
import { cn } from 'cn'
import { ChevronLeftIcon, ChevronRightIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'

// 源自 shadcn/ui new-york-v4 pagination 的本地改写：链接版（<a href>）
// 服务整页跳转，管理页分页是页内状态切换，统一改为按钮承载；仅保留
// 上一页 / 下一页两端项，页码展示交由使用方（DataTable）拼装。
function Pagination({ className, ...props }: React.ComponentProps<'nav'>) {
  return (
    <nav
      role="navigation"
      aria-label="分页"
      data-slot="pagination"
      className={cn('mx-auto flex w-full items-center justify-center', className)}
      {...props}
    />
  )
}

function PaginationContent({
  className,
  ...props
}: React.ComponentProps<'ul'>) {
  return (
    <ul
      data-slot="pagination-content"
      className={cn('flex flex-row items-center gap-1', className)}
      {...props}
    />
  )
}

function PaginationItem({ ...props }: React.ComponentProps<'li'>) {
  return <li data-slot="pagination-item" {...props} />
}

function PaginationPrevious({
  className,
  ...props
}: React.ComponentProps<typeof Button>) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className={cn('gap-1 pl-2.5', className)}
      {...props}
    >
      <ChevronLeftIcon aria-hidden className="size-4" />
      <span>上一页</span>
    </Button>
  )
}

function PaginationNext({
  className,
  ...props
}: React.ComponentProps<typeof Button>) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className={cn('gap-1 pr-2.5', className)}
      {...props}
    >
      <span>下一页</span>
      <ChevronRightIcon aria-hidden className="size-4" />
    </Button>
  )
}

export { Pagination, PaginationContent, PaginationItem, PaginationNext, PaginationPrevious }
