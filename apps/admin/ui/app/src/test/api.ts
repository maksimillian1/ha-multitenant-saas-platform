import { vi } from 'vitest'

// The API stub replaces fetch: each test lists the answers it needs as
// "METHOD /path": body (200 JSON), a Response, or a function of the request.
// Every request is recorded, so a test can check what the SPA sent.

export type Call = {
  method: string
  path: string
  query: URLSearchParams
  body: unknown
  headers: Headers
}

type Answer = unknown | Response | ((call: Call) => unknown | Response | Promise<unknown | Response>)

export function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

export function noContent() {
  return new Response(null, { status: 204 })
}

export function apiError(status: number, error: string) {
  return json({ error }, status)
}

export function mockApi(answers: Record<string, Answer>) {
  const routes = { ...answers }
  const calls: Call[] = []

  const fetchStub = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = input instanceof Request ? input : new Request(input, init)
    const url = new URL(request.url)
    const text = await request.clone().text()
    const call: Call = {
      method: request.method,
      path: url.pathname,
      query: url.searchParams,
      body: text ? JSON.parse(text) : undefined,
      headers: request.headers,
    }
    calls.push(call)
    const key = `${call.method} ${call.path}`
    if (!(key in routes)) {
      return apiError(500, `the test has no answer for ${key}`)
    }
    const answer = routes[key]
    const result = typeof answer === 'function' ? await answer(call) : answer
    // A Response can be read once; the same answer may serve several calls.
    return result instanceof Response ? result.clone() : json(result)
  })
  vi.stubGlobal('fetch', fetchStub)

  return {
    calls,
    // answer sets or replaces the answer for a route.
    answer(key: string, value: Answer) {
      routes[key] = value
    },
    // sent returns the requests to one route.
    sent(method: string, path: string) {
      return calls.filter((call) => call.method === method && call.path === path)
    },
    // lastQuery is the query string of the last request to a route.
    lastQuery(method: string, path: string) {
      const matching = calls.filter((call) => call.method === method && call.path === path)
      return Object.fromEntries(matching.at(-1)?.query ?? [])
    },
  }
}
