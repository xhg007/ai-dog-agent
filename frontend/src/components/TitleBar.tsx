import { useEffect, useState } from 'react'
import { Window } from '@wailsio/runtime'

// 无边框窗口自定义标题栏：整条为拖拽区（--wails-draggable: drag），
// 右侧按钮 no-drag，走 @wailsio/runtime Window API。
function TitleBar() {
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
    <div className="titlebar">
      <span className="titlebar-title">ai-dog-agent</span>
      <div className="titlebar-controls">
        <button className="win-btn" title="最小化" onClick={() => Window.Minimise().catch(() => { /* 忽略 */ })}>
          <svg width="10" height="10" viewBox="0 0 10 10"><path d="M0 5 H10" stroke="currentColor" strokeWidth="1" /></svg>
        </button>
        <button className="win-btn" title={maxed ? '向下还原' : '最大化'} onClick={toggleMaximise}>
          {maxed
            ? <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor">
                <rect x="0.5" y="2.5" width="7" height="7" />
                <path d="M2.5 2.5 V0.5 H9.5 V7.5 H7.5" />
              </svg>
            : <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor">
                <rect x="0.5" y="0.5" width="9" height="9" />
              </svg>}
        </button>
        <button className="win-btn close" title="关闭" onClick={() => Window.Close().catch(() => { /* 忽略 */ })}>
          <svg width="10" height="10" viewBox="0 0 10 10"><path d="M0 0 L10 10 M10 0 L0 10" stroke="currentColor" strokeWidth="1" /></svg>
        </button>
      </div>
    </div>
  )
}

export default TitleBar
