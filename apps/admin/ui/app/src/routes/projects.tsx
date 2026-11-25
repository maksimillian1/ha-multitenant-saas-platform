import { useQuery } from '@tanstack/react-query'
import { getRouteApi, Link } from '@tanstack/react-router'
import { createColumnHelper, tableFeatures, useTable } from '@tanstack/react-table'
import { PAGE_SIZE, projectsQuery, type Project } from '@/api/queries'
import { StateBadge } from '@/components/badges'
import { DataTable } from '@/components/data-table'
import { PageHeader, QueryView } from '@/components/page'
import { Pager } from '@/components/pager'
import { SearchInput } from '@/components/search-input'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { formatDate } from '@/lib/format'

const route = getRouteApi('/projects')

const features = tableFeatures({})
const column = createColumnHelper<typeof features, Project>()
const columns = column.columns([
  column.accessor('name', {
    header: 'Name',
    cell: (info) => (
      <Link to="/projects/$projectId" params={{ projectId: info.row.original.id }} className="font-medium hover:underline">
        {info.getValue()}
      </Link>
    ),
  }),
  column.accessor('org_name', {
    header: 'Owner',
    cell: (info) => (
      <Link to="/orgs/$orgId" params={{ orgId: info.row.original.org_id }} className="hover:underline">
        {info.getValue() || info.row.original.org_id}
      </Link>
    ),
  }),
  column.accessor('state', { header: 'State', cell: (info) => <StateBadge state={info.getValue()} /> }),
  column.display({
    id: 'settings',
    header: 'Settings',
    cell: (info) => <ProjectSettings project={info.row.original} />,
  }),
  column.accessor('created_at', { header: 'Created', cell: (info) => formatDate(info.getValue()) }),
])

export function ProjectSettings({ project }: { project: Project }) {
  return (
    <div className="flex flex-wrap gap-1">
      {project.role_assertion && <Badge variant="outline">Roles in tokens</Badge>}
      {project.authorization_required && <Badge variant="outline">Role required</Badge>}
      {project.access_required && <Badge variant="outline">Access required</Badge>}
      {!project.role_assertion && !project.authorization_required && !project.access_required && (
        <span className="text-muted-foreground">—</span>
      )}
    </div>
  )
}

const NONE: Project[] = []

export function ProjectsPage() {
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const offset = search.offset ?? 0
  const projects = useQuery(projectsQuery({ search: search.q, org_id: search.org, offset, limit: PAGE_SIZE }))
  const table = useTable({ features, columns, data: projects.data?.items ?? NONE })

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Projects"
        description="Projects hold the roles of apps, such as platform-admin for this app. Read only here."
      />
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <SearchInput
          label="Search projects"
          placeholder="Search by name"
          value={search.q ?? ''}
          onSearch={(q) => navigate({ search: (prev) => ({ ...prev, q: q || undefined, offset: undefined }) })}
        />
        {search.org && (
          <Button variant="outline" size="sm" onClick={() => navigate({ search: (prev) => ({ ...prev, org: undefined, offset: undefined }) })}>
            Show every organization&apos;s projects
          </Button>
        )}
      </div>
      <Card>
        <CardContent className="flex flex-col gap-4">
          <QueryView query={projects} skeleton={<Skeleton className="h-64" />}>
            {(list) => (
              <>
                <DataTable table={table} emptyText="No projects match." />
                <Pager
                  offset={offset}
                  limit={PAGE_SIZE}
                  total={list.total}
                  loading={projects.isPlaceholderData}
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
