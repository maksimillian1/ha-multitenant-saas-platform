import { describe, expect, it, vi } from 'vitest'
import { apiError, mockApi, noContent } from '@/test/api'
import { ApiError, api, navigation, unwrap } from './client'

describe('api client', () => {
  it('sends the CSRF header and the session cookie on every request', async () => {
    const stub = mockApi({ 'GET /api/me': {}, 'POST /api/users/ann/lock': noContent() })

    await unwrap(api.GET('/api/me'))
    await unwrap(api.POST('/api/users/{userId}/lock', { params: { path: { userId: 'ann' } } }))

    expect(stub.calls).toHaveLength(2)
    for (const call of stub.calls) {
      expect(call.headers.get('X-Requested-With')).toBe('XMLHttpRequest')
    }
    expect(stub.calls[1].path).toBe('/api/users/ann/lock')
    const fetchStub = vi.mocked(globalThis.fetch)
    expect((fetchStub.mock.calls[0][0] as Request).credentials).toBe('same-origin')
  })

  it("throws ApiError with the server's message", async () => {
    mockApi({ 'POST /api/orgs': apiError(409, 'Organisation already exists') })

    const error = await unwrap(api.POST('/api/orgs', { body: { name: 'tenant-a' } })).catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 409, message: 'Organisation already exists' })
  })

  it('sends the browser to the login when the session has ended', async () => {
    const toLogin = vi.spyOn(navigation, 'toLogin').mockImplementation(() => {})
    mockApi({ 'GET /api/me': apiError(401, 'not signed in') })

    const settled = vi.fn()
    void api.GET('/api/me').then(settled, settled)
    await vi.waitFor(() => expect(toLogin).toHaveBeenCalledOnce())
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(settled).not.toHaveBeenCalled()
  })
})
