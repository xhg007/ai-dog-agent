import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import ObservabilityApp from './components/observability/ObservabilityApp'
import { applyStoredGlass } from './lib/glass'

applyStoredGlass()

// 观测窗口复用同一 bundle：由 OpenWindow 的 URL（/?view=observability）分流。
// 用查询参数而非路径，确保 Wails 静态资源处理与 Vite dev server 都命中 index.html。
const isObservability = new URLSearchParams(window.location.search).get('view') === 'observability'

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    {isObservability ? <ObservabilityApp /> : <App />}
  </React.StrictMode>,
)
