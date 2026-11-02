import { z } from 'zod'

// The rules mirror the API's (openapi.yaml); Zitadel checks the rest, such
// as password complexity and unique names, and the dialog shows its answer.

export const requiredText = (max = 200) =>
  z.string().trim().min(1, 'Required').max(max, `At most ${max} characters`)

export const optionalText = (max = 200) => z.string().trim().max(max, `At most ${max} characters`)

export const email = requiredText().refine((value) => value.includes('@'), 'Not an email address')

export const password = z.string().max(200, 'At most 200 characters')
