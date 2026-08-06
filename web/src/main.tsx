import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { App } from './App'
import { connectAuthToApi } from './store/auth'
import './styles/global.css'

// Wire the session into the API client before anything renders, so the very first request
// already carries the Authorization header and can refresh on 401.
connectAuthToApi()

const container = document.getElementById('root')
if (!container) throw new Error('#root not found in index.html')

createRoot(container).render(
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
)
