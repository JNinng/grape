import * as React from 'react'

// 主题提供者：移植自 shadcn/ui 官方 vite-app 模板（零依赖，替代 next-themes）。
// theme 持久化在 localStorage（键 "theme"，dark/light/system），system 经
// matchMedia 实时跟随系统切换；切换时临时禁用过渡避免颜色渐变闪烁；跨标签
// 页通过 storage 事件同步。html 上的 .dark 类在 index.html 头部脚本预挂载，
// 避免暗色用户刷新时闪白。相较模板移除了按 d 键切换的彩蛋，并补充
// resolvedTheme 供切换按钮展示当前生效主题。
type Theme = 'dark' | 'light' | 'system'
type ResolvedTheme = 'dark' | 'light'

// 主题色系（accent）：与明暗正交的品牌色选择，violet 即 :root 默认值，
// 其余经 html[data-accent] 触发 index.css 的覆盖块（含色盲友好色系）。
export type Accent = 'violet' | 'blue' | 'green' | 'amber' | 'safe'

type ThemeProviderProps = {
  children: React.ReactNode
  defaultTheme?: Theme
  defaultAccent?: Accent
  storageKey?: string
  accentStorageKey?: string
  disableTransitionOnChange?: boolean
}

type ThemeProviderState = {
  theme: Theme
  resolvedTheme: ResolvedTheme
  setTheme: (theme: Theme) => void
  accent: Accent
  setAccent: (accent: Accent) => void
}

const COLOR_SCHEME_QUERY = '(prefers-color-scheme: dark)'
const THEME_VALUES: Theme[] = ['dark', 'light', 'system']
const ACCENT_VALUES: Accent[] = ['violet', 'blue', 'green', 'amber', 'safe']

const ThemeProviderContext = React.createContext<ThemeProviderState | undefined>(
  undefined
)

function isTheme(value: string | null): value is Theme {
  if (value === null) {
    return false
  }

  return THEME_VALUES.includes(value as Theme)
}

function isAccent(value: string | null): value is Accent {
  if (value === null) {
    return false
  }

  return ACCENT_VALUES.includes(value as Accent)
}

function getSystemTheme(): ResolvedTheme {
  if (window.matchMedia(COLOR_SCHEME_QUERY).matches) {
    return 'dark'
  }

  return 'light'
}

function resolveTheme(theme: Theme): ResolvedTheme {
  return theme === 'system' ? getSystemTheme() : theme
}

function disableTransitionsTemporarily() {
  const style = document.createElement('style')
  style.appendChild(
    document.createTextNode(
      '*,*::before,*::after{-webkit-transition:none!important;transition:none!important}'
    )
  )
  document.head.appendChild(style)

  return () => {
    window.getComputedStyle(document.body)
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        style.remove()
      })
    })
  }
}

export function ThemeProvider({
  children,
  defaultTheme = 'system',
  defaultAccent = 'violet',
  storageKey = 'theme',
  accentStorageKey = 'accent',
  disableTransitionOnChange = true,
  ...props
}: ThemeProviderProps) {
  const [theme, setThemeState] = React.useState<Theme>(() => {
    const storedTheme = localStorage.getItem(storageKey)
    if (isTheme(storedTheme)) {
      return storedTheme
    }

    return defaultTheme
  })

  const [accent, setAccentState] = React.useState<Accent>(() => {
    const storedAccent = localStorage.getItem(accentStorageKey)
    if (isAccent(storedAccent)) {
      return storedAccent
    }

    return defaultAccent
  })

  const [resolvedTheme, setResolvedTheme] = React.useState<ResolvedTheme>(() =>
    resolveTheme(theme)
  )

  const setTheme = React.useCallback(
    (nextTheme: Theme) => {
      localStorage.setItem(storageKey, nextTheme)
      setThemeState(nextTheme)
    },
    [storageKey]
  )

  const setAccent = React.useCallback(
    (nextAccent: Accent) => {
      localStorage.setItem(accentStorageKey, nextAccent)
      setAccentState(nextAccent)
    },
    [accentStorageKey]
  )

  // accent 挂到 html 的 data-accent 属性：默认色系不落属性，保持 :root 值。
  React.useEffect(() => {
    const root = document.documentElement
    if (accent === 'violet') {
      root.removeAttribute('data-accent')
    } else {
      root.dataset.accent = accent
    }
  }, [accent])

  const applyTheme = React.useCallback(
    (nextTheme: Theme) => {
      const root = document.documentElement
      const nextResolvedTheme = resolveTheme(nextTheme)
      const restoreTransitions = disableTransitionOnChange
        ? disableTransitionsTemporarily()
        : null

      root.classList.remove('light', 'dark')
      root.classList.add(nextResolvedTheme)
      setResolvedTheme(nextResolvedTheme)

      if (restoreTransitions) {
        restoreTransitions()
      }
    },
    [disableTransitionOnChange]
  )

  React.useEffect(() => {
    applyTheme(theme)

    if (theme !== 'system') {
      return undefined
    }

    const mediaQuery = window.matchMedia(COLOR_SCHEME_QUERY)
    const handleChange = () => {
      applyTheme('system')
    }

    mediaQuery.addEventListener('change', handleChange)

    return () => {
      mediaQuery.removeEventListener('change', handleChange)
    }
  }, [theme, applyTheme])

  React.useEffect(() => {
    const handleStorageChange = (event: StorageEvent) => {
      if (event.storageArea !== localStorage) {
        return
      }

      if (event.key === storageKey) {
        if (isTheme(event.newValue)) {
          setThemeState(event.newValue)
        } else {
          setThemeState(defaultTheme)
        }
        return
      }

      if (event.key === accentStorageKey) {
        if (isAccent(event.newValue)) {
          setAccentState(event.newValue)
        } else {
          setAccentState(defaultAccent)
        }
      }
    }

    window.addEventListener('storage', handleStorageChange)

    return () => {
      window.removeEventListener('storage', handleStorageChange)
    }
  }, [defaultTheme, defaultAccent, storageKey, accentStorageKey])

  const value = React.useMemo(
    () => ({
      theme,
      resolvedTheme,
      setTheme,
      accent,
      setAccent,
    }),
    [theme, resolvedTheme, setTheme, accent, setAccent]
  )

  return (
    <ThemeProviderContext.Provider {...props} value={value}>
      {children}
    </ThemeProviderContext.Provider>
  )
}

export const useTheme = () => {
  const context = React.useContext(ThemeProviderContext)

  if (context === undefined) {
    throw new Error('useTheme must be used within a ThemeProvider')
  }

  return context
}
