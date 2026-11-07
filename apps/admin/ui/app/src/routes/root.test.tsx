import { screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { setTheme } from '@/lib/theme'
import { apiError, mockApi } from '@/test/api'
import { me, platformBranding, platformSettings, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'

type User = ReturnType<typeof renderApp>['user']

// pickTheme picks a theme in the user menu at the foot of the sidebar.
async function pickTheme(user: User, theme: 'Light' | 'Dark' | 'System') {
  const sidebar = await screen.findByRole('complementary')
  await user.click(within(sidebar).getByRole('button', { name: new RegExp(me.name) }))
  await user.click(screen.getByRole('menuitemradio', { name: theme }))
}

const overview = { 'GET /api/overview': { organizations: 0, human_users: 0, machine_users: 0, projects: 0 } }

afterEach(() => {
  setTheme('system')
  localStorage.clear()
})

describe('layout', () => {
  it('collapses the sidebar to icons and keeps it collapsed after a reload', async () => {
    mockApi({ ...signedIn, ...overview })
    const { user, unmount } = renderApp('/')

    const sidebar = await screen.findByRole('navigation', { name: 'Main' })
    expect(within(sidebar).getByText('Manage')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Collapse sidebar' }))

    expect(within(sidebar).queryByText('Manage')).not.toBeInTheDocument()
    // The label stays for screen readers and becomes the tooltip.
    const orgs = within(sidebar).getByRole('link', { name: 'Organizations' })
    expect(orgs).toHaveAttribute('title', 'Organizations')
    expect(screen.getByRole('button', { name: 'Expand sidebar' })).toHaveAttribute('aria-expanded', 'false')

    unmount()
    renderApp('/')
    expect(await screen.findByRole('button', { name: 'Expand sidebar' })).toBeInTheDocument()
  })

  it('collapses the sidebar with Ctrl+B, but not while typing', async () => {
    mockApi({ ...signedIn, ...overview })
    const { user } = renderApp('/')

    await screen.findByRole('button', { name: 'Collapse sidebar' })
    await user.keyboard('{Control>}b{/Control}')
    expect(screen.getByRole('button', { name: 'Expand sidebar' })).toHaveAttribute('aria-keyshortcuts', 'Control+B Meta+B')
    await user.keyboard('{Meta>}b{/Meta}')
    expect(screen.getByRole('button', { name: 'Collapse sidebar' })).toBeInTheDocument()

    const field = document.createElement('input')
    document.body.append(field)
    field.focus()
    await user.keyboard('{Control>}b{/Control}')
    expect(screen.getByRole('button', { name: 'Collapse sidebar' })).toBeInTheDocument()
    field.remove()
  })

  it('has no header: the theme and sign-out are in the user menu', async () => {
    mockApi({ ...signedIn, ...overview })
    const { user } = renderApp('/')

    await screen.findByRole('heading', { name: 'Overview' })
    expect(screen.queryByRole('banner')).not.toBeInTheDocument()

    await pickTheme(user, 'Dark')
    expect(document.documentElement).toHaveClass('dark')
    expect(localStorage.getItem('admin.theme')).toBe('dark')

    await pickTheme(user, 'Light')
    expect(document.documentElement).not.toHaveClass('dark')

    const sidebar = screen.getByRole('complementary')
    await user.click(within(sidebar).getByRole('button', { name: new RegExp(me.name) }))
    expect(screen.getByRole('menuitemradio', { name: 'Light' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('menuitem', { name: 'Sign out' })).toBeInTheDocument()
    await user.click(screen.getByRole('menuitemradio', { name: 'System' }))
    expect(localStorage.getItem('admin.theme')).toBeNull()
  })

  it('opens the menu sheet from the floating button on small screens', async () => {
    mockApi({ ...signedIn, ...overview })
    const { user } = renderApp('/')

    await user.click(await screen.findByRole('button', { name: 'Open menu' }))
    const sheet = screen.getByRole('dialog', { name: 'Menu' })
    expect(within(sheet).getByRole('button', { name: new RegExp(me.name) })).toBeInTheDocument()

    await user.click(within(sheet).getByRole('link', { name: 'Overview' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Menu' })).not.toBeInTheDocument())
  })

  it('applies the platform branding', async () => {
    const assets = { logo: true, 'logo-dark': false, icon: true, 'icon-dark': true, font: false }
    mockApi({
      ...signedIn,
      ...overview,
      'GET /api/platform/branding': {
        ...platformBranding,
        active: {
          ...platformBranding.active,
          settings: { ...platformSettings, primary_color: '#facc15', primary_color_dark: '#1e3a8a' },
          assets,
        },
      },
    })
    const { user } = renderApp('/')

    await waitFor(() => expect(document.getElementById('platform-brand')).not.toBeNull())
    const css = document.getElementById('platform-brand')?.textContent
    // A light primary gets dark text, a dark one light text.
    expect(css).toContain(':root { --primary: #facc15; --primary-foreground: #0a0a0a;')
    expect(css).toContain('.dark { --primary: #1e3a8a; --primary-foreground: #fafafa;')

    const sidebar = screen.getByRole('complementary')
    expect(within(sidebar).getByRole('img', { name: 'HAMSP admin' })).toHaveAttribute(
      'src',
      expect.stringMatching(/^\/api\/platform\/branding\/assets\/logo\?version=active/),
    )
    const icon = () => sidebar.querySelector('img[alt=""]')?.getAttribute('src')
    expect(icon()).toMatch(/^\/api\/platform\/branding\/assets\/icon\?version=active/)

    // The dark theme takes the dark icon; there is no dark logo, so the logo stays.
    await pickTheme(user, 'Dark')
    expect(icon()).toMatch(/^\/api\/platform\/branding\/assets\/icon-dark\?/)
    expect(within(sidebar).getByRole('img', { name: 'HAMSP admin' }).getAttribute('src')).toMatch(/\/logo\?/)
  })

  it('falls back to the built-in look', async () => {
    for (const answer of [
      apiError(502, 'Zitadel refused the admin app\'s service account'),
      // Nothing usable set: no assets, and colors that are not #rrggbb.
      {
        ...platformBranding,
        active: {
          ...platformBranding.active,
          settings: { ...platformSettings, primary_color: 'red;}*{', primary_color_dark: '' },
          assets: { ...platformBranding.active.assets, logo: false },
        },
      },
    ]) {
      const api = mockApi({ ...signedIn, ...overview, 'GET /api/platform/branding': answer })
      const { unmount } = renderApp('/')

      // The pages keep working.
      expect(await screen.findByRole('heading', { name: 'Overview' })).toBeInTheDocument()
      await waitFor(() => expect(api.sent('GET', '/api/platform/branding')).toHaveLength(1))
      expect(document.getElementById('platform-brand')).toBeNull()
      expect(within(screen.getByRole('complementary')).getByText('HA')).toBeInTheDocument()
      unmount()
    }
  })
})
