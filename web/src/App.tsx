import { ChatPage } from './pages/ChatPage'

// App 是前端路由与全局装配点：A1 先以占位对话页打通构建与嵌入闭环，
// 登录路由与受保护路由随后接入（见 auth/ 与 pages/LoginPage）。
export default function App() {
  return <ChatPage />
}
