import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createAppRouter, createQueryClient } from '@/router'

// renderApp renders the whole SPA at path, with a fresh cache and router.
export function renderApp(path: string) {
  const queryClient = createQueryClient()
  queryClient.setDefaultOptions({ queries: { retry: false, staleTime: 30_000 } })
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  const user = userEvent.setup()
  const view = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { ...view, user, router, queryClient }
}
