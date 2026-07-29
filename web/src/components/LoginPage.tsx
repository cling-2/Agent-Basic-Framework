import { useState, useEffect } from 'react'
import { login, register, saveToken, ApiError } from '../api'

interface LoginProps {
  onSuccess: () => void
  initialMessage?: string
}

export default function LoginPage({ onSuccess, initialMessage }: LoginProps) {
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(initialMessage || '')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)

    try {
      let res
      if (mode === 'login') {
        res = await login({ username, password })
      } else {
        // 注册时不再选择角色，后端固定分配 visitor
        res = await register({ username, password })
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

          {/* 注册时提示角色为访客 */}
          {mode === 'register' && (
            <div className="form-group">
              <p style={{ color: '#6b7280', fontSize: '0.85rem', margin: 0 }}>
                注册用户默认为 <strong>访客</strong> 角色，可使用计算器和文件搜索工具。
                如需管理员权限，请联系管理员。
              </p>
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
