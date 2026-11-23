import createClient from 'openapi-fetch'
import type { paths } from './schema'

// A failed tenant change also returns the tenant in the body.
export class ApiError extends Error {
  readonly status: number
  readonly body: unknown

  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.status = status
    this.body = body
  }
}

// X-Requested-With is the CSRF check: cross-site pages cannot send it without a refused preflight.
// fetch is looked up per call so tests can replace it.
export const api = createClient<paths>({
  baseUrl: window.location.origin,
  credentials: 'same-origin',
  headers: { 'X-Requested-With': 'XMLHttpRequest' },
  fetch: (request) => globalThis.fetch(request),
})

// Tests replace it.
export const navigation = {
  toLogin: () => window.location.assign('/auth/login'),
}

// The request never settles, so no error screen shows while the browser leaves for sign-in.
api.use({
  onResponse({ response }) {
    if (response.status === 401) {
      navigation.toLogin()
      return new Promise<Response>(() => {})
    }
  },
})

type Result<T> = { data?: T; error?: unknown; response: Response }

export async function unwrap<T>(call: Promise<Result<T>>): Promise<T> {
  const { data, error, response } = await call
  if (!response.ok) {
    throw new ApiError(response.status, errorMessage(error, response), error)
  }
  return data as T
}

function errorMessage(error: unknown, response: Response) {
  if (error && typeof error === 'object' && 'error' in error && typeof error.error === 'string') {
    return error.error
  }
  return `${response.status} ${response.statusText}`.trim()
}

export function message(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}
