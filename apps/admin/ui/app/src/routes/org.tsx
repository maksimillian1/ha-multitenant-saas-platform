import { useQuery, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router'
import { CirclePause, CirclePlay, Trash2 } from 'lucide-react'
import { api, unwrap } from '@/api/client'
import { meQuery, orgQuery, projectsQuery, useChange, userQuery, usersQuery } from '@/api/queries'
import { StateBadge, UserTypeBadge } from '@/components/badges'
import { BrandingEditor } from '@/components/branding-editor'
import { ConfirmButton } from '@/components/confirm'
import { DetailList, Mono, PageHeader, QueryView } from '@/components/page'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { RenameOrgDialog } from '@/forms/org-forms'
import { CreateHumanUserDialog, CreateMachineUserDialog } from '@/forms/user-forms'
import { formatDateTime, plural } from '@/lib/format'

const route = getRouteApi('/orgs/$orgId')

// How many users and projects the page lists; the lists link to the rest.
const PREVIEW = 10

export function OrgPage() {
  const { orgId } = route.useParams()
  const { tab = 'overview' } = route.useSearch()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { data: org } = useSuspenseQuery(orgQuery(orgId))
  const { data: me } = useSuspenseQuery(meQuery)
  const self = useQuery(userQuery(me.subject))

  const path = { params: { path: { orgId } } }
  const deactivate = useChange(() => unwrap(api.POST('/api/orgs/{orgId}/deactivate', path)), {
    success: `${org.name} deactivated`,
  })
  const activate = useChange(() => unwrap(api.POST('/api/orgs/{orgId}/activate', path)), {
    success: `${org.name} activated`,
  })
  const remove = useChange(() => unwrap(api.DELETE('/api/orgs/{orgId}', path)), {
    success: `${org.name} deleted`,
    onSuccess: async () => {
      queryClient.removeQueries({ queryKey: orgQuery(orgId).queryKey })
      await navigate({ to: '/orgs' })
    },
  })

  // The server refuses these too (409); the page says why up front.
  const ownOrg = self.data?.org_id === orgId
  const lockout = ownOrg ? 'This is your own organization.' : undefined

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={org.name}
        badges={
          <>
            <StateBadge state={org.state} />
            {org.is_default && <Badge variant="secondary">Default</Badge>}
          </>
        }
        description="Organization"
        actions={
          <>
            <RenameOrgDialog org={org} />
            {org.state === 'inactive' ? (
              <ConfirmButton
                label="Activate"
                icon={<CirclePlay />}
                title={`Activate ${org.name}?`}
                description="Its users can sign in again."
                onConfirm={() => activate.mutateAsync()}
              />
            ) : (
              <ConfirmButton
                label="Deactivate"
                icon={<CirclePause />}
                title={`Deactivate ${org.name}?`}
                description="Its users can no longer sign in. Tokens already issued stay valid until they expire."
                disabled={ownOrg}
                disabledReason={lockout}
                onConfirm={() => deactivate.mutateAsync()}
              />
            )}
            <ConfirmButton
              label="Delete"
              icon={<Trash2 />}
              destructive
              title={`Delete ${org.name}?`}
              description={
                <>
                  <p>Zitadel deletes the organization with its users, projects and grants; its users can no longer sign in.</p>
                  <p className="mt-2">
                    The tenant&apos;s data in other stores (PostgreSQL, usage events) stays: deleting a tenant is part of the
                    tenant lifecycle (HAMSP-6).
                  </p>
                </>
              }
              confirmText={org.name}
              disabled={org.is_default || ownOrg}
              disabledReason={org.is_default ? 'Zitadel keeps the default organization.' : lockout}
              onConfirm={() => remove.mutateAsync()}
            />
          </>
        }
      />

      <nav aria-label="Organization" className="flex gap-1 border-b">
        {(
          [
            { tab: 'overview', label: 'Overview' },
            { tab: 'branding', label: 'Branding' },
          ] as const
        ).map((item) => (
          <Link
            key={item.tab}
            to="/orgs/$orgId"
            params={{ orgId }}
            search={item.tab === 'overview' ? {} : { tab: item.tab }}
            aria-current={tab === item.tab ? 'page' : undefined}
            className="-mb-px border-b-2 border-transparent px-3 py-2 text-sm text-muted-foreground transition-colors hover:text-foreground aria-[current=page]:border-primary aria-[current=page]:font-medium aria-[current=page]:text-foreground"
          >
            {item.label}
          </Link>
        ))}
      </nav>

      {tab === 'branding' ? (
        <BrandingEditor scope={{ orgId }} name={org.name} />
      ) : (
        <OrgOverview orgId={orgId} />
      )}
    </div>
  )
}

function OrgOverview({ orgId }: { orgId: string }) {
  const { data: org } = useSuspenseQuery(orgQuery(orgId))
  const users = useQuery(usersQuery({ org_id: orgId, limit: PREVIEW }))
  const projects = useQuery(projectsQuery({ org_id: orgId, limit: PREVIEW }))

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Details</CardTitle>
        </CardHeader>
        <CardContent>
          <DetailList
            items={[
              { label: 'ID', value: <Mono>{org.id}</Mono> },
              { label: 'Primary domain', value: org.primary_domain || '—' },
              { label: 'Created', value: formatDateTime(org.created_at) },
              { label: 'Changed', value: formatDateTime(org.changed_at) },
            ]}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Users</CardTitle>
          <CardDescription>
            {users.data ? plural(users.data.total, 'user') : 'People and service accounts of this organization'}
          </CardDescription>
          <CardAction className="flex gap-2">
            <CreateMachineUserDialog orgId={orgId} />
            <CreateHumanUserDialog orgId={orgId} />
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <QueryView query={users}>
            {(list) =>
              list.items.length === 0 ? (
                <p className="text-sm text-muted-foreground">No users yet.</p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Type</TableHead>
                      <TableHead>State</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {list.items.map((user) => (
                      <TableRow key={user.id}>
                        <TableCell>
                          <Link to="/users/$userId" params={{ userId: user.id }} className="font-medium hover:underline">
                            {user.display_name}
                          </Link>
                          <div className="text-xs text-muted-foreground">{user.username}</div>
                        </TableCell>
                        <TableCell>
                          <UserTypeBadge type={user.type} />
                        </TableCell>
                        <TableCell>
                          <StateBadge state={user.state} />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )
            }
          </QueryView>
          {users.data && users.data.total > PREVIEW && (
            <Button variant="link" className="self-start px-0" asChild>
              <Link to="/users" search={{ org: orgId }}>
                All {users.data.total.toLocaleString()} users
              </Link>
            </Button>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Projects</CardTitle>
          <CardDescription>Projects this organization owns</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <QueryView query={projects}>
            {(list) =>
              list.items.length === 0 ? (
                <p className="text-sm text-muted-foreground">This organization owns no projects.</p>
              ) : (
                <ul className="flex flex-col gap-2 text-sm">
                  {list.items.map((project) => (
                    <li key={project.id} className="flex items-center gap-2">
                      <Link to="/projects/$projectId" params={{ projectId: project.id }} className="font-medium hover:underline">
                        {project.name}
                      </Link>
                      <StateBadge state={project.state} />
                    </li>
                  ))}
                </ul>
              )
            }
          </QueryView>
          {projects.data && projects.data.total > PREVIEW && (
            <Button variant="link" className="self-start px-0" asChild>
              <Link to="/projects" search={{ org: orgId }}>
                All {projects.data.total.toLocaleString()} projects
              </Link>
            </Button>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
