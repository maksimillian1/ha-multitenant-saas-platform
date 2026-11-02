import { useSuspenseQuery } from '@tanstack/react-query'
import { Link, Outlet } from '@tanstack/react-router'
import { Building2, FolderKanban, LayoutDashboard, ShieldCheck, Users } from 'lucide-react'
import { meQuery } from '@/api/queries'
import { Toaster } from '@/components/ui/sonner'
import { UserMenu } from '@/components/user-menu'

const nav = [
  { to: '/', label: 'Overview', icon: LayoutDashboard, exact: true },
  { to: '/orgs', label: 'Organizations', icon: Building2, exact: false },
  { to: '/users', label: 'Users', icon: Users, exact: false },
  { to: '/projects', label: 'Projects', icon: FolderKanban, exact: false },
] as const

function NavLinks({ className }: { className: string }) {
  return (
    <>
      {nav.map(({ to, label, icon: Icon, exact }) => (
        <Link
          key={to}
          to={to}
          activeOptions={{ exact, includeSearch: false }}
          className={`${className} flex items-center gap-2 rounded-lg px-2.5 py-1.5 text-sm whitespace-nowrap text-muted-foreground transition-colors hover:bg-muted hover:text-foreground data-[status=active]:bg-muted data-[status=active]:font-medium data-[status=active]:text-foreground`}
        >
          <Icon className="size-4" />
          {label}
        </Link>
      ))}
    </>
  )
}

export function RootLayout() {
  const { data: me } = useSuspenseQuery(meQuery)

  return (
    <div className="min-h-svh bg-muted/40">
      <header className="sticky top-0 z-20 border-b bg-background/95 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-7xl items-center justify-between px-4">
          <Link to="/" className="flex items-center gap-2 font-semibold">
            <ShieldCheck className="size-5" />
            HAMSP admin
          </Link>
          <UserMenu me={me} />
        </div>
        <nav aria-label="Sections" className="mx-auto flex max-w-7xl gap-1 overflow-x-auto px-4 pb-2 md:hidden">
          <NavLinks className="" />
        </nav>
      </header>
      <div className="mx-auto flex max-w-7xl gap-8 px-4 py-6 md:py-8">
        <aside className="hidden w-48 shrink-0 md:block">
          <nav aria-label="Main" className="sticky top-22 flex flex-col gap-1">
            <NavLinks className="" />
          </nav>
        </aside>
        <main className="min-w-0 flex-1">
          <Outlet />
        </main>
      </div>
      <Toaster position="bottom-right" />
    </div>
  )
}
