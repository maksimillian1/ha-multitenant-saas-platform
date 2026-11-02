import { screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { setTheme } from '@/lib/theme'
import { mockApi } from '@/test/api'
import { signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'

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

  it('switches between the light, dark and system themes', async () => {
    mockApi({ ...signedIn, ...overview })
    const { user } = renderApp('/')

    await user.click(await screen.findByRole('button', { name: 'Theme: System' }))
    await user.click(screen.getByRole('menuitemradio', { name: 'Dark' }))
    expect(document.documentElement).toHaveClass('dark')
    expect(localStorage.getItem('admin.theme')).toBe('dark')

    await user.click(screen.getByRole('button', { name: 'Theme: Dark' }))
    await user.click(screen.getByRole('menuitemradio', { name: 'Light' }))
    expect(document.documentElement).not.toHaveClass('dark')

    await user.click(screen.getByRole('button', { name: 'Theme: Light' }))
    await user.click(screen.getByRole('menuitemradio', { name: 'System' }))
    expect(localStorage.getItem('admin.theme')).toBeNull()
  })
})
