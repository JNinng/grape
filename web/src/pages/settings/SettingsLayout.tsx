import { createContext, useContext, type ComponentType } from 'react'
import { Link, Navigate, NavLink, Outlet } from 'react-router'
import {
  ArrowLeftIcon,
  BookOpenIcon,
  BuildingIcon,
  ClipboardListIcon,
  CpuIcon,
  FileCogIcon,
  FolderTreeIcon,
  KeyIcon,
  KeyRoundIcon,
  ListTreeIcon,
  ScrollTextIcon,
  ShapesIcon,
  UserRoundIcon,
  UsersIcon,
} from 'lucide-react'
import { cn } from 'cn'
import { PERM } from '@/auth/permissionCodes'
import {
  isRequirementMet,
  PermissionProvider,
  usePermissions,
  type PermissionRequirement,
} from '@/auth/permissions'
import { Spinner } from '@/components/ui/spinner'
import { GrapeMark } from '@/ui/icons'

// SettingsModule 描述左侧导航的一个模块项：requirement 是打开该模块的
// 权限要求（列表/树页为「页面码 + Query 码」，多视图页为「页面码 + 任一
// 视图码」），缺省表示只受认证约束的个人面（如个人中心）；path 缺省即
// 未开放模块（禁用态展示）。新增模块时补上 path、权限要求并在 App 注册
// 带 RequirePermission 的子路由。
interface SettingsModule {
  key: string
  label: string
  icon: ComponentType<{ className?: string }>
  requirement?: PermissionRequirement
  path?: string
}

// SETTINGS_MODULES 是管理面模块全量清单与各自的权限要求。列表/树页必须
// 同时持有页面码与 Query 码，避免进入页面后列表请求 403；审计/配置按
// 页面码 + 任一视图码判定。
const SETTINGS_MODULES: SettingsModule[] = [
  { key: 'profile', label: '个人中心', icon: UserRoundIcon, path: '/settings/profile' },
  {
    key: 'users',
    label: '用户',
    icon: UsersIcon,
    requirement: { required: [PERM.UserPage, PERM.UserQuery] },
    path: '/settings/users',
  },
  {
    key: 'depts',
    label: '部门',
    icon: FolderTreeIcon,
    requirement: { required: [PERM.DeptPage, PERM.DeptQuery] },
    path: '/settings/depts',
  },
  {
    key: 'user-groups',
    label: '用户组',
    icon: ShapesIcon,
    requirement: { required: [PERM.UserGroupPage, PERM.UserGroupQuery] },
    path: '/settings/user-groups',
  },
  {
    key: 'roles',
    label: '角色',
    icon: KeyRoundIcon,
    requirement: { required: [PERM.RolePage, PERM.RoleQuery] },
    path: '/settings/roles',
  },
  {
    key: 'menus',
    label: '菜单',
    icon: ListTreeIcon,
    requirement: { required: [PERM.MenuPage, PERM.MenuQuery] },
    path: '/settings/menus',
  },
  {
    key: 'dicts',
    label: '字典',
    icon: ScrollTextIcon,
    requirement: { required: [PERM.DictPage, PERM.DictQuery] },
    path: '/settings/dicts',
  },
  {
    key: 'audit-logs',
    label: '审计日志',
    icon: ClipboardListIcon,
    requirement: {
      required: [PERM.AuditPage],
      anyOf: [PERM.AuditOperationLog, PERM.AuditLoginLog],
    },
    path: '/settings/audit-logs',
  },
  {
    key: 'sessions',
    label: '会话',
    icon: KeyIcon,
    requirement: { required: [PERM.SessionPage, PERM.SessionQuery] },
    path: '/settings/sessions',
  },
  {
    key: 'configs',
    label: '配置',
    icon: FileCogIcon,
    requirement: {
      required: [PERM.ConfigPage],
      anyOf: [
        PERM.ConfigGlobalQuery,
        PERM.ConfigTenantQuery,
        PERM.ConfigFeatureFlagQuery,
        PERM.ConfigTranslationQuery,
      ],
    },
    path: '/settings/configs',
  },
  {
    key: 'tenants',
    label: '租户',
    icon: BuildingIcon,
    requirement: { required: [PERM.TenantPage, PERM.TenantQuery] },
    path: '/settings/tenants',
  },
  {
    key: 'knowledge',
    label: '知识库',
    icon: BookOpenIcon,
    requirement: { required: [PERM.KnowledgePage, PERM.KnowledgeQuery] },
    path: '/settings/knowledge',
  },
  {
    key: 'models',
    label: '模型',
    icon: CpuIcon,
    requirement: { required: [PERM.ModelPage, PERM.ModelQuery] },
    path: '/settings/models',
  },
]

// modulePermitted 判定模块是否满足权限要求；无 requirement 的模块（个人
// 中心）只受认证约束，恒通过。
function modulePermitted(module: SettingsModule, can: (code?: string) => boolean): boolean {
  return module.requirement === undefined || isRequirementMet(can, module.requirement)
}

// SettingsAccess 是布局向索引路由共享的权限视图：loading 期间不下结论
//（避免先跳到无权页再纠正）；visible 已按权限码过滤且保持模块声明顺序。
interface SettingsAccess {
  loading: boolean
  visible: SettingsModule[]
}

const SettingsAccessContext = createContext<SettingsAccess>({ loading: true, visible: [] })

// SettingsIndexRedirect 是 /settings 的索引路由：权限码加载完成后优先跳到
// 导航清单中第一个有权限的管理模块，没有管理权限时回落到无码模块（个人
// 中心）；加载失败沿用全量回落。与左侧导航共用同一份过滤结果。
export function SettingsIndexRedirect() {
  const { loading, visible } = useContext(SettingsAccessContext)
  if (loading) {
    return (
      <div className="flex min-h-32 items-center justify-center" aria-label="管理模块加载中">
        <Spinner aria-hidden className="size-6" />
      </div>
    )
  }
  const target =
    visible.find((module) => module.path !== undefined && module.requirement !== undefined) ??
    visible.find((module) => module.path !== undefined)
  if (target?.path === undefined) {
    return (
      <p className="grid min-h-32 place-items-center text-sm text-muted-foreground">
        暂无可访问的管理模块
      </p>
    )
  }
  return <Navigate to={target.path} replace />
}

// SettingsLayout 是管理页布局：左侧模块导航 + 右侧子路由内容区。权限码由
// PermissionProvider 统一拉取一次（GET /v1/permissions/mine），导航、路由
// 守卫与页面按钮共用；拉取失败回落静态全量展示（越权由 API 拒绝兜底）。
// 加载完成前导航留空，避免先展示无权模块再移除的闪烁。
export function SettingsLayout() {
  return (
    <PermissionProvider>
      <SettingsLayoutContent />
    </PermissionProvider>
  )
}

function SettingsLayoutContent() {
  const { loading, can } = usePermissions()
  const visible = loading ? [] : SETTINGS_MODULES.filter((module) => modulePermitted(module, can))

  return (
    <SettingsAccessContext.Provider value={{ loading, visible }}>
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
          {/* 工作台是应用主界面（对话页）：设置面提供显式返回入口，
           * 不依赖浏览器后退。 */}
          <div className="shrink-0 border-t border-sidebar-border p-3">
            <Link
              to="/"
              className="flex items-center gap-2 rounded-xl px-3 py-2 text-sm font-medium text-foreground transition-colors hover:bg-sidebar-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-sidebar-ring"
            >
              <ArrowLeftIcon aria-hidden className="size-4 shrink-0" />
              返回工作台
            </Link>
          </div>
        </aside>
        <main aria-label="管理内容区" className="flex min-w-0 flex-1 flex-col overflow-y-auto">
          <Outlet />
        </main>
      </div>
    </SettingsAccessContext.Provider>
  )
}
