import type { BrandingSettings } from '@/api/queries'

export type PreviewMode = 'light' | 'dark'

// PreviewAssets are the URLs of the draft's assets; a missing one is not set.
export type PreviewAssets = {
  logo?: string
  logoDark?: string
  icon?: string
  iconDark?: string
  font?: string
}

// The font is loaded once per URL under its own family name; the CSP allows
// fonts from the app's origin (default-src 'self').
function fontFamily(url: string) {
  let hash = 0
  for (const char of url) {
    hash = (hash * 31 + char.charCodeAt(0)) | 0
  }
  return `branding-preview-${(hash >>> 0).toString(36)}`
}

// LoginPreview is a sketch of Zitadel's login card with the draft's colors,
// logo, icon and font. It is close to the real login, not the same: the real
// login is checked after publishing.
export function LoginPreview({
  settings,
  assets,
  mode,
}: {
  settings: BrandingSettings
  assets: PreviewAssets
  mode: PreviewMode
}) {
  const dark = mode === 'dark'
  const colors = dark
    ? {
        primary: settings.primary_color_dark,
        background: settings.background_color_dark,
        warn: settings.warn_color_dark,
        font: settings.font_color_dark,
      }
    : {
        primary: settings.primary_color,
        background: settings.background_color,
        warn: settings.warn_color,
        font: settings.font_color,
      }
  // A dark logo falls back to the light one, as in Zitadel.
  const logo = (dark && assets.logoDark) || assets.logo
  const icon = (dark && assets.iconDark) || assets.icon
  const family = assets.font ? fontFamily(assets.font) : undefined
  const label = `Login preview (${mode})`

  return (
    <figure aria-label={label} className="flex flex-col gap-2">
      {assets.font && family && (
        <style>{`@font-face { font-family: "${family}"; src: url("${assets.font}"); font-display: swap; }`}</style>
      )}
      <figcaption className="text-xs font-medium text-muted-foreground">{dark ? 'Dark' : 'Light'}</figcaption>
      <div
        data-testid={`preview-${mode}`}
        className="flex min-h-72 flex-col items-center justify-center rounded-xl border p-6"
        style={{
          backgroundColor: colors.background,
          color: colors.font,
          fontFamily: family ? `"${family}", system-ui, sans-serif` : undefined,
        }}
      >
        <div className="flex w-full max-w-64 flex-col gap-4">
          <div className="flex items-center justify-center gap-2">
            {icon && <img src={icon} alt="Icon" className="size-6 object-contain" />}
            {logo ? (
              <img src={logo} alt="Logo" className="max-h-10 max-w-40 object-contain" />
            ) : (
              <span className="text-sm font-semibold opacity-70">No logo</span>
            )}
          </div>
          <div className="text-center">
            <div className="text-base font-semibold">Welcome back!</div>
            <div className="text-xs opacity-70">Enter your login data.</div>
          </div>
          <div className="flex flex-col gap-1">
            <span className="text-xs opacity-80">Login name</span>
            <div
              className="flex h-8 items-center rounded-md border px-2 text-xs"
              style={{ borderColor: colors.primary }}
            >
              ann{settings.hide_login_name_suffix ? '' : '@tenant.example'}
            </div>
            <span className="text-xs" style={{ color: colors.warn }}>
              User could not be found
            </span>
          </div>
          <div
            role="presentation"
            className="flex h-8 items-center justify-center rounded-md text-xs font-medium"
            style={{ backgroundColor: colors.primary, color: colors.background }}
          >
            Next
          </div>
          {!settings.disable_watermark && <div className="text-center text-[10px] opacity-60">Powered by ZITADEL</div>}
        </div>
      </div>
    </figure>
  )
}
