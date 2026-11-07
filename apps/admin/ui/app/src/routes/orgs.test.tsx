import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { apiError, mockApi, noContent } from '@/test/api'
import { admin, ann, list, orgA, orgB, orgZitadel, platformAdmin, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'

const orgs = list([orgZitadel, orgA, orgB], 45)

describe('organizations list', () => {
  it('lists organizations with their state and the default one marked', async () => {
    const api = mockApi({ ...signedIn, 'GET /api/orgs': orgs })
    renderApp('/orgs')

    const row = (await screen.findByRole('link', { name: 'tenant-b' })).closest('tr') as HTMLElement
    expect(within(row).getByText('Inactive')).toBeInTheDocument()
    expect(within(row).getByText('org-b')).toBeInTheDocument()
    const zitadel = screen.getByRole('link', { name: 'ZITADEL' }).closest('tr') as HTMLElement
    expect(within(zitadel).getByText('Default')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'tenant-a' })).toHaveAttribute('href', '/orgs/org-a')
    expect(screen.getByText('1–20 of 45')).toBeInTheDocument()
    expect(api.lastQuery('GET', '/api/orgs')).toEqual({ offset: '0', limit: '20' })
  })

  it('searches, filters by state and pages through the URL', async () => {
    const api = mockApi({ ...signedIn, 'GET /api/orgs': orgs })
    const { user, router } = renderApp('/orgs')
    await screen.findByRole('link', { name: 'tenant-a' })

    await user.type(screen.getByRole('searchbox', { name: 'Search organizations' }), 'tenant')
    await waitFor(() => expect(api.lastQuery('GET', '/api/orgs')).toMatchObject({ search: 'tenant', offset: '0' }))
    expect(router.state.location.search).toMatchObject({ q: 'tenant' })

    await user.selectOptions(screen.getByRole('combobox', { name: 'State' }), 'inactive')
    await waitFor(() => expect(api.lastQuery('GET', '/api/orgs')).toMatchObject({ search: 'tenant', state: 'inactive' }))

    await user.click(screen.getByRole('button', { name: /Next/ }))
    await waitFor(() => expect(api.lastQuery('GET', '/api/orgs')).toMatchObject({ offset: '20', state: 'inactive' }))
    expect(router.state.location.search).toEqual({ q: 'tenant', state: 'inactive', offset: 20 })
  })

  it('creates an organization and opens it', async () => {
    const created = { ...orgA, id: 'org-new', name: 'tenant-c' }
    const api = mockApi({
      ...signedIn,
      'GET /api/orgs': orgs,
      'POST /api/orgs': () => new Response(JSON.stringify({ id: 'org-new' }), { status: 201 }),
      'GET /api/orgs/org-new': created,
      'GET /api/users/admin': admin,
      'GET /api/users': list([]),
      'GET /api/projects': list([]),
    })
    const { user, router } = renderApp('/orgs')
    await user.click(await screen.findByRole('button', { name: 'New organization' }))
    const dialog = await screen.findByRole('dialog', { name: 'New organization' })

    // An empty name never reaches the API.
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('Required')).toBeInTheDocument()
    expect(api.sent('POST', '/api/orgs')).toHaveLength(0)

    await user.type(within(dialog).getByLabelText('Name'), '  tenant-c ')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))

    expect(await screen.findByRole('heading', { name: 'tenant-c' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/orgs/org-new')
    expect(api.sent('POST', '/api/orgs')[0].body).toEqual({ name: 'tenant-c' })
    expect(await screen.findByText('Organization tenant-c created')).toBeInTheDocument()
  })

  it("keeps the dialog open with Zitadel's answer when the name is taken", async () => {
    mockApi({
      ...signedIn,
      'GET /api/orgs': orgs,
      'POST /api/orgs': apiError(409, 'Organisation already exists (V2-HN3SD)'),
    })
    const { user } = renderApp('/orgs')
    await user.click(await screen.findByRole('button', { name: 'New organization' }))
    const dialog = await screen.findByRole('dialog', { name: 'New organization' })
    await user.type(within(dialog).getByLabelText('Name'), 'tenant-a')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))

    expect(await within(dialog).findByText('Organisation already exists (V2-HN3SD)')).toBeInTheDocument()
    expect(screen.getByRole('dialog', { name: 'New organization' })).toBeInTheDocument()
  })
})

describe('organization page', () => {
  function answers(org = orgA) {
    return {
      ...signedIn,
      [`GET /api/orgs/${org.id}`]: org,
      'GET /api/users/admin': admin,
      'GET /api/users': list([ann]),
      'GET /api/projects': list(org.id === orgZitadel.id ? [platformAdmin] : []),
    }
  }

  it('shows the details, users and projects of the organization', async () => {
    const api = mockApi(answers(orgZitadel))
    renderApp('/orgs/org-zitadel')

    expect(await screen.findByRole('heading', { name: 'ZITADEL' })).toBeInTheDocument()
    expect(screen.getByText('zitadel.zitadel.localtest.me')).toBeInTheDocument()
    expect(await screen.findByRole('link', { name: 'Ann Lee' })).toHaveAttribute('href', '/users/ann')
    expect(await screen.findByRole('link', { name: 'platform-admin' })).toHaveAttribute('href', '/projects/p-admin')
    expect(api.lastQuery('GET', '/api/users')).toMatchObject({ org_id: 'org-zitadel' })
    expect(api.lastQuery('GET', '/api/projects')).toMatchObject({ org_id: 'org-zitadel' })
  })

  it('renames, deactivates and activates an organization', async () => {
    const api = mockApi({
      ...answers(),
      'PATCH /api/orgs/org-a': noContent(),
      'POST /api/orgs/org-a/deactivate': noContent(),
    })
    const { user } = renderApp('/orgs/org-a')
    await screen.findByRole('heading', { name: 'tenant-a' })

    await user.click(screen.getByRole('button', { name: 'Rename' }))
    const dialog = await screen.findByRole('dialog', { name: 'Rename organization' })
    const name = within(dialog).getByLabelText('Name')
    expect(name).toHaveValue('tenant-a')
    await user.clear(name)
    await user.type(name, 'tenant-a-renamed')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.sent('PATCH', '/api/orgs/org-a')[0]?.body).toEqual({ name: 'tenant-a-renamed' }))

    await user.click(screen.getByRole('button', { name: 'Deactivate' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Deactivate tenant-a?' })
    await user.click(within(confirm).getByRole('button', { name: 'Deactivate' }))
    await waitFor(() => expect(api.sent('POST', '/api/orgs/org-a/deactivate')).toHaveLength(1))
    expect(api.sent('POST', '/api/orgs/org-a/deactivate')[0].headers.get('X-Requested-With')).toBe('XMLHttpRequest')
  })

  it('offers activate for an inactive organization', async () => {
    const api = mockApi({ ...answers(orgB), 'POST /api/orgs/org-b/activate': noContent() })
    const { user } = renderApp('/orgs/org-b')
    await user.click(await screen.findByRole('button', { name: 'Activate' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Activate' }))
    await waitFor(() => expect(api.sent('POST', '/api/orgs/org-b/activate')).toHaveLength(1))
  })

  it('deletes only after the name is typed, then goes back to the list', async () => {
    const api = mockApi({ ...answers(), 'DELETE /api/orgs/org-a': noContent(), 'GET /api/orgs': list([orgZitadel]) })
    const { user, router } = renderApp('/orgs/org-a')
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Delete tenant-a?' })
    expect(within(confirm).getByText(/data in other stores/)).toBeInTheDocument()

    const button = within(confirm).getByRole('button', { name: 'Delete' })
    expect(button).toBeDisabled()
    await user.type(within(confirm).getByLabelText('Type tenant-a to confirm'), 'tenant-')
    expect(button).toBeDisabled()
    await user.type(within(confirm).getByLabelText('Type tenant-a to confirm'), 'a')
    await user.click(button)

    await waitFor(() => expect(router.state.location.pathname).toBe('/orgs'))
    expect(api.sent('DELETE', '/api/orgs/org-a')).toHaveLength(1)
  })

  it("does not offer to delete the default organization or to lock the admin's own", async () => {
    mockApi(answers(orgZitadel))
    renderApp('/orgs/org-zitadel')
    await screen.findByRole('heading', { name: 'ZITADEL' })

    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Deactivate' })).toBeDisabled())
    expect(screen.getByRole('button', { name: 'Deactivate' })).toHaveAttribute('title', 'This is your own organization.')
  })

  it('shows not found for a deleted organization', async () => {
    mockApi({ ...signedIn, 'GET /api/orgs/gone': apiError(404, 'Organisation not found') })
    renderApp('/orgs/gone')
    expect(await screen.findByText('Not found')).toBeInTheDocument()
    expect(screen.getByText(/Organisation not found/)).toBeInTheDocument()
  })
})
