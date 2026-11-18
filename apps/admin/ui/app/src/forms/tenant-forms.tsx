import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import { api, message, unwrap } from '@/api/client'
import { failedTenant, tenantQuery, useChange } from '@/api/queries'
import { Button } from '@/components/ui/button'
import { FormDialog } from './form-dialog'
import { TextField } from './fields'
import { requiredText } from './schemas'

const tenantSchema = z.object({
  name: requiredText(100),
  admin_email: requiredText(254).refine((value) => value.includes('@'), 'Not an email address'),
})
type TenantValues = z.infer<typeof tenantSchema>

const empty: TenantValues = { name: '', admin_email: '' }

// CreateTenantDialog creates a tenant: Tenant Management runs every step in
// the request. When a step fails, the tenant exists in provisioning; the
// dialog closes and the tenant's page shows the step, the error and Retry.
export function CreateTenantDialog() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const form = useForm<TenantValues>({ resolver: zodResolver(tenantSchema), defaultValues: empty })
  const create = useChange((body: TenantValues) => unwrap(api.POST('/api/tenants', { body })), {
    success: (tenant) => `Tenant ${tenant.name ?? ''} created`,
    toastErrors: false,
    onSuccess: (tenant) => navigate({ to: '/tenants/$tenantId', params: { tenantId: tenant.id } }),
  })

  async function submit(values: TenantValues) {
    try {
      await create.mutateAsync(values)
    } catch (error) {
      const tenant = failedTenant(error)
      if (!tenant) {
        throw error
      }
      queryClient.setQueryData(tenantQuery(tenant.id).queryKey, tenant)
      void queryClient.invalidateQueries({ queryKey: ['tenants'] })
      toast.error(`Tenant ${values.name} is not ready: ${message(error)}`)
      await navigate({ to: '/tenants/$tenantId', params: { tenantId: tenant.id } })
    }
  }

  return (
    <FormDialog
      trigger={
        <Button>
          <Plus /> New tenant
        </Button>
      }
      title="New tenant"
      description="Creates the tenant's organization, invites its first admin by email and creates its namespace. This takes a few seconds."
      submitLabel="Create"
      form={form}
      values={empty}
      onSubmit={submit}
    >
      <TextField control={form.control} name="name" label="Name" description="Unique among tenants." />
      <TextField
        control={form.control}
        name="admin_email"
        type="email"
        label="First admin's email"
        description="Gets an invitation with the role ORG_OWNER in the tenant's organization."
      />
    </FormDialog>
  )
}
