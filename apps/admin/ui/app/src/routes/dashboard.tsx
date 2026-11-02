import { useSuspenseQuery } from '@tanstack/react-query'
import { createColumnHelper, tableFeatures, useTable } from '@tanstack/react-table'
import { useMemo } from 'react'
import { meQuery } from '@/api/queries'
import { DataTable } from '@/components/data-table'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

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

const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const relative = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

function sessionEnds(expiresAt: string) {
  const at = new Date(expiresAt)
  const minutes = Math.round((at.getTime() - Date.now()) / 60_000)
  return `${dateTime.format(at)} (${relative.format(minutes, 'minute')})`
}

export function DashboardPage() {
  const { data: me } = useSuspenseQuery(meQuery)
  const roles = useMemo(
    () => me.roles.map((role) => ({ role, grantsAccess: role === me.admin_role })),
    [me],
  )
  const table = useTable({ features, columns, data: roles })

  const details = [
    { label: 'Name', value: me.name },
    { label: 'Email', value: me.email || '—' },
    { label: 'User ID', value: <code className="font-mono text-xs">{me.subject}</code> },
    { label: 'Session ends', value: sessionEnds(me.expires_at) },
  ]

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Super admin</h1>
        <p className="text-muted-foreground">
          Only users with the <code className="font-mono text-sm">{me.admin_role}</code> role see this page.
        </p>
      </div>

      <div className="grid gap-6 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Session</CardTitle>
            <CardDescription>Signed in with Zitadel</CardDescription>
          </CardHeader>
          <CardContent>
            <dl className="grid grid-cols-[8rem_1fr] gap-x-4 gap-y-3 text-sm">
              {details.map(({ label, value }) => (
                <div key={label} className="contents">
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="break-all">{value}</dd>
                </div>
              ))}
            </dl>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Roles</CardTitle>
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
