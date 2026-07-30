import { useState, useEffect, useCallback } from 'react'
import { loadToken, clearToken, getSettingsStatus, getSession, logout, setOnAuthExpired, type SessionInfo } from './api'
import LoginPage from './components/LoginPage'
import ChatPage from './components/ChatPage'
import Sidebar, { type SessionItem } from './components/Sidebar'
import SettingsModal from './components/SettingsModal'

// localStorage key 按 userId 区分，防止不同用户会话串
function sessionsKey(userId: number | null): string {
  return `kingsoft_agent_sessions_${userId ?? 'anon'}`
}

function loadSessions(userId: number | null): SessionItem[] {
  try {
    const raw = localStorage.getItem(sessionsKey(userId))
    return raw ? JSON.parse(raw) : []
  } catch {
    return []
  }
}

function saveSessions(userId: number | null, sessions: SessionItem[]) {
  localStorage.setItem(sessionsKey(userId), JSON.stringify(sessions))
}

export default function App() {
  const [loggedIn, setLoggedIn] = useState(false)
  const [showSettings, setShowSettings] = useState(false)
  const [session, setSession] = useState<SessionInfo | null>(null)

  // 登录过期提示
  const [expiredMsg, setExpiredMsg] = useState('')

  // 侧栏 & 会话管理
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [sessions, setSessions] = useState<SessionItem[]>([])
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null)

  // 注册认证过期回调
  useEffect(() => {
    setOnAuthExpired((msg: string) => {
      setExpiredMsg(msg)
      setLoggedIn(false)
      setSession(null)
      setSessions([])
      setActiveSessionId(null)
    })
  }, [])

  // 登录后：加载 session 信息、会话列表、检查 LLM 配置
  useEffect(() => {
    if (loggedIn) {
      getSession().then(info => {
        setSession(info)
        // 按 userId 加载/保存会话列表
        const loaded = loadSessions(info.user_id)
        setSessions(loaded)
        if (loaded.length > 0 && !activeSessionId) {
          setActiveSessionId(loaded[0]!.id)
        }
        if (loaded.length === 0) {
          handleNewSession()
        }
      }).catch(() => {})

      getSettingsStatus()
        .then(res => {
          if (!res.configured) setShowSettings(true)
        })
        .catch(() => {})
    }
  }, [loggedIn]) // eslint-disable-line react-hooks/exhaustive-deps

  // 页面加载时检查已有 token
  useEffect(() => {
    if (loadToken()) {
      setLoggedIn(true)
    }
  }, [])

  // 会话列表变化时持久化（按 userId）
  useEffect(() => {
    if (session) {
      saveSessions(session.user_id, sessions)
    }
  }, [sessions, session])

  const isAdmin = session?.role === 'admin'
  const roleLabel = session?.role === 'admin' ? '管理员' : '访客'

  // 新建会话
  const handleNewSession = useCallback(() => {
    const id = `thread_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
    const newSession: SessionItem = {
      id,
      title: '新会话',
      createdAt: Date.now(),
    }
    setSessions(prev => [newSession, ...prev])
    setActiveSessionId(id)
  }, [])

  // 删除会话
  const handleDeleteSession = useCallback((id: string) => {
    setSessions(prev => prev.filter(s => s.id !== id))
    if (activeSessionId === id) {
      setActiveSessionId(null)
    }
  }, [activeSessionId])

  // 重命名会话
  const handleRenameSession = useCallback((id: string, title: string) => {
    setSessions(prev => prev.map(s => s.id === id ? { ...s, title } : s))
  }, [])

  // 选择会话
  const handleSelectSession = useCallback((id: string) => {
    setActiveSessionId(id)
  }, [])

  // 退出登录
  const handleLogout = useCallback(async () => {
    try { await logout() } catch {}
    clearToken()
    setLoggedIn(false)
    setSession(null)
    setSessions([])
    setActiveSessionId(null)
    setExpiredMsg('')
  }, [])

  // 登录成功回调
  const handleLoginSuccess = useCallback(() => {
    setLoggedIn(true)
    setExpiredMsg('')
  }, [])

  if (!loggedIn) {
    return <LoginPage onSuccess={handleLoginSuccess} initialMessage={expiredMsg} />
  }

  // 确保有活跃会话
  if (!activeSessionId && sessions.length > 0) {
    setActiveSessionId(sessions[0]!.id)
  }

  return (
    <div className="app-layout">
      {/* 侧栏 */}
      <Sidebar
        sessions={sessions}
        activeId={activeSessionId}
        onSelect={handleSelectSession}
        onNew={handleNewSession}
        onDelete={handleDeleteSession}
        onRename={handleRenameSession}
        collapsed={sidebarCollapsed}
        onToggle={() => setSidebarCollapsed(!sidebarCollapsed)}
      />

      {/* 主内容区 */}
      <div className="app-main-wrapper">
        {/* 顶栏 */}
        <nav className="app-nav">
          <div className="nav-left">
            <button
              className={`nav-hamburger ${!sidebarCollapsed ? 'nav-hamburger-hidden' : ''}`}
              onClick={() => setSidebarCollapsed(!sidebarCollapsed)}
              title="切换侧栏"
            >
              ☰
            </button>
            <span className="nav-brand">Kingsoft Agent</span>
          </div>
          <div className="nav-right">
            <span className={`role-tag ${session?.role === 'admin' ? 'role-admin' : 'role-visitor'}`}>
              {roleLabel}
            </span>
            {isAdmin && (
              <button
                className="nav-settings-btn"
                onClick={() => setShowSettings(true)}
                title="LLM配置"
              >
                ⚙️ 设置
              </button>
            )}
            <button className="btn-logout" onClick={handleLogout}>
              退出登录
            </button>
          </div>
        </nav>

        {/* Chat 区域 */}
        <main className="app-main">
          <ChatPage
            key={activeSessionId || 'empty'}
            threadId={activeSessionId}
            sessionTitle={sessions.find(s => s.id === activeSessionId)?.title}
            onSessionTitleUpdate={(id, title) => handleRenameSession(id, title)}
            onSessionDelete={(id) => handleDeleteSession(id)}
          />
        </main>
      </div>

      {/* LLM 设置弹窗 */}
      {showSettings && isAdmin && (
        <SettingsModal onClose={() => setShowSettings(false)} />
      )}
    </div>
  )
}
