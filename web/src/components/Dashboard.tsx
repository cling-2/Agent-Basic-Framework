import { useEffect, useState } from 'react'
import {
  getSession,
  getTools,
  listTools,
  listAgents,
  logout,
  clearToken,
  type SessionInfo,
  type ToolsResponse,
  type ToolItem,
  type AgentInfo,
  ApiError,
} from '../api'

interface DashboardProps {
  onLogout: () => void
}

// 完整工具定义（含分类，与后端注册一致）
const ALL_TOOLS = [
  { name: 'calculator', label: '🔢 计算器', category: '查询类' },
  { name: 'grep_files', label: '📄 文件搜索', category: '查询类' },
  // 管理员专属
  { name: 'hash_compute', label: '🔐 哈希计算', category: '管理员类' },
  { name: 'send_email', label: '📧 发送邮件', category: '管理员类（需审批）' },
]

export default function Dashboard({ onLogout }: DashboardProps) {
  const [session, setSession] = useState<SessionInfo | null>(null)
  const [tools, setTools] = useState<ToolsResponse | null>(null)
  const [registeredTools, setRegisteredTools] = useState<ToolItem[]>([])
  const [agents, setAgents] = useState<AgentInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const fetchDashboard = async () => {
    setLoading(true)
    setError('')
    try {
      const [sessionInfo, toolsInfo] = await Promise.all([
        getSession(),
        getTools(),
      ])
      setSession(sessionInfo)
      setTools(toolsInfo)

      // 尝试获取管理端数据（admin 可见）
      try {
        const [toolsRes, agentsRes] = await Promise.all([
          listTools(),
          listAgents(),
        ])
        setRegisteredTools(toolsRes.tools)
        setAgents(agentsRes.agents)
      } catch {
        // visitor 无权限，忽略
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        clearToken()
        onLogout()
        return
      }
      setError(err instanceof Error ? err.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchDashboard()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const handleLogout = async () => {
    try { await logout() } catch { /* ignore */ }
    clearToken()
    onLogout()
  }

  const isToolAllowed = (toolName: string): boolean => {
    if (!tools) return false
    if (tools.tools.includes('*')) return true
    return tools.tools.includes(toolName)
  }

  const isAdmin = session?.role === 'admin'

  if (loading) {
    return <div className="dashboard-page"><div className="loading">加载中...</div></div>
  }

  if (error) {
    return (
      <div className="dashboard-page">
        <div className="error-box">
          <p>❌ {error}</p>
          <button className="btn-secondary" onClick={fetchDashboard}>重试</button>
        </div>
      </div>
    )
  }

  return (
    <div className="dashboard-page">
      {/* 顶部 */}
      <header className="dashboard-header">
        <div className="header-left">
          <h1>📋 权限面板</h1>
        </div>
        <div className="header-right">
          <span className="user-badge">
            <span className={`role-tag role-${session?.role}`}>
              {session?.role}
            </span>
            <span className="user-name">用户 #{session?.user_id}</span>
          </span>
          <button className="btn-logout" onClick={handleLogout}>登出</button>
        </div>
      </header>

      <main className="dashboard-content">
        {/* 会话信息 */}
        <section className="card session-card">
          <h2>📋 会话信息</h2>
          <div className="info-grid">
            <div className="info-item">
              <span className="info-label">用户 ID</span>
              <span className="info-value">{session?.user_id}</span>
            </div>
            <div className="info-item">
              <span className="info-label">角色</span>
              <span className="info-value">
                <span className={`role-tag role-${session?.role}`}>
                  {isAdmin ? '管理员' : '访客'}
                </span>
              </span>
            </div>
            <div className="info-item">
              <span className="info-label">会话过期</span>
              <span className="info-value">
                {session?.expires_at ? new Date(session.expires_at).toLocaleString('zh-CN') : '-'}
              </span>
            </div>
          </div>
        </section>

        {/* 工具权限 */}
        <section className="card permission-card">
          <h2>🔐 工具权限 (ACLToolMiddleware)</h2>
          <p className="card-desc">
            {isAdmin
              ? '当前用户为管理员，拥有所有工具的调用权限。'
              : '当前用户为访客，仅拥有查询类工具的调用权限，管理员类工具被 ACL 拦截并回灌拒绝信息。'}
          </p>
          <div className="tools-grid">
            {ALL_TOOLS.map((tool) => {
              const allowed = isToolAllowed(tool.name)
              return (
                <div key={tool.name} className={`tool-item ${allowed ? 'tool-allowed' : 'tool-denied'}`}>
                  <div className="tool-header">
                    <span className="tool-name">{tool.label}</span>
                    <span className={`tool-badge ${allowed ? 'badge-yes' : 'badge-no'}`}>
                      {allowed ? '✅ 允许' : '🚫 拒绝'}
                    </span>
                  </div>
                  <div className="tool-category">{tool.category}</div>
                  {!allowed && (
                    <div className="tool-deny-reason">
                      ACL 拦截：当前角色无权调用此工具，ACLToolMiddleware 将回灌拒绝信息
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </section>

        {/* Agent 列表（admin 可见） */}
        {agents.length > 0 && (
          <section className="card agent-card">
            <h2>🤖 Specialist Agents</h2>
            <p className="card-desc">Supervisor 模式下的专家 Agent 列表，Host 根据 LLM 推理选择 Specialist</p>
            <div className="agent-grid">
              {agents.map((a) => (
                <div key={a.name} className="agent-item">
                  <div className="agent-name">{a.name}</div>
                  <div className="agent-use">{a.intended_use}</div>
                </div>
              ))}
            </div>
          </section>
        )}

        {/* 已注册工具（admin 可见） */}
        {registeredTools.length > 0 && (
          <section className="card registry-card">
            <h2>🧰 已注册工具 (ToolRegistry)</h2>
            <p className="card-desc">系统中所有已注册的 Eino InvokableTool，通过 InferTool 创建</p>
            <div className="tools-grid">
              {registeredTools.map((t) => (
                <div key={t.name} className="tool-item tool-allowed">
                  <div className="tool-header">
                    <span className="tool-name">{t.name}</span>
                    <span className="tool-badge badge-yes">已注册</span>
                  </div>
                  <div className="tool-category">{t.desc}</div>
                </div>
              ))}
            </div>
          </section>
        )}

        {/* 权限对比表 */}
        <section className="card diff-card">
          <h2>📊 权限差异对比</h2>
          <table className="diff-table">
            <thead>
              <tr><th>场景</th><th>admin</th><th>visitor</th></tr>
            </thead>
            <tbody>
              <tr>
                <td>calculator / grep_files</td>
                <td className="cell-yes">✅ 放行</td>
                <td className="cell-yes">✅ 放行</td>
              </tr>
              <tr>
                <td>hash_compute</td>
                <td className="cell-yes">✅ 放行</td>
                <td className="cell-no">🚫 回灌拒绝</td>
              </tr>
              <tr>
                <td>send_email</td>
                <td className="cell-yes">✅ 放行（需HITL审批）</td>
                <td className="cell-no">🚫 回灌拒绝</td>
              </tr>
              <tr>
                <td>数据隔离</td>
                <td className="cell-neutral" colSpan={2}>按 userId/thread_id 隔离，不同用户不可串读</td>
              </tr>
              <tr>
                <td>上下文传递</td>
                <td className="cell-neutral" colSpan={2}>身份上下文通过 context.Context 透传，对 LLM 不可见</td>
              </tr>
              <tr>
                <td>ACL 拦截方式</td>
                <td className="cell-neutral" colSpan={2}>Eino ToolMiddleware 拦截，回灌拒绝信息（不中断流程）</td>
              </tr>
            </tbody>
          </table>
        </section>
      </main>
    </div>
  )
}
