import { useSuspenseQuery } from '@tanstack/react-query'
import { Link, Outlet } from '@tanstack/react-router'
import { ShieldCheck } from 'lucide-react'
import { meQuery } from '@/api/queries'
import { UserMenu } from '@/components/user-menu'

export function RootLayout() {
  const { data: me } = useSuspenseQuery(meQuery)

  return (
    <div className="min-h-svh bg-muted/40">
      <header className="sticky top-0 z-10 border-b bg-background/95 backdrop-blur">
        <div className="mx-auto flex h-14 max-w-5xl items-center justify-between px-4">
          <Link to="/" className="flex items-center gap-2 font-semibold">
            <ShieldCheck className="size-5" />
            HAMSP admin
          </Link>
          <UserMenu me={me} />
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  )
}
