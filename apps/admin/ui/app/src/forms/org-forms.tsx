import { zodResolver } from '@hookform/resolvers/zod'
import { useNavigate } from '@tanstack/react-router'
import { Pencil, Plus } from 'lucide-react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { api, unwrap } from '@/api/client'
import { useChange, type Org } from '@/api/queries'
import { Button } from '@/components/ui/button'
import { FormDialog } from './form-dialog'
import { TextField } from './fields'
import { requiredText } from './schemas'

const orgSchema = z.object({ name: requiredText() })
type OrgValues = z.infer<typeof orgSchema>

export function CreateOrgDialog() {
  const navigate = useNavigate()
  const form = useForm<OrgValues>({ resolver: zodResolver(orgSchema), defaultValues: { name: '' } })
  const create = useChange((body: OrgValues) => unwrap(api.POST('/api/orgs', { body })), {
    success: (_, body) => `Organization ${body.name} created`,
    toastErrors: false,
    onSuccess: (created) => navigate({ to: '/orgs/$orgId', params: { orgId: created.id } }),
  })

  return (
    <FormDialog
      trigger={
        <Button>
          <Plus /> New organization
        </Button>
      }
      title="New organization"
      description="A tenant's organization in Zitadel, without admin users. Instance admins can still manage it."
      submitLabel="Create"
      form={form}
      values={{ name: '' }}
      onSubmit={(values) => create.mutateAsync(values)}
    >
      <TextField control={form.control} name="name" label="Name" description="Unique in the instance." />
    </FormDialog>
  )
}

export function RenameOrgDialog({ org }: { org: Org }) {
  const form = useForm<OrgValues>({ resolver: zodResolver(orgSchema), defaultValues: { name: org.name } })
  const rename = useChange(
    (body: OrgValues) => unwrap(api.PATCH('/api/orgs/{orgId}', { params: { path: { orgId: org.id } }, body })),
    { success: 'Organization renamed', toastErrors: false },
  )

  return (
    <FormDialog
      trigger={
        <Button variant="outline">
          <Pencil /> Rename
        </Button>
      }
      title="Rename organization"
      submitLabel="Save"
      form={form}
      values={{ name: org.name }}
      onSubmit={(values) => rename.mutateAsync(values)}
    >
      <TextField control={form.control} name="name" label="Name" />
    </FormDialog>
  )
}
