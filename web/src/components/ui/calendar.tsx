import * as React from 'react'
import { ChevronDownIcon, ChevronLeftIcon, ChevronRightIcon, ChevronUpIcon } from 'lucide-react'
import { DayPicker, getDefaultClassNames, type DayButtonProps } from 'react-day-picker'
import { cn } from 'cn'
import { buttonVariants } from '@/components/ui/button'

// 源自 shadcn/ui new-york-v4 calendar（底层 react-day-picker v10），按 hex
// 兼容规则改写：官方版依赖 oklch、/NN 透明度与渐变工具类，此处改用语义
// token，并把日期格子的选中/区间样式集中到 DayButton 上按 modifiers 计算
// （避免依赖 td 透底叠色的写法），交互与官方单/区间选择保持一致。

// CalendarDayButton 渲染单个日期格子：today/outside/disabled/区间与选中态
// 全部经 data 属性与条件类呈现，selected 优先级最高（区间端点是主色圆角，
// 中段是 accent 平铺）。
function CalendarDayButton({ day: _day, modifiers, className, ...props }: DayButtonProps) {
  void _day
  const rounded = modifiers.range_start
    ? 'rounded-l-md'
    : modifiers.range_end
      ? 'rounded-r-md'
      : modifiers.range_middle
        ? 'rounded-none'
        : 'rounded-md'
  const appearance = modifiers.selected
    ? 'bg-primary text-primary-foreground hover:bg-primary-hover'
    : modifiers.range_middle || modifiers.range_start || modifiers.range_end
      ? 'bg-accent text-accent-foreground hover:bg-accent'
      : modifiers.outside || modifiers.disabled
        ? 'text-muted-foreground hover:bg-transparent'
        : 'hover:bg-accent hover:text-accent-foreground'
  return (
    <button
      type="button"
      data-selected={modifiers.selected || undefined}
      data-today={modifiers.today || undefined}
      className={cn(
        buttonVariants({ variant: 'ghost' }),
        'size-8 p-0 text-sm font-normal',
        rounded,
        appearance,
        modifiers.today && 'ring-1 ring-ring',
        modifiers.disabled && 'pointer-events-none opacity-50',
        className,
      )}
      {...props}
    />
  )
}

// Calendar 日历：默认展示单月导航 + 周标题 + 非本月日期；mode（single/
// range 等）、locale、disabled 等经 props 直传 DayPicker。
function Calendar({
  className,
  classNames,
  showOutsideDays = true,
  ...props
}: React.ComponentProps<typeof DayPicker>) {
  const defaultClassNames = getDefaultClassNames()
  return (
    <DayPicker
      showOutsideDays={showOutsideDays}
      className={cn('p-3', className)}
      classNames={{
        ...defaultClassNames,
        months: 'relative flex flex-col gap-2 sm:flex-row',
        month: 'flex flex-col gap-2',
        month_caption: 'flex h-8 items-center justify-center text-sm font-medium',
        nav: 'absolute inset-x-0 top-0 flex items-center justify-between',
        button_previous: cn(
          buttonVariants({ variant: 'ghost' }),
          'size-8 p-0 text-muted-foreground hover:text-foreground aria-disabled:opacity-50',
        ),
        button_next: cn(
          buttonVariants({ variant: 'ghost' }),
          'size-8 p-0 text-muted-foreground hover:text-foreground aria-disabled:opacity-50',
        ),
        month_grid: 'mt-2 w-full border-collapse',
        weekdays: 'flex',
        weekday: 'w-8 text-center text-xs font-normal text-muted-foreground select-none',
        week: 'mt-1 flex w-full',
        day: 'relative p-0 text-center text-sm',
        day_button: 'size-8',
        ...classNames,
      }}
      components={{
        DayButton: CalendarDayButton,
        Chevron: ({ orientation, className, ...chevronProps }) =>
          orientation === 'left' ? (
            <ChevronLeftIcon aria-hidden className={cn('size-4 text-muted-foreground', className)} {...chevronProps} />
          ) : orientation === 'right' ? (
            <ChevronRightIcon aria-hidden className={cn('size-4 text-muted-foreground', className)} {...chevronProps} />
          ) : orientation === 'up' ? (
            <ChevronUpIcon aria-hidden className={cn('size-4 text-muted-foreground', className)} {...chevronProps} />
          ) : (
            <ChevronDownIcon aria-hidden className={cn('size-4 text-muted-foreground', className)} {...chevronProps} />
          ),
      }}
      {...props}
    />
  )
}

export { Calendar }
