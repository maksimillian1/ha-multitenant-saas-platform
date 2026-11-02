import { Link, useRouter, type ErrorComponentProps } from '@tanstack/react-router'
import { CircleAlert, SearchX, ShieldX } from 'lucide-react'
import { ApiError } from '@/api/client'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

// RoutePending is the whole screen before the signed-in admin is known.
export function RoutePending() {
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 px-4 py-8">
      <Skeleton className="h-8 w-48" />
      <div className="grid gap-6 md:grid-cols-2">
        <Skeleton className="h-56" />
        <Skeleton className="h-56" />
      </div>
    </div>
  )
}

// PagePending fills the content area while a page loads.
export function PagePending() {
  return (
    <div className="flex flex-col gap-6" aria-busy="true">
      <Skeleton className="h-8 w-64" />
      <Skeleton className="h-40" />
      <Skeleton className="h-64" />
    </div>
  )
}

// RouteError is the whole screen when the signed-in admin cannot be loaded.
export function RouteError({ error }: ErrorComponentProps) {
  if (error instanceof ApiError && error.status === 403) {
    return <AccessDenied />
  }
  return (
    <div className="mx-auto max-w-xl px-4 py-16">
      <ErrorAlert error={error} />
    </div>
  )
}

// PageError replaces a page whose data cannot be loaded; the layout stays.
export function PageError({ error }: ErrorComponentProps) {
  if (error instanceof ApiError && error.status === 404) {
    return (
      <Card className="max-w-xl">
        <CardHeader>
          <SearchX className="mb-2 size-8 text-muted-foreground" />
          <CardTitle>Not found</CardTitle>
          <CardDescription>{error.message}. It may have been deleted.</CardDescription>
        </CardHeader>
        <CardFooter>
          <Button variant="outline" asChild>
            <Link to="/">Back to the overview</Link>
          </Button>
        </CardFooter>
      </Card>
    )
  }
  return <ErrorAlert error={error} />
}

function ErrorAlert({ error }: { error: unknown }) {
  const router = useRouter()
  return (
    <Alert variant="destructive" className="max-w-xl">
      <CircleAlert />
      <AlertTitle>Something went wrong</AlertTitle>
      <AlertDescription>
        <p>{error instanceof Error ? error.message : String(error)}</p>
        <Button variant="outline" size="sm" className="mt-3" onClick={() => router.invalidate()}>
          Try again
        </Button>
      </AlertDescription>
    </Alert>
  )
}

// The API answers 403 to a signed-in user without the admin role. Signing out
// lets them sign in with another account.
function AccessDenied() {
  return (
    <div className="flex min-h-svh items-center justify-center bg-muted/40 px-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <ShieldX className="mb-2 size-8 text-destructive" />
          <CardTitle>Access denied</CardTitle>
          <CardDescription>
            Your account does not have the role this app requires. Sign in with another account,
            or ask a platform admin for access.
          </CardDescription>
        </CardHeader>
        <CardFooter>
          <form method="post" action="/auth/logout">
            <Button type="submit">Sign out</Button>
          </form>
        </CardFooter>
      </Card>
    </div>
  )
}

export function NotFound() {
  return (
    <div className="flex flex-col items-start gap-3">
      <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
      <Button variant="outline" asChild>
        <Link to="/">Back to the overview</Link>
      </Button>
    </div>
  )
}
