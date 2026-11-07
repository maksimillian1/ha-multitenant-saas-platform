import { useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { assetUrl, platformBrandingQuery, type Branding } from '@/api/queries'
import { useTheme } from '@/lib/theme'

// The admin app takes its primary color, logo and icon from the published
// platform branding (HAMSP-37). Without it (Zitadel down, the call refused,
// nothing set) the app keeps its built-in look from index.css.

const hex = /^#[0-9a-fA-F]{6}$/

// foreground is black or white, whichever reads better on the color.
function foreground(color: string) {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(color.slice(i, i + 2), 16) / 255
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  const luminance = 0.2126 * r + 0.7152 * g + 0.0722 * b
  return luminance > 0.4 ? '#0a0a0a' : '#fafafa'
}

function variables(color: string) {
  const fg = foreground(color)
  return `--primary: ${color}; --primary-foreground: ${fg}; --ring: ${color}; --sidebar-primary: ${color}; --sidebar-primary-foreground: ${fg};`
}

// brandCss overrides the primary color of the light and the dark theme; a
// color that is not #rrggbb is left out, so nothing else can get into the
// style sheet.
export function brandCss(active: Branding | undefined) {
  if (!active) {
    return ''
  }
  const { primary_color: light, primary_color_dark: dark } = active.settings
  return [hex.test(light) ? `:root { ${variables(light)} }` : '', hex.test(dark) ? `.dark { ${variables(dark)} }` : '']
    .filter(Boolean)
    .join('\n')
}

const styleId = 'platform-brand'

function isDark(theme: string) {
  return theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
}

export type Brand = { logo?: string; icon?: string }

// useBrand applies the platform branding to the page and returns the URLs of
// its logo and icon for the current theme.
export function useBrand(): Brand {
  const { data, dataUpdatedAt } = useQuery(platformBrandingQuery)
  const theme = useTheme()
  const active = data?.active
  const css = brandCss(active)

  useEffect(() => {
    if (!css) {
      return
    }
    const style = document.createElement('style')
    style.id = styleId
    style.textContent = css
    document.head.append(style)
    return () => style.remove()
  }, [css])

  const brand: Brand = {}
  if (active) {
    const dark = isDark(theme)
    const logo = dark && active.assets['logo-dark'] ? 'logo-dark' : active.assets.logo ? 'logo' : undefined
    const icon = dark && active.assets['icon-dark'] ? 'icon-dark' : active.assets.icon ? 'icon' : undefined
    if (logo) {
      brand.logo = assetUrl('platform', logo, 'active', dataUpdatedAt)
    }
    if (icon) {
      brand.icon = assetUrl('platform', icon, 'active', dataUpdatedAt)
    }
  }

  // The tab shows the platform icon too.
  useEffect(() => {
    const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
    if (!link || !brand.icon) {
      return
    }
    const previous = { href: link.getAttribute('href'), type: link.getAttribute('type') }
    link.href = brand.icon
    link.removeAttribute('type')
    return () => {
      link.setAttribute('href', previous.href ?? '/favicon.svg')
      if (previous.type) {
        link.setAttribute('type', previous.type)
      }
    }
  }, [brand.icon])

  return brand
}
