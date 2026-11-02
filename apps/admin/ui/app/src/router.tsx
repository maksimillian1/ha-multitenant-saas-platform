import { QueryClient } from '@tanstack/react-query'
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  type RouterHistory,
} from '@tanstack/react-router'
import { ApiError } from '@/api/client'
import { meQuery, orgQuery, projectQuery, userQuery } from '@/api/queries'
import { OrgPage } from '@/routes/org'
import { OrgsPage } from '@/routes/orgs'
import { OverviewPage } from '@/routes/overview'
import { ProjectPage } from '@/routes/project'
import { ProjectsPage } from '@/routes/projects'
import { RootLayout } from '@/routes/root'
import { NotFound, PageError, PagePending, RouteError, RoutePending } from '@/routes/status'
import { UserPage } from '@/routes/user'
import { UsersPage } from '@/routes/users'

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        // An answer of the API will not change on retry; a network error may.
        retry: (failures, error) => !(error instanceof ApiError) && failures < 2,
      },
    },
  })
}

// Search parameters of the list pages. Anything unexpected is dropped, so a
// hand-edited URL cannot send the API a value it rejects.
const text = (value: unknown) => (typeof value === 'string' && value.trim() ? value.trim() : undefined)
const offset = (value: unknown) => {
  const n = Number(value)
  return Number.isInteger(n) && n > 0 ? n : undefined
}
const oneOf =
  <T extends string>(...values: T[]) =>
  (value: unknown) =>
    values.find((v) => v === value)

export const orgStates = ['active', 'inactive'] as const
export const userTypes = ['human', 'machine'] as const
export const userStates = ['active', 'inactive', 'locked', 'initial'] as const
const orgState = oneOf(...orgStates)
const userType = oneOf(...userTypes)
const userState = oneOf(...userStates)

// Every search parameter is optional, so links need not name them.
export type OrgsSearch = { q?: string; state?: (typeof orgStates)[number]; offset?: number }
export type UsersSearch = {
  q?: string
  org?: string
  type?: (typeof userTypes)[number]
  state?: (typeof userStates)[number]
  offset?: number
}
export type ProjectsSearch = { q?: string; org?: string; offset?: number }

// Every page needs the signed-in admin (the header shows it), so the root
// route loads it before rendering.
const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  loader: ({ context }) => context.queryClient.ensureQueryData(meQuery),
  component: RootLayout,
  pendingComponent: RoutePending,
  errorComponent: RouteError,
  notFoundComponent: NotFound,
})

const overviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: OverviewPage,
})

const orgsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/orgs',
  validateSearch: (search: Record<string, unknown>): OrgsSearch => ({
    q: text(search.q),
    state: orgState(search.state),
    offset: offset(search.offset),
  }),
  component: OrgsPage,
})

const orgRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/orgs/$orgId',
  loader: ({ context, params }) => context.queryClient.ensureQueryData(orgQuery(params.orgId)),
  component: OrgPage,
})

const usersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/users',
  validateSearch: (search: Record<string, unknown>): UsersSearch => ({
    q: text(search.q),
    org: text(search.org),
    type: userType(search.type),
    state: userState(search.state),
    offset: offset(search.offset),
  }),
  component: UsersPage,
})

const userRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/users/$userId',
  loader: ({ context, params }) => context.queryClient.ensureQueryData(userQuery(params.userId)),
  component: UserPage,
})

const projectsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects',
  validateSearch: (search: Record<string, unknown>): ProjectsSearch => ({
    q: text(search.q),
    org: text(search.org),
    offset: offset(search.offset),
  }),
  component: ProjectsPage,
})

const projectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/projects/$projectId',
  loader: ({ context, params }) => context.queryClient.ensureQueryData(projectQuery(params.projectId)),
  component: ProjectPage,
})

export const routeTree = rootRoute.addChildren([
  overviewRoute,
  orgsRoute,
  orgRoute,
  usersRoute,
  userRoute,
  projectsRoute,
  projectRoute,
])

// createAppRouter builds the router; tests pass their own query client and
// a memory history.
export function createAppRouter(queryClient: QueryClient, history?: RouterHistory) {
  return createRouter({
    routeTree,
    history,
    context: { queryClient },
    defaultPreload: 'intent',
    // The loader data lives in the TanStack Query cache.
    defaultPreloadStaleTime: 0,
    // Pages render inside the layout; the root route has its own full-page
    // pending and error screens.
    defaultPendingComponent: PagePending,
    defaultErrorComponent: PageError,
  })
}

export const queryClient = createQueryClient()
export const router = createAppRouter(queryClient)

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
