import createClient from 'openapi-fetch'
import type { paths } from './schema'

// ApiError is a non-2xx answer of the Go API, with its {"error": "..."} text.
export class ApiError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

// The session is an HttpOnly cookie of the Go server, so requests carry no
// token. Types come from apps/admin/openapi.yaml (npm run gen:api).
//
// X-Requested-With is the CSRF check of state-changing routes: a cross-site
// page cannot send it without a CORS preflight, which the server refuses.
// fetch is looked up on every call, so tests can replace it.
export const api = createClient<paths>({
  baseUrl: window.location.origin,
  credentials: 'same-origin',
  headers: { 'X-Requested-With': 'XMLHttpRequest' },
  fetch: (request) => globalThis.fetch(request),
})

// navigation leaves the SPA; tests replace it.
export const navigation = {
  toLogin: () => window.location.assign('/auth/login'),
}

// 401 means the session has ended: the Go server starts a new sign-in. The
// request never settles, so no error screen shows while the browser leaves.
api.use({
  onResponse({ response }) {
    if (response.status === 401) {
      navigation.toLogin()
      return new Promise<Response>(() => {})
    }
  },
})

type Result<T> = { data?: T; error?: unknown; response: Response }

// unwrap returns the data of a successful call, or throws ApiError with the
// server's message.
export async function unwrap<T>(call: Promise<Result<T>>): Promise<T> {
  const { data, error, response } = await call
  if (!response.ok) {
    throw new ApiError(response.status, errorMessage(error, response))
  }
  return data as T
}

function errorMessage(error: unknown, response: Response) {
  if (error && typeof error === 'object' && 'error' in error && typeof error.error === 'string') {
    return error.error
  }
  return `${response.status} ${response.statusText}`.trim()
}

// message is what the UI shows for any error.
export function message(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}
