import { keepPreviousData, queryOptions, useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api, message, unwrap } from './client'
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

export type OrgListParams = NonNullable<operations['listOrgs']['parameters']['query']>
export type UserListParams = NonNullable<operations['listUsers']['parameters']['query']>
export type ProjectListParams = NonNullable<operations['listProjects']['parameters']['query']>

// PAGE_SIZE is the page size of every list; the API allows up to 100.
export const PAGE_SIZE = 20
// MAX_OPTIONS is how many organizations or projects a picker offers.
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

// useChange runs a change against the API. On success it refreshes every
// cached query (a rename shows up in lists, a new user in the counts) and
// shows the success text; on failure it shows the server's message, unless
// the caller handles errors itself (forms show them in place).
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
