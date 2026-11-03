import { useSyncExternalStore } from 'react'

// The admin picks a light or dark theme, or follows the system; shadcn/ui
// switches on the .dark class. The choice lives in localStorage, which a
// private window or blocked site data may refuse.
export const themes = ['light', 'dark', 'system'] as const
export type Theme = (typeof themes)[number]

const key = 'admin.theme'
const listeners = new Set<() => void>()
const darkScheme = () => window.matchMedia('(prefers-color-scheme: dark)')

function stored(): Theme {
  try {
    const value = localStorage.getItem(key)
    return value === 'light' || value === 'dark' ? value : 'system'
  } catch {
    return 'system'
  }
}

let current = stored()

function apply() {
  const dark = current === 'dark' || (current === 'system' && darkScheme().matches)
  document.documentElement.classList.toggle('dark', dark)
}

// initTheme applies the stored choice before the first render and follows
// the system theme while the choice is "system".
export function initTheme() {
  apply()
  darkScheme().addEventListener('change', apply)
}

export function setTheme(theme: Theme) {
  current = theme
  try {
    if (theme === 'system') {
      localStorage.removeItem(key)
    } else {
      localStorage.setItem(key, theme)
    }
  } catch {
    // The choice still holds until the page reloads.
  }
  apply()
  listeners.forEach((listener) => listener())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function useTheme() {
  return useSyncExternalStore(subscribe, () => current)
}
