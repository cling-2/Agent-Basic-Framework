// ---- 类型定义 ----

export interface LoginRequest {
  username: string
  password: string
}

export interface LoginResponse {
  session_id: string
  expires_in: number
}

export interface SessionInfo {
  user_id: number
  role: string
  expires_at: string
}

export interface ToolsResponse {
  tools: string[]
}

export interface ChatRequest {
  thread_id: string
  message: string
}

export interface ChatResponse {
  reply: string
  thread_id: string
  interrupt?: InterruptInfo
}

export interface InterruptInfo {
  interrupt_id: string
  tool_name: string
  tool_input: string
  risk_reason: string
}

export interface ResumeRequest {
  decision: 'approve' | 'reject'
  comment?: string
}

export interface InterruptCard {
  interrupt_id: string
  approval_info: {
    tool_name: string
    tool_input: string
    risk_reason: string
    call_id: string
    thread_id: string
  }
  original_message: string
  created_at: string
  expires_at: string
}

export interface CheckpointsResponse {
  checkpoints: InterruptCard[]
}

export interface ToolItem {
  name: string
  desc: string
}

export interface ToolInfoResponse {
  tools: ToolItem[]
}

export interface AgentInfo {
  name: string
  intended_use: string
}

export interface AgentsResponse {
  agents: AgentInfo[]
}

export interface ErrorDetail {
  code: string
  message: string
}

export interface ErrorResponse {
  error: ErrorDetail
}

// ---- Token 管理 ----

const TOKEN_KEY = 'kingsoft_agent_session_id'

export function saveToken(sessionId: string): void {
  localStorage.setItem(TOKEN_KEY, sessionId)
}

export function loadToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
}

// ---- 通用请求 ----

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const token = loadToken()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string> ?? {}),
  }
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  const res = await fetch(path, { ...options, headers })

  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as ErrorResponse | null
    const code = body?.error?.code ?? 'UNKNOWN'
    const message = body?.error?.message ?? res.statusText
    throw new ApiError(res.status, code, message)
  }

  return res.json() as Promise<T>
}

// ---- 自定义错误 ----

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

// ---- DOC-01: 身份与权限 API ----

/** 登录 */
export function login(req: LoginRequest): Promise<LoginResponse> {
  return request<LoginResponse>('/api/auth/login', {
    method: 'POST',
    body: JSON.stringify(req),
  })
}

/** 登出 */
export function logout(): Promise<{ message: string }> {
  return request<{ message: string }>('/api/auth/logout', {
    method: 'POST',
  })
}

/** 查询当前会话信息 */
export function getSession(): Promise<SessionInfo> {
  return request<SessionInfo>('/api/auth/session')
}

/** 查询当前用户可调用工具 (DOC-01) */
export function getTools(): Promise<ToolsResponse> {
  return request<ToolsResponse>('/api/permissions/tools')
}

// ---- DOC-02: Agent & 工具调用 API ----

/** 与 Agent 对话 */
export function chat(req: ChatRequest): Promise<ChatResponse> {
  return request<ChatResponse>('/api/agent/chat', {
    method: 'POST',
    body: JSON.stringify(req),
  })
}

/** 列出所有已注册工具（admin） */
export function listTools(): Promise<ToolInfoResponse> {
  return request<ToolInfoResponse>('/api/tools')
}

/** 列出所有已注册 Agent（admin） */
export function listAgents(): Promise<AgentsResponse> {
  return request<AgentsResponse>('/api/agents')
}

// ---- LLM 配置 API ----

export interface SettingsStatusResponse {
  configured: boolean
}

export interface SettingsResponse {
  api_key: string
  base_url: string
  model: string
}

export interface UpdateSettingsRequest {
  api_key: string
  base_url: string
  model: string
}

/** 查询LLM配置状态 */
export function getSettingsStatus(): Promise<SettingsStatusResponse> {
  return request<SettingsStatusResponse>('/api/settings/status')
}

/** 获取当前LLM配置（admin） */
export function getSettings(): Promise<SettingsResponse> {
  return request<SettingsResponse>('/api/settings')
}

/** 更新LLM配置（admin） */
export function updateSettings(req: UpdateSettingsRequest): Promise<{ message: string; configured: boolean }> {
  return request<{ message: string; configured: boolean }>('/api/settings', {
    method: 'PUT',
    body: JSON.stringify(req),
  })
}

export interface TestConnectionResponse {
  success: boolean
  message: string
}

/** 测试LLM连接（admin） */
export function testConnection(req: UpdateSettingsRequest): Promise<TestConnectionResponse> {
  return request<TestConnectionResponse>('/api/settings/test', {
    method: 'POST',
    body: JSON.stringify(req),
  })
}

/** 提交审批决策（非流式，保留向后兼容） */
export function decideCheckpoint(threadId: string, req: ResumeRequest): Promise<ChatResponse> {
  return request<ChatResponse>(`/api/agent/checkpoint/${threadId}/decide`, {
    method: 'POST',
    body: JSON.stringify(req),
  })
}

/**
 * 流式审批决策 —— 通过 SSE 接收实时恢复事件
 * 审批后 Agent 继续执行，步骤和结果通过 SSE 事件回传
 */
export function decideCheckpointStream(
  threadId: string,
  req: ResumeRequest,
  onEvent: StreamEventHandler,
): () => void {
  const token = loadToken()
  const params = new URLSearchParams({
    decision: req.decision,
    ...(req.comment ? { comment: req.comment } : {}),
  })
  const url = `/api/agent/checkpoint/${encodeURIComponent(threadId)}/decide/stream?${params}`

  const controller = new AbortController()

  ;(async () => {
    try {
      const headers: Record<string, string> = {}
      if (token) {
        headers['Authorization'] = `Bearer ${token}`
      }

      const resp = await fetch(url, { headers, signal: controller.signal })

      if (!resp.ok) {
        onEvent({ type: 'error', content: `请求失败: ${resp.status}` })
        onEvent({ type: 'done' })
        return
      }

      const reader = resp.body?.getReader()
      if (!reader) {
        onEvent({ type: 'error', content: '无法读取响应流' })
        onEvent({ type: 'done' })
        return
      }

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })

        const parts = buffer.split('\n\n')
        buffer = parts.pop() || ''

        for (const part of parts) {
          const lines = part.split('\n')
          let dataLine = ''
          for (const line of lines) {
            if (line.startsWith('data: ')) {
              dataLine += line.slice(6)
            }
          }
          if (!dataLine.trim()) continue

          try {
            const event: StreamEvent = JSON.parse(dataLine)
            onEvent(event)
          } catch {
            // 忽略解析失败的行
          }
        }
      }
    } catch (err: any) {
      if (err.name !== 'AbortError') {
        onEvent({ type: 'error', content: err.message || '流式连接异常' })
        onEvent({ type: 'done' })
      }
    }
  })()

  return () => controller.abort()
}

/** 查询待审批检查点 */
export function listCheckpoints(): Promise<CheckpointsResponse> {
  return request<CheckpointsResponse>('/api/agent/checkpoints')
}

// ---- SSE 流式事件类型 ----

export type StreamEventType = 'thinking' | 'tool_call' | 'tool_result' | 'answer' | 'interrupt' | 'routing' | 'done' | 'error'

export interface StreamToolCallInfo {
  name: string
  args: string
  id: string
}

export interface StreamEvent {
  type: StreamEventType
  content?: string
  tool?: StreamToolCallInfo
  interrupt?: InterruptInfo
}

export type StreamEventHandler = (event: StreamEvent) => void

/**
 * 流式对话 —— 通过 SSE 接收实时事件
 * 使用 EventSource 发起 GET 请求，逐条解析 SSE 事件
 */
export function chatStream(
  params: { thread_id: string; message: string },
  onEvent: StreamEventHandler,
): () => void {
  const token = loadToken()
  const url = `/api/agent/chat/stream?thread_id=${encodeURIComponent(params.thread_id)}&message=${encodeURIComponent(params.message)}`

  // 使用 fetch 实现 SSE（支持 Authorization header，EventSource 不支持自定义 header）
  const controller = new AbortController()

  ;(async () => {
    try {
      const headers: Record<string, string> = {}
      if (token) {
        headers['Authorization'] = `Bearer ${token}`
      }

      const resp = await fetch(url, { headers, signal: controller.signal })

      if (!resp.ok) {
        onEvent({ type: 'error', content: `请求失败: ${resp.status}` })
        onEvent({ type: 'done' })
        return
      }

      const reader = resp.body?.getReader()
      if (!reader) {
        onEvent({ type: 'error', content: '无法读取响应流' })
        onEvent({ type: 'done' })
        return
      }

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })

        // 解析 SSE 格式：以 \n\n 分隔事件，每事件可能多行 data:
        const parts = buffer.split('\n\n')
        buffer = parts.pop() || '' // 最后一个可能不完整，留在 buffer

        for (const part of parts) {
          const lines = part.split('\n')
          let dataLine = ''
          for (const line of lines) {
            if (line.startsWith('data: ')) {
              dataLine += line.slice(6)
            }
          }
          if (!dataLine.trim()) continue

          try {
            const event: StreamEvent = JSON.parse(dataLine)
            onEvent(event)
          } catch {
            // 忽略解析失败的行
          }
        }
      }
    } catch (err: any) {
      if (err.name !== 'AbortError') {
        onEvent({ type: 'error', content: err.message || '流式连接异常' })
        onEvent({ type: 'done' })
      }
    }
  })()

  // 返回取消函数
  return () => controller.abort()
}
