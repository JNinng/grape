import * as React from 'react'
import { cn } from 'cn'
import { Collapsible as CollapsiblePrimitive } from 'radix-ui'

function Collapsible({
  ...props
}: React.ComponentProps<typeof CollapsiblePrimitive.Root>) {
  return <CollapsiblePrimitive.Root data-slot="collapsible" {...props} />
}

function CollapsibleTrigger({
  ...props
}: React.ComponentProps<typeof CollapsiblePrimitive.CollapsibleTrigger>) {
  return (
    <CollapsiblePrimitive.CollapsibleTrigger
      data-slot="collapsible-trigger"
      {...props}
    />
  )
}

// CollapsibleContent 默认带高度开合动画（见 index.css 的
// --animate-collapsible-*；radix 在内容元素上注入目标高度变量，
// overflow-hidden 保证高度过渡只裁剪自身），motion-safe 包裹以尊重
// 系统的减弱动效设置。radix 会在关闭动画结束后才卸载内容。
function CollapsibleContent({
  className,
  ...props
}: React.ComponentProps<typeof CollapsiblePrimitive.CollapsibleContent>) {
  return (
    <CollapsiblePrimitive.CollapsibleContent
      data-slot="collapsible-content"
      className={cn(
        'overflow-hidden motion-safe:data-[state=closed]:animate-collapsible-up motion-safe:data-[state=open]:animate-collapsible-down',
        className
      )}
      {...props}
    />
  )
}

export { Collapsible, CollapsibleTrigger, CollapsibleContent }
