import { useEffect } from 'react'
import { Navigate, Route, Routes, useNavigate } from 'react-router'
import { setSessionExpiredHandler } from './auth/callbacks'
import { ProtectedRoute } from './auth/ProtectedRoute'
import { PageTransition } from './components/page-transition'
import { Toaster } from './components/ui/toast'
import { ChatPage } from './pages/ChatPage'
import { LoginPage } from './pages/LoginPage'
import { ReplayPage } from './pages/replay/ReplayPage'
import { SettingsLayout } from './pages/settings/SettingsLayout'
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
          {/* 管理页子路由：未实现模块不注册，路由表保持与已交付页面一致。 */}
          <Route index element={<Navigate to="/settings/users" replace />} />
          <Route path="users" element={<UserManagePage />} />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      <Toaster />
    </>
  )
}
