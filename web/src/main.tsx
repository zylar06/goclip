import React from 'react'
import ReactDOM from 'react-dom/client'
import { createHashRouter, RouterProvider } from 'react-router-dom'
import './i18n'
import { appRoutes } from './routes'
import './index.css'
import './features/studio/studio.css'
import './web.css'

// Hash routes work with any static host without server-side route remapping.
const router = createHashRouter(appRoutes)
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode><RouterProvider router={router} future={{ v7_startTransition: true }} /></React.StrictMode>,
)
