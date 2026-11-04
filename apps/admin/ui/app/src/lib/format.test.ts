import { describe, expect, it } from 'vitest'
import { initials } from './format'

describe('initials', () => {
  it.each([
    ['Ann Lee', 'AL'],
    ['super-admin (test)', 'SA'],
    ['(test) viewer', 'TV'],
    ['regular_user', 'RU'],
    ['Ünal Çelik', 'ÜÇ'],
    ['Мария', 'М'],
    ['42 Robots', '4R'],
    ['   ', '?'],
    ['()', '?'],
  ])('%s → %s', (name, want) => {
    expect(initials(name)).toBe(want)
  })
})
