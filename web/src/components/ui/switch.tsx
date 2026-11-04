import * as React from 'react'
import { cn } from 'cn'
import { Switch as SwitchPrimitive } from 'radix-ui'

// 源自 shadcn/ui new-york-v4 switch，按 hex 兼容规则改写：unchecked 面
// dark:bg-input/30 等 alpha 分支删除，统一 bg-input；聚焦环 ring-ring。
// 滑块位移不用 translate-x-* 工具类：v4 该族类输出 CSS translate 属性，
// 兼容目标内核不支持（声明被静默丢弃，滑块不动），统一走 transform。
function Switch({
  className,
  ...props
}: React.ComponentProps<typeof SwitchPrimitive.Root>) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={cn(
        'peer inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border border-transparent shadow-xs transition-all outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 data-[state=checked]:bg-primary data-[state=unchecked]:bg-input',
        className
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="pointer-events-none block size-4 rounded-full bg-background ring-0 transition-transform data-[state=unchecked]:[transform:translateX(0.125rem)] data-[state=checked]:[transform:translateX(1rem)]"
      />
    </SwitchPrimitive.Root>
  )
}

export { Switch }
