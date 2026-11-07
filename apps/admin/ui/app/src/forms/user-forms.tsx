import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Bot, KeyRound, Pencil, UserPlus } from 'lucide-react'
import { useForm, useWatch, type Control, type FieldPath, type FieldValues } from 'react-hook-form'
import { z } from 'zod'
import { api, unwrap } from '@/api/client'
import { MAX_OPTIONS, orgsQuery, useChange, type Schemas, type User } from '@/api/queries'
import { Button } from '@/components/ui/button'
import { CheckboxField, SelectField, TextareaField, TextField } from './fields'
import { FormDialog } from './form-dialog'
import { email, optionalText, password, requiredText } from './schemas'

// OrgSelect offers the first organizations by name; it mounts only while
// its dialog is open, so the list loads only then.
function OrgSelect<T extends FieldValues>({ control, name }: { control: Control<T>; name: FieldPath<T> }) {
  const orgs = useQuery(orgsQuery({ limit: MAX_OPTIONS }))
  return (
    <SelectField
      control={control}
      name={name}
      label="Organization"
      placeholder={orgs.isPending ? 'Loading…' : 'Choose an organization'}
      options={(orgs.data?.items ?? []).map((org) => ({ value: org.id, label: org.name }))}
    />
  )
}

const humanSchema = z.object({
    org_id: z.string().min(1, 'Required'),
    username: optionalText(),
    given_name: requiredText(),
    family_name: requiredText(),
    display_name: optionalText(),
    email,
    email_verified: z.boolean(),
    password,
    password_change_required: z.boolean(),
  })
type HumanValues = z.infer<typeof humanSchema>

export function CreateHumanUserDialog({ orgId = '' }: { orgId?: string }) {
  const navigate = useNavigate()
  const empty: HumanValues = {
    org_id: orgId,
    username: '',
    given_name: '',
    family_name: '',
    display_name: '',
    email: '',
    email_verified: true,
    password: '',
    password_change_required: true,
  }
  const form = useForm<HumanValues>({ resolver: zodResolver(humanSchema), defaultValues: empty })
  const create = useChange(
    (body: Schemas['HumanUserCreate']) => unwrap(api.POST('/api/users/human', { body })),
    {
      success: (_, body) => `User ${body.username || body.email} created`,
      toastErrors: false,
      onSuccess: (created) => navigate({ to: '/users/$userId', params: { userId: created.id } }),
    },
  )

  return (
    <FormDialog
      trigger={
        <Button>
          <UserPlus /> New user
        </Button>
      }
      title="New user"
      description="A person who signs in with a password."
      submitLabel="Create"
      form={form}
      values={empty}
      onSubmit={(values) =>
        create.mutateAsync({
          org_id: values.org_id,
          username: values.username || undefined,
          given_name: values.given_name,
          family_name: values.family_name,
          display_name: values.display_name || undefined,
          email: values.email,
          email_verified: values.email_verified,
          password: values.password || undefined,
          password_change_required: values.password !== '' && values.password_change_required,
        })
      }
    >
      <OrgSelect control={form.control} name="org_id" />
      <div className="grid gap-4 sm:grid-cols-2">
        <TextField control={form.control} name="given_name" label="Given name" />
        <TextField control={form.control} name="family_name" label="Family name" />
      </div>
      <TextField control={form.control} name="email" label="Email" type="email" />
      <CheckboxField
        control={form.control}
        name="email_verified"
        label="The email address is verified"
        description="Otherwise Zitadel sends a verification email, if it can send email."
      />
      <TextField control={form.control} name="username" label="Username" description="Leave empty to use the email." />
      <TextField control={form.control} name="display_name" label="Display name" description="Optional." />
      <TextField
        control={form.control}
        name="password"
        label="Initial password"
        type="password"
        autoComplete="new-password"
        description="Optional. Without it the user stays in the initial state until they set one."
      />
      <CheckboxField control={form.control} name="password_change_required" label="Ask for a new password at the first sign-in" />
    </FormDialog>
  )
}

const machineSchema = z.object({
  org_id: z.string().min(1, 'Required'),
  username: requiredText(),
  name: requiredText(),
  description: optionalText(500),
  access_token_type: z.enum(['jwt', 'bearer']),
})
type MachineValues = z.infer<typeof machineSchema>

export function CreateMachineUserDialog({ orgId = '' }: { orgId?: string }) {
  const navigate = useNavigate()
  const empty: MachineValues = { org_id: orgId, username: '', name: '', description: '', access_token_type: 'jwt' }
  const form = useForm<MachineValues>({ resolver: zodResolver(machineSchema), defaultValues: empty })
  const create = useChange(
    (body: Schemas['MachineUserCreate']) => unwrap(api.POST('/api/users/machine', { body })),
    {
      success: (_, body) => `Machine user ${body.username} created`,
      toastErrors: false,
      onSuccess: (created) => navigate({ to: '/users/$userId', params: { userId: created.id } }),
    },
  )

  return (
    <FormDialog
      trigger={
        <Button variant="outline">
          <Bot /> New machine user
        </Button>
      }
      title="New machine user"
      description="A service account, such as a tenant's API client. Generate its client secret on its page."
      submitLabel="Create"
      form={form}
      values={empty}
      onSubmit={(values) => create.mutateAsync({ ...values, description: values.description || undefined })}
    >
      <OrgSelect control={form.control} name="org_id" />
      <TextField control={form.control} name="username" label="Username" description="Also the client ID for client credentials." />
      <TextField control={form.control} name="name" label="Name" />
      <TextareaField control={form.control} name="description" label="Description" description="Optional." />
      <SelectField
        control={form.control}
        name="access_token_type"
        label="Access tokens"
        options={[
          { value: 'jwt', label: 'JWT (the API Gateway validates them locally)' },
          { value: 'bearer', label: 'Opaque bearer token' },
        ]}
      />
    </FormDialog>
  )
}

const humanEditSchema = z.object({
  username: requiredText(),
  given_name: requiredText(),
  family_name: requiredText(),
  nick_name: optionalText(),
  display_name: optionalText(),
  email,
  email_verified: z.boolean(),
})
type HumanEditValues = z.infer<typeof humanEditSchema>

const machineEditSchema = z.object({
  username: requiredText(),
  name: requiredText(),
  description: optionalText(500),
})
type MachineEditValues = z.infer<typeof machineEditSchema>

function useUpdateUser(user: User) {
  return useChange(
    (body: Schemas['UserUpdate']) =>
      unwrap(api.PATCH('/api/users/{userId}', { params: { path: { userId: user.id } }, body })),
    { success: 'User saved', toastErrors: false },
  )
}

const editTrigger = (
  <Button variant="outline">
    <Pencil /> Edit
  </Button>
)

// EditUserDialog sends only the fields that changed.
export function EditUserDialog({ user }: { user: User }) {
  return user.human ? <EditHumanDialog user={user} human={user.human} /> : <EditMachineDialog user={user} />
}

function EditHumanDialog({ user, human }: { user: User; human: Schemas['HumanProfile'] }) {
  const current: HumanEditValues = {
    username: user.username,
    given_name: human.given_name,
    family_name: human.family_name,
    nick_name: human.nick_name,
    display_name: human.display_name,
    email: human.email,
    email_verified: false,
  }
  const form = useForm<HumanEditValues>({ resolver: zodResolver(humanEditSchema), defaultValues: current })
  const update = useUpdateUser(user)
  const emailChanged = useWatch({ control: form.control, name: 'email' }) !== human.email

  async function save(values: HumanEditValues) {
    const changes: Schemas['HumanUserUpdate'] = {}
    if (values.given_name !== human.given_name) changes.given_name = values.given_name
    if (values.family_name !== human.family_name) changes.family_name = values.family_name
    // Zitadel cannot clear these; an emptied field stays as it is.
    if (values.nick_name && values.nick_name !== human.nick_name) changes.nick_name = values.nick_name
    if (values.display_name && values.display_name !== human.display_name) changes.display_name = values.display_name
    if (values.email !== human.email) {
      changes.email = values.email
      changes.email_verified = values.email_verified
    }
    const body: Schemas['UserUpdate'] = {}
    if (values.username !== user.username) body.username = values.username
    if (Object.keys(changes).length > 0) body.human = changes
    if (Object.keys(body).length > 0) {
      await update.mutateAsync(body)
    }
  }

  return (
    <FormDialog trigger={editTrigger} title="Edit user" submitLabel="Save" form={form} values={current} onSubmit={save}>
      <TextField
        control={form.control}
        name="username"
        label="Username"
        description="Changing it ends the user's sessions and tokens."
      />
      <div className="grid gap-4 sm:grid-cols-2">
        <TextField control={form.control} name="given_name" label="Given name" />
        <TextField control={form.control} name="family_name" label="Family name" />
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <TextField control={form.control} name="nick_name" label="Nickname" />
        <TextField control={form.control} name="display_name" label="Display name" />
      </div>
      <TextField control={form.control} name="email" label="Email" type="email" />
      {emailChanged && (
        <CheckboxField
          control={form.control}
          name="email_verified"
          label="The new address is verified"
          description="Otherwise Zitadel sends a verification email, if it can send email."
        />
      )}
    </FormDialog>
  )
}

function EditMachineDialog({ user }: { user: User }) {
  const machine = user.machine
  const current: MachineEditValues = {
    username: user.username,
    name: machine?.name ?? '',
    description: machine?.description ?? '',
  }
  const form = useForm<MachineEditValues>({ resolver: zodResolver(machineEditSchema), defaultValues: current })
  const update = useUpdateUser(user)

  async function save(values: MachineEditValues) {
    const changes: Schemas['MachineUserUpdate'] = {}
    if (values.name !== current.name) changes.name = values.name
    if (values.description !== current.description) changes.description = values.description
    const body: Schemas['UserUpdate'] = {}
    if (values.username !== user.username) body.username = values.username
    if (Object.keys(changes).length > 0) body.machine = changes
    if (Object.keys(body).length > 0) {
      await update.mutateAsync(body)
    }
  }

  return (
    <FormDialog trigger={editTrigger} title="Edit machine user" submitLabel="Save" form={form} values={current} onSubmit={save}>
      <TextField
        control={form.control}
        name="username"
        label="Username"
        description="Also the client ID; changing it ends the user's tokens."
      />
      <TextField control={form.control} name="name" label="Name" />
      <TextareaField control={form.control} name="description" label="Description" />
    </FormDialog>
  )
}

const passwordSchema = z
  .object({
    password: z.string().min(1, 'Required').max(200, 'At most 200 characters'),
    confirm: z.string(),
    change_required: z.boolean(),
  })
  .refine((values) => values.password === values.confirm, { path: ['confirm'], message: 'The passwords differ' })
type PasswordValues = z.infer<typeof passwordSchema>

export function SetPasswordDialog({ user }: { user: User }) {
  const empty: PasswordValues = { password: '', confirm: '', change_required: true }
  const form = useForm<PasswordValues>({ resolver: zodResolver(passwordSchema), defaultValues: empty })
  const setPassword = useChange(
    (body: Schemas['PasswordSet']) =>
      unwrap(api.POST('/api/users/{userId}/password', { params: { path: { userId: user.id } }, body })),
    { success: 'Password set', toastErrors: false },
  )

  return (
    <FormDialog
      trigger={
        <Button variant="outline">
          <KeyRound /> Set password
        </Button>
      }
      title="Set password"
      description="Zitadel checks it against the password policy."
      submitLabel="Set password"
      form={form}
      values={empty}
      onSubmit={(values) => setPassword.mutateAsync({ password: values.password, change_required: values.change_required })}
    >
      <TextField control={form.control} name="password" label="New password" type="password" autoComplete="new-password" />
      <TextField control={form.control} name="confirm" label="Repeat the password" type="password" autoComplete="new-password" />
      <CheckboxField control={form.control} name="change_required" label="Ask for a new password at the next sign-in" />
    </FormDialog>
  )
}
