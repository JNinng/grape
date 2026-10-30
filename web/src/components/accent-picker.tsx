import { PaletteIcon } from 'lucide-react'
import { useTheme, type Accent } from '@/components/theme-provider'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

// 可选主题色系：swatch 为浅色主色值，仅作下拉内的示意色点。
const ACCENTS: { value: Accent; label: string; swatch: string }[] = [
  { value: 'violet', label: '紫色', swatch: '#7c3aed' },
  { value: 'blue', label: '蓝色', swatch: '#2563eb' },
  { value: 'green', label: '绿色', swatch: '#047857' },
  { value: 'amber', label: '琥珀', swatch: '#b45309' },
  { value: 'safe', label: '色盲友好', swatch: '#0072b2' },
]

// AccentPicker 主题色系切换器：品牌色单选下拉，选择持久化并即时生效
// （按钮、会话选中、链路高亮、审批方向软色对随 data-accent 整体切换）。
export function AccentPicker() {
  const { accent, setAccent } = useTheme()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label="切换主题色">
          <PaletteIcon className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuRadioGroup value={accent} onValueChange={setAccent}>
          {ACCENTS.map((item) => (
            <DropdownMenuRadioItem key={item.value} value={item.value}>
              <span
                aria-hidden
                className="size-2.5 shrink-0 rounded-full"
                style={{ backgroundColor: item.swatch }}
              />
              {item.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
