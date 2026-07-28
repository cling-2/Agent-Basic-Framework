import { useState } from 'react'

// 会话条目
export interface SessionItem {
  id: string
  title: string
  createdAt: number
}

interface SidebarProps {
  sessions: SessionItem[]
  activeId: string | null
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (id: string) => void
  onRename: (id: string, title: string) => void
  collapsed: boolean
  onToggle: () => void
}

export default function Sidebar({
  sessions,
  activeId,
  onSelect,
  onNew,
  onDelete,
  onRename,
  collapsed,
  onToggle,
}: SidebarProps) {
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const [contextMenuId, setContextMenuId] = useState<string | null>(null)

  const handleRenameStart = (s: SessionItem) => {
    setRenamingId(s.id)
    setRenameValue(s.title)
    setContextMenuId(null)
  }

  const handleRenameConfirm = () => {
    if (renamingId && renameValue.trim()) {
      onRename(renamingId, renameValue.trim())
    }
    setRenamingId(null)
  }

  const handleRenameKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') handleRenameConfirm()
    if (e.key === 'Escape') setRenamingId(null)
  }

  return (
    <>
      {/* 移动端遮罩 */}
      {!collapsed && <div className="sidebar-overlay" onClick={onToggle} />}

      <div className={`sidebar ${collapsed ? 'sidebar-collapsed' : ''}`}>
        {/* 顶部：新建会话 + 折叠按钮 */}
        <div className="sidebar-header">
          <button className="sidebar-new-btn" onClick={onNew} title="新建会话">
            ＋ 新会话
          </button>
          <button className="sidebar-toggle-btn" onClick={onToggle} title="折叠侧栏">
            ◀
          </button>
        </div>

        {/* 会话列表 */}
        <div className="sidebar-list">
          {sessions.map(s => (
            <div
              key={s.id}
              className={`sidebar-item ${s.id === activeId ? 'sidebar-item-active' : ''}`}
              onClick={() => onSelect(s.id)}
              onContextMenu={e => {
                e.preventDefault()
                setContextMenuId(contextMenuId === s.id ? null : s.id)
              }}
            >
              {renamingId === s.id ? (
                <input
                  className="sidebar-rename-input"
                  value={renameValue}
                  onChange={e => setRenameValue(e.target.value)}
                  onKeyDown={handleRenameKeyDown}
                  onBlur={handleRenameConfirm}
                  autoFocus
                  onClick={e => e.stopPropagation()}
                />
              ) : (
                <span className="sidebar-item-title">{s.title}</span>
              )}

              {/* 右键/长按操作菜单 */}
              {contextMenuId === s.id && (
                <div className="sidebar-item-menu" onClick={e => e.stopPropagation()}>
                  <button onClick={() => handleRenameStart(s)}>✏️ 重命名</button>
                  <button
                    className="sidebar-item-delete"
                    onClick={() => { onDelete(s.id); setContextMenuId(null) }}
                  >
                    🗑️ 删除
                  </button>
                </div>
              )}
            </div>
          ))}

          {sessions.length === 0 && (
            <div className="sidebar-empty">暂无会话</div>
          )}
        </div>
      </div>
    </>
  )
}
