import { keepPreviousData, queryOptions, useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api, ApiError, message, unwrap } from './client'
import type { components, operations } from './schema'

export type Schemas = components['schemas']
export type Me = Schemas['Me']
export type Org = Schemas['Org']
export type OrgState = Schemas['OrgState']
export type User = Schemas['User']
export type UserState = Schemas['UserState']
export type UserType = Schemas['UserType']
export type Authorization = Schemas['Authorization']
export type Project = Schemas['Project']
export type ProjectRole = Schemas['ProjectRole']
export type Tenant = Schemas['Tenant']
export type TenantState = Schemas['TenantState']
export type TenantPart = Schemas['TenantPart']

export type OrgListParams = NonNullable<operations['listOrgs']['parameters']['query']>
export type UserListParams = NonNullable<operations['listUsers']['parameters']['query']>
export type ProjectListParams = NonNullable<operations['listProjects']['parameters']['query']>

// The API allows up to 100.
export const PAGE_SIZE = 20
export const MAX_OPTIONS = 100

// Query keys start with the resource, so a change can refresh what it touches.
export const meQuery = queryOptions({
  queryKey: ['me'],
  queryFn: ({ signal }) => unwrap(api.GET('/api/me', { signal })),
})

export const overviewQuery = queryOptions({
  queryKey: ['overview'],
  queryFn: ({ signal }) => unwrap(api.GET('/api/overview', { signal })),
})

export const orgsQuery = (query: OrgListParams) =>
  queryOptions({
    queryKey: ['orgs', 'list', query],
    queryFn: ({ signal }) => unwrap(api.GET('/api/orgs', { params: { query }, signal })),
    placeholderData: keepPreviousData,
  })

export const orgQuery = (orgId: string) =>
  queryOptions({
    queryKey: ['orgs', 'detail', orgId],
    queryFn: ({ signal }) => unwrap(api.GET('/api/orgs/{orgId}', { params: { path: { orgId } }, signal })),
  })

export const usersQuery = (query: UserListParams) =>
  queryOptions({
    queryKey: ['users', 'list', query],
    queryFn: ({ signal }) => unwrap(api.GET('/api/users', { params: { query }, signal })),
    placeholderData: keepPreviousData,
  })

export const userQuery = (userId: string) =>
  queryOptions({
    queryKey: ['users', 'detail', userId],
    queryFn: ({ signal }) => unwrap(api.GET('/api/users/{userId}', { params: { path: { userId } }, signal })),
  })

export const userAuthorizationsQuery = (userId: string) =>
  queryOptions({
    queryKey: ['users', 'detail', userId, 'authorizations'],
    queryFn: ({ signal }) =>
      unwrap(api.GET('/api/users/{userId}/authorizations', { params: { path: { userId } }, signal })),
  })

export const projectsQuery = (query: ProjectListParams) =>
  queryOptions({
    queryKey: ['projects', 'list', query],
    queryFn: ({ signal }) => unwrap(api.GET('/api/projects', { params: { query }, signal })),
    placeholderData: keepPreviousData,
  })

export const projectQuery = (projectId: string) =>
  queryOptions({
    queryKey: ['projects', 'detail', projectId],
    queryFn: ({ signal }) =>
      unwrap(api.GET('/api/projects/{projectId}', { params: { path: { projectId } }, signal })),
  })

export const projectRolesQuery = (projectId: string) =>
  queryOptions({
    queryKey: ['projects', 'detail', projectId, 'roles'],
    queryFn: ({ signal }) =>
      unwrap(api.GET('/api/projects/{projectId}/roles', { params: { path: { projectId } }, signal })),
  })

export const projectAuthorizationsQuery = (projectId: string) =>
  queryOptions({
    queryKey: ['projects', 'detail', projectId, 'authorizations'],
    queryFn: ({ signal }) =>
      unwrap(api.GET('/api/projects/{projectId}/authorizations', { params: { path: { projectId } }, signal })),
  })

// No paging: a few thousand tenants at most.
export const tenantsQuery = (includeDeleted: boolean) =>
  queryOptions({
    queryKey: ['tenants', 'list', { includeDeleted }],
    queryFn: ({ signal }) =>
      unwrap(api.GET('/api/tenants', { params: { query: includeDeleted ? { include_deleted: true } : {} }, signal })),
  })

export const tenantQuery = (tenantId: string) =>
  queryOptions({
    queryKey: ['tenants', 'detail', tenantId],
    queryFn: ({ signal }) => unwrap(api.GET('/api/tenants/{tenantId}', { params: { path: { tenantId } }, signal })),
  })

export function failedTenant(error: unknown): Tenant | undefined {
  if (error instanceof ApiError && error.body && typeof error.body === 'object' && 'tenant' in error.body) {
    return (error.body as { tenant?: Tenant }).tenant
  }
  return undefined
}

// Refreshes every cached query: a rename shows up in lists, a new user in the counts.
export function useChange<TVars, TData>(
  change: (vars: TVars) => Promise<TData>,
  options: {
    success?: string | ((data: TData, vars: TVars) => string)
    onSuccess?: (data: TData, vars: TVars) => void | Promise<void>
    toastErrors?: boolean
  } = {},
) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: change,
    onSuccess: async (data, vars) => {
      if (options.success) {
        toast.success(typeof options.success === 'string' ? options.success : options.success(data, vars))
      }
      await options.onSuccess?.(data, vars)
      void queryClient.invalidateQueries()
    },
    onError: (error) => {
      if (options.toastErrors ?? true) {
        toast.error(message(error))
      }
    },
  })
}

export type BrandingState = Schemas['BrandingState']
export type Branding = Schemas['Branding']
export type BrandingSettings = Schemas['BrandingSettings']
export type BrandingAssetName = Schemas['BrandingAssetName']
export type ThemeMode = Schemas['ThemeMode']

export const brandingAssets: BrandingAssetName[] = ['logo', 'logo-dark', 'icon', 'icon-dark', 'font']

// Zitadel's limit; the API refuses larger files too (413).
export const MAX_ASSET_SIZE = 512 * 1024

// The platform branding is inherited by every organization without its own.
export type BrandingScope = { orgId: string } | 'platform'

const scopeKey = (scope: BrandingScope) => (scope === 'platform' ? 'platform' : scope.orgId)

export const brandingQuery = (scope: BrandingScope) =>
  queryOptions({
    queryKey: ['branding', scopeKey(scope)],
    queryFn: ({ signal }) =>
      scope === 'platform'
        ? unwrap(api.GET('/api/platform/branding', { signal }))
        : unwrap(api.GET('/api/orgs/{orgId}/branding', { params: { path: { orgId: scope.orgId } }, signal })),
  })

// Not retried: without it the app keeps its built-in look.
export const platformBrandingQuery = queryOptions({
  ...brandingQuery('platform'),
  retry: false,
  staleTime: 5 * 60_000,
})

// stamp changes the URL after an upload, so the browser loads it again.
export function assetUrl(scope: BrandingScope, asset: BrandingAssetName, version: 'active' | 'draft', stamp?: number) {
  const base = scope === 'platform' ? '/api/platform/branding' : `/api/orgs/${encodeURIComponent(scope.orgId)}/branding`
  const query = new URLSearchParams({ version })
  if (stamp !== undefined) {
    query.set('v', String(stamp))
  }
  return `${base}/assets/${asset}?${query}`
}

export function brandingCalls(scope: BrandingScope) {
  // openapi-fetch sends FormData as is; the schema types the part as a string.
  const fileBody = (file: File) => {
    const form = new FormData()
    form.append('file', file)
    return form as unknown as { file: string }
  }
  if (scope === 'platform') {
    return {
      save: (body: BrandingSettings) => unwrap(api.PUT('/api/platform/branding', { body })),
      publish: () => unwrap(api.POST('/api/platform/branding/publish')),
      reset: undefined,
      upload: (asset: BrandingAssetName, file: File) =>
        unwrap(api.PUT('/api/platform/branding/assets/{asset}', { params: { path: { asset } }, body: fileBody(file) })),
      remove: (asset: BrandingAssetName) =>
        unwrap(api.DELETE('/api/platform/branding/assets/{asset}', { params: { path: { asset } } })),
    }
  }
  const orgId = scope.orgId
  return {
    save: (body: BrandingSettings) => unwrap(api.PUT('/api/orgs/{orgId}/branding', { params: { path: { orgId } }, body })),
    publish: () => unwrap(api.POST('/api/orgs/{orgId}/branding/publish', { params: { path: { orgId } } })),
    reset: () => unwrap(api.DELETE('/api/orgs/{orgId}/branding', { params: { path: { orgId } } })),
    upload: (asset: BrandingAssetName, file: File) =>
      unwrap(
        api.PUT('/api/orgs/{orgId}/branding/assets/{asset}', {
          params: { path: { orgId, asset } },
          body: fileBody(file),
        }),
      ),
    remove: (asset: BrandingAssetName) =>
      unwrap(api.DELETE('/api/orgs/{orgId}/branding/assets/{asset}', { params: { path: { orgId, asset } } })),
  }
}
