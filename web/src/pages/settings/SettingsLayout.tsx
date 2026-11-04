import { useEffect, useRef, useState, type ComponentType } from 'react'
import { NavLink, Outlet } from 'react-router'
import {
  BuildingIcon,
  ClipboardListIcon,
  FileCogIcon,
  FolderTreeIcon,
  KeyRoundIcon,
  ListTreeIcon,
  ScrollTextIcon,
  SettingsIcon,
  ShapesIcon,
  UsersIcon,
} from 'lucide-react'
import { cn } from 'cn'
import { fetchMyPermissions } from '@/api/permissions'
import { GrapeMark } from '@/ui/icons'

// SettingsModule 描述左侧导航的一个模块项：code 是打开该模块页面的页面级
// 权限码（对齐部署面菜单种子的 sys:* 页面码）；path 缺省即尚未实现的占位
// 模块（禁用态展示）。后续里程碑接入时补上 path 并在 App 注册子路由。
interface SettingsModule {
  key: string
  label: string
  icon: ComponentType<{ className?: string }>
  code: string
  path?: string
}

// SETTINGS_MODULES 是管理面模块全量清单与各自的页面级权限码（来源：部署
// 面菜单种子 seed.sql / 99_perm_seed.sql 的页面菜单 permission_code）。
// 持有对应权限码才展示导航项；未实现模块的禁用占位项同样参与过滤。
const SETTINGS_MODULES: SettingsModule[] = [
  { key: 'users', label: '用户', icon: UsersIcon, code: 'sys:user', path: '/settings/users' },
  { key: 'depts', label: '部门', icon: FolderTreeIcon, code: 'sys:dept', path: '/settings/depts' },
  { key: 'user-groups', label: '用户组', icon: ShapesIcon, code: 'sys:user-group', path: '/settings/user-groups' },
  { key: 'roles', label: '角色', icon: KeyRoundIcon, code: 'sys:role', path: '/settings/roles' },
  { key: 'menus', label: '菜单', icon: ListTreeIcon, code: 'sys:menu', path: '/settings/menus' },
  { key: 'dicts', label: '字典', icon: ScrollTextIcon, code: 'sys:dict' },
  { key: 'audit-logs', label: '审计日志', icon: ClipboardListIcon, code: 'sys:audit' },
  { key: 'sessions', label: '会话', icon: SettingsIcon, code: 'sys:session' },
  { key: 'configs', label: '配置', icon: FileCogIcon, code: 'sys:config' },
  { key: 'tenants', label: '租户', icon: BuildingIcon, code: 'sys:tenant' },
]

// SettingsLayout 是管理页布局：左侧模块导航 + 右侧子路由内容区。挂载时拉
// 取当前用户的功能权限码（GET /v1/permissions/mine）过滤导航项；拉取失败
// 回落静态全量展示（M0 口径，越权由 API 拒绝兜底）。加载完成前导航留空，
// 避免先展示无权模块再移除的闪烁。
export function SettingsLayout() {
  // granted 为 null 且非加载中即回落态（展示全量）；loading 期间导航留空。
  const [granted, setGranted] = useState<ReadonlySet<string> | null>(null)
  const [loading, setLoading] = useState(true)

  // 取数：requestSeq 使迟到旧响应被丢弃，卸载后不再写状态。
  const requestSeq = useRef(0)
  useEffect(() => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    setLoading(true)
    fetchMyPermissions()
      .then((codes) => {
        if (requestSeq.current !== seq) {
          return
        }
        setGranted(new Set(codes))
        setLoading(false)
      })
      .catch(() => {
        // 拉取失败（含 403/网络错误）回落全量展示，越权由 API 拒绝兜底。
        if (requestSeq.current !== seq) {
          return
        }
        setGranted(null)
        setLoading(false)
      })
  }, [])

  const visible = loading
    ? []
    : granted === null
      ? SETTINGS_MODULES
      : SETTINGS_MODULES.filter((module) => granted.has(module.code))

  return (
    <div className="flex h-full overflow-hidden bg-background">
      <aside aria-label="管理导航" className="flex w-56 shrink-0 flex-col border-r border-sidebar-border bg-sidebar">
        <div className="flex items-center gap-2.5 px-5 pt-5 pb-4">
          <GrapeMark className="h-9 w-9" />
          <div className="min-w-0 flex-1">
            <h2 className="text-sm font-semibold text-foreground">系统管理</h2>
            <p className="truncate text-xs text-muted-foreground">租户内资源管理</p>
          </div>
        </div>
        <nav className="min-h-0 flex-1 overflow-y-auto px-3 pb-4" aria-busy={loading}>
          <ul aria-label="管理模块" className="space-y-1">
            {visible.map((module) => (
              <li key={module.key}>
                {module.path !== undefined ? (
                  <NavLink
                    to={module.path}
                    className={({ isActive }) =>
                      cn(
                        'flex items-center gap-2 rounded-xl px-3 py-2 text-sm font-medium transition-colors select-none hover:bg-sidebar-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-sidebar-ring',
                        isActive ? 'bg-brand-soft text-brand-text hover:bg-brand-soft' : 'text-foreground',
                      )
                    }
                  >
                    <module.icon aria-hidden className="size-4 shrink-0" />
                    {module.label}
                  </NavLink>
                ) : (
                  <span
                    aria-disabled="true"
                    title="该模块尚未开放"
                    className="flex cursor-not-allowed items-center gap-2 rounded-xl px-3 py-2 text-sm font-medium text-muted-foreground/60 select-none"
                  >
                    <module.icon aria-hidden className="size-4 shrink-0" />
                    {module.label}
                    <span className="ml-auto text-[10px] leading-none">待接入</span>
                  </span>
                )}
              </li>
            ))}
          </ul>
        </nav>
      </aside>
      <main aria-label="管理内容区" className="flex min-w-0 flex-1 flex-col overflow-y-auto">
        <Outlet />
      </main>
    </div>
  )
}
