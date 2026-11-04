import { LogOut, Monitor, Moon, Sun } from 'lucide-react'
import { useRef } from 'react'
import type { Me } from '@/api/queries'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { initials } from '@/lib/format'
import { setTheme, themes, useTheme, type Theme } from '@/lib/theme'

const themeOptions: Record<Theme, { label: string; icon: typeof Sun }> = {
  light: { label: 'Light', icon: Sun },
  dark: { label: 'Dark', icon: Moon },
  system: { label: 'System', icon: Monitor },
}

// UserMenu sits at the foot of the sidebar and of the mobile menu; a
// collapsed sidebar shows only the avatar. It holds the theme and sign-out.
export function UserMenu({
  me,
  collapsed = false,
  side = 'right',
}: {
  me: Me
  collapsed?: boolean
  side?: 'right' | 'top'
}) {
  // Sign-out is a POST form: the Go server drops the session cookie and sends
  // the browser on to Zitadel's end_session endpoint.
  const logoutForm = useRef<HTMLFormElement>(null)
  const theme = useTheme()

  return (
    <>
      <form ref={logoutForm} method="post" action="/auth/logout" hidden />
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
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
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" side={side} className="w-56">
          <DropdownMenuLabel className="flex flex-col">
            <span>{me.name}</span>
            <span className="text-xs font-normal text-muted-foreground">{me.email || 'no email'}</span>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">Theme</DropdownMenuLabel>
          <DropdownMenuRadioGroup value={theme} onValueChange={(value) => setTheme(value as Theme)}>
            {themes.map((value) => {
              const { label, icon: Icon } = themeOptions[value]
              return (
                <DropdownMenuRadioItem key={value} value={value}>
                  <Icon />
                  {label}
                </DropdownMenuRadioItem>
              )
            })}
          </DropdownMenuRadioGroup>
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
