import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { applyStoredGlass } from './lib/glass'

applyStoredGlass()

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
