import { useNavigate } from 'react-router'
import { logout } from '../api/session'
import { getUser } from '../auth/store'

// ChatPage 占位对话页：A2 将以 @ag-ui/client 实现流式对话（接入点见
// agui/agent.ts），本页只验证登录闭环并承载登出。
export function ChatPage() {
  const navigate = useNavigate()
  const user = getUser()

  const handleLogout = async () => {
    // 后端撤销失败也要清本地态（见 api/session.ts logout），随后回登录页。
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <main>
      <header>
        <h1>Grape AI 对话</h1>
        <button type="button" onClick={handleLogout}>
          登出
        </button>
      </header>
      <p>欢迎，{user?.nickname || user?.name || '用户'}</p>
      <p>对话功能建设中：流式对话将在后续版本提供。</p>
    </main>
  )
}
