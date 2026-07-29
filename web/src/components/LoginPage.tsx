import { useState, useEffect } from 'react'
import { login, register, getRoles, saveToken, ApiError } from '../api'

interface LoginProps {
  onSuccess: () => void
  initialMessage?: string
}

interface RoleOption {
  id: number
  name: string
  description: string
}

export default function LoginPage({ onSuccess, initialMessage }: LoginProps) {
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState('visitor')
  const [roles, setRoles] = useState<RoleOption[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(initialMessage || '')

  // 加载可选角色列表
  useEffect(() => {
    getRoles().then(res => setRoles(res.roles)).catch(() => {
      // fallback
      setRoles([
        { id: 1, name: 'admin', description: '管理员，可调用所有工具' },
        { id: 2, name: 'visitor', description: '访客，仅可调用查询类工具' },
      ])
    })
  }, [])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)

    try {
      let res
      if (mode === 'login') {
        res = await login({ username, password })
      } else {
        res = await register({ username, password, role })
      }
      saveToken(res.session_id)
      onSuccess()
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401) {
          setError('用户名或密码错误')
        } else if (err.status === 409) {
          setError('用户名已存在')
        } else {
          setError(err.message)
        }
      } else {
        setError('网络错误，请稍后重试')
      }
    } finally {
      setLoading(false)
    }
  }

  const toggleMode = () => {
    setMode(mode === 'login' ? 'register' : 'login')
    setError('')
  }

  return (
    <div className="login-page">
      <div className="login-card">
        <div className="login-header">
          <h1>🤖 Kingsoft Agent</h1>
          <p>{mode === 'login' ? '身份与权限子系统' : '注册新用户'}</p>
        </div>

        <form onSubmit={handleSubmit} className="login-form">
          <div className="form-group">
            <label htmlFor="username">用户名</label>
            <input
              id="username"
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="请输入用户名"
              autoComplete="username"
              required
              autoFocus
            />
          </div>

          <div className="form-group">
            <label htmlFor="password">密码</label>
            <input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="请输入密码"
              autoComplete={mode === 'register' ? 'new-password' : 'current-password'}
              required
              minLength={4}
            />
          </div>

          {/* 注册时选择角色 */}
          {mode === 'register' && (
            <div className="form-group">
              <label>用户类型</label>
              <div className="role-select-group">
                {roles.map(r => (
                  <label key={r.id} className={`role-select-item ${role === r.name ? 'role-select-active' : ''}`}>
                    <input
                      type="radio"
                      name="role"
                      value={r.name}
                      checked={role === r.name}
                      onChange={() => setRole(r.name)}
                    />
                    <div className="role-select-content">
                      <span className="role-select-name">{r.name === 'admin' ? '🔑 管理员' : '👤 访客'}</span>
                      <span className="role-select-desc">{r.description}</span>
                    </div>
                  </label>
                ))}
              </div>
            </div>
          )}

          {error && <div className="form-error">{error}</div>}

          <button type="submit" className="btn-primary" disabled={loading}>
            {loading
              ? (mode === 'login' ? '登录中...' : '注册中...')
              : (mode === 'login' ? '登 录' : '注 册')
            }
          </button>
        </form>

        <div className="login-switch">
          {mode === 'login' ? (
            <span>没有账号？<button onClick={toggleMode}>立即注册</button></span>
          ) : (
            <span>已有账号？<button onClick={toggleMode}>返回登录</button></span>
          )}
        </div>

        {mode === 'login' && (
          <div className="login-hint">
            <p>预设账号：</p>
            <code>admin / admin123</code>
            <span>（管理员，全部工具权限）</span>
            <br />
            <code>visitor / visitor123</code>
            <span>（访客，仅查询类工具）</span>
          </div>
        )}
      </div>
    </div>
  )
}
