import { useQuery } from '@tanstack/react-query'
import { getRouteApi, Link } from '@tanstack/react-router'
import { createColumnHelper, tableFeatures, useTable } from '@tanstack/react-table'
import { MAX_OPTIONS, orgsQuery, PAGE_SIZE, usersQuery, type User } from '@/api/queries'
import { StateBadge, UserTypeBadge } from '@/components/badges'
import { DataTable } from '@/components/data-table'
import { PageHeader, QueryView } from '@/components/page'
import { Pager } from '@/components/pager'
import { SearchInput } from '@/components/search-input'
import { Card, CardContent } from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { CreateHumanUserDialog, CreateMachineUserDialog } from '@/forms/user-forms'
import { formatDate } from '@/lib/format'
import { userStates, userTypes } from '@/router'

const route = getRouteApi('/users')

const features = tableFeatures({})
const column = createColumnHelper<typeof features, User>()
const columns = column.columns([
  column.accessor('display_name', {
    header: 'Name',
    cell: (info) => (
      <div>
        <Link to="/users/$userId" params={{ userId: info.row.original.id }} className="font-medium hover:underline">
          {info.getValue()}
        </Link>
        <div className="text-xs text-muted-foreground">{info.row.original.preferred_login_name || info.row.original.username}</div>
      </div>
    ),
  }),
  column.accessor('type', { header: 'Type', cell: (info) => <UserTypeBadge type={info.getValue()} /> }),
  column.accessor('org_name', {
    header: 'Organization',
    cell: (info) => (
      <Link to="/orgs/$orgId" params={{ orgId: info.row.original.org_id }} className="hover:underline">
        {info.getValue() || info.row.original.org_id}
      </Link>
    ),
  }),
  column.display({
    id: 'contact',
    header: 'Email or description',
    cell: (info) => (
      <span className="text-muted-foreground">
        {info.row.original.human?.email || info.row.original.machine?.description || '—'}
      </span>
    ),
  }),
  column.accessor('state', { header: 'State', cell: (info) => <StateBadge state={info.getValue()} /> }),
  column.accessor('created_at', { header: 'Created', cell: (info) => formatDate(info.getValue()) }),
])

const NONE: User[] = []

export function UsersPage() {
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const offset = search.offset ?? 0
  const users = useQuery(
    usersQuery({ search: search.q, org_id: search.org, type: search.type, state: search.state, offset, limit: PAGE_SIZE }),
  )
  const orgs = useQuery(orgsQuery({ limit: MAX_OPTIONS }))
  const table = useTable({ features, columns, data: users.data?.items ?? NONE })

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Users"
        description="People and service accounts of every organization."
        actions={
          <>
            <CreateMachineUserDialog orgId={search.org} />
            <CreateHumanUserDialog orgId={search.org} />
          </>
        }
      />
      <div className="flex flex-wrap gap-2">
        <SearchInput
          label="Search users"
          placeholder="Username, name or email"
          value={search.q ?? ''}
          onSearch={(q) => navigate({ search: (prev) => ({ ...prev, q: q || undefined, offset: undefined }) })}
        />
        <NativeSelect
          aria-label="Organization"
          value={search.org ?? ''}
          onChange={(e) => navigate({ search: (prev) => ({ ...prev, org: e.target.value || undefined, offset: undefined }) })}
        >
          <NativeSelectOption value="">All organizations</NativeSelectOption>
          {(orgs.data?.items ?? []).map((org) => (
            <NativeSelectOption key={org.id} value={org.id}>
              {org.name}
            </NativeSelectOption>
          ))}
          {search.org && !orgs.data?.items.some((org) => org.id === search.org) && (
            <NativeSelectOption value={search.org}>{search.org}</NativeSelectOption>
          )}
        </NativeSelect>
        <NativeSelect
          aria-label="Type"
          value={search.type ?? ''}
          onChange={(e) =>
            navigate({
              search: (prev) => ({ ...prev, type: userTypes.find((t) => t === e.target.value), offset: undefined }),
            })
          }
        >
          <NativeSelectOption value="">All types</NativeSelectOption>
          <NativeSelectOption value="human">Humans</NativeSelectOption>
          <NativeSelectOption value="machine">Machines</NativeSelectOption>
        </NativeSelect>
        <NativeSelect
          aria-label="State"
          value={search.state ?? ''}
          onChange={(e) =>
            navigate({
              search: (prev) => ({ ...prev, state: userStates.find((s) => s === e.target.value), offset: undefined }),
            })
          }
        >
          <NativeSelectOption value="">All states</NativeSelectOption>
          <NativeSelectOption value="active">Active</NativeSelectOption>
          <NativeSelectOption value="inactive">Inactive</NativeSelectOption>
          <NativeSelectOption value="locked">Locked</NativeSelectOption>
          <NativeSelectOption value="initial">Initial</NativeSelectOption>
        </NativeSelect>
      </div>
      <Card>
        <CardContent className="flex flex-col gap-4">
          <QueryView query={users} skeleton={<Skeleton className="h-64" />}>
            {(list) => (
              <>
                <DataTable table={table} emptyText="No users match." />
                <Pager
                  offset={offset}
                  limit={PAGE_SIZE}
                  total={list.total}
                  loading={users.isPlaceholderData}
                  onOffset={(next) => navigate({ search: (prev) => ({ ...prev, offset: next || undefined }) })}
                />
              </>
            )}
          </QueryView>
        </CardContent>
      </Card>
    </div>
  )
}
