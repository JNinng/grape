import type { ComponentType } from 'react'
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
import { GrapeMark } from '@/ui/icons'

// SettingsModule 描述左侧导航的一个模块项：path 缺省即尚未实现的占位
// 模块（禁用态展示）。后续里程碑接入时补上 path 并在 App 注册子路由。
interface SettingsModule {
  key: string
  label: string
  icon: ComponentType<{ className?: string }>
  path?: string
}

// SETTINGS_MODULES 静态全量展示管理面模块（对齐 TODO 现状速览）。M0
// 仅用户模块可路由；权限码过滤留待 M3-6 接入后统一处理，越权由 API
// 拒绝兜底。
const SETTINGS_MODULES: SettingsModule[] = [
  { key: 'users', label: '用户', icon: UsersIcon, path: '/settings/users' },
  { key: 'depts', label: '部门', icon: FolderTreeIcon, path: '/settings/depts' },
  { key: 'user-groups', label: '用户组', icon: ShapesIcon },
  { key: 'roles', label: '角色', icon: KeyRoundIcon },
  { key: 'menus', label: '菜单', icon: ListTreeIcon },
  { key: 'dicts', label: '字典', icon: ScrollTextIcon },
  { key: 'audit-logs', label: '审计日志', icon: ClipboardListIcon },
  { key: 'sessions', label: '会话', icon: SettingsIcon },
  { key: 'configs', label: '配置', icon: FileCogIcon },
  { key: 'tenants', label: '租户', icon: BuildingIcon },
]

// SettingsLayout 是管理页布局：左侧模块导航 + 右侧子路由内容区。导航
// 静态全量展示，未实现模块以禁用项占位，接入时只需补 path 与路由。
export function SettingsLayout() {
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
        <nav className="min-h-0 flex-1 overflow-y-auto px-3 pb-4">
          <ul aria-label="管理模块" className="space-y-1">
            {SETTINGS_MODULES.map((module) => (
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
