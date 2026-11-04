import { useSuspenseQuery } from '@tanstack/react-query'
import { Link, Outlet } from '@tanstack/react-router'
import { Building2, ChevronLeft, ChevronRight, FolderKanban, LayoutDashboard, Menu, Palette, Users } from 'lucide-react'
import { Dialog as DialogPrimitive } from 'radix-ui'
import { useCallback, useEffect, useState } from 'react'
import { meQuery, type Me } from '@/api/queries'
import { Button } from '@/components/ui/button'
import { Dialog, DialogOverlay, DialogPortal, DialogTitle } from '@/components/ui/dialog'
import { Toaster } from '@/components/ui/sonner'
import { UserMenu } from '@/components/user-menu'
import { useBrand, type Brand } from '@/lib/brand'

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
function NavLinks({
  collapsed = false,
  className = '',
  onNavigate,
}: {
  collapsed?: boolean
  className?: string
  onNavigate?: () => void
}) {
  return (
    <>
      {nav.map(({ to, label, icon: Icon, exact }) => (
        <Link
          key={to}
          to={to}
          activeOptions={{ exact, includeSearch: false }}
          title={collapsed ? label : undefined}
          onClick={onNavigate}
          className={`${linkStyle} ${className}`}
        >
          <Icon className="size-4 shrink-0" />
          <span className={collapsed ? 'sr-only' : undefined}>{label}</span>
        </Link>
      ))}
    </>
  )
}

// Logo is the platform's icon, or its logo, or the built-in mark.
function Logo({ brand }: { brand: Brand }) {
  const src = brand.icon ?? brand.logo
  if (src) {
    return <img src={src} alt="" className="size-8 shrink-0 rounded-lg object-contain" />
  }
  return (
    <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-xs font-bold text-primary-foreground">
      HA
    </span>
  )
}

// Wordmark is the platform's logo when it has one; the name stays for
// screen readers.
function Wordmark({ brand }: { brand: Brand }) {
  if (brand.logo && brand.icon) {
    return (
      <span className="flex min-w-0 flex-col">
        <img src={brand.logo} alt="HAMSP admin" className="h-6 max-w-40 object-contain object-left" />
        <span className="truncate text-xs text-muted-foreground">Platform administration</span>
      </span>
    )
  }
  return (
    <span className="flex min-w-0 flex-col">
      <span className="truncate text-sm font-semibold">HAMSP admin</span>
      <span className="truncate text-xs text-muted-foreground">Platform administration</span>
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

// Ctrl+B (Cmd+B on a Mac) toggles the sidebar, except while typing, where it
// may mean bold.
function isToggleKey(event: KeyboardEvent) {
  const target = event.target as HTMLElement | null
  const typing = target?.closest('input, textarea, select, [contenteditable="true"]')
  return (event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey && event.key.toLowerCase() === 'b' && !typing
}

// MobileMenu replaces the sidebar on small screens: a floating button opens
// a sheet with the same links and the user menu.
function MobileMenu({ me, brand }: { me: Me; brand: Brand }) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogPrimitive.Trigger asChild>
        <Button size="icon-lg" aria-label="Open menu" className="fixed bottom-4 left-4 z-40 size-12 rounded-full shadow-lg md:hidden">
          <Menu />
        </Button>
      </DialogPrimitive.Trigger>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className="fixed inset-y-0 left-0 z-50 flex w-72 max-w-[85vw] flex-col gap-6 border-r bg-sidebar px-3 py-4 text-sidebar-foreground outline-none data-open:animate-in data-open:slide-in-from-left data-closed:animate-out data-closed:slide-out-to-left"
        >
          <DialogTitle className="sr-only">Menu</DialogTitle>
          <Link to="/" onClick={() => setOpen(false)} className="flex items-center gap-2.5 px-1.5">
            <Logo brand={brand} />
            <Wordmark brand={brand} />
          </Link>
          <nav aria-label="Menu" className="flex flex-col gap-1">
            <NavLinks className="px-2.5 py-2" onNavigate={() => setOpen(false)} />
          </nav>
          <div className="mt-auto border-t pt-3">
            <UserMenu me={me} side="top" />
          </div>
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  )
}

export function RootLayout() {
  const { data: me } = useSuspenseQuery(meQuery)
  const [collapsed, setCollapsed] = useState(storedCollapsed)
  const brand = useBrand()
  const toggle = useCallback(
    () =>
      setCollapsed((was) => {
        storeCollapsed(!was)
        return !was
      }),
    [],
  )

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (isToggleKey(event)) {
        event.preventDefault()
        toggle()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [toggle])

  return (
    <div className="flex min-h-svh bg-muted/40">
      <aside
        className={`sticky top-0 z-30 hidden h-svh shrink-0 flex-col gap-6 border-r bg-sidebar px-3 py-4 text-sidebar-foreground transition-[width] duration-200 md:flex ${collapsed ? 'w-16' : 'w-64'}`}
      >
        <Link to="/" className={`flex items-center gap-2.5 ${collapsed ? 'justify-center' : 'px-1.5'}`}>
          <Logo brand={brand} />
          {collapsed ? <span className="sr-only">HAMSP admin</span> : <Wordmark brand={brand} />}
        </Link>
        {/* The handle sits on the sidebar's border, level with the logo. */}
        <Button
          variant="outline"
          size="icon-xs"
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-expanded={!collapsed}
          aria-keyshortcuts="Control+B Meta+B"
          title={`${collapsed ? 'Expand' : 'Collapse'} sidebar (Ctrl+B)`}
          onClick={toggle}
          className="absolute top-6 -right-3 rounded-full bg-background text-muted-foreground shadow-xs hover:text-foreground"
        >
          {collapsed ? <ChevronRight /> : <ChevronLeft />}
        </Button>
        <nav aria-label="Main" className="flex flex-col gap-1">
          {!collapsed && (
            <span className="px-2.5 pb-1 text-xs font-medium tracking-wide text-muted-foreground uppercase">Manage</span>
          )}
          <NavLinks collapsed={collapsed} className={collapsed ? 'justify-center px-0 py-2' : 'px-2.5 py-2'} />
        </nav>
        <div className="mt-auto border-t pt-3">
          <UserMenu me={me} collapsed={collapsed} />
        </div>
      </aside>

      <main className="mx-auto w-full max-w-7xl min-w-0 px-4 pt-6 pb-24 md:px-8 md:py-8">
        <Outlet />
      </main>
      <MobileMenu me={me} brand={brand} />
      <Toaster position="bottom-right" />
    </div>
  )
}
