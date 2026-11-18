import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { mockApi } from '@/test/api'
import { admin, ann, ci, list, orgA, orgB, orgZitadel, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'

const created = (id: string) => () => new Response(JSON.stringify({ id }), { status: 201 })

describe('users list', () => {
  it('lists humans and machines of every organization', async () => {
    mockApi({ ...signedIn, 'GET /api/users': list([ann, admin, ci]), 'GET /api/orgs': list([orgZitadel, orgA]) })
    renderApp('/users')

    const annRow = (await screen.findByRole('link', { name: 'Ann Lee' })).closest('tr') as HTMLElement
    expect(within(annRow).getByText('Human')).toBeInTheDocument()
    expect(within(annRow).getByRole('link', { name: 'tenant-a' })).toHaveAttribute('href', '/orgs/org-a')
    expect(within(annRow).getByText('ann@example.test')).toBeInTheDocument()
    expect(within(annRow).getByText('Active')).toBeInTheDocument()

    const ciRow = screen.getByRole('link', { name: 'tenant-a API client' }).closest('tr') as HTMLElement
    expect(within(ciRow).getByText('Machine')).toBeInTheDocument()
    expect(within(ciRow).getByText('Calls the API')).toBeInTheDocument()
  })

  it('filters by search, organization, type and state', async () => {
    const api = mockApi({ ...signedIn, 'GET /api/users': list([ann]), 'GET /api/orgs': list([orgZitadel, orgA, orgB]) })
    const { user, router } = renderApp('/users')
    await screen.findByRole('link', { name: 'Ann Lee' })

    await user.type(screen.getByRole('searchbox', { name: 'Search users' }), 'ann')
    await waitFor(() => expect(api.lastQuery('GET', '/api/users')).toMatchObject({ search: 'ann' }))
    await user.selectOptions(screen.getByRole('combobox', { name: 'Organization' }), 'tenant-a')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Type' }), 'machine')
    await user.selectOptions(screen.getByRole('combobox', { name: 'State' }), 'locked')

    await waitFor(() =>
      expect(api.lastQuery('GET', '/api/users')).toEqual({
        search: 'ann',
        org_id: 'org-a',
        type: 'machine',
        state: 'locked',
        offset: '0',
        limit: '20',
      }),
    )
    expect(router.state.location.search).toEqual({ q: 'ann', org: 'org-a', type: 'machine', state: 'locked' })
  })

  it('reads the filters from the URL, as the overview links them', async () => {
    const api = mockApi({ ...signedIn, 'GET /api/users': list([ci]), 'GET /api/orgs': list([orgA]) })
    renderApp('/users?type=machine&state=bogus')
    await screen.findByRole('link', { name: 'tenant-a API client' })
    expect(api.lastQuery('GET', '/api/users')).toEqual({ type: 'machine', offset: '0', limit: '20' })
    expect(screen.getByRole('combobox', { name: 'Type' })).toHaveValue('machine')
  })

  it('creates a human user and opens them', async () => {
    const bo = { ...ann, id: 'bo', username: 'bo', display_name: 'Bo Ng' }
    const api = mockApi({
      ...signedIn,
      'GET /api/users': list([ann]),
      'GET /api/orgs': list([orgZitadel, orgA]),
      'POST /api/users/human': created('bo'),
      'GET /api/users/bo': bo,
      'GET /api/users/bo/authorizations': list([]),
    })
    const { user, router } = renderApp('/users')
    await user.click(await screen.findByRole('button', { name: 'New user' }))
    const dialog = await screen.findByRole('dialog', { name: 'New user' })

    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(within(dialog).getByLabelText('Organization')).toHaveAttribute('aria-invalid', 'true'))
    expect(within(dialog).getByLabelText('Email')).toHaveAttribute('aria-invalid', 'true')
    expect(api.sent('POST', '/api/users/human')).toHaveLength(0)

    await user.type(within(dialog).getByLabelText('Email'), 'not-an-email')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('Not an email address')).toBeInTheDocument()
    await user.clear(within(dialog).getByLabelText('Email'))

    await waitFor(() => expect(within(dialog).getByRole('option', { name: 'tenant-a' })).toBeInTheDocument())
    await user.selectOptions(within(dialog).getByLabelText('Organization'), 'tenant-a')
    await user.type(within(dialog).getByLabelText('Given name'), 'Bo')
    await user.type(within(dialog).getByLabelText('Family name'), 'Ng')
    await user.type(within(dialog).getByLabelText('Email'), 'bo@example.test')
    await user.type(within(dialog).getByLabelText('Initial password'), 'Secret1!')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))

    expect(await screen.findByRole('heading', { name: 'Bo Ng' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/users/bo')
    expect(api.sent('POST', '/api/users/human')[0].body).toEqual({
      org_id: 'org-a',
      given_name: 'Bo',
      family_name: 'Ng',
      email: 'bo@example.test',
      email_verified: true,
      password: 'Secret1!',
      password_change_required: true,
    })
  })

  it('creates a machine user with JWT access tokens by default', async () => {
    const api = mockApi({
      ...signedIn,
      'GET /api/users': list([]),
      'GET /api/orgs': list([orgA]),
      'POST /api/users/machine': created('m1'),
      'GET /api/users/m1': { ...ci, id: 'm1' },
      'GET /api/users/m1/authorizations': list([]),
    })
    const { user } = renderApp('/users?org=org-a')
    await user.click(await screen.findByRole('button', { name: 'New machine user' }))
    const dialog = await screen.findByRole('dialog', { name: 'New machine user' })

    // The organization filter of the list is the default.
    await waitFor(() => expect(within(dialog).getByLabelText('Organization')).toHaveValue('org-a'))
    await user.type(within(dialog).getByLabelText('Username'), 'ci-2')
    await user.type(within(dialog).getByLabelText('Name'), 'CI 2')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))

    await waitFor(() =>
      expect(api.sent('POST', '/api/users/machine')[0]?.body).toEqual({
        org_id: 'org-a',
        username: 'ci-2',
        name: 'CI 2',
        access_token_type: 'jwt',
      }),
    )
  })
})
