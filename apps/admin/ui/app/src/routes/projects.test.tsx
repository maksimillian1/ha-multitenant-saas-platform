import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { mockApi } from '@/test/api'
import { grantAdmin, grantAnn, list, platformAdmin, platformApi, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'

describe('projects', () => {
  it('lists projects with their owner and settings, and searches them', async () => {
    const api = mockApi({ ...signedIn, 'GET /api/projects': list([platformAdmin, platformApi]) })
    const { user } = renderApp('/projects')

    const row = (await screen.findByRole('link', { name: 'platform-admin' })).closest('tr') as HTMLElement
    expect(within(row).getByRole('link', { name: 'ZITADEL' })).toHaveAttribute('href', '/orgs/org-zitadel')
    expect(within(row).getByText('Roles in tokens')).toBeInTheDocument()
    expect(within(row).getByText('Role required')).toBeInTheDocument()

    await user.type(screen.getByRole('searchbox', { name: 'Search projects' }), 'admin')
    await waitFor(() => expect(api.lastQuery('GET', '/api/projects')).toMatchObject({ search: 'admin' }))
  })

  it("shows a project's roles and who holds them", async () => {
    mockApi({
      ...signedIn,
      'GET /api/projects/p-admin': platformAdmin,
      'GET /api/projects/p-admin/roles': list([
        { key: 'super-admin', display_name: 'Super admin', group: 'admin' },
        { key: 'viewer', display_name: '', group: '' },
      ]),
      'GET /api/projects/p-admin/authorizations': list([grantAdmin, grantAnn]),
    })
    renderApp('/projects/p-admin')

    expect(await screen.findByRole('heading', { name: 'platform-admin' })).toBeInTheDocument()
    const roles = (await screen.findByText('Super admin')).closest('table') as HTMLElement
    expect(within(roles).getByText('super-admin')).toBeInTheDocument()
    expect(within(roles).getByText('admin')).toBeInTheDocument()

    const holder = (await screen.findByRole('link', { name: 'Ann Lee' })).closest('tr') as HTMLElement
    expect(holder).toHaveTextContent('viewer')
    expect(screen.getByRole('link', { name: 'Super Admin' })).toHaveAttribute('href', '/users/admin')
  })
})
