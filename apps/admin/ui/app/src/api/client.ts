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
export const api = createClient<paths>({ credentials: 'same-origin' })

// 401 means the session has ended: the Go server starts a new sign-in. The
// request never settles, so no error screen shows while the browser leaves.
api.use({
  onResponse({ response }) {
    if (response.status === 401) {
      window.location.assign('/auth/login')
      return new Promise<Response>(() => {})
    }
  },
})
