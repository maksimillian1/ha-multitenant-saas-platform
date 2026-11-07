import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { Pencil, Plus } from 'lucide-react'
import { useEffect } from 'react'
import { useForm, useWatch, type UseFormReturn } from 'react-hook-form'
import { z } from 'zod'
import { api, unwrap } from '@/api/client'
import {
  MAX_OPTIONS,
  projectRolesQuery,
  projectsQuery,
  useChange,
  type Authorization,
  type Schemas,
  type User,
} from '@/api/queries'
import { Button } from '@/components/ui/button'
import { FieldDescription } from '@/components/ui/field'
import { Skeleton } from '@/components/ui/skeleton'
import { ChoiceList, SelectField } from './fields'
import { FormDialog } from './form-dialog'

const roleKeys = z.array(z.string()).min(1, 'Choose at least one role')

const addSchema = z.object({ project_id: z.string().min(1, 'Required'), role_keys: roleKeys })
type AddValues = z.infer<typeof addSchema>

// RoleChoices lists the roles of a project as checkboxes.
function RoleChoices<T extends { role_keys: string[] }>({
  form,
  projectId,
}: {
  form: UseFormReturn<T>
  projectId: string
}) {
  const roles = useQuery({ ...projectRolesQuery(projectId), enabled: projectId !== '' })
  // The form's type is generic here; the field always exists.
  const control = form.control as unknown as UseFormReturn<{ role_keys: string[] }>['control']
  if (!projectId) {
    return null
  }
  if (roles.isPending) {
    return <Skeleton className="h-16" />
  }
  if (!roles.data?.items.length) {
    return <FieldDescription>This project has no roles.</FieldDescription>
  }
  return (
    <ChoiceList
      control={control}
      name="role_keys"
      label="Roles"
      options={roles.data.items.map((role) => ({
        value: role.key,
        label: role.display_name && role.display_name !== role.key ? `${role.display_name} (${role.key})` : role.key,
        hint: role.group || undefined,
      }))}
    />
  )
}

// AddAuthorizationFields mounts only while the dialog is open, so the
// projects load only then.
function AddAuthorizationFields({ form }: { form: UseFormReturn<AddValues> }) {
  const projects = useQuery(projectsQuery({ limit: MAX_OPTIONS }))
  const projectId = useWatch({ control: form.control, name: 'project_id' })
  // Roles belong to one project: a new project starts with none chosen.
  useEffect(() => {
    form.setValue('role_keys', [])
  }, [form, projectId])

  return (
    <>
      <SelectField
        control={form.control}
        name="project_id"
        label="Project"
        placeholder={projects.isPending ? 'Loading…' : 'Choose a project'}
        options={(projects.data?.items ?? []).map((project) => ({
          value: project.id,
          label: project.org_name ? `${project.name} (${project.org_name})` : project.name,
        }))}
      />
      <RoleChoices form={form} projectId={projectId} />
    </>
  )
}

export function AddAuthorizationDialog({ user }: { user: User }) {
  const empty: AddValues = { project_id: '', role_keys: [] }
  const form = useForm<AddValues>({ resolver: zodResolver(addSchema), defaultValues: empty })
  const create = useChange((body: Schemas['AuthorizationCreate']) => unwrap(api.POST('/api/authorizations', { body })), {
    success: 'Role assignment added',
    toastErrors: false,
  })

  return (
    <FormDialog
      trigger={
        <Button size="sm">
          <Plus /> Add
        </Button>
      }
      title="Add role assignment"
      description={`Give ${user.display_name} roles in a project, on behalf of the organization that owns it.`}
      submitLabel="Add"
      form={form}
      values={empty}
      onSubmit={(values) => create.mutateAsync({ user_id: user.id, project_id: values.project_id, role_keys: values.role_keys })}
    >
      <AddAuthorizationFields form={form} />
    </FormDialog>
  )
}

const editSchema = z.object({ role_keys: roleKeys })
type EditValues = z.infer<typeof editSchema>

export function EditRolesDialog({
  authorization,
  disabled,
  disabledReason,
}: {
  authorization: Authorization
  disabled?: boolean
  disabledReason?: string
}) {
  const current: EditValues = { role_keys: authorization.roles.map((role) => role.key) }
  const form = useForm<EditValues>({ resolver: zodResolver(editSchema), defaultValues: current })
  const update = useChange(
    (body: Schemas['AuthorizationUpdate']) =>
      unwrap(
        api.PATCH('/api/authorizations/{authorizationId}', {
          params: { path: { authorizationId: authorization.id } },
          body,
        }),
      ),
    { success: 'Roles saved', toastErrors: false },
  )

  return (
    <FormDialog
      trigger={
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={`Edit roles in ${authorization.project_name}`}
          disabled={disabled}
          title={disabled ? disabledReason : undefined}
        >
          <Pencil />
        </Button>
      }
      title={`Roles in ${authorization.project_name}`}
      description="Roles left unchecked are revoked."
      submitLabel="Save"
      form={form}
      values={current}
      onSubmit={(values) => update.mutateAsync(values)}
    >
      <RoleChoices form={form} projectId={authorization.project_id} />
    </FormDialog>
  )
}
