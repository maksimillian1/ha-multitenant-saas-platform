import { Bot, User as UserIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import type { UserType } from '@/api/queries'

type Tone = 'good' | 'warn' | 'bad' | 'none'

const dots: Record<Tone, string> = {
  good: 'bg-emerald-500',
  warn: 'bg-amber-500',
  bad: 'bg-red-500',
  none: 'bg-muted-foreground/50',
}

const states: Record<string, { label: string; tone: Tone }> = {
  active: { label: 'Active', tone: 'good' },
  inactive: { label: 'Inactive', tone: 'warn' },
  locked: { label: 'Locked', tone: 'bad' },
  initial: { label: 'Initial', tone: 'none' },
  deleted: { label: 'Deleted', tone: 'bad' },
  removed: { label: 'Removed', tone: 'bad' },
  unspecified: { label: 'Unknown', tone: 'none' },
}

// StateBadge shows the state of an organization, user, project or role
// assignment.
export function StateBadge({ state }: { state: string }) {
  const { label, tone } = states[state] ?? states.unspecified
  return (
    <Badge variant="outline" className="gap-1.5" data-state={state}>
      <span className={`size-1.5 rounded-full ${dots[tone]}`} aria-hidden />
      {label}
    </Badge>
  )
}

export function UserTypeBadge({ type }: { type: UserType }) {
  return type === 'machine' ? (
    <Badge variant="secondary">
      <Bot /> Machine
    </Badge>
  ) : (
    <Badge variant="secondary">
      <UserIcon /> Human
    </Badge>
  )
}
