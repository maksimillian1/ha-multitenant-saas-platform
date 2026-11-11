import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { apiError, json, mockApi } from '@/test/api'
import { signedIn, tenantActive, tenantDeleted, tenantStuck, tenantSuspended } from '@/test/fixtures'
import { renderApp } from '@/test/render'

const tenants = { items: [tenantActive, tenantSuspended, tenantStuck] }

// Verifies: tenant-management AC-13
describe('tenants list', () => {
  it('lists tenants with their state and shows deleted ones on request', async () => {
    const api = mockApi({
      ...signedIn,
      'GET /api/tenants': (call: { query: URLSearchParams }) =>
        call.query.get('include_deleted') === 'true' ? { items: [...tenants.items, tenantDeleted] } : tenants,
    })
    const { user, router } = renderApp('/tenants')

    const row = (await screen.findByRole('link', { name: 'Initech' })).closest('tr') as HTMLElement
    expect(within(row).getByText('Provisioning')).toBeInTheDocument()
    expect(within(row).getByText('it@initech.test')).toBeInTheDocument()
    expect(within(row).getByText(tenantStuck.id)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Acme' })).toHaveAttribute('href', `/tenants/${tenantActive.id}`)
    expect(screen.queryByRole('link', { name: 'Deleted tenant' })).not.toBeInTheDocument()
    expect(api.lastQuery('GET', '/api/tenants')).toEqual({})

    await user.click(screen.getByRole('checkbox', { name: 'Show deleted tenants' }))
    expect(await screen.findByRole('link', { name: 'Deleted tenant' })).toBeInTheDocument()
    expect(api.lastQuery('GET', '/api/tenants')).toEqual({ include_deleted: 'true' })
    expect(router.state.location.search).toEqual({ deleted: true })
  })

  it('creates a tenant with its first admin and opens it', async () => {
    const created = { ...tenantActive, id: '4f5a6b7c-8d9e-4f0a-1b2c-3d4e5f6a7b8c', name: 'Umbrella', admin_email: 'it@umbrella.test' }
    const api = mockApi({
      ...signedIn,
      'GET /api/tenants': tenants,
      'POST /api/tenants': json(created, 201),
      [`GET /api/tenants/${created.id}`]: created,
    })
    const { user, router } = renderApp('/tenants')
    await user.click(await screen.findByRole('button', { name: 'New tenant' }))
    const dialog = await screen.findByRole('dialog', { name: 'New tenant' })

    // Invalid input never reaches the API.
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findAllByText('Required')).toHaveLength(2)
    await user.type(within(dialog).getByLabelText('Name'), '  Umbrella ')
    await user.type(within(dialog).getByLabelText("First admin's email"), 'not-an-email')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('Not an email address')).toBeInTheDocument()
    expect(api.sent('POST', '/api/tenants')).toHaveLength(0)

    await user.clear(within(dialog).getByLabelText("First admin's email"))
    await user.type(within(dialog).getByLabelText("First admin's email"), 'it@umbrella.test')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))

    expect(await screen.findByRole('heading', { name: 'Umbrella' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe(`/tenants/${created.id}`)
    expect(api.sent('POST', '/api/tenants')[0].body).toEqual({ name: 'Umbrella', admin_email: 'it@umbrella.test' })
    expect(api.sent('POST', '/api/tenants')[0].headers.get('X-Requested-With')).toBe('XMLHttpRequest')
  })

  it('keeps the dialog open when the name is taken', async () => {
    mockApi({ ...signedIn, 'GET /api/tenants': tenants, 'POST /api/tenants': apiError(409, 'a tenant with this name exists') })
    const { user } = renderApp('/tenants')
    await user.click(await screen.findByRole('button', { name: 'New tenant' }))
    const dialog = await screen.findByRole('dialog', { name: 'New tenant' })
    await user.type(within(dialog).getByLabelText('Name'), 'Acme')
    await user.type(within(dialog).getByLabelText("First admin's email"), 'x@acme.test')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))

    expect(await within(dialog).findByText('a tenant with this name exists')).toBeInTheDocument()
  })

  it('opens the tenant with its error and Retry when a step fails', async () => {
    mockApi({
      ...signedIn,
      'GET /api/tenants': tenants,
      'POST /api/tenants': json({ error: 'step admin: zitadel: 503: unavailable', tenant: tenantStuck }, 502),
      [`GET /api/tenants/${tenantStuck.id}`]: tenantStuck,
    })
    const { user, router } = renderApp('/tenants')
    await user.click(await screen.findByRole('button', { name: 'New tenant' }))
    const dialog = await screen.findByRole('dialog', { name: 'New tenant' })
    await user.type(within(dialog).getByLabelText('Name'), 'Initech')
    await user.type(within(dialog).getByLabelText("First admin's email"), 'it@initech.test')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))

    expect(await screen.findByRole('heading', { name: 'Initech' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe(`/tenants/${tenantStuck.id}`)
    expect(screen.getByText('Provisioning stopped')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(await screen.findByText(/Tenant Initech is not ready/)).toBeInTheDocument()
  })
})

// Verifies: tenant-management AC-13
describe('tenant page', () => {
  const at = (tenant: typeof tenantActive) => `/tenants/${tenant.id}`
  const apiAt = (tenant: typeof tenantActive) => `/api/tenants/${tenant.id}`

  it('shows the steps and the last error, and retries a stuck tenant', async () => {
    const ready = { ...tenantStuck, state: 'active' as const, parts: tenantActive.parts, last_error: undefined }
    const api = mockApi({
      ...signedIn,
      [`GET ${apiAt(tenantStuck)}`]: tenantStuck,
      [`POST ${apiAt(tenantStuck)}/retry`]: ready,
    })
    const { user } = renderApp(at(tenantStuck))
    await screen.findByRole('heading', { name: 'Initech' })

    const steps = screen.getByRole('list', { name: 'Steps' })
    expect(within(steps).getAllByLabelText('Done')).toHaveLength(1)
    expect(within(steps).getAllByLabelText('Not done')).toHaveLength(2)
    expect(screen.getByText('admin: zitadel: 503: unavailable')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Suspend' })).not.toBeInTheDocument()

    api.answer(`GET ${apiAt(tenantStuck)}`, ready)
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(api.sent('POST', `${apiAt(tenantStuck)}/retry`)).toHaveLength(1))
    expect(await screen.findByRole('button', { name: 'Suspend' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(screen.queryByText('Provisioning stopped')).not.toBeInTheDocument()
  })

  it('suspends an active tenant and does not offer to delete it', async () => {
    const api = mockApi({
      ...signedIn,
      [`GET ${apiAt(tenantActive)}`]: tenantActive,
      [`POST ${apiAt(tenantActive)}/suspend`]: { ...tenantActive, state: 'suspended' },
    })
    const { user } = renderApp(at(tenantActive))
    await screen.findByRole('heading', { name: 'Acme' })

    const remove = screen.getByRole('button', { name: 'Delete' })
    expect(remove).toBeDisabled()
    expect(remove).toHaveAttribute('title', 'Only a suspended tenant can be deleted. Suspend it first.')
    expect(screen.getByRole('link', { name: 'Open the organization' })).toHaveAttribute('href', `/orgs/${tenantActive.id}`)

    api.answer(`GET ${apiAt(tenantActive)}`, { ...tenantActive, state: 'suspended' })
    await user.click(screen.getByRole('button', { name: 'Suspend' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Suspend Acme?' })
    await user.click(within(confirm).getByRole('button', { name: 'Suspend' }))
    await waitFor(() => expect(api.sent('POST', `${apiAt(tenantActive)}/suspend`)).toHaveLength(1))
    expect(await screen.findByRole('button', { name: 'Resume' })).toBeInTheDocument()
  })

  it('resumes a suspended tenant', async () => {
    const api = mockApi({
      ...signedIn,
      [`GET ${apiAt(tenantSuspended)}`]: tenantSuspended,
      [`POST ${apiAt(tenantSuspended)}/resume`]: { ...tenantSuspended, state: 'active' },
    })
    const { user } = renderApp(at(tenantSuspended))
    await user.click(await screen.findByRole('button', { name: 'Resume' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Resume' }))
    await waitFor(() => expect(api.sent('POST', `${apiAt(tenantSuspended)}/resume`)).toHaveLength(1))
  })

  it('deletes a suspended tenant only after its name is typed', async () => {
    const api = mockApi({
      ...signedIn,
      [`GET ${apiAt(tenantSuspended)}`]: tenantSuspended,
      [`DELETE ${apiAt(tenantSuspended)}`]: { ...tenantDeleted, id: tenantSuspended.id },
      'GET /api/tenants': tenants,
    })
    const { user, router } = renderApp(at(tenantSuspended))
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Delete Globex?' })

    const button = within(confirm).getByRole('button', { name: 'Delete' })
    expect(button).toBeDisabled()
    await user.type(within(confirm).getByLabelText('Type Globex to confirm'), 'Globe')
    expect(button).toBeDisabled()
    await user.type(within(confirm).getByLabelText('Type Globex to confirm'), 'x')
    await user.click(button)

    await waitFor(() => expect(router.state.location.pathname).toBe('/tenants'))
    expect(api.sent('DELETE', apiAt(tenantSuspended))).toHaveLength(1)
  })

  it('shows the tenant as a failed deletion left it', async () => {
    const stuck = { ...tenantSuspended, state: 'deleting' as const, parts: ['organization' as const], last_error: 'namespace: kube: timeout' }
    mockApi({
      ...signedIn,
      [`GET ${apiAt(tenantSuspended)}`]: tenantSuspended,
      [`DELETE ${apiAt(tenantSuspended)}`]: json({ error: 'step namespace: kube: timeout', tenant: stuck }, 504),
    })
    const { user } = renderApp(at(tenantSuspended))
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    const confirm = await screen.findByRole('alertdialog')
    await user.type(within(confirm).getByLabelText('Type Globex to confirm'), 'Globex')
    await user.click(within(confirm).getByRole('button', { name: 'Delete' }))

    expect(await screen.findByText('Deletion stopped')).toBeInTheDocument()
    // The dialog stays open on a failure; behind it the page shows the tenant
    // as the failure left it.
    await user.click(within(confirm).getByRole('button', { name: 'Cancel' }))
    expect(screen.getByText('namespace: kube: timeout')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(await screen.findByText('step namespace: kube: timeout')).toBeInTheDocument()
  })

  it('shows a tombstone without actions', async () => {
    mockApi({ ...signedIn, [`GET ${apiAt(tenantDeleted)}`]: tenantDeleted })
    renderApp(at(tenantDeleted))
    expect(await screen.findByRole('heading', { name: 'Deleted tenant' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(screen.queryByRole('list', { name: 'Steps' })).not.toBeInTheDocument()
  })
})
