import { LogOut } from 'lucide-react'
import { useRef } from 'react'
import type { Me } from '@/api/queries'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { initials } from '@/lib/format'

// UserMenu sits in the header on small screens and at the foot of the
// sidebar on wide ones; a collapsed sidebar shows only the avatar.
export function UserMenu({
  me,
  placement = 'header',
  collapsed = false,
}: {
  me: Me
  placement?: 'header' | 'sidebar'
  collapsed?: boolean
}) {
  // Sign-out is a POST form: the Go server drops the session cookie and sends
  // the browser on to Zitadel's end_session endpoint.
  const logoutForm = useRef<HTMLFormElement>(null)
  const sidebar = placement === 'sidebar'

  return (
    <>
      <form ref={logoutForm} method="post" action="/auth/logout" hidden />
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          {sidebar ? (
            <Button
              variant="ghost"
              aria-label={collapsed ? me.name : undefined}
              className={`h-auto w-full gap-2 py-2 ${collapsed ? 'justify-center px-0' : 'justify-start px-2'}`}
            >
              <Avatar className="size-8">
                <AvatarFallback className="text-xs">{initials(me.name)}</AvatarFallback>
              </Avatar>
              {!collapsed && (
                <span className="flex min-w-0 flex-col text-left">
                  <span className="truncate text-sm font-medium">{me.name}</span>
                  <span className="truncate text-xs font-normal text-muted-foreground">{me.email || 'no email'}</span>
                </span>
              )}
            </Button>
          ) : (
            <Button variant="ghost" className="h-9 gap-2 px-2">
              <Avatar className="size-7">
                <AvatarFallback className="text-xs">{initials(me.name)}</AvatarFallback>
              </Avatar>
              <span className="hidden text-sm sm:inline">{me.name}</span>
            </Button>
          )}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" side={sidebar ? 'right' : 'bottom'} className="w-56">
          <DropdownMenuLabel className="flex flex-col">
            <span>{me.name}</span>
            <span className="text-xs font-normal text-muted-foreground">{me.email || 'no email'}</span>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={() => logoutForm.current?.requestSubmit()}>
            <LogOut />
            Sign out
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  )
}
