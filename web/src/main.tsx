import React from 'react'
import ReactDOM from 'react-dom/client'
import { createHashRouter, RouterProvider } from 'react-router-dom'
import './i18n'
import App, { RouteError } from './App'
import HomePage from './pages/HomePage'
import ProjectPage from './pages/ProjectPage'
import SettingsPage from './pages/SettingsPage'
import StudioEditor from './features/studio/StudioEditor'
import './index.css'
import './features/studio/studio.css'
import './web.css'

// Hash routes work with any static host and need no build/deploy rewrite rules.
const router = createHashRouter([{
  element: <App />, errorElement: <RouteError />, children: [
    { path: '/', element: <HomePage /> },
    { path: '/project/:id', element: <ProjectPage /> },
    { path: '/project/:id/studio/:draftId', element: <StudioEditor /> },
    { path: '/settings', element: <SettingsPage /> },
    { path: '*', element: <main className="ac-page"><h1>Page not found</h1><a href="#/">Back to projects</a></main> },
  ],
}])
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode><RouterProvider router={router} future={{ v7_startTransition: true }} /></React.StrictMode>,
)
