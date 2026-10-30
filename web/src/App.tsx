import { useEffect } from 'react'
import { Navigate, Route, Routes, useNavigate } from 'react-router'
import { setSessionExpiredHandler } from './auth/callbacks'
import { ProtectedRoute } from './auth/ProtectedRoute'
import { PageTransition } from './components/page-transition'
import { ChatPage } from './pages/ChatPage'
import { LoginPage } from './pages/LoginPage'
import { ReplayPage } from './pages/replay/ReplayPage'

// App 是路由表与全局装配点：网络层会话失效（401 且刷新失败）时回到
// 登录页，路由本身不感知网络细节；每个路由经 PageTransition 淡入换页。
export default function App() {
  const navigate = useNavigate()

  useEffect(() => {
    setSessionExpiredHandler(() => navigate('/login', { replace: true }))
  }, [navigate])

  return (
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
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
