import { useQuery } from '@tanstack/react-query'
import { getRouteApi, Link } from '@tanstack/react-router'
import { createColumnHelper, tableFeatures, useTable } from '@tanstack/react-table'
import { tenantsQuery, type Tenant } from '@/api/queries'
import { StateBadge } from '@/components/badges'
import { DataTable } from '@/components/data-table'
import { Mono, PageHeader, QueryView } from '@/components/page'
import { Card, CardContent } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { CreateTenantDialog } from '@/forms/tenant-forms'
import { formatDateTime } from '@/lib/format'

const route = getRouteApi('/tenants')

const features = tableFeatures({})
const column = createColumnHelper<typeof features, Tenant>()
const columns = column.columns([
  column.accessor('name', {
    header: 'Name',
    cell: (info) => (
      <Link
        to="/tenants/$tenantId"
        params={{ tenantId: info.row.original.id }}
        className="font-medium hover:underline"
      >
        {info.getValue() || 'Deleted tenant'}
      </Link>
    ),
  }),
  column.accessor('state', { header: 'State', cell: (info) => <StateBadge state={info.getValue()} /> }),
  column.accessor('admin_email', {
    header: 'First admin',
    cell: (info) => <span className="text-muted-foreground">{info.getValue() || '—'}</span>,
  }),
  column.accessor('created_at', { header: 'Created', cell: (info) => formatDateTime(info.getValue()) }),
  column.accessor('id', { header: 'Tenant ID', cell: (info) => <Mono>{info.getValue()}</Mono> }),
])

const NONE: Tenant[] = []

export function TenantsPage() {
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const includeDeleted = search.deleted ?? false
  const tenants = useQuery(tenantsQuery(includeDeleted))
  const table = useTable({ features, columns, data: tenants.data?.items ?? NONE })

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Tenants"
        description="Each tenant has a Zitadel organization, a first admin and a namespace, created and deleted together by Tenant Management (ADR-0012)."
        actions={<CreateTenantDialog />}
      />
      <div className="flex items-center gap-2">
        <Checkbox
          id="show-deleted"
          checked={includeDeleted}
          onCheckedChange={(checked) =>
            navigate({ search: (prev) => ({ ...prev, deleted: checked === true ? true : undefined }) })
          }
        />
        <Label htmlFor="show-deleted">Show deleted tenants</Label>
      </div>
      <Card>
        <CardContent className="flex flex-col gap-4">
          <QueryView query={tenants} skeleton={<Skeleton className="h-64" />}>
            {() => <DataTable table={table} emptyText="No tenants yet." />}
          </QueryView>
        </CardContent>
      </Card>
    </div>
  )
}
