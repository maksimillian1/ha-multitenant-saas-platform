import { useSuspenseQuery } from '@tanstack/react-query'
import { Link, Outlet } from '@tanstack/react-router'
import { Building2, FolderKanban, LayoutDashboard, PanelLeftClose, PanelLeftOpen, Palette, Users } from 'lucide-react'
import { useState } from 'react'
import { meQuery } from '@/api/queries'
import { ThemeMenu } from '@/components/theme-menu'
import { Button } from '@/components/ui/button'
import { Toaster } from '@/components/ui/sonner'
import { UserMenu } from '@/components/user-menu'

const nav = [
  { to: '/', label: 'Overview', icon: LayoutDashboard, exact: true },
  { to: '/orgs', label: 'Organizations', icon: Building2, exact: false },
  { to: '/users', label: 'Users', icon: Users, exact: false },
  { to: '/projects', label: 'Projects', icon: FolderKanban, exact: false },
  { to: '/branding', label: 'Branding', icon: Palette, exact: false },
] as const

const linkStyle =
  'flex items-center gap-2.5 rounded-lg text-sm whitespace-nowrap text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground data-[status=active]:bg-sidebar-accent data-[status=active]:font-medium data-[status=active]:text-sidebar-accent-foreground'

// A collapsed link keeps its label for screen readers and as a tooltip.
function NavLinks({ collapsed = false, className = '' }: { collapsed?: boolean; className?: string }) {
  return (
    <>
      {nav.map(({ to, label, icon: Icon, exact }) => (
        <Link
          key={to}
          to={to}
          activeOptions={{ exact, includeSearch: false }}
          title={collapsed ? label : undefined}
          className={`${linkStyle} ${className}`}
        >
          <Icon className="size-4 shrink-0" />
          <span className={collapsed ? 'sr-only' : undefined}>{label}</span>
        </Link>
      ))}
    </>
  )
}

function Logo() {
  return (
    <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-xs font-bold text-primary-foreground">
      HA
    </span>
  )
}

// The sidebar's width survives a reload; localStorage may be unavailable.
const collapsedKey = 'admin.sidebar.collapsed'

function storedCollapsed() {
  try {
    return localStorage.getItem(collapsedKey) === 'true'
  } catch {
    return false
  }
}

function storeCollapsed(collapsed: boolean) {
  try {
    localStorage.setItem(collapsedKey, String(collapsed))
  } catch {
    // The sidebar keeps its width until the page reloads.
  }
}

export function RootLayout() {
  const { data: me } = useSuspenseQuery(meQuery)
  const [collapsed, setCollapsed] = useState(storedCollapsed)
  const toggle = () => {
    storeCollapsed(!collapsed)
    setCollapsed(!collapsed)
  }

  return (
    <div className="flex min-h-svh bg-muted/40">
      <aside
        className={`sticky top-0 hidden h-svh shrink-0 flex-col gap-6 border-r bg-sidebar px-3 py-4 text-sidebar-foreground transition-[width] duration-200 md:flex ${collapsed ? 'w-16' : 'w-64'}`}
      >
        <Link to="/" className={`flex items-center gap-2.5 ${collapsed ? 'justify-center' : 'px-1.5'}`}>
          <Logo />
          {collapsed ? (
            <span className="sr-only">HAMSP admin</span>
          ) : (
            <span className="flex min-w-0 flex-col">
              <span className="truncate text-sm font-semibold">HAMSP admin</span>
              <span className="truncate text-xs text-muted-foreground">Platform administration</span>
            </span>
          )}
        </Link>
        <nav aria-label="Main" className="flex flex-col gap-1">
          {!collapsed && (
            <span className="px-2.5 pb-1 text-xs font-medium tracking-wide text-muted-foreground uppercase">Manage</span>
          )}
          <NavLinks collapsed={collapsed} className={collapsed ? 'justify-center px-0 py-2' : 'px-2.5 py-2'} />
        </nav>
        <div className="mt-auto border-t pt-3">
          <UserMenu me={me} placement="sidebar" collapsed={collapsed} />
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-20 border-b bg-background/95 backdrop-blur">
          <div className="flex h-14 items-center gap-2 px-4 md:px-6">
            <Button
              variant="ghost"
              size="icon-lg"
              className="hidden md:inline-flex"
              aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
              aria-expanded={!collapsed}
              onClick={toggle}
            >
              {collapsed ? <PanelLeftOpen /> : <PanelLeftClose />}
            </Button>
            <Link to="/" className="flex items-center gap-2 font-semibold md:hidden">
              <Logo />
              HAMSP admin
            </Link>
            <div className="ml-auto flex items-center gap-1">
              <ThemeMenu />
              <div className="md:hidden">
                <UserMenu me={me} />
              </div>
            </div>
          </div>
          <nav aria-label="Sections" className="flex gap-1 overflow-x-auto px-4 pb-2 md:hidden">
            <NavLinks className="px-2.5 py-1.5" />
          </nav>
        </header>
        <main className="mx-auto w-full max-w-7xl min-w-0 px-4 py-6 md:px-8 md:py-8">
          <Outlet />
        </main>
      </div>
      <Toaster position="bottom-right" />
    </div>
  )
}
