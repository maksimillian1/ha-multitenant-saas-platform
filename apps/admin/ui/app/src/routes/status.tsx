import { Link, useRouter, type ErrorComponentProps } from '@tanstack/react-router'
import { CircleAlert, ShieldX } from 'lucide-react'
import { ApiError } from '@/api/client'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

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

export function RouteError({ error }: ErrorComponentProps) {
  const router = useRouter()

  if (error instanceof ApiError && error.status === 403) {
    return <AccessDenied />
  }
  return (
    <div className="mx-auto max-w-xl px-4 py-16">
      <Alert variant="destructive">
        <CircleAlert />
        <AlertTitle>Something went wrong</AlertTitle>
        <AlertDescription>
          <p>{error instanceof Error ? error.message : String(error)}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => router.invalidate()}>
            Try again
          </Button>
        </AlertDescription>
      </Alert>
    </div>
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
        <Link to="/">Back to the dashboard</Link>
      </Button>
    </div>
  )
}
