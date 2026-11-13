// 思考强度的前端契约：档位经 AG-UI forwardedProps 随每次运行下发
// （键 thinking，取值 off/low/medium/high），后端据此覆盖本次运行的模型
// 生成配置；偏好在 localStorage 持久化，刷新后沿用。缺省为中档，与后端
// 装配期生成配置（思考开启、不指定 effort）语义一致。

const storageKey = 'grape.thinking'

export type ThinkingLevel = 'off' | 'low' | 'medium' | 'high'

// THINKING_LEVELS 是弹框内的档位顺序：由关闭到高，措辞面向使用者而非
// provider 的 effort 取值。
export const THINKING_LEVELS: readonly { value: ThinkingLevel; label: string }[] = [
  { value: 'off', label: '关闭' },
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
]

const defaultLevel: ThinkingLevel = 'medium'

export interface ThinkingForwardedProps {
  thinking: ThinkingLevel
}

// isThinkingLevel 收窄存储值与协议取值：词表外一律视为未指定。
export function isThinkingLevel(value: string): value is ThinkingLevel {
  return THINKING_LEVELS.some((level) => level.value === value)
}

// thinkingLevelLabel 取档位的中文标签：未知值按默认档位呈现，不抛错。
export function thinkingLevelLabel(level: ThinkingLevel): string {
  return THINKING_LEVELS.find((item) => item.value === level)?.label ?? thinkingLevelLabel(defaultLevel)
}

// buildThinkingForwardedProps 构造运行请求的 forwardedProps：显式下发档位，
// 不依赖 provider 的默认值。
export function buildThinkingForwardedProps(level: ThinkingLevel): ThinkingForwardedProps {
  return { thinking: level }
}

// loadThinkingPreference 读取本地偏好：未设置、词表外或存储不可用时回落
// 缺省档位；旧版本的 on/off 值中 off 是合法档位，on 落到缺省档位。
export function loadThinkingPreference(): ThinkingLevel {
  try {
    const stored = localStorage.getItem(storageKey)
    return stored !== null && isThinkingLevel(stored) ? stored : defaultLevel
  } catch {
    return defaultLevel
  }
}

// saveThinkingPreference 持久化偏好：存储不可用（隐私模式等）只影响记忆，
// 不影响本次运行。
export function saveThinkingPreference(level: ThinkingLevel): void {
  try {
    localStorage.setItem(storageKey, level)
  } catch {
    // 忽略存储失败。
  }
}
