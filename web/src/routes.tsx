import type { RouteObject } from 'react-router-dom'
import App, { RouteError, NotFound } from './App'
import HomePage from './pages/HomePage'
import ProjectPage from './pages/ProjectPage'
import SettingsPage from './pages/SettingsPage'
import ImportReview from './pages/ImportReview'
import StudioEditor from './features/studio/StudioEditor'

/** Shared by the deployed hash router and the route-level workflow tests. */
export const appRoutes: RouteObject[] = [{
  element: <App />, errorElement: <RouteError />, children: [
    { path: '/', element: <HomePage /> },
    { path: '/import/:id', element: <ImportReview /> },
    { path: '/project/:id', element: <ProjectPage /> },
    { path: '/project/:id/studio/:draftId', element: <StudioEditor /> },
    { path: '/settings', element: <SettingsPage /> },
    { path: '*', element: <NotFound /> },
  ],
}]
