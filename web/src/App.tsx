import { useState, useEffect } from 'react'
import { loadToken, getSettingsStatus, getSession, type SessionInfo } from './api'
import LoginPage from './components/LoginPage'
import Dashboard from './components/Dashboard'
import ChatPage from './components/ChatPage'
import SettingsModal from './components/SettingsModal'

export type PageTab = 'dashboard' | 'chat'

export default function App() {
  const [loggedIn, setLoggedIn] = useState(false)
  const [activeTab, setActiveTab] = useState<PageTab>('chat')
  const [showSettings, setShowSettings] = useState(false)
  const [session, setSession] = useState<SessionInfo | null>(null)

  // 登录后检查是否需要弹出设置
  useEffect(() => {
    if (loggedIn) {
      // 获取会话信息（用于判断角色）
      getSession().then(setSession).catch(() => {})
      // 检查LLM是否已配置
      getSettingsStatus()
        .then((res) => {
          if (!res.configured) {
            setShowSettings(true)
          }
        })
        .catch(() => {})
    }
  }, [loggedIn])

  // 页面加载时检查已有token
  useEffect(() => {
    if (loadToken()) {
      setLoggedIn(true)
    }
  }, [])

  const isAdmin = session?.role === 'admin'

  if (!loggedIn) {
    return <LoginPage onSuccess={() => setLoggedIn(true)} />
  }

  return (
    <div className="app-layout">
      <nav className="app-nav">
        <div className="nav-left">
          <span className="nav-brand">🤖 Kingsoft Agent</span>
          <button
            className={`nav-tab ${activeTab === 'chat' ? 'active' : ''}`}
            onClick={() => setActiveTab('chat')}
          >
            💬 Agent 对话
          </button>
          <button
            className={`nav-tab ${activeTab === 'dashboard' ? 'active' : ''}`}
            onClick={() => setActiveTab('dashboard')}
          >
            📋 权限面板
          </button>
        </div>
        <div className="nav-right">
          {isAdmin && (
            <button
              className="nav-settings-btn"
              onClick={() => setShowSettings(true)}
              title="LLM配置"
            >
              ⚙️ 设置
            </button>
          )}
        </div>
      </nav>
      <main className="app-main">
        {activeTab === 'chat' && (
          <ChatPage onLogout={() => setLoggedIn(false)} />
        )}
        {activeTab === 'dashboard' && (
          <Dashboard onLogout={() => setLoggedIn(false)} />
        )}
      </main>

      {/* LLM 设置弹窗 */}
      {showSettings && isAdmin && (
        <SettingsModal onClose={() => setShowSettings(false)} />
      )}
    </div>
  )
}
