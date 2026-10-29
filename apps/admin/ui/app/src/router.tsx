import { QueryClient } from '@tanstack/react-query'
import { createRootRouteWithContext, createRoute, createRouter } from '@tanstack/react-router'
import { ApiError } from '@/api/client'
import { meQuery } from '@/api/queries'
import { DashboardPage } from '@/routes/dashboard'
import { RootLayout } from '@/routes/root'
import { NotFound, RouteError, RoutePending } from '@/routes/status'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      // An answer of the API will not change on retry; a network error may.
      retry: (failures, error) => !(error instanceof ApiError) && failures < 2,
    },
  },
})

// Every page needs the signed-in admin (the header shows it), so the root
// route loads it before rendering.
const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  loader: ({ context }) => context.queryClient.ensureQueryData(meQuery),
  component: RootLayout,
  pendingComponent: RoutePending,
  errorComponent: RouteError,
  notFoundComponent: NotFound,
})

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: DashboardPage,
})

export const router = createRouter({
  routeTree: rootRoute.addChildren([dashboardRoute]),
  context: { queryClient },
  defaultPreload: 'intent',
  // The loader data lives in the TanStack Query cache.
  defaultPreloadStaleTime: 0,
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
