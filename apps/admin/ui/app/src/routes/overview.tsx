import { useQuery, useSuspenseQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { createColumnHelper, tableFeatures, useTable } from '@tanstack/react-table'
import { Bot, Building2, FolderKanban, Users, type LucideIcon } from 'lucide-react'
import { useMemo } from 'react'
import { meQuery, overviewQuery } from '@/api/queries'
import { DataTable } from '@/components/data-table'
import { DetailList, Mono, PageHeader, QueryView } from '@/components/page'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { sessionEnds } from '@/lib/format'

type RoleRow = { role: string; grantsAccess: boolean }

const features = tableFeatures({})
const column = createColumnHelper<typeof features, RoleRow>()
const columns = column.columns([
  column.accessor('role', {
    header: 'Role',
    cell: (info) => <code className="font-mono text-xs">{info.getValue()}</code>,
  }),
  column.accessor('grantsAccess', {
    header: 'Admin app',
    cell: (info) =>
      info.getValue() ? <Badge>Grants access</Badge> : <Badge variant="outline">No access</Badge>,
  }),
])

// statLink is the focus and hover style of a link around a StatCard.
const statLink = 'group rounded-xl focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none'

function StatCard({ label, value, icon: Icon }: { label: string; value: number; icon: LucideIcon }) {
  return (
    <Card className="transition-colors group-hover:bg-muted/50">
      <CardHeader>
        <CardDescription className="flex items-center gap-2">
          <Icon className="size-4" />
          {label}
        </CardDescription>
        <CardTitle className="text-3xl font-semibold tabular-nums">{value.toLocaleString()}</CardTitle>
      </CardHeader>
    </Card>
  )
}

export function OverviewPage() {
  const { data: me } = useSuspenseQuery(meQuery)
  const overview = useQuery(overviewQuery)
  const roles = useMemo(
    () => me.roles.map((role) => ({ role, grantsAccess: role === me.admin_role })),
    [me],
  )
  const table = useTable({ features, columns, data: roles })

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Overview"
        description={
          <>
            What lives in Zitadel. Only users with the <code className="font-mono">{me.admin_role}</code> role see
            this app.
          </>
        }
      />

      <QueryView
        query={overview}
        skeleton={
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-24" />
            ))}
          </div>
        }
      >
        {(counts) => (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Link to="/orgs" className={statLink}>
              <StatCard label="Organizations" value={counts.organizations} icon={Building2} />
            </Link>
            <Link to="/users" search={{ type: 'human' }} className={statLink}>
              <StatCard label="Human users" value={counts.human_users} icon={Users} />
            </Link>
            <Link to="/users" search={{ type: 'machine' }} className={statLink}>
              <StatCard label="Machine users" value={counts.machine_users} icon={Bot} />
            </Link>
            <Link to="/projects" className={statLink}>
              <StatCard label="Projects" value={counts.projects} icon={FolderKanban} />
            </Link>
          </div>
        )}
      </QueryView>

      <div className="grid gap-6 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Your session</CardTitle>
            <CardDescription>Signed in with Zitadel</CardDescription>
          </CardHeader>
          <CardContent>
            <DetailList
              items={[
                { label: 'Name', value: me.name },
                { label: 'Email', value: me.email || '—' },
                {
                  label: 'User ID',
                  value: (
                    <Link to="/users/$userId" params={{ userId: me.subject }} className="hover:underline">
                      <Mono>{me.subject}</Mono>
                    </Link>
                  ),
                },
                { label: 'Session ends', value: sessionEnds(me.expires_at) },
              ]}
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Your roles</CardTitle>
            <CardDescription>In the platform-admin project, copied at sign-in</CardDescription>
          </CardHeader>
          <CardContent>
            <DataTable table={table} emptyText="No roles." />
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
