import { useQuery } from '@tanstack/react-query'
import { getRouteApi, Link } from '@tanstack/react-router'
import { createColumnHelper, tableFeatures, useTable } from '@tanstack/react-table'
import { orgsQuery, PAGE_SIZE, type Org } from '@/api/queries'
import { StateBadge } from '@/components/badges'
import { DataTable } from '@/components/data-table'
import { Mono, PageHeader, QueryView } from '@/components/page'
import { Pager } from '@/components/pager'
import { SearchInput } from '@/components/search-input'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent } from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { CreateOrgDialog } from '@/forms/org-forms'
import { formatDate } from '@/lib/format'
import { orgStates } from '@/router'

const route = getRouteApi('/orgs')

const features = tableFeatures({})
const column = createColumnHelper<typeof features, Org>()
const columns = column.columns([
  column.accessor('name', {
    header: 'Name',
    cell: (info) => (
      <div className="flex items-center gap-2">
        <Link to="/orgs/$orgId" params={{ orgId: info.row.original.id }} className="font-medium hover:underline">
          {info.getValue()}
        </Link>
        {info.row.original.is_default && <Badge variant="secondary">Default</Badge>}
      </div>
    ),
  }),
  column.accessor('state', { header: 'State', cell: (info) => <StateBadge state={info.getValue()} /> }),
  column.accessor('primary_domain', {
    header: 'Primary domain',
    cell: (info) => <span className="text-muted-foreground">{info.getValue() || '—'}</span>,
  }),
  column.accessor('created_at', { header: 'Created', cell: (info) => formatDate(info.getValue()) }),
  column.accessor('id', { header: 'ID', cell: (info) => <Mono>{info.getValue()}</Mono> }),
])

const NONE: Org[] = []

export function OrgsPage() {
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const offset = search.offset ?? 0
  const orgs = useQuery(orgsQuery({ search: search.q, state: search.state, offset, limit: PAGE_SIZE }))
  const table = useTable({ features, columns, data: orgs.data?.items ?? NONE })

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Organizations"
        description="One organization per tenant (ADR-0003), plus the instance's own."
        actions={<CreateOrgDialog />}
      />
      <div className="flex flex-wrap gap-2">
        <SearchInput
          label="Search organizations"
          placeholder="Search by name"
          value={search.q ?? ''}
          onSearch={(q) => navigate({ search: (prev) => ({ ...prev, q: q || undefined, offset: undefined }) })}
        />
        <NativeSelect
          aria-label="State"
          value={search.state ?? ''}
          onChange={(e) =>
            navigate({
              search: (prev) => ({
                ...prev,
                state: orgStates.find((s) => s === e.target.value),
                offset: undefined,
              }),
            })
          }
        >
          <NativeSelectOption value="">All states</NativeSelectOption>
          <NativeSelectOption value="active">Active</NativeSelectOption>
          <NativeSelectOption value="inactive">Inactive</NativeSelectOption>
        </NativeSelect>
      </div>
      <Card>
        <CardContent className="flex flex-col gap-4">
          <QueryView query={orgs} skeleton={<Skeleton className="h-64" />}>
            {(list) => (
              <>
                <DataTable table={table} emptyText="No organizations match." />
                <Pager
                  offset={offset}
                  limit={PAGE_SIZE}
                  total={list.total}
                  loading={orgs.isPlaceholderData}
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
