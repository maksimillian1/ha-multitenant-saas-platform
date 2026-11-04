const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const date = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' })
const relative = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

export function formatDateTime(value?: string) {
  return value ? dateTime.format(new Date(value)) : '—'
}

export function formatDate(value?: string) {
  return value ? date.format(new Date(value)) : '—'
}

// sessionEnds is "Nov 2, 2026, 3:00 PM (in 42 minutes)".
export function sessionEnds(expiresAt: string) {
  const at = new Date(expiresAt)
  const minutes = Math.round((at.getTime() - Date.now()) / 60_000)
  return `${dateTime.format(at)} (${relative.format(minutes, 'minute')})`
}

// initials are the first letters of the first two words of a name; words
// are runs of letters and digits, so "super-admin (test)" gives "SA", not "S(".
export function initials(name: string) {
  const letters = (name.match(/[\p{L}\p{N}]+/gu) ?? []).map((word) => [...word][0])
  return (letters.length ? letters.slice(0, 2).join('') : '?').toLocaleUpperCase()
}

export function plural(count: number, one: string, many = `${one}s`) {
  return `${count.toLocaleString()} ${count === 1 ? one : many}`
}
