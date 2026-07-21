import { createRoot } from 'react-dom/client'
import './lib/dev/mockWails'
import './index.css'
import App from './App.tsx'

// No StrictMode: dev double-mount raced StartVKLogin and could taskkill the
// native VK WebView2 worker (~1s window flash).
createRoot(document.getElementById('root')!).render(<App />)
