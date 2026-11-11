import { useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { getRouteApi, Link, useNavigate } from '@tanstack/react-router'
import { CircleAlert, CircleCheck, CircleDashed, CirclePause, CirclePlay, RotateCw, Trash2 } from 'lucide-react'
import { api, unwrap } from '@/api/client'
import { failedTenant, tenantQuery, useChange, type Tenant, type TenantPart } from '@/api/queries'
import { StateBadge } from '@/components/badges'
import { ConfirmButton } from '@/components/confirm'
import { DetailList, Mono, PageHeader } from '@/components/page'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Spinner } from '@/components/ui/spinner'
import { formatDateTime } from '@/lib/format'

const route = getRouteApi('/tenants/$tenantId')

// The provisioning steps in their order (ADR-0012); deletion runs them
// backwards.
const steps: Array<{ part: TenantPart; label: (id: string) => string }> = [
  { part: 'organization', label: () => 'Zitadel organization' },
  { part: 'admin', label: () => 'First admin, invited by email with ORG_OWNER' },
  { part: 'namespace', label: (id) => `Namespace tenant-${id} with its quota and network policy` },
]

// useTenantChange runs one change of the tenant. Tenant Management answers
// a failed step with the tenant as the failure left it, which the page
// shows at once; the toast says what failed.
function useTenantChange(tenantId: string, call: () => Promise<Tenant>, success: string, onSuccess?: () => Promise<void>) {
  const queryClient = useQueryClient()
  const key = tenantQuery(tenantId).queryKey
  return useChange(
    async () => {
      try {
        return await call()
      } catch (error) {
        const tenant = failedTenant(error)
        if (tenant) {
          queryClient.setQueryData(key, tenant)
        }
        throw error
      }
    },
    {
      success,
      onSuccess: async (tenant) => {
        queryClient.setQueryData(key, tenant)
        await onSuccess?.()
      },
    },
  )
}

export function TenantPage() {
  const { tenantId } = route.useParams()
  const navigate = useNavigate()
  const { data: tenant } = useSuspenseQuery(tenantQuery(tenantId))
  const name = tenant.name ?? 'Deleted tenant'
  const path = { params: { path: { tenantId } } }

  const retry = useTenantChange(
    tenantId,
    () => unwrap(api.POST('/api/tenants/{tenantId}/retry', path)),
    tenant.state === 'deleting' ? `${name} deleted` : `${name} is ready`,
  )
  const suspend = useTenantChange(tenantId, () => unwrap(api.POST('/api/tenants/{tenantId}/suspend', path)), `${name} suspended`)
  const resume = useTenantChange(tenantId, () => unwrap(api.POST('/api/tenants/{tenantId}/resume', path)), `${name} resumed`)
  const remove = useTenantChange(
    tenantId,
    () => unwrap(api.DELETE('/api/tenants/{tenantId}', path)),
    `${name} deleted`,
    async () => {
      await navigate({ to: '/tenants' })
    },
  )

  const unfinished = tenant.state === 'provisioning' || tenant.state === 'deleting'
  const deleted = tenant.state === 'deleted'
  const done = new Set(tenant.parts)

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={name}
        badges={<StateBadge state={tenant.state} />}
        description="Tenant"
        actions={
          deleted ? undefined : (
            <>
              {unfinished && (
                <Button onClick={() => retry.mutate()} disabled={retry.isPending}>
                  {retry.isPending ? <Spinner /> : <RotateCw />}
                  Retry
                </Button>
              )}
              {tenant.state === 'active' && (
                <ConfirmButton
                  label="Suspend"
                  icon={<CirclePause />}
                  title={`Suspend ${name}?`}
                  description="Its organization is deactivated: its users can no longer sign in. All its data is kept, and resuming undoes it."
                  onConfirm={() => suspend.mutateAsync()}
                />
              )}
              {tenant.state === 'suspended' && (
                <ConfirmButton
                  label="Resume"
                  icon={<CirclePlay />}
                  title={`Resume ${name}?`}
                  description="Its organization is activated again and its users can sign in."
                  onConfirm={() => resume.mutateAsync()}
                />
              )}
              <ConfirmButton
                label="Delete"
                icon={<Trash2 />}
                destructive
                title={`Delete ${name}?`}
                description={
                  <>
                    <p>
                      Tenant Management deletes the tenant&apos;s namespace and then its Zitadel organization with its users.
                      Only a tombstone with the tenant ID stays.
                    </p>
                  </>
                }
                confirmText={tenant.name}
                disabled={tenant.state !== 'suspended'}
                disabledReason="Only a suspended tenant can be deleted. Suspend it first."
                onConfirm={() => remove.mutateAsync()}
              />
            </>
          )
        }
      />

      {tenant.last_error && (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertTitle>{tenant.state === 'deleting' ? 'Deletion stopped' : 'Provisioning stopped'}</AlertTitle>
          <AlertDescription>
            <p>{tenant.last_error}</p>
            <p className="mt-1">Retry continues at the first step not done.</p>
          </AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Details</CardTitle>
        </CardHeader>
        <CardContent>
          <DetailList
            items={[
              { label: 'Tenant ID', value: <Mono>{tenant.id}</Mono> },
              {
                label: 'Organization',
                value:
                  done.has('organization') && !deleted ? (
                    <Link to="/orgs/$orgId" params={{ orgId: tenant.id }} className="hover:underline">
                      Open the organization
                    </Link>
                  ) : (
                    '—'
                  ),
              },
              { label: 'First admin', value: tenant.admin_email || '—' },
              { label: 'Created', value: `${formatDateTime(tenant.created_at)} by ${tenant.created_by}` },
              { label: 'Changed', value: formatDateTime(tenant.updated_at) },
              ...(deleted
                ? [{ label: 'Deleted', value: `${formatDateTime(tenant.deleted_at)} by ${tenant.deleted_by ?? '—'}` }]
                : []),
            ]}
          />
        </CardContent>
      </Card>

      {!deleted && (
        <Card>
          <CardHeader>
            <CardTitle>Steps</CardTitle>
            <CardDescription>What exists of the tenant outside the registry.</CardDescription>
          </CardHeader>
          <CardContent>
            <ol className="flex flex-col gap-2 text-sm" aria-label="Steps">
              {steps.map(({ part, label }) => (
                <li key={part} className="flex items-center gap-2" data-done={done.has(part)}>
                  {done.has(part) ? (
                    <CircleCheck className="size-4 text-emerald-600" aria-label="Done" />
                  ) : (
                    <CircleDashed className="size-4 text-muted-foreground" aria-label="Not done" />
                  )}
                  {label(tenant.id)}
                </li>
              ))}
            </ol>
          </CardContent>
        </Card>
      )}
    </div>
  )
}
