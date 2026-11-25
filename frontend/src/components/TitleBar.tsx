import { useEffect, useState } from 'react'
import { Window } from '@wailsio/runtime'
import { PanelLeft, PanelRight } from 'lucide-react'

// 无边框窗口自定义标题栏：整条为拖拽区（--wails-draggable: drag），
// 右侧按钮 no-drag，走 @wailsio/runtime Window API。
function TitleBar({ sidebarOpen, reviewOpen, onToggleSidebar, onToggleReview }: {
  sidebarOpen: boolean
  reviewOpen: boolean
  onToggleSidebar: () => void
  onToggleReview: () => void
}) {
  const [maxed, setMaxed] = useState(false)

  useEffect(() => {
    Window.IsMaximised().then(setMaxed).catch(() => { /* 忽略 */ })
  }, [])

  const toggleMaximise = async () => {
    try {
      if (await Window.IsMaximised()) {
        await Window.Restore()
      } else {
        await Window.Maximise()
      }
      setMaxed(await Window.IsMaximised())
    } catch { /* 忽略 */ }
  }

  return (
    <div
      className="titlebar"
      onDoubleClick={e => {
        if ((e.target as HTMLElement).closest('button')) return
        toggleMaximise()
      }}
    >
      <div className="titlebar-left">
        <button
          className="titlebar-btn"
          title={sidebarOpen ? '收起侧栏' : '展开侧栏'}
          aria-label={sidebarOpen ? '收起侧栏' : '展开侧栏'}
          aria-pressed={sidebarOpen}
          onClick={onToggleSidebar}
        >
          <PanelLeft className="size-3.5" />
        </button>
        <span className="titlebar-title">ai-dog-agent</span>
      </div>
      <div className="titlebar-controls">
        <button
          className="titlebar-btn"
          title={reviewOpen ? '收起审查栏' : '展开审查栏'}
          aria-label={reviewOpen ? '收起审查栏' : '展开审查栏'}
          aria-pressed={reviewOpen}
          onClick={onToggleReview}
        >
          <PanelRight className="size-3.5" />
        </button>
        <button className="win-btn" title="最小化" aria-label="最小化" onClick={() => Window.Minimise().catch(() => { /* 忽略 */ })}>
          <svg width="10" height="10" viewBox="0 0 10 10"><path d="M0 5 H10" stroke="currentColor" strokeWidth="1" /></svg>
        </button>
        <button className="win-btn" title={maxed ? '向下还原' : '最大化'} aria-label={maxed ? '向下还原' : '最大化'} onClick={toggleMaximise}>
          {maxed
            ? <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor">
                <rect x="0.5" y="2.5" width="7" height="7" />
                <path d="M2.5 2.5 V0.5 H9.5 V7.5 H7.5" />
              </svg>
            : <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor">
                <rect x="0.5" y="0.5" width="9" height="9" />
              </svg>}
        </button>
        <button className="win-btn close" title="关闭" aria-label="关闭" onClick={() => Window.Close().catch(() => { /* 忽略 */ })}>
          <svg width="10" height="10" viewBox="0 0 10 10"><path d="M0 0 L10 10 M10 0 L0 10" stroke="currentColor" strokeWidth="1" /></svg>
        </button>
      </div>
    </div>
  )
}

export default TitleBar
