import { useState, useRef, useEffect } from 'react'
import {
  getSession,
  getHistory,
  decideCheckpointStream,
  chatStream,
  listCheckpoints,
  type SessionInfo,
  type InterruptInfo,
  type StreamEvent,
} from '../api'
import MarkdownRenderer from './MarkdownRenderer'

interface ChatPageProps {
  threadId: string | null
  sessionTitle?: string
  onSessionTitleUpdate?: (id: string, title: string) => void
  onSessionDelete?: (id: string) => void
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
  { label: '📧 发送邮件', message: '发送一封邮件。主题：项目进展 正文：目前进展不错。 发送给alice@example.com' },
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

export default function ChatPage({ threadId, sessionTitle, onSessionTitleUpdate, onSessionDelete }: ChatPageProps) {
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [session, setSession] = useState<SessionInfo | null>(null)
  const [historyLoaded, setHistoryLoaded] = useState(false)
  const [staleSession, setStaleSession] = useState(false) // 服务器重启后会话数据丢失
  const threadIdRef = useRef(threadId || `thread_${Date.now()}`)
  const abortRef = useRef<(() => void) | null>(null)
  const messagesEndRef = useRef<HTMLDivElement>(null) // 自动滚动锚点

  // 自动滚动到底部：消息变化或流式更新时触发
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  useEffect(() => {
    getSession().then(setSession).catch(() => {})
    return () => {
      abortRef.current?.()
    }
  }, [])

  // 从后端加载消息历史
  useEffect(() => {
    if (!threadId) {
      setHistoryLoaded(true)
      return
    }
    setHistoryLoaded(false)
    setStaleSession(false)

    let pollTimer: ReturnType<typeof setTimeout> | null = null
    let cancelled = false

    function loadHistory() {
      getHistory(threadId!)
        .then(async res => {
          if (cancelled) return
          const loaded: Message[] = res.messages
            .filter(m => m.role === 'user' || m.role === 'assistant')
            .map((m, i) => ({
              id: `hist_${i}`,
              role: m.role as 'user' | 'assistant',
              content: m.content,
              steps: [],
              streamDone: true,
              // 从历史中恢复中断状态
              interrupt: m.interrupt ? {
                interrupt_id: m.interrupt.interrupt_id,
                tool_name: m.interrupt.tool_name,
                tool_input: m.interrupt.tool_input,
                risk_reason: m.interrupt.risk_reason,
              } : undefined,
            }))

          // 如果后端返回空历史，且会话标题不是"新会话"（说明之前有过对话但服务器重启后数据丢失）
          if (loaded.length === 0 && res.messages.length === 0 && sessionTitle && sessionTitle !== '新会话') {
            setStaleSession(true)
          }

          // 兜底检查：如果历史中无中断但 ApprovalStore 仍有待审批卡片，追加中断消息
          // 使用 await 同步化，确保在决策之前 hasInterrupt 状态已确定
          let hasInterrupt = loaded.some(m => m.interrupt)
          if (!hasInterrupt) {
            try {
              const cpRes = await listCheckpoints()
              if (cancelled) return
              const pending = cpRes.checkpoints.find(cp => cp.approval_info.thread_id === threadId)
              if (pending) {
                hasInterrupt = true
                loaded.push({
                  id: `interrupt_${Date.now()}`,
                  role: 'assistant' as const,
                  content: '⏸️ 操作需要人工审批，请在下方审批面板中确认。',
                  steps: [],
                  streamDone: true,
                  interrupt: {
                    interrupt_id: pending.interrupt_id,
                    tool_name: pending.approval_info.tool_name,
                    tool_input: pending.approval_info.tool_input,
                    risk_reason: pending.approval_info.risk_reason,
                  },
                })
              }
            } catch {
              // 非关键，忽略错误
            }
          }

          // 三分支决策：中断优先 → Agent 仍在处理 → 对话正常结束
          // 关键区分：中断 = Agent 暂停等待用户操作（不轮询、不设置 loading）；
          //           待处理 = Agent 仍在运行（需要轮询、显示思考占位）
          const lastMsg = loaded.length > 0 ? loaded[loaded.length - 1]! : null

          if (hasInterrupt) {
            // 有待审批的中断：Agent 已暂停，等待用户操作
            // 不设置 loading（确保审批按钮可点击），不追加思考占位消息，不启动轮询
            setLoading(false)
            setMessages(loaded)
          } else if (lastMsg?.role === 'user') {
            // Agent 仍在处理中，尚未回复
            setLoading(true)
            // 在消息末尾追加"思考中"占位消息，让用户看到 agent 正在工作
            setMessages([...loaded, {
              id: 'thinking_placeholder',
              role: 'assistant' as const,
              content: '',
              steps: [{ type: 'thinking', content: '正在思考...' }],
              streamDone: false,
            }])
            pollTimer = setTimeout(loadHistory, 2000) // 2 秒后再次加载
          } else {
            // 正常的最终回复已到，停止轮询
            setLoading(false)
            setMessages(loaded)
          }
        })
        .catch(() => {
          if (cancelled) return
          setMessages([])
        })
        .finally(() => {
          if (!cancelled) setHistoryLoaded(true)
        })
    }

    loadHistory()

    return () => {
      cancelled = true
      if (pollTimer) clearTimeout(pollTimer)
    }
  }, [threadId])

  const handleSend = async (text?: string) => {
    const msg = text || input.trim()
    if (!msg || loading) return

    setInput('')
    setLoading(true)

    // 首次发送消息时，用消息内容更新会话标题
    if (messages.length === 0 && onSessionTitleUpdate && threadId) {
      onSessionTitleUpdate(threadId, msg.slice(0, 30) + (msg.length > 30 ? '...' : ''))
    }

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
      { thread_id: threadIdRef.current, message: msg },
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
      threadIdRef.current,
      { decision, comment: decision === 'reject' ? '操作被拒绝' : '' },
      handler,
    )
    abortRef.current = cancel
  }

  return (
    <div className="chat-page">
      {/* 消息区 */}
      <div className="chat-messages">
        {!historyLoaded && (
          <div className="chat-empty">
            <div className="chat-empty-icon">⏳</div>
            <h3>加载历史消息...</h3>
          </div>
        )}
        {historyLoaded && messages.length === 0 && staleSession && (
          <div className="chat-empty">
            <div className="chat-empty-icon">⚠️</div>
            <h3>会话数据已丢失</h3>
            <div className="chat-empty-hint">
              服务器重启后历史消息不可恢复，建议删除此会话并创建新会话
            </div>
            {onSessionDelete && threadId && (
              <button
                className="btn-approve"
                style={{ marginTop: 12, background: '#ef4444' }}
                onClick={() => onSessionDelete(threadId)}
              >
                🗑️ 删除此会话
              </button>
            )}
          </div>
        )}
        {historyLoaded && messages.length === 0 && !staleSession && (
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

        {messages.map(msg => {
          // 判断是否有正在进行的 thinking/tool/routing 步骤
          const isThinking = !msg.streamDone && !msg.content && msg.steps.length > 0 &&
            ['thinking', 'tool_call', 'routing'].includes(msg.steps[msg.steps.length - 1]?.type || '')

          return (
            <div key={msg.id} className={`chat-bubble ${msg.role} ${msg.isError ? 'bubble-error' : ''}`}>
              {/* 头像 */}
              <div className="bubble-avatar">
                {msg.role === 'user' ? '👤' : '🤖'}
              </div>

              {/* 气泡体 */}
              <div className="bubble-body">
                {/* 推理步骤链 */}
                {msg.steps.length > 0 && (
                  <div className="bubble-steps">
                    {msg.steps.map((step, i) => (
                      <div key={i} className={`bubble-step bubble-step-${step.type}`}>
                        {step.type === 'thinking' && <span className="step-icon">💭</span>}
                        {step.type === 'routing' && <span className="step-icon">🔀</span>}
                        {step.type === 'tool_call' && <span className="step-icon">🔧</span>}
                        {step.type === 'tool_result' && <span className="step-icon">✅</span>}
                        <span className="step-text">{step.content}</span>
                        {step.tool && step.type === 'tool_call' && (
                          <span className="step-tool-detail">{step.tool.name}</span>
                        )}
                        {(step.type === 'thinking' || step.type === 'tool_call' || step.type === 'routing') && i === msg.steps.length - 1 && isThinking && (
                          <span className="step-anim-dots">
                            <span></span><span></span><span></span>
                          </span>
                        )}
                      </div>
                    ))}
                    {msg.content && msg.steps.length > 0 && (
                      <div className="bubble-steps-divider"></div>
                    )}
                  </div>
                )}

                {/* 最终答案（中断消息只显示审批卡片，不显示文字内容） */}
                {msg.content && !msg.interrupt ? (
                  <div className="bubble-content">
                    <MarkdownRenderer content={msg.content} />
                  </div>
                ) : null}

                {/* 打字指示器 */}
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
          )
        })}
        {/* 自动滚动锚点 */}
        <div ref={messagesEndRef} />
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
