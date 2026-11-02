import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { User } from '@/api/queries'
import { apiError, mockApi, noContent } from '@/test/api'
import {
  admin,
  ann,
  ci,
  grantAdmin,
  grantAnn,
  list,
  platformAdmin,
  platformApi,
  signedIn,
} from '@/test/fixtures'
import { renderApp } from '@/test/render'

function answers(user: User, grants = [grantAnn]) {
  return {
    ...signedIn,
    [`GET /api/users/${user.id}`]: user,
    [`GET /api/users/${user.id}/authorizations`]: list(grants.filter((g) => g.user_id === user.id)),
  }
}

describe('user page', () => {
  it('shows the profile and organization of a human user', async () => {
    mockApi(answers(ann))
    renderApp('/users/ann')

    expect(await screen.findByRole('heading', { name: 'Ann Lee' })).toBeInTheDocument()
    expect(screen.getByText('ann@tenant-a.zitadel.localtest.me', { selector: 'p' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'tenant-a' })).toHaveAttribute('href', '/orgs/org-a')
    expect(screen.getByText('ann@example.test')).toBeInTheDocument()
    expect(screen.getByText('Verified')).toBeInTheDocument()
    expect(screen.queryByText('Client secret')).not.toBeInTheDocument()
  })

  it('saves only the profile fields that changed', async () => {
    const api = mockApi({ ...answers(ann), 'PATCH /api/users/ann': noContent() })
    const { user } = renderApp('/users/ann')
    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    const dialog = await screen.findByRole('dialog', { name: 'Edit user' })

    await user.clear(within(dialog).getByLabelText('Given name'))
    await user.type(within(dialog).getByLabelText('Given name'), 'Anna')
    await user.clear(within(dialog).getByLabelText('Email'))
    await user.type(within(dialog).getByLabelText('Email'), 'anna@example.test')
    await user.click(within(dialog).getByLabelText('The new address is verified'))
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(api.sent('PATCH', '/api/users/ann')[0]?.body).toEqual({
        human: { given_name: 'Anna', email: 'anna@example.test', email_verified: true },
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Edit user' })).not.toBeInTheDocument())
  })

  it('sets a password after both entries match', async () => {
    const api = mockApi({
      ...answers(ann),
      'POST /api/users/ann/password': apiError(400, 'Password must contain a symbol (COMMA-ZBlk2)'),
    })
    const { user } = renderApp('/users/ann')
    await user.click(await screen.findByRole('button', { name: 'Set password' }))
    const dialog = await screen.findByRole('dialog', { name: 'Set password' })

    await user.type(within(dialog).getByLabelText('New password'), 'Secret1')
    await user.type(within(dialog).getByLabelText('Repeat the password'), 'Secret2')
    await user.click(within(dialog).getByRole('button', { name: 'Set password' }))
    expect(await within(dialog).findByText('The passwords differ')).toBeInTheDocument()
    expect(api.sent('POST', '/api/users/ann/password')).toHaveLength(0)

    await user.clear(within(dialog).getByLabelText('Repeat the password'))
    await user.type(within(dialog).getByLabelText('Repeat the password'), 'Secret1')
    await user.click(within(dialog).getByRole('button', { name: 'Set password' }))
    // Zitadel's password policy decides; its answer shows in the dialog.
    expect(await within(dialog).findByText('Password must contain a symbol (COMMA-ZBlk2)')).toBeInTheDocument()
    expect(api.sent('POST', '/api/users/ann/password')[0].body).toEqual({ password: 'Secret1', change_required: true })
  })

  it('locks, deactivates and deletes a user after confirmation', async () => {
    const api = mockApi({
      ...answers(ann),
      'POST /api/users/ann/lock': noContent(),
      'POST /api/users/ann/deactivate': noContent(),
      'DELETE /api/users/ann': noContent(),
      'GET /api/users': list([]),
      'GET /api/orgs': list([]),
    })
    const { user, router } = renderApp('/users/ann')
    await screen.findByRole('heading', { name: 'Ann Lee' })

    await user.click(screen.getByRole('button', { name: 'Lock' }))
    await user.click(within(await screen.findByRole('alertdialog', { name: 'Lock ann?' })).getByRole('button', { name: 'Lock' }))
    await waitFor(() => expect(api.sent('POST', '/api/users/ann/lock')).toHaveLength(1))
    expect(await screen.findByText('ann locked')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Deactivate' }))
    await user.click(
      within(await screen.findByRole('alertdialog', { name: 'Deactivate ann?' })).getByRole('button', { name: 'Deactivate' }),
    )
    await waitFor(() => expect(api.sent('POST', '/api/users/ann/deactivate')).toHaveLength(1))

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Delete ann?' })
    await user.type(within(confirm).getByLabelText('Type ann to confirm'), 'ann')
    await user.click(within(confirm).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/users'))
    expect(api.sent('DELETE', '/api/users/ann')).toHaveLength(1)
  })

  it('offers unlock and reactivate for a locked, inactive user', async () => {
    const api = mockApi({ ...answers({ ...ann, state: 'locked' }), 'POST /api/users/ann/unlock': noContent() })
    const { user } = renderApp('/users/ann')
    await user.click(await screen.findByRole('button', { name: 'Unlock' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Unlock' }))
    await waitFor(() => expect(api.sent('POST', '/api/users/ann/unlock')).toHaveLength(1))

    mockApi(answers({ ...ann, state: 'inactive' }))
    renderApp('/users/ann')
    expect(await screen.findAllByRole('button', { name: 'Reactivate' })).not.toHaveLength(0)
  })

  it('does not let the admin lock, deactivate or delete themselves', async () => {
    mockApi(answers(admin, [grantAdmin]))
    renderApp('/users/admin')
    await screen.findByRole('heading', { name: 'Super Admin' })

    expect(screen.getByText('You')).toBeInTheDocument()
    for (const name of ['Lock', 'Deactivate', 'Delete']) {
      expect(screen.getByRole('button', { name })).toBeDisabled()
    }
    // Nor change their own role assignments.
    expect(await screen.findByRole('button', { name: 'Edit roles in platform-admin' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Remove the role assignment in platform-admin' })).toBeDisabled()
  })

  it('generates a client secret for a machine user and shows it once', async () => {
    const api = mockApi({
      ...answers(ci),
      'POST /api/users/ci/secret': { client_id: 'tenant-a-client', client_secret: 's3cr3t-value' },
      'DELETE /api/users/ci/secret': noContent(),
    })
    const { user } = renderApp('/users/ci')
    expect(await screen.findByText(/has a client secret/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Set password' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Generate new secret' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Replace the client secret?' })
    await user.click(within(confirm).getByRole('button', { name: 'Generate' }))

    const dialog = await screen.findByRole('dialog', { name: 'Client credentials' })
    expect(within(dialog).getByLabelText('Client ID')).toHaveValue('tenant-a-client')
    expect(within(dialog).getByLabelText('Client secret')).toHaveValue('s3cr3t-value')
    expect(within(dialog).getByText(/not shown again/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Done' }))
    await waitFor(() => expect(screen.queryByDisplayValue('s3cr3t-value')).not.toBeInTheDocument())
    expect(api.sent('POST', '/api/users/ci/secret')).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: 'Remove secret' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Remove secret' }))
    await waitFor(() => expect(api.sent('DELETE', '/api/users/ci/secret')).toHaveLength(1))
  })
})

describe('role assignments', () => {
  it("lists the user's role assignments", async () => {
    mockApi(answers(ann))
    renderApp('/users/ann')
    const row = (await screen.findByRole('link', { name: 'platform-admin' })).closest('tr') as HTMLElement
    expect(within(row).getByText('viewer')).toBeInTheDocument()
    expect(within(row).getByText('ZITADEL')).toBeInTheDocument()
  })

  it('adds a role assignment: a project, then its roles', async () => {
    const api = mockApi({
      ...answers(ann, []),
      'GET /api/projects': list([platformAdmin, platformApi]),
      'GET /api/projects/p-admin/roles': list([
        { key: 'super-admin', display_name: 'super-admin', group: '' },
        { key: 'viewer', display_name: 'Viewer', group: 'read' },
      ]),
      'POST /api/authorizations': () => new Response(JSON.stringify({ id: 'grant-new' }), { status: 201 }),
    })
    const { user } = renderApp('/users/ann')
    expect(await screen.findByText('No role assignments.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Add' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add role assignment' })

    await waitFor(() => expect(within(dialog).getByRole('option', { name: 'platform-admin (ZITADEL)' })).toBeInTheDocument())
    await user.click(within(dialog).getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(within(dialog).getByLabelText('Project')).toHaveAttribute('aria-invalid', 'true'))
    expect(api.sent('POST', '/api/authorizations')).toHaveLength(0)

    await user.selectOptions(within(dialog).getByLabelText('Project'), 'platform-admin (ZITADEL)')
    await user.click(await within(dialog).findByLabelText('Viewer (viewer)'))
    await user.click(within(dialog).getByRole('button', { name: 'Add' }))

    await waitFor(() =>
      expect(api.sent('POST', '/api/authorizations')[0]?.body).toEqual({
        user_id: 'ann',
        project_id: 'p-admin',
        role_keys: ['viewer'],
      }),
    )
  })

  it('changes the roles of an assignment and removes it', async () => {
    const api = mockApi({
      ...answers(ann),
      'GET /api/projects/p-admin/roles': list([
        { key: 'super-admin', display_name: 'super-admin', group: '' },
        { key: 'viewer', display_name: 'viewer', group: '' },
      ]),
      'PATCH /api/authorizations/grant-ann': noContent(),
      'DELETE /api/authorizations/grant-ann': noContent(),
    })
    const { user } = renderApp('/users/ann')
    await user.click(await screen.findByRole('button', { name: 'Edit roles in platform-admin' }))
    const dialog = await screen.findByRole('dialog', { name: 'Roles in platform-admin' })

    expect(await within(dialog).findByLabelText('viewer')).toBeChecked()
    await user.click(within(dialog).getByLabelText('super-admin'))
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(api.sent('PATCH', '/api/authorizations/grant-ann')[0]?.body).toEqual({ role_keys: ['viewer', 'super-admin'] }),
    )

    await user.click(screen.getByRole('button', { name: 'Remove the role assignment in platform-admin' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    await waitFor(() => expect(api.sent('DELETE', '/api/authorizations/grant-ann')).toHaveLength(1))
  })
})
