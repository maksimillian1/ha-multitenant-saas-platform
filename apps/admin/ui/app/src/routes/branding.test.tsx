import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MAX_ASSET_SIZE } from '@/api/queries'
import { apiError, mockApi, noContent } from '@/test/api'
import {
  admin,
  inheritedBranding,
  orgA,
  orgB,
  platformBranding,
  platformSettings,
  signedIn,
  tenantBBranding,
} from '@/test/fixtures'
import { renderApp } from '@/test/render'

const orgAnswers = {
  ...signedIn,
  'GET /api/users/admin': admin,
  'GET /api/orgs/org-a': orgA,
  'GET /api/orgs/org-b': orgB,
  'GET /api/orgs/org-a/branding': inheritedBranding,
  'GET /api/orgs/org-b/branding': tenantBBranding,
}

describe('organization branding', () => {
  it('shows the active branding, the draft and inheritance', async () => {
    mockApi(orgAnswers)
    const { user, router } = renderApp('/orgs/org-b')
    const tabs = await screen.findByRole('navigation', { name: 'Organization' })
    expect(within(tabs).getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page')
    await user.click(within(tabs).getByRole('link', { name: 'Branding' }))
    expect(router.state.location.search).toEqual({ tab: 'branding' })

    expect(await screen.findByText('Own branding')).toBeInTheDocument()
    expect(screen.getByText('Unpublished changes')).toBeInTheDocument()
    expect(screen.getByLabelText('Primary color (light)')).toHaveValue('#dc2626')
    expect(screen.getByTitle('Primary color (light)')).toHaveTextContent('#16a34a')
    expect(screen.getByRole('button', { name: 'Publish' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Reset to platform branding' })).toBeInTheDocument()
  })

  it('shows that an organization inherits the platform branding', async () => {
    mockApi(orgAnswers)
    renderApp('/orgs/org-a?tab=branding')

    expect(await screen.findByText('Inherits the platform branding')).toBeInTheDocument()
    expect(screen.getByLabelText('Primary color (light)')).toHaveValue(platformSettings.primary_color)
    expect(screen.queryByText('Unpublished changes')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Publish' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Reset to platform branding' })).not.toBeInTheDocument()
  })

  it('saves the draft', async () => {
    const api = mockApi({ ...orgAnswers, 'PUT /api/orgs/org-a/branding': noContent() })
    const { user } = renderApp('/orgs/org-a?tab=branding')
    const primary = await screen.findByLabelText('Primary color (light)')

    // A color the API would refuse never leaves the form.
    await user.clear(primary)
    await user.type(primary, '#fff')
    await user.click(screen.getByRole('button', { name: 'Save draft' }))
    expect(await screen.findByText('A color like #1d4ed8')).toBeInTheDocument()
    expect(api.sent('PUT', '/api/orgs/org-a/branding')).toHaveLength(0)

    await user.clear(primary)
    await user.type(primary, '#7c3aed')
    await user.selectOptions(screen.getByLabelText('Theme mode'), 'dark')
    await user.click(screen.getByLabelText('Disable watermark'))
    // Publishing waits for the save.
    expect(screen.getByRole('button', { name: 'Publish' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Save draft' }))

    await waitFor(() => expect(api.sent('PUT', '/api/orgs/org-a/branding')).toHaveLength(1))
    expect(api.sent('PUT', '/api/orgs/org-a/branding')[0].body).toEqual({
      ...platformSettings,
      primary_color: '#7c3aed',
      theme_mode: 'dark',
      disable_watermark: true,
    })
    expect(await screen.findByText('Draft saved')).toBeInTheDocument()
  })

  it('shows the error of a refused save', async () => {
    mockApi({ ...orgAnswers, 'PUT /api/orgs/org-b/branding': apiError(502, 'Zitadel did not answer') })
    const { user } = renderApp('/orgs/org-b?tab=branding')
    await user.click(await screen.findByRole('button', { name: 'Save draft' }))
    expect(await screen.findByText('Zitadel did not answer')).toBeInTheDocument()
  })

  it('uploads and removes a logo', async () => {
    const withLogo = {
      ...tenantBBranding,
      draft: { ...tenantBBranding.draft, assets: { ...tenantBBranding.draft.assets, logo: true } },
    }
    const api = mockApi({
      ...orgAnswers,
      'PUT /api/orgs/org-b/branding/assets/logo': noContent(),
      'DELETE /api/orgs/org-b/branding/assets/icon': noContent(),
    })
    const { user } = renderApp('/orgs/org-b?tab=branding')
    const upload = await screen.findByLabelText('Upload logo')

    // Larger than Zitadel accepts: refused before the upload.
    const big = new File([new Uint8Array(MAX_ASSET_SIZE + 1)], 'big.png', { type: 'image/png' })
    await user.upload(upload, big)
    expect(await screen.findByText('big.png is larger than 512 KiB.')).toBeInTheDocument()
    expect(api.sent('PUT', '/api/orgs/org-b/branding/assets/logo')).toHaveLength(0)

    api.answer('GET /api/orgs/org-b/branding', withLogo)
    await user.upload(upload, new File(['\x89PNG'], 'logo.png', { type: 'image/png' }))
    await waitFor(() => expect(api.sent('PUT', '/api/orgs/org-b/branding/assets/logo')).toHaveLength(1))
    const sent = api.sent('PUT', '/api/orgs/org-b/branding/assets/logo')[0]
    expect(sent.headers.get('Content-Type')).toMatch(/^multipart\/form-data; boundary=/)
    expect(sent.headers.get('X-Requested-With')).toBe('XMLHttpRequest')
    // jsdom's File loses its name in Node's FormData; the server names the
    // file by its content anyway.
    expect(sent.body).toContain('name="file"')
    expect(sent.body).toContain('Content-Type: image/png')
    expect(await screen.findByText('Logo uploaded to the draft')).toBeInTheDocument()

    // The draft now has a logo: the row and the previews show it.
    const thumbnail = await screen.findByRole('img', { name: 'Logo in the draft' })
    expect(thumbnail.getAttribute('src')).toMatch(/^\/api\/orgs\/org-b\/branding\/assets\/logo\?version=draft&v=\d+$/)
    expect(within(screen.getByRole('figure', { name: 'Login preview (light)' })).getByRole('img', { name: 'Logo' })).toBeInTheDocument()

    api.answer('GET /api/orgs/org-b/branding', {
      ...withLogo,
      draft: { ...withLogo.draft, assets: { ...withLogo.draft.assets, icon: true } },
    })
    api.answer('PUT /api/orgs/org-b/branding/assets/icon', noContent())
    await user.upload(screen.getByLabelText('Upload icon'), new File(['GIF89a'], 'icon.gif', { type: 'image/gif' }))
    await user.click(await screen.findByRole('button', { name: 'Remove icon' }))
    await waitFor(() => expect(api.sent('DELETE', '/api/orgs/org-b/branding/assets/icon')).toHaveLength(1))
    expect(await screen.findByText('Icon removed from the draft')).toBeInTheDocument()
  })

  it('previews the draft in light and dark', async () => {
    mockApi(orgAnswers)
    const { user } = renderApp('/orgs/org-a?tab=branding')
    const light = await screen.findByTestId('preview-light')
    const dark = screen.getByTestId('preview-dark')
    expect(light).toHaveStyle({ backgroundColor: 'rgb(250, 250, 250)', color: 'rgb(0, 0, 0)' })
    expect(dark).toHaveStyle({ backgroundColor: 'rgb(17, 24, 39)', color: 'rgb(255, 255, 255)' })
    expect(within(light).getByText('Next')).toHaveStyle({ backgroundColor: 'rgb(84, 105, 212)' })
    expect(within(light).getByText('Powered by ZITADEL')).toBeInTheDocument()
    expect(within(light).getByRole('img', { name: 'Logo' })).toHaveAttribute(
      'src',
      expect.stringMatching(/^\/api\/orgs\/org-a\/branding\/assets\/logo\?version=draft/),
    )

    // The preview follows the form before anything is saved.
    const darkBackground = screen.getByLabelText('Background color (dark)')
    await user.clear(darkBackground)
    await user.type(darkBackground, '#000080')
    await user.click(screen.getByLabelText('Disable watermark'))
    await user.click(screen.getByLabelText('Hide login name suffix'))
    expect(dark).toHaveStyle({ backgroundColor: 'rgb(0, 0, 128)' })
    expect(within(light).queryByText('Powered by ZITADEL')).not.toBeInTheDocument()
    expect(within(light).getByText('ann')).toBeInTheDocument()
  })

  it("previews the draft's icon, dark logo and font", async () => {
    const all = { logo: true, 'logo-dark': true, icon: true, 'icon-dark': true, font: true }
    mockApi({
      ...orgAnswers,
      'GET /api/orgs/org-b/branding': { ...tenantBBranding, draft: { ...tenantBBranding.draft, assets: all } },
    })
    renderApp('/orgs/org-b?tab=branding')
    const light = await screen.findByTestId('preview-light')
    const dark = screen.getByTestId('preview-dark')
    const src = (scope: HTMLElement, name: string) => within(scope).getByRole('img', { name }).getAttribute('src')

    // Light shows the logo and icon, dark their dark versions, all from the draft.
    expect(src(light, 'Logo')).toMatch(/\/branding\/assets\/logo\?version=draft/)
    expect(src(light, 'Icon')).toMatch(/\/branding\/assets\/icon\?version=draft/)
    expect(src(dark, 'Logo')).toMatch(/\/branding\/assets\/logo-dark\?version=draft/)
    expect(src(dark, 'Icon')).toMatch(/\/branding\/assets\/icon-dark\?version=draft/)

    // The draft's font is loaded from the BFF and used by both cards.
    const fontFace = screen.getByRole('figure', { name: 'Login preview (light)' }).querySelector('style')?.textContent ?? ''
    const family = fontFace.match(/font-family: "([^"]+)"/)?.[1]
    expect(fontFace).toMatch(/src: url\("\/api\/orgs\/org-b\/branding\/assets\/font\?version=draft&v=\d+"\)/)
    expect(family).toBeTruthy()
    expect(light.style.fontFamily).toContain(family)
    expect(dark.style.fontFamily).toContain(family)
  })

  it('shows a dark logo fallback and no font without those assets', async () => {
    mockApi(orgAnswers)
    renderApp('/orgs/org-a?tab=branding')
    const dark = await screen.findByTestId('preview-dark')
    // Without a dark logo the dark card shows the light one, as Zitadel does.
    expect(within(dark).getByRole('img', { name: 'Logo' }).getAttribute('src')).toMatch(/\/assets\/logo\?/)
    expect(within(dark).queryByRole('img', { name: 'Icon' })).not.toBeInTheDocument()
    expect(dark.style.fontFamily).toBe('')
  })

  it('publishes the draft after confirming', async () => {
    const api = mockApi({ ...orgAnswers, 'POST /api/orgs/org-b/branding/publish': noContent() })
    const { user } = renderApp('/orgs/org-b?tab=branding')
    await user.click(await screen.findByRole('button', { name: 'Publish' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Publish the branding of tenant-b?' })
    await user.click(within(confirm).getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(api.sent('POST', '/api/orgs/org-b/branding/publish')).toHaveLength(1))
    expect(await screen.findByText('Branding of tenant-b published')).toBeInTheDocument()
  })

  it('resets to the platform branding after confirming', async () => {
    const api = mockApi({ ...orgAnswers, 'DELETE /api/orgs/org-b/branding': noContent() })
    const { user } = renderApp('/orgs/org-b?tab=branding')
    await user.click(await screen.findByRole('button', { name: 'Reset to platform branding' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Reset the branding of tenant-b?' })

    await user.click(within(confirm).getByRole('button', { name: 'Cancel' }))
    expect(api.sent('DELETE', '/api/orgs/org-b/branding')).toHaveLength(0)

    api.answer('GET /api/orgs/org-b/branding', inheritedBranding)
    await user.click(screen.getByRole('button', { name: 'Reset to platform branding' }))
    await user.click(
      within(await screen.findByRole('alertdialog', { name: 'Reset the branding of tenant-b?' })).getByRole('button', {
        name: 'Reset',
      }),
    )
    await waitFor(() => expect(api.sent('DELETE', '/api/orgs/org-b/branding')).toHaveLength(1))
    expect(await screen.findByText('tenant-b uses the platform branding again')).toBeInTheDocument()
    expect(await screen.findByText('Inherits the platform branding')).toBeInTheDocument()
  })
})

describe('platform branding page', () => {
  it('edits and publishes the platform branding, without a reset', async () => {
    const changed = {
      ...platformBranding,
      draft: { ...platformBranding.draft, settings: { ...platformSettings, primary_color: '#7c3aed' } },
    }
    const api = mockApi({
      ...signedIn,
      'GET /api/platform/branding': changed,
      'PUT /api/platform/branding': noContent(),
      'POST /api/platform/branding/publish': noContent(),
      'DELETE /api/platform/branding/assets/logo': noContent(),
    })
    const { user } = renderApp('/')
    await user.click(within(await screen.findByRole('navigation', { name: 'Main' })).getByRole('link', { name: 'Branding' }))

    expect(await screen.findByRole('heading', { name: 'Platform branding' })).toBeInTheDocument()
    expect(await screen.findByLabelText('Primary color (light)')).toHaveValue('#7c3aed')
    expect(screen.getByText('Unpublished changes')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reset to platform branding' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Save draft' }))
    await waitFor(() => expect(api.sent('PUT', '/api/platform/branding')).toHaveLength(1))

    await user.click(screen.getByRole('button', { name: 'Remove logo' }))
    await waitFor(() => expect(api.sent('DELETE', '/api/platform/branding/assets/logo')).toHaveLength(1))

    await user.click(screen.getByRole('button', { name: 'Publish' }))
    await user.click(
      within(await screen.findByRole('alertdialog', { name: 'Publish the branding of the platform?' })).getByRole(
        'button',
        { name: 'Publish' },
      ),
    )
    await waitFor(() => expect(api.sent('POST', '/api/platform/branding/publish')).toHaveLength(1))
  })
})
