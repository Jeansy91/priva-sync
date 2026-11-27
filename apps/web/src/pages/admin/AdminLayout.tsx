import { Outlet } from 'react-router-dom'

// Container without its own navigation — admin routes appear directly in the
// main sidebar (AppShell).
export function AdminLayout() {
  return <Outlet />
}
