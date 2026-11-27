import { lazy, Suspense } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import { AdminLayout } from './AdminLayout'

const AdminUsersPage = lazy(() => import('../AdminPage'))
const AuditLogPage   = lazy(() => import('./AuditLogPage'))
const AdminStatsPage = lazy(() => import('./AdminStatsPage'))
const AdminJobsPage    = lazy(() => import('./AdminJobsPage'))
const AdminClientsPage = lazy(() => import('./AdminClientsPage'))

export function AdminRoutes() {
  return (
    <Routes>
      <Route element={<AdminLayout />}>
        <Route index             element={<Navigate to="users" replace />} />
        <Route path="users"      element={<Suspense fallback={null}><AdminUsersPage /></Suspense>} />
        <Route path="stats"      element={<Suspense fallback={null}><AdminStatsPage /></Suspense>} />
        <Route path="jobs"       element={<Suspense fallback={null}><AdminJobsPage /></Suspense>} />
        <Route path="clients"    element={<Suspense fallback={null}><AdminClientsPage /></Suspense>} />
        <Route path="audit-log"  element={<Suspense fallback={null}><AuditLogPage /></Suspense>} />
        <Route path="*"          element={<Navigate to="users" replace />} />
      </Route>
    </Routes>
  )
}
