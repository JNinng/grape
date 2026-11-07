import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { Navigate } from 'react-router'
import { fetchMyPermissions } from '@/api/permissions'
import { Spinner } from '@/components/ui/spinner'

// PermissionState 是设置面的权限视图：granted 为 null 表示权限码拉取失败，
// 统一回落为「全量放行」（越权由 API 拒绝兜底）；loading 期间 can() 恒
// false，避免先展示无权按钮再移除的闪烁。
interface PermissionState {
  loading: boolean
  granted: ReadonlySet<string> | null
  can: (code?: string) => boolean
}

// PermissionRequirement 是模块/路由的权限要求：required 全部持有，
// anyOf 非空时至少持有其一。列表页用「页面码 + Query 码」全部持有，
// 多视图页（审计/配置）用「页面码 + 任一视图码」。
export interface PermissionRequirement {
  required?: string[]
  anyOf?: string[]
}

// isRequirementMet 按 can() 判定权限要求；空要求视为通过。
export function isRequirementMet(
  can: (code?: string) => boolean,
  requirement: PermissionRequirement,
): boolean {
  const required = requirement.required ?? []
  if (!required.every((code) => can(code))) {
    return false
  }
  const anyOf = requirement.anyOf ?? []
  return anyOf.length === 0 || anyOf.some((code) => can(code))
}

const DENY_FALLBACK = '/settings/profile'

// 默认上下文用于未包 Provider 的独立渲染（例如组件测试）：按全量放行处理，
// 与拉取失败的回落口径一致。
const defaultState: PermissionState = {
  loading: false,
  granted: null,
  can: () => true,
}

const PermissionContext = createContext<PermissionState>(defaultState)

// PermissionProvider 只拉取一次当前用户的功能权限码（GET
// /v1/permissions/mine），供导航、路由守卫与页面按钮共用。requestSeq 使
// 重新执行 effect 后迟到的旧响应被丢弃。
export function PermissionProvider({ children }: { children: ReactNode }) {
  const [granted, setGranted] = useState<ReadonlySet<string> | null>(null)
  const [loading, setLoading] = useState(true)
  const requestSeq = useRef(0)

  useEffect(() => {
    const seq = requestSeq.current + 1
    requestSeq.current = seq
    fetchMyPermissions()
      .then((codes) => {
        if (requestSeq.current !== seq) {
          return
        }
        setGranted(new Set(codes))
        setLoading(false)
      })
      .catch(() => {
        if (requestSeq.current !== seq) {
          return
        }
        setGranted(null)
        setLoading(false)
      })
  }, [])

  const value = useMemo<PermissionState>(
    () => ({
      loading,
      granted,
      can: (code) => {
        if (code === undefined) {
          return true
        }
        if (loading) {
          return false
        }
        return granted === null || granted.has(code)
      },
    }),
    [granted, loading],
  )

  return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>
}

// usePermissions 读取当前权限视图；未包 Provider 时返回全量放行的默认态。
export function usePermissions(): PermissionState {
  return useContext(PermissionContext)
}

// RequirePermission 是设置面子路由的权限守卫：加载期占位，无权限时回到
// 个人中心（无码模块）。权限拉取失败的回落态由 can() 统一放行。
export function RequirePermission({
  code,
  requirement,
  children,
  fallbackTo = DENY_FALLBACK,
}: {
  // code 是单码简写，保留给只要求一个权限码的场景；多码要求用 requirement。
  code?: string
  requirement?: PermissionRequirement
  children: ReactNode
  fallbackTo?: string
}) {
  const { loading, can } = usePermissions()
  if (loading) {
    return (
      <div className="grid min-h-32 place-items-center" aria-label="权限校验中">
        <Spinner aria-hidden className="size-6" />
      </div>
    )
  }
  const effective: PermissionRequirement =
    requirement ?? (code === undefined ? {} : { required: [code] })
  if (!isRequirementMet(can, effective)) {
    return <Navigate to={fallbackTo} replace />
  }
  return <>{children}</>
}
