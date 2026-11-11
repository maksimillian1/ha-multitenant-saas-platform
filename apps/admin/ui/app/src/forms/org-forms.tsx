import { zodResolver } from '@hookform/resolvers/zod'
import { Pencil } from 'lucide-react'
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
