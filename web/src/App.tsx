import { useEffect } from 'react'
import { Navigate, Route, Routes, useNavigate } from 'react-router'
import { setSessionExpiredHandler } from './auth/callbacks'
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
          {/* 管理页子路由：按已交付页面注册。索引路由按权限码跳转到第一个
           * 有权限的模块，避免先落到无权页面。 */}
          <Route index element={<SettingsIndexRedirect />} />
          <Route path="users" element={<UserManagePage />} />
          <Route path="depts" element={<DeptManagePage />} />
          <Route path="user-groups" element={<UserGroupManagePage />} />
          <Route path="roles" element={<RoleManagePage />} />
          <Route path="menus" element={<MenuManagePage />} />
          <Route path="dicts" element={<DictManagePage />} />
          <Route path="audit-logs" element={<AuditLogManagePage />} />
          <Route path="sessions" element={<SessionManagePage />} />
          <Route path="configs" element={<ConfigManagePage />} />
          <Route path="tenants" element={<TenantManagePage />} />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      <Toaster />
    </>
  )
}
