import { useId, type ReactNode } from 'react'
import { Controller, type Control, type FieldPath, type FieldValues } from 'react-hook-form'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldContent, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'

// The fields bind shadcn/ui field components to react-hook-form; the zod
// schema of the form decides what is valid.

type FieldProps<T extends FieldValues> = {
  control: Control<T>
  name: FieldPath<T>
  label: string
  description?: ReactNode
}

export function TextField<T extends FieldValues>({
  control,
  name,
  label,
  description,
  type = 'text',
  autoComplete = 'off',
  placeholder,
}: FieldProps<T> & { type?: 'text' | 'email' | 'password'; autoComplete?: string; placeholder?: string }) {
  const id = useId()
  return (
    <Controller
      control={control}
      name={name}
      render={({ field, fieldState }) => (
        <Field data-invalid={fieldState.invalid}>
          <FieldLabel htmlFor={id}>{label}</FieldLabel>
          <Input
            {...field}
            value={field.value ?? ''}
            id={id}
            type={type}
            autoComplete={autoComplete}
            placeholder={placeholder}
            aria-invalid={fieldState.invalid}
          />
          {description && <FieldDescription>{description}</FieldDescription>}
          {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
        </Field>
      )}
    />
  )
}

export function TextareaField<T extends FieldValues>({ control, name, label, description }: FieldProps<T>) {
  const id = useId()
  return (
    <Controller
      control={control}
      name={name}
      render={({ field, fieldState }) => (
        <Field data-invalid={fieldState.invalid}>
          <FieldLabel htmlFor={id}>{label}</FieldLabel>
          <Textarea {...field} value={field.value ?? ''} id={id} rows={3} aria-invalid={fieldState.invalid} />
          {description && <FieldDescription>{description}</FieldDescription>}
          {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
        </Field>
      )}
    />
  )
}

export function SelectField<T extends FieldValues>({
  control,
  name,
  label,
  description,
  options,
  placeholder,
}: FieldProps<T> & { options: Array<{ value: string; label: string }>; placeholder?: string }) {
  const id = useId()
  return (
    <Controller
      control={control}
      name={name}
      render={({ field, fieldState }) => (
        <Field data-invalid={fieldState.invalid}>
          <FieldLabel htmlFor={id}>{label}</FieldLabel>
          <NativeSelect {...field} value={field.value ?? ''} id={id} className="w-full" aria-invalid={fieldState.invalid}>
            {placeholder !== undefined && <NativeSelectOption value="">{placeholder}</NativeSelectOption>}
            {options.map((option) => (
              <NativeSelectOption key={option.value} value={option.value}>
                {option.label}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          {description && <FieldDescription>{description}</FieldDescription>}
          {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
        </Field>
      )}
    />
  )
}

export function CheckboxField<T extends FieldValues>({ control, name, label, description }: FieldProps<T>) {
  const id = useId()
  return (
    <Controller
      control={control}
      name={name}
      render={({ field, fieldState }) => (
        <Field orientation="horizontal" data-invalid={fieldState.invalid}>
          <Checkbox
            id={id}
            name={field.name}
            checked={Boolean(field.value)}
            onCheckedChange={(checked) => field.onChange(checked === true)}
            onBlur={field.onBlur}
            ref={field.ref}
          />
          <FieldContent>
            <FieldLabel htmlFor={id} className="font-normal">
              {label}
            </FieldLabel>
            {description && <FieldDescription>{description}</FieldDescription>}
          </FieldContent>
        </Field>
      )}
    />
  )
}

// ChoiceList is a group of checkboxes for picking several values, such as
// the roles of a role assignment.
export function ChoiceList<T extends FieldValues>({
  control,
  name,
  label,
  description,
  options,
}: FieldProps<T> & { options: Array<{ value: string; label: string; hint?: string }> }) {
  return (
    <Controller
      control={control}
      name={name}
      render={({ field, fieldState }) => {
        const selected: string[] = field.value ?? []
        return (
          <fieldset className="flex flex-col gap-3" aria-invalid={fieldState.invalid}>
            <legend className="mb-1 text-sm font-medium">{label}</legend>
            {description && <FieldDescription>{description}</FieldDescription>}
            {options.map((option) => (
              <ChoiceOption
                key={option.value}
                option={option}
                checked={selected.includes(option.value)}
                onChange={(checked) =>
                  field.onChange(
                    checked ? [...selected, option.value] : selected.filter((value) => value !== option.value),
                  )
                }
              />
            ))}
            {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
          </fieldset>
        )
      }}
    />
  )
}

function ChoiceOption({
  option,
  checked,
  onChange,
}: {
  option: { value: string; label: string; hint?: string }
  checked: boolean
  onChange: (checked: boolean) => void
}) {
  const id = useId()
  return (
    <Field orientation="horizontal">
      <Checkbox id={id} checked={checked} onCheckedChange={(next) => onChange(next === true)} />
      <FieldContent>
        <FieldLabel htmlFor={id} className="font-normal">
          {option.label}
        </FieldLabel>
        {option.hint && <FieldDescription>{option.hint}</FieldDescription>}
      </FieldContent>
    </Field>
  )
}
