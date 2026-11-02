import { LogOut } from 'lucide-react'
import { useRef } from 'react'
import type { components } from '@/api/schema'
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

type Me = components['schemas']['Me']

function initials(name: string) {
  const letters = name
    .split(/\s+/)
    .filter(Boolean)
    .map((part) => part[0])
  return (letters.length ? letters.slice(0, 2).join('') : '?').toUpperCase()
}

export function UserMenu({ me }: { me: Me }) {
  // Sign-out is a POST form: the Go server drops the session cookie and sends
  // the browser on to Zitadel's end_session endpoint.
  const logoutForm = useRef<HTMLFormElement>(null)

  return (
    <>
      <form ref={logoutForm} method="post" action="/auth/logout" hidden />
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" className="h-9 gap-2 px-2">
            <Avatar className="size-7">
              <AvatarFallback className="text-xs">{initials(me.name)}</AvatarFallback>
            </Avatar>
            <span className="hidden text-sm sm:inline">{me.name}</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-56">
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
