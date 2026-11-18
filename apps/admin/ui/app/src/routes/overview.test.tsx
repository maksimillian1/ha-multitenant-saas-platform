import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { apiError, mockApi } from '@/test/api'
import { me, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'

describe('overview', () => {
  it('shows the counts from Zitadel and links them to the lists', async () => {
    mockApi({
      ...signedIn,
      'GET /api/overview': { organizations: 3, human_users: 12, machine_users: 2, projects: 4 },
    })
    renderApp('/')

    expect(await screen.findByRole('heading', { name: 'Overview' })).toBeInTheDocument()
    const orgs = await screen.findByRole('link', { name: /Organizations\s*3/ })
    expect(orgs).toHaveAttribute('href', '/orgs')
    expect(screen.getByRole('link', { name: /Human users\s*12/ })).toHaveAttribute('href', '/users?type=human')
    expect(screen.getByRole('link', { name: /Machine users\s*2/ })).toHaveAttribute('href', '/users?type=machine')
    expect(screen.getByRole('link', { name: /Projects\s*4/ })).toHaveAttribute('href', '/projects')
  })

  it('shows the session and the roles of the signed-in admin', async () => {
    mockApi({ ...signedIn, 'GET /api/overview': { organizations: 0, human_users: 0, machine_users: 0, projects: 0 } })
    renderApp('/')

    const session = (await screen.findByText('Your session')).closest('[data-slot=card]') as HTMLElement
    expect(within(session).getByText(me.name)).toBeInTheDocument()
    expect(within(session).getByText(me.email)).toBeInTheDocument()
    expect(within(session).getByRole('link', { name: me.subject })).toHaveAttribute('href', `/users/${me.subject}`)
    expect(screen.getByText('Grants access')).toBeInTheDocument()
  })

  it('keeps the page when Zitadel is down and offers a retry', async () => {
    const api = mockApi({ ...signedIn, 'GET /api/overview': apiError(502, 'Zitadel is unavailable') })
    const { user } = renderApp('/')

    expect(await screen.findByText('Zitadel is unavailable')).toBeInTheDocument()
    expect(screen.getByText('Your session')).toBeInTheDocument()

    api.answer('GET /api/overview', { organizations: 1, human_users: 1, machine_users: 0, projects: 1 })
    await user.click(screen.getByRole('button', { name: 'Try again' }))
    expect(await screen.findByRole('link', { name: /Organizations\s*1/ })).toBeInTheDocument()
  })

  it('shows the access denied page to a user without the admin role', async () => {
    mockApi({ 'GET /api/me': apiError(403, 'access denied') })
    renderApp('/')

    expect(await screen.findByText('Access denied')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Sign out' }).closest('form')).toHaveAttribute('action', '/auth/logout')
  })
})
