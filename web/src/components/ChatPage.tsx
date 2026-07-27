import { useState, useRef, useEffect } from 'react'
import {
  logout,
  clearToken,
  getSession,
  decideCheckpointStream,
  chatStream,
  type SessionInfo,
  type InterruptInfo,
  type StreamEvent,
} from '../api'
import MarkdownRenderer from './MarkdownRenderer'

interface ChatPageProps {
  onLogout: () => void
}

// 推理/工具步骤（DeepSeek 样式：灰色小字、单独段落、持久保留）
interface Step {
  type: 'thinking' | 'tool_call' | 'tool_result' | 'routing'
  content: string
  tool?: { name: string; args: string; id?: string }
}

interface Message {
  id: string
  role: 'user' | 'assistant'
  content: string
  isError?: boolean
  interrupt?: InterruptInfo
  // 推理步骤链（累积追加，答案到达后不清除）
  steps: Step[]
  // 流式是否已结束（done 事件到达后不再显示加载动画）
  streamDone?: boolean
  // 标记：此消息正在被流式恢复更新（用于 handleDecision 定位）
  resuming?: boolean
}

// 快捷提问建议
const QUICK_PROMPTS = [
  { label: '🔢 计算 2+3*4', message: '计算2+3*4' },
  { label: '📄 搜索文件 TODO', message: 'grep TODO' },
  { label: '🔐 计算SHA256', message: '计算 hello 的SHA256哈希' },
  { label: '📧 发送邮件', message: '发送一封邮件' },
]

// 处理 SSE 流事件的通用逻辑
// 返回一个处理函数，可复用于 handleSend 和 handleDecision
function createStreamEventHandler(
  assistantId: string,
  setMessages: React.Dispatch<React.SetStateAction<Message[]>>,
  setLoading: React.Dispatch<React.SetStateAction<boolean>>,
  // 可选：用于定位要更新的消息（默认按 assistantId）
  findTarget?: (m: Message) => boolean,
) {
  const isTarget = findTarget ?? ((m: Message) => m.id === assistantId)

  return (event: StreamEvent) => {
    switch (event.type) {
      case 'thinking':
        // Agent 思考/推理 → 仅当最后一步不是 thinking/routing 时追加（去重）
        setMessages(prev => prev.map(m => {
          if (!isTarget(m)) return m
          const steps = m.steps
          const last = steps.length > 0 ? steps[steps.length - 1] : null
          if (last && (last.type === 'thinking' || last.type === 'routing')) return m
          return { ...m, steps: [...steps, { type: 'thinking', content: ' 思考中...' }] }
        }))
        break

      case 'routing':
        // Supervisor 路由到 Specialist → 仅当未路由到同一 Specialist 时追加（去重）
        if (event.tool) {
          setMessages(prev => prev.map(m => {
            if (!isTarget(m)) return m
            const steps = m.steps
            const alreadyRouted = steps.some(s =>
              s.type === 'routing' && s.tool?.name === event.tool?.name
            )
            if (alreadyRouted) return m
            return {
              ...m,
              steps: [...steps, {
                type: 'routing' as const,
                content: ` 路由到 ${event.tool!.name}`,
                tool: { name: event.tool!.name, args: event.tool!.args, id: event.tool!.id },
              }],
            }
          }))
        }
        break

      case 'tool_call':
        // 工具调用开始 → 按工具名+id去重
        if (event.tool) {
          const toolKey = `${event.tool.name}:${event.tool.id || ''}`
          setMessages(prev => prev.map(m => {
            if (!isTarget(m)) return m
            const steps = m.steps
            const alreadyHas = steps.some(s =>
              s.type === 'tool_call' && s.tool &&
              `${s.tool.name}:${s.tool.id || ''}` === toolKey
            )
            if (alreadyHas) return m
            return {
              ...m,
              steps: [...steps, {
                type: 'tool_call' as const,
                content: ` 调用 ${event.tool!.name}`,
                tool: { name: event.tool!.name, args: event.tool!.args, id: event.tool!.id },
              }],
            }
          }))
        }
        break

      case 'tool_result':
        // 工具返回结果 → 追加步骤（去重：检查最后一步是否已是 tool_result）
        setMessages(prev => prev.map(m => {
          if (!isTarget(m)) return m
          const steps = m.steps
          const last = steps.length > 0 ? steps[steps.length - 1] : null
          if (last && last.type === 'tool_result') return m
          return { ...m, steps: [...steps, { type: 'tool_result', content: ` ${event.content || '工具执行完成'}` }] }
        }))
        break

      case 'answer':
        // 最终答案片段（流式增量追加，不清除步骤历史）
        setMessages(prev => prev.map(m => {
          if (!isTarget(m)) return m
          const newContent = (m.content || '') + (event.content || '')
          // 如果有步骤但只有初始 thinking（没有 tool_call/tool_result/routing），且答案已开始，清除步骤
          const hasRealSteps = m.steps.some(s => s.type === 'tool_call' || s.type === 'tool_result' || s.type === 'routing')
          const newSteps = (!hasRealSteps && newContent) ? [] : m.steps
          return { ...m, content: newContent, steps: newSteps }
        }))
        break

      case 'interrupt':
        // HITL 中断 → 清除步骤历史，只显示中断信息
        setMessages(prev => prev.map(m =>
          isTarget(m)
            ? {
                ...m,
                content: event.content || '⏸️ 等待人工审批...',
                interrupt: event.interrupt,
                steps: [],
                streamDone: true,
                resuming: false,
              }
            : m
        ))
        break

      case 'error':
        // 错误 → 标记流结束
        setMessages(prev => prev.map(m =>
          isTarget(m)
            ? { ...m, content: event.content || '执行出错', isError: true, streamDone: true, resuming: false }
            : m
        ))
        break

      case 'done':
        // 流式结束 → 标记完成，停止所有动画
        setMessages(prev => prev.map(m =>
          isTarget(m)
            ? { ...m, streamDone: true, resuming: false }
            : m
        ))
        setLoading(false)
        break
    }
  }
}

export default function ChatPage({ onLogout }: ChatPageProps) {
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [session, setSession] = useState<SessionInfo | null>(null)
  const threadId = useRef(`thread_${Date.now()}`)
  const abortRef = useRef<(() => void) | null>(null)

  useEffect(() => {
    getSession().then(setSession).catch(() => {})
    return () => {
      // 组件卸载时取消正在进行的流式请求
      abortRef.current?.()
    }
  }, [])

  const handleSend = async (text?: string) => {
    const msg = text || input.trim()
    if (!msg || loading) return

    setInput('')
    setLoading(true)

    const userMsg: Message = { id: `u_${Date.now()}`, role: 'user', content: msg, steps: [] }
    setMessages(prev => [...prev, userMsg])

    // 创建流式 assistant 消息占位
    const assistantId = `a_${Date.now()}`
    const assistantMsg: Message = {
      id: assistantId,
      role: 'assistant',
      content: '',
      steps: [{ type: 'thinking', content: ' 思考中...' }],
    }
    setMessages(prev => [...prev, assistantMsg])

    // 使用流式 SSE
    const handler = createStreamEventHandler(assistantId, setMessages, setLoading)
    const cancel = chatStream(
      { thread_id: threadId.current, message: msg },
      handler,
    )
    abortRef.current = cancel
  }

  const handleDecision = async (interrupt: InterruptInfo, decision: 'approve' | 'reject') => {
    setLoading(true)

    // 找到被中断的消息，标记为正在恢复
    const resumeId = `resume_${Date.now()}`
    setMessages(prev => prev.map(msg => {
      if (msg.interrupt?.interrupt_id !== interrupt.interrupt_id) return msg
      return {
        ...msg,
        interrupt: undefined,
        content: '',
        steps: [{ type: 'thinking', content: ' 恢复执行中...' }],
        streamDone: false,
        resuming: true,
      }
    }))

    // 使用流式审批恢复
    const handler = createStreamEventHandler(
      resumeId,
      setMessages,
      setLoading,
      // 定位正在恢复的消息
      (m: Message) => m.resuming === true,
    )

    const cancel = decideCheckpointStream(
      threadId.current,
      { decision, comment: decision === 'reject' ? '操作被拒绝' : '' },
      handler,
    )
    abortRef.current = cancel
  }

  const handleLogout = async () => {
    try { await logout() } catch {}
    clearToken()
    onLogout()
  }

  const roleLabel = session?.role === 'admin' ? '管理员' : '访客'
  const roleClass = session?.role === 'admin' ? 'role-admin' : 'role-visitor'

  // 判断是否有正在进行的 thinking/tool/routing 步骤（需要显示加载动画）
  // 条件：流未结束 + 最后一步是 thinking/tool_call/routing + 还没有答案内容
  const isThinking = (msg: Message): boolean => {
    if (msg.streamDone) return false
    if (msg.content) return false  // 已有答案内容，思考完成
    if (msg.steps.length === 0) return false
    const last = msg.steps[msg.steps.length - 1]
    return last?.type === 'thinking' || last?.type === 'tool_call' || last?.type === 'routing'
  }

  return (
    <div className="chat-page">
      {/* 顶栏 */}
      <div className="chat-header">
        <h2>💬 Kingsoft Agent</h2>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <span className={`role-tag ${roleClass}`}>{roleLabel}</span>
          <button className="nav-settings-btn" onClick={handleLogout}>退出登录</button>
        </div>
      </div>

      {/* 消息区 */}
      <div className="chat-messages">
        {messages.length === 0 && (
          <div className="chat-empty">
            <div className="chat-empty-icon">🤖</div>
            <h3>Kingsoft Agent 助手</h3>
            <div className="chat-empty-hint">
              {session?.role === 'admin'
                ? '管理员可使用所有工具，包括哈希计算和邮件发送（需审批）'
                : '访客可使用计算器和文件搜索工具'}
            </div>
            <div className="quick-prompts">
              {QUICK_PROMPTS.map((p, i) => (
                <button key={i} className="quick-prompt-btn" onClick={() => handleSend(p.message)}>
                  {p.label}
                </button>
              ))}
            </div>
          </div>
        )}

        {messages.map(msg => (
          <div key={msg.id} className={`chat-bubble ${msg.role} ${msg.isError ? 'bubble-error' : ''}`}>
            {/* 头像 */}
            <div className="bubble-avatar">
              {msg.role === 'user' ? '👤' : '🤖'}
            </div>

            {/* 气泡体 */}
            <div className="bubble-body">
              {/* 推理步骤链（DeepSeek 样式：灰色小字、单独段落、持久保留） */}
              {msg.steps.length > 0 && (
                <div className="bubble-steps">
                  {msg.steps.map((step, i) => (
                    <div key={i} className={`bubble-step bubble-step-${step.type}`}>
                      {step.type === 'thinking' && (
                        <span className="step-icon">💭</span>
                      )}
                      {step.type === 'routing' && (
                        <span className="step-icon">🔀</span>
                      )}
                      {step.type === 'tool_call' && (
                        <span className="step-icon">🔧</span>
                      )}
                      {step.type === 'tool_result' && (
                        <span className="step-icon">✅</span>
                      )}
                      <span className="step-text">{step.content}</span>
                      {step.tool && step.type === 'tool_call' && (
                        <span className="step-tool-detail">{step.tool.name}</span>
                      )}
                      {/* 思考中/路由中的步骤加动画 */}
                      {(step.type === 'thinking' || step.type === 'tool_call' || step.type === 'routing') && i === msg.steps.length - 1 && isThinking(msg) && (
                        <span className="step-anim-dots">
                          <span></span><span></span><span></span>
                        </span>
                      )}
                    </div>
                  ))}
                  {/* 步骤与最终答案之间用分割线 */}
                  {msg.content && msg.steps.length > 0 && (
                    <div className="bubble-steps-divider"></div>
                  )}
                </div>
              )}

              {/* 最终答案 */}
              {msg.content ? (
                <div className="bubble-content">
                  <MarkdownRenderer content={msg.content} />
                </div>
              ) : null}

              {/* 打字指示器（无内容且无步骤时） */}
              {msg.role === 'assistant' && !msg.content && msg.steps.length === 0 && loading && (
                <div className="bubble-typing">
                  <span></span><span></span><span></span>
                </div>
              )}

              {msg.isError && msg.content?.includes('权限不足') && (
                <div className="bubble-acl-note">
                  ⚠️ ACLToolMiddleware 拦截：回灌拒绝信息，Agent 可自主调整策略
                </div>
              )}
              {msg.interrupt && (
                <div className="interrupt-card">
                  <div className="interrupt-header">⏸️ 需要人工审批</div>
                  <div className="interrupt-detail">
                    <p><strong>工具：</strong>{msg.interrupt.tool_name}</p>
                    <p><strong>输入：</strong>{msg.interrupt.tool_input}</p>
                    <p><strong>原因：</strong>{msg.interrupt.risk_reason}</p>
                  </div>
                  <div className="interrupt-actions">
                    <button
                      className="btn-approve"
                      onClick={() => handleDecision(msg.interrupt!, 'approve')}
                      disabled={loading}
                    >
                      ✅ 批准
                    </button>
                    <button
                      className="btn-reject"
                      onClick={() => handleDecision(msg.interrupt!, 'reject')}
                      disabled={loading}
                    >
                      ❌ 拒绝
                    </button>
                  </div>
                </div>
              )}
            </div>
          </div>
        ))}
      </div>

      {/* 输入区 */}
      <div className="chat-input-area">
        <input
          className="chat-input"
          type="text"
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && !e.shiftKey && handleSend()}
          placeholder="输入消息，如：计算2+3*4、grep TODO、发送邮件..."
          disabled={loading}
        />
        <button
          className="chat-send-btn"
          onClick={() => handleSend()}
          disabled={loading || !input.trim()}
        >
          发送
        </button>
      </div>
    </div>
  )
}
