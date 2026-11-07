import { useEffect } from 'react'
import { Navigate, Route, Routes, useNavigate } from 'react-router'
import { setSessionExpiredHandler } from './auth/callbacks'
import { PERM } from './auth/permissionCodes'
import { RequirePermission } from './auth/permissions'
import { ProtectedRoute } from './auth/ProtectedRoute'
import { PageTransition } from './components/page-transition'
import { Toaster } from './components/ui/toast'
import { ChatPage } from './pages/ChatPage'
import { LoginPage } from './pages/LoginPage'
import { ReplayPage } from './pages/replay/ReplayPage'
import { AuditLogManagePage } from './pages/settings/AuditLogManagePage'
import { ConfigManagePage } from './pages/settings/ConfigManagePage'
import { DeptManagePage } from './pages/settings/DeptManagePage'
import { DictManagePage } from './pages/settings/DictManagePage'
import { MenuManagePage } from './pages/settings/MenuManagePage'
import { ProfilePage } from './pages/settings/ProfilePage'
import { RoleManagePage } from './pages/settings/RoleManagePage'
import { SessionManagePage } from './pages/settings/SessionManagePage'
import { SettingsIndexRedirect, SettingsLayout } from './pages/settings/SettingsLayout'
import { TenantManagePage } from './pages/settings/TenantManagePage'
import { UserGroupManagePage } from './pages/settings/UserGroupManagePage'
import { UserManagePage } from './pages/settings/UserManagePage'

// App 是路由表与全局装配点：网络层会话失效（401 且刷新失败）时回到
// 登录页，路由本身不感知网络细节；每个路由经 PageTransition 淡入换页。
// Toaster 是全局通知挂载点：管理页的异步动作反馈统一经 toast 呈现。
export default function App() {
  const navigate = useNavigate()

  useEffect(() => {
    setSessionExpiredHandler(() => navigate('/login', { replace: true }))
  }, [navigate])

  return (
    <>
      <Routes>
        <Route
          path="/login"
          element={
            <PageTransition>
              <LoginPage />
            </PageTransition>
          }
        />
        <Route
          path="/"
          element={
            <ProtectedRoute>
              <PageTransition>
                <ChatPage />
              </PageTransition>
            </ProtectedRoute>
          }
        />
        <Route
          path="/replay"
          element={
            <ProtectedRoute>
              <PageTransition>
                <ReplayPage />
              </PageTransition>
            </ProtectedRoute>
          }
        />
        <Route
          path="/settings"
          element={
            <ProtectedRoute>
              <PageTransition>
                <SettingsLayout />
              </PageTransition>
            </ProtectedRoute>
          }
        >
          {/* 管理页子路由：注册全部设置页。索引路由优先跳转到第一个
           * 有权限的管理模块，无管理权限时落到个人中心。 */}
          <Route index element={<SettingsIndexRedirect />} />
          <Route path="profile" element={<ProfilePage />} />
          <Route
            path="users"
            element={
              <RequirePermission requirement={{ required: [PERM.UserPage, PERM.UserQuery] }}>
                <UserManagePage />
              </RequirePermission>
            }
          />
          <Route
            path="depts"
            element={
              <RequirePermission requirement={{ required: [PERM.DeptPage, PERM.DeptQuery] }}>
                <DeptManagePage />
              </RequirePermission>
            }
          />
          <Route
            path="user-groups"
            element={
              <RequirePermission requirement={{ required: [PERM.UserGroupPage, PERM.UserGroupQuery] }}>
                <UserGroupManagePage />
              </RequirePermission>
            }
          />
          <Route
            path="roles"
            element={
              <RequirePermission requirement={{ required: [PERM.RolePage, PERM.RoleQuery] }}>
                <RoleManagePage />
              </RequirePermission>
            }
          />
          <Route
            path="menus"
            element={
              <RequirePermission requirement={{ required: [PERM.MenuPage, PERM.MenuQuery] }}>
                <MenuManagePage />
              </RequirePermission>
            }
          />
          <Route
            path="dicts"
            element={
              <RequirePermission requirement={{ required: [PERM.DictPage, PERM.DictQuery] }}>
                <DictManagePage />
              </RequirePermission>
            }
          />
          <Route
            path="audit-logs"
            element={
              <RequirePermission
                requirement={{ required: [PERM.AuditPage], anyOf: [PERM.AuditOperationLog, PERM.AuditLoginLog] }}
              >
                <AuditLogManagePage />
              </RequirePermission>
            }
          />
          <Route
            path="sessions"
            element={
              <RequirePermission requirement={{ required: [PERM.SessionPage, PERM.SessionQuery] }}>
                <SessionManagePage />
              </RequirePermission>
            }
          />
          <Route
            path="configs"
            element={
              <RequirePermission
                requirement={{
                  required: [PERM.ConfigPage],
                  anyOf: [
                    PERM.ConfigGlobalQuery,
                    PERM.ConfigTenantQuery,
                    PERM.ConfigFeatureFlagQuery,
                    PERM.ConfigTranslationQuery,
                  ],
                }}
              >
                <ConfigManagePage />
              </RequirePermission>
            }
          />
          <Route
            path="tenants"
            element={
              <RequirePermission requirement={{ required: [PERM.TenantPage, PERM.TenantQuery] }}>
                <TenantManagePage />
              </RequirePermission>
            }
          />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      <Toaster />
    </>
  )
}
