import { useQuery, useSuspenseQuery } from '@tanstack/react-query'
import { getRouteApi, Link } from '@tanstack/react-router'
import { projectAuthorizationsQuery, projectQuery, projectRolesQuery } from '@/api/queries'
import { StateBadge } from '@/components/badges'
import { DetailList, Mono, PageHeader, QueryView } from '@/components/page'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { formatDateTime } from '@/lib/format'
import { ProjectSettings } from './projects'

const route = getRouteApi('/projects/$projectId')

export function ProjectPage() {
  const { projectId } = route.useParams()
  const { data: project } = useSuspenseQuery(projectQuery(projectId))
  const roles = useQuery(projectRolesQuery(projectId))
  const holders = useQuery(projectAuthorizationsQuery(projectId))

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title={project.name} badges={<StateBadge state={project.state} />} description="Project" />

      <Card>
        <CardHeader>
          <CardTitle>Details</CardTitle>
        </CardHeader>
        <CardContent>
          <DetailList
            items={[
              { label: 'ID', value: <Mono>{project.id}</Mono> },
              {
                label: 'Owner',
                value: (
                  <Link to="/orgs/$orgId" params={{ orgId: project.org_id }} className="hover:underline">
                    {project.org_name || project.org_id}
                  </Link>
                ),
              },
              ...(project.granted_org_id
                ? [{ label: 'Granted to', value: project.granted_org_name || project.granted_org_id }]
                : []),
              { label: 'Settings', value: <ProjectSettings project={project} /> },
              { label: 'Created', value: formatDateTime(project.created_at) },
              { label: 'Changed', value: formatDateTime(project.changed_at) },
            ]}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Roles</CardTitle>
          <CardDescription>Assign them on a user&apos;s page</CardDescription>
        </CardHeader>
        <CardContent>
          <QueryView query={roles}>
            {(list) =>
              list.items.length === 0 ? (
                <p className="text-sm text-muted-foreground">No roles.</p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Key</TableHead>
                      <TableHead>Display name</TableHead>
                      <TableHead>Group</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {list.items.map((role) => (
                      <TableRow key={role.key}>
                        <TableCell>
                          <Mono>{role.key}</Mono>
                        </TableCell>
                        <TableCell>{role.display_name || '—'}</TableCell>
                        <TableCell>{role.group || '—'}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )
            }
          </QueryView>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Role holders</CardTitle>
          <CardDescription>Users with roles in this project</CardDescription>
        </CardHeader>
        <CardContent>
          <QueryView query={holders}>
            {(list) =>
              list.items.length === 0 ? (
                <p className="text-sm text-muted-foreground">Nobody holds a role.</p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>User</TableHead>
                      <TableHead>Roles</TableHead>
                      <TableHead>State</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {list.items.map((holder) => (
                      <TableRow key={holder.id}>
                        <TableCell>
                          <Link to="/users/$userId" params={{ userId: holder.user_id }} className="font-medium hover:underline">
                            {holder.user_display_name || holder.user_login_name || holder.user_id}
                          </Link>
                          <div className="text-xs text-muted-foreground">{holder.user_login_name}</div>
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-wrap gap-1">
                            {holder.roles.map((role) => (
                              <Badge key={role.key} variant="secondary">
                                {role.key}
                              </Badge>
                            ))}
                          </div>
                        </TableCell>
                        <TableCell>
                          <StateBadge state={holder.state} />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )
            }
          </QueryView>
        </CardContent>
      </Card>
    </div>
  )
}
