import type { Authorization, BrandingSettings, BrandingState, Me, Org, Project, User } from '@/api/queries'

// The instance after bootstrap-tenants.sh, as the API returns it.

export const me: Me = {
  subject: 'admin',
  name: 'Super Admin',
  email: 'super-admin@admin.localtest.me',
  roles: ['super-admin'],
  admin_role: 'super-admin',
  expires_at: new Date(Date.now() + 3_600_000).toISOString(),
}

const created = '2026-10-01T08:00:00Z'

export const orgZitadel: Org = {
  id: 'org-zitadel',
  name: 'ZITADEL',
  state: 'active',
  primary_domain: 'zitadel.zitadel.localtest.me',
  is_default: true,
  created_at: created,
}

export const orgA: Org = {
  id: 'org-a',
  name: 'tenant-a',
  state: 'active',
  primary_domain: 'tenant-a.zitadel.localtest.me',
  is_default: false,
  created_at: created,
}

export const orgB: Org = {
  id: 'org-b',
  name: 'tenant-b',
  state: 'inactive',
  primary_domain: 'tenant-b.zitadel.localtest.me',
  is_default: false,
  created_at: created,
}

const human = (fields: Partial<NonNullable<User['human']>> = {}): NonNullable<User['human']> => ({
  given_name: '',
  family_name: '',
  nick_name: '',
  display_name: '',
  email: '',
  email_verified: true,
  phone: '',
  phone_verified: false,
  password_change_required: false,
  ...fields,
})

export const admin: User = {
  id: 'admin',
  type: 'human',
  state: 'active',
  username: 'super-admin',
  display_name: 'Super Admin',
  login_names: ['super-admin@zitadel.zitadel.localtest.me'],
  preferred_login_name: 'super-admin@zitadel.zitadel.localtest.me',
  org_id: orgZitadel.id,
  org_name: orgZitadel.name,
  created_at: created,
  human: human({ given_name: 'Super', family_name: 'Admin', display_name: 'Super Admin', email: me.email }),
}

export const ann: User = {
  id: 'ann',
  type: 'human',
  state: 'active',
  username: 'ann',
  display_name: 'Ann Lee',
  login_names: ['ann@tenant-a.zitadel.localtest.me'],
  preferred_login_name: 'ann@tenant-a.zitadel.localtest.me',
  org_id: orgA.id,
  org_name: orgA.name,
  created_at: created,
  human: human({ given_name: 'Ann', family_name: 'Lee', display_name: 'Ann Lee', email: 'ann@example.test' }),
}

export const ci: User = {
  id: 'ci',
  type: 'machine',
  state: 'active',
  username: 'tenant-a-client',
  display_name: 'tenant-a API client',
  login_names: [],
  preferred_login_name: '',
  org_id: orgA.id,
  org_name: orgA.name,
  created_at: created,
  machine: { name: 'tenant-a API client', description: 'Calls the API', has_secret: true, access_token_type: 'jwt' },
}

export const platformAdmin: Project = {
  id: 'p-admin',
  name: 'platform-admin',
  org_id: orgZitadel.id,
  org_name: orgZitadel.name,
  state: 'active',
  role_assertion: true,
  authorization_required: true,
  access_required: false,
  created_at: created,
}

export const platformApi: Project = {
  id: 'p-api',
  name: 'platform-api',
  org_id: orgZitadel.id,
  org_name: orgZitadel.name,
  state: 'active',
  role_assertion: false,
  authorization_required: false,
  access_required: false,
}

const grant = (id: string, user: User, roles: string[]): Authorization => ({
  id,
  state: 'active',
  project_id: platformAdmin.id,
  project_name: platformAdmin.name,
  project_org_id: platformAdmin.org_id,
  org_id: orgZitadel.id,
  org_name: orgZitadel.name,
  user_id: user.id,
  user_login_name: user.preferred_login_name,
  user_display_name: user.display_name,
  user_org_id: user.org_id,
  roles: roles.map((key) => ({ key, display_name: key, group: '' })),
  created_at: created,
})

export const grantAdmin = grant('grant-admin', admin, ['super-admin'])
export const grantAnn = grant('grant-ann', ann, ['viewer'])

export const list = <T>(items: T[], total = items.length) => ({ items, total })

// signedIn are the answers every page needs.
export const signedIn = {
  'GET /api/me': me,
}

// Branding as the API returns it: the platform's, with a logo, and
// tenant-b's own, published green with a red draft.
export const platformSettings: BrandingSettings = {
  primary_color: '#5469d4',
  background_color: '#fafafa',
  warn_color: '#cd3d56',
  font_color: '#000000',
  primary_color_dark: '#a5b4fc',
  background_color_dark: '#111827',
  warn_color_dark: '#ff3b5b',
  font_color_dark: '#ffffff',
  hide_login_name_suffix: false,
  disable_watermark: false,
  theme_mode: 'auto',
}

const noAssets = { logo: false, 'logo-dark': false, icon: false, 'icon-dark': false, font: false }

export const platformBranding: BrandingState = {
  inherited: false,
  active: { settings: platformSettings, assets: { ...noAssets, logo: true }, changed_at: created },
  draft: { settings: platformSettings, assets: { ...noAssets, logo: true }, changed_at: created },
}

export const inheritedBranding: BrandingState = { ...platformBranding, inherited: true }

export const tenantBBranding: BrandingState = {
  inherited: false,
  active: { settings: { ...platformSettings, primary_color: '#16a34a' }, assets: noAssets, changed_at: created },
  draft: { settings: { ...platformSettings, primary_color: '#dc2626' }, assets: noAssets, changed_at: created },
}
