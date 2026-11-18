import { useQuery, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router'
import { CirclePause, CirclePlay, KeyRound, Lock, LockOpen, Trash2, X } from 'lucide-react'
import { useState } from 'react'
import { api, unwrap } from '@/api/client'
import {
  meQuery,
  userAuthorizationsQuery,
  userQuery,
  useChange,
  type Authorization,
  type Schemas,
  type User,
} from '@/api/queries'
import { StateBadge, UserTypeBadge } from '@/components/badges'
import { ConfirmButton } from '@/components/confirm'
import { DetailList, Mono, PageHeader, QueryView } from '@/components/page'
import { SecretDialog } from '@/components/secret-dialog'
import { Badge } from '@/components/ui/badge'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { AddAuthorizationDialog, EditRolesDialog } from '@/forms/authorization-forms'
import { EditUserDialog, SetPasswordDialog } from '@/forms/user-forms'
import { formatDateTime } from '@/lib/format'

const route = getRouteApi('/users/$userId')

export function UserPage() {
  const { userId } = route.useParams()
  const { data: user } = useSuspenseQuery(userQuery(userId))
  const { data: me } = useSuspenseQuery(meQuery)
  const isSelf = user.id === me.subject

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={user.display_name}
        badges={
          <>
            <UserTypeBadge type={user.type} />
            <StateBadge state={user.state} />
            {isSelf && <Badge>You</Badge>}
          </>
        }
        description={user.preferred_login_name || user.username}
        actions={<UserActions user={user} isSelf={isSelf} />}
      />
      <Card>
        <CardHeader>
          <CardTitle>Details</CardTitle>
        </CardHeader>
        <CardContent>
          <DetailList items={details(user)} />
        </CardContent>
      </Card>
      {user.machine && <SecretCard user={user} hasSecret={user.machine.has_secret} />}
      <AuthorizationsCard user={user} isSelf={isSelf} />
    </div>
  )
}

function details(user: User) {
  const items = [
    { label: 'ID', value: <Mono>{user.id}</Mono> },
    { label: 'Username', value: user.username },
    {
      label: 'Organization',
      value: (
        <Link to="/orgs/$orgId" params={{ orgId: user.org_id }} className="hover:underline">
          {user.org_name || user.org_id}
        </Link>
      ),
    },
    { label: 'Login names', value: user.login_names.length ? user.login_names.join(', ') : '—' },
  ]
  if (user.human) {
    const human = user.human
    items.push(
      { label: 'Name', value: `${human.given_name} ${human.family_name}`.trim() || '—' },
      { label: 'Nickname', value: human.nick_name || '—' },
      {
        label: 'Email',
        value: (
          <span className="flex flex-wrap items-center gap-2">
            {human.email || '—'}
            {human.email && (
              <Badge variant={human.email_verified ? 'secondary' : 'outline'}>
                {human.email_verified ? 'Verified' : 'Not verified'}
              </Badge>
            )}
          </span>
        ),
      },
      { label: 'Phone', value: human.phone || '—' },
      {
        label: 'Password',
        value: human.password_change_required
          ? 'Must be changed at the next sign-in'
          : `Changed ${formatDateTime(human.password_changed_at)}`,
      },
    )
  }
  if (user.machine) {
    items.push(
      { label: 'Name', value: user.machine.name },
      { label: 'Description', value: user.machine.description || '—' },
      { label: 'Access tokens', value: user.machine.access_token_type === 'jwt' ? 'JWT' : 'Opaque bearer token' },
    )
  }
  items.push(
    { label: 'Created', value: formatDateTime(user.created_at) },
    { label: 'Changed', value: formatDateTime(user.changed_at) },
  )
  return items
}

function UserActions({ user, isSelf }: { user: User; isSelf: boolean }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const path = { params: { path: { userId: user.id } } }
  const lock = useChange(() => unwrap(api.POST('/api/users/{userId}/lock', path)), { success: `${user.username} locked` })
  const unlock = useChange(() => unwrap(api.POST('/api/users/{userId}/unlock', path)), {
    success: `${user.username} unlocked`,
  })
  const deactivate = useChange(() => unwrap(api.POST('/api/users/{userId}/deactivate', path)), {
    success: `${user.username} deactivated`,
  })
  const reactivate = useChange(() => unwrap(api.POST('/api/users/{userId}/reactivate', path)), {
    success: `${user.username} reactivated`,
  })
  const remove = useChange(() => unwrap(api.DELETE('/api/users/{userId}', path)), {
    success: `${user.username} deleted`,
    onSuccess: async () => {
      queryClient.removeQueries({ queryKey: userQuery(user.id).queryKey })
      await navigate({ to: '/users' })
    },
  })
  // The server refuses these too (409); the page says why up front.
  const selfReason = isSelf ? 'You cannot do this to your own user.' : undefined

  return (
    <>
      <EditUserDialog user={user} />
      {user.human && <SetPasswordDialog user={user} />}
      {user.state === 'locked' ? (
        <ConfirmButton
          label="Unlock"
          icon={<LockOpen />}
          title={`Unlock ${user.username}?`}
          description="The user can sign in again."
          onConfirm={() => unlock.mutateAsync()}
        />
      ) : (
        <ConfirmButton
          label="Lock"
          icon={<Lock />}
          title={`Lock ${user.username}?`}
          description="The user can no longer sign in until unlocked. Tokens already issued stay valid until they expire."
          disabled={isSelf}
          disabledReason={selfReason}
          onConfirm={() => lock.mutateAsync()}
        />
      )}
      {user.state === 'inactive' ? (
        <ConfirmButton
          label="Reactivate"
          icon={<CirclePlay />}
          title={`Reactivate ${user.username}?`}
          description="The user can sign in again."
          onConfirm={() => reactivate.mutateAsync()}
        />
      ) : (
        <ConfirmButton
          label="Deactivate"
          icon={<CirclePause />}
          title={`Deactivate ${user.username}?`}
          description="The user can no longer sign in until reactivated."
          disabled={isSelf}
          disabledReason={selfReason}
          onConfirm={() => deactivate.mutateAsync()}
        />
      )}
      <ConfirmButton
        label="Delete"
        icon={<Trash2 />}
        destructive
        title={`Delete ${user.username}?`}
        description="Zitadel deletes the user with their role assignments and sessions."
        confirmText={user.username}
        disabled={isSelf}
        disabledReason={selfReason}
        onConfirm={() => remove.mutateAsync()}
      />
    </>
  )
}

function SecretCard({ user, hasSecret }: { user: User; hasSecret: boolean }) {
  const [secret, setSecret] = useState<Schemas['MachineSecret']>()
  const path = { params: { path: { userId: user.id } } }
  const generate = useChange(() => unwrap(api.POST('/api/users/{userId}/secret', path)), {
    onSuccess: (credentials) => setSecret(credentials),
  })
  const remove = useChange(() => unwrap(api.DELETE('/api/users/{userId}/secret', path)), {
    success: 'Client secret removed',
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>Client secret</CardTitle>
        <CardDescription>
          {hasSecret
            ? 'The user has a client secret for the client credentials grant.'
            : 'No client secret: the user cannot get tokens with client credentials.'}
        </CardDescription>
        <CardAction className="flex gap-2">
          {hasSecret && (
            <ConfirmButton
              label="Remove secret"
              icon={<X />}
              title="Remove the client secret?"
              description="Clients that use it can no longer get new tokens."
              onConfirm={() => remove.mutateAsync()}
            />
          )}
          <ConfirmButton
            label={hasSecret ? 'Generate new secret' : 'Generate secret'}
            icon={<KeyRound />}
            title={hasSecret ? 'Replace the client secret?' : 'Generate a client secret?'}
            description={
              hasSecret
                ? 'The old secret stops working at once. The new one is shown only once.'
                : 'The secret is shown only once.'
            }
            confirmLabel="Generate"
            onConfirm={() => generate.mutateAsync()}
          />
        </CardAction>
      </CardHeader>
      <SecretDialog secret={secret} onClose={() => setSecret(undefined)} />
    </Card>
  )
}

function AuthorizationsCard({ user, isSelf }: { user: User; isSelf: boolean }) {
  const authorizations = useQuery(userAuthorizationsQuery(user.id))
  return (
    <Card>
      <CardHeader>
        <CardTitle>Role assignments</CardTitle>
        <CardDescription>Roles in projects, such as super-admin in platform-admin</CardDescription>
        <CardAction>
          <AddAuthorizationDialog user={user} />
        </CardAction>
      </CardHeader>
      <CardContent>
        <QueryView query={authorizations}>
          {(list) =>
            list.items.length === 0 ? (
              <p className="text-sm text-muted-foreground">No role assignments.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Project</TableHead>
                    <TableHead>On behalf of</TableHead>
                    <TableHead>Roles</TableHead>
                    <TableHead>State</TableHead>
                    <TableHead className="w-0">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {list.items.map((authorization) => (
                    <AuthorizationRow key={authorization.id} authorization={authorization} isSelf={isSelf} />
                  ))}
                </TableBody>
              </Table>
            )
          }
        </QueryView>
      </CardContent>
    </Card>
  )
}

function AuthorizationRow({ authorization, isSelf }: { authorization: Authorization; isSelf: boolean }) {
  const remove = useChange(
    () =>
      unwrap(
        api.DELETE('/api/authorizations/{authorizationId}', {
          params: { path: { authorizationId: authorization.id } },
        }),
      ),
    { success: 'Role assignment removed' },
  )
  const selfReason = isSelf ? 'You cannot change your own role assignments.' : undefined

  return (
    <TableRow>
      <TableCell>
        <Link to="/projects/$projectId" params={{ projectId: authorization.project_id }} className="font-medium hover:underline">
          {authorization.project_name}
        </Link>
      </TableCell>
      <TableCell>{authorization.org_name || authorization.org_id}</TableCell>
      <TableCell>
        <div className="flex flex-wrap gap-1">
          {authorization.roles.map((role) => (
            <Badge key={role.key} variant="secondary">
              {role.key}
            </Badge>
          ))}
        </div>
      </TableCell>
      <TableCell>
        <StateBadge state={authorization.state} />
      </TableCell>
      <TableCell>
        <div className="flex justify-end gap-1">
          <EditRolesDialog authorization={authorization} disabled={isSelf} disabledReason={selfReason} />
          <ConfirmButton
            label={`Remove the role assignment in ${authorization.project_name}`}
            icon={<Trash2 />}
            iconOnly
            title={`Remove the roles in ${authorization.project_name}?`}
            description={`Revokes ${authorization.roles.map((role) => role.key).join(', ')}.`}
            confirmLabel="Remove"
            destructive
            disabled={isSelf}
            disabledReason={selfReason}
            onConfirm={() => remove.mutateAsync()}
          />
        </div>
      </TableCell>
    </TableRow>
  )
}
