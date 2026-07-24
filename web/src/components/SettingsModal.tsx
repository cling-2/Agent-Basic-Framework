import { useState, useEffect } from 'react'
import {
  getSettings,
  updateSettings,
  testConnection,
  type SettingsResponse,
  type TestConnectionResponse,
  ApiError,
} from '../api'

interface SettingsModalProps {
  onClose: () => void
}

export default function SettingsModal({ onClose }: SettingsModalProps) {
  const [apiKey, setApiKey] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [model, setModel] = useState('')
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState('')
  const [testResult, setTestResult] = useState<TestConnectionResponse | null>(null)

  // 加载当前配置
  useEffect(() => {
    getSettings()
      .then((res: SettingsResponse) => {
        setApiKey(res.api_key)
        setBaseUrl(res.base_url)
        setModel(res.model)
      })
      .catch(() => {
        // visitor可能无法读取，忽略
      })
  }, [])

  const handleTest = async () => {
    setTesting(true)
    setTestResult(null)
    setError('')
    try {
      const result = await testConnection({
        api_key: apiKey,
        base_url: baseUrl,
        model: model,
      })
      setTestResult(result)
    } catch (err) {
      if (err instanceof ApiError) {
        setTestResult({ success: false, message: err.message })
      } else {
        setTestResult({ success: false, message: '测试失败，请重试' })
      }
    } finally {
      setTesting(false)
    }
  }

  const handleSave = async () => {
    setSaving(true)
    setError('')
    try {
      await updateSettings({
        api_key: apiKey,
        base_url: baseUrl,
        model: model,
      })
      onClose()
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message)
      } else {
        setError('保存失败，请重试')
      }
    } finally {
      setSaving(false)
    }
  }

  const handleSkip = () => {
    onClose()
  }

  return (
    <div className="modal-overlay" onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal-content">
        <div className="modal-header">
          <h2>⚙️ LLM 配置</h2>
          <p className="modal-desc">配置大模型API以启用真实LLM对话，留空API Key则使用内置Mock模式</p>
        </div>

        <div className="modal-body">
          <div className="form-group">
            <label className="form-label">API Key</label>
            <input
              type="password"
              className="form-input"
              placeholder="输入你的 API Key"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              disabled={saving}
            />
            <span className="form-hint">OpenAI兼容API的密钥</span>
          </div>

          <div className="form-group">
            <label className="form-label">Base URL</label>
            <input
              type="text"
              className="form-input"
              placeholder="如 https://api.example.com/v1"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              disabled={saving}
            />
            <span className="form-hint">OpenAI兼容API地址（末尾需包含 /v1）</span>
          </div>

          <div className="form-group">
            <label className="form-label">模型名称</label>
            <input
              type="text"
              className="form-input"
              placeholder="如 qwen-plus、gpt-4o"
              value={model}
              onChange={(e) => setModel(e.target.value)}
              disabled={saving}
            />
            <span className="form-hint">要使用的模型标识</span>
          </div>

          {/* 测试连接结果 */}
          {testResult && (
            <div className={`test-result ${testResult.success ? 'test-success' : 'test-fail'}`}>
              {testResult.success ? '✅' : '❌'} {testResult.message}
            </div>
          )}

          {error && <div className="form-error">❌ {error}</div>}
        </div>

        <div className="modal-footer">
          <button className="btn-skip" onClick={handleSkip} disabled={saving}>
            跳过（使用Mock模式）
          </button>
          <button
            className="btn-test"
            onClick={handleTest}
            disabled={testing || saving}
          >
            {testing ? '测试中...' : '🔗 测试连接'}
          </button>
          <button className="btn-save" onClick={handleSave} disabled={saving}>
            {saving ? '保存中...' : '💾 保存配置'}
          </button>
        </div>
      </div>
    </div>
  )
}
