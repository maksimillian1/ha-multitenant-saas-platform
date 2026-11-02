import type { UseQueryResult } from '@tanstack/react-query'
import { CircleAlert } from 'lucide-react'
import type { ReactNode } from 'react'
import { message } from '@/api/client'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'

// PageHeader is the title row of a page, with its actions on the right.
export function PageHeader({
  title,
  description,
  badges,
  actions,
}: {
  title: ReactNode
  description?: ReactNode
  badges?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="truncate text-2xl font-semibold tracking-tight">{title}</h1>
          {badges}
        </div>
        {description && <p className="mt-1 text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap gap-2">{actions}</div>}
    </div>
  )
}

// DetailList shows label and value pairs.
export function DetailList({ items }: { items: Array<{ label: string; value: ReactNode }> }) {
  return (
    <dl className="grid grid-cols-[minmax(7rem,auto)_1fr] gap-x-4 gap-y-3 text-sm">
      {items.map(({ label, value }) => (
        <div key={label} className="contents">
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="min-w-0 break-words">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

export function Mono({ children }: { children: ReactNode }) {
  return <code className="font-mono text-xs break-all">{children}</code>
}

// QueryView renders a query's data, or a skeleton while it loads and an
// error with a retry button when it fails.
export function QueryView<T>({
  query,
  children,
  skeleton = <Skeleton className="h-24" />,
}: {
  query: UseQueryResult<T>
  children: (data: T) => ReactNode
  skeleton?: ReactNode
}) {
  if (query.isPending) {
    return skeleton
  }
  if (query.isError) {
    return (
      <Alert variant="destructive">
        <CircleAlert />
        <AlertTitle>Could not load</AlertTitle>
        <AlertDescription>
          <p>{message(query.error)}</p>
          <Button variant="outline" size="sm" className="mt-2" onClick={() => void query.refetch()}>
            Try again
          </Button>
        </AlertDescription>
      </Alert>
    )
  }
  return children(query.data)
}
