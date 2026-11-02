import { CircleAlert } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import type { DefaultValues, FieldValues, UseFormReturn } from 'react-hook-form'
import { message } from '@/api/client'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { FieldGroup } from '@/components/ui/field'
import { Spinner } from '@/components/ui/spinner'

// FormDialog is a form in a dialog. It resets the form to `values` when it
// opens, keeps it open and shows the server's message when the submit
// fails, and closes when it succeeds.
export function FormDialog<T extends FieldValues>({
  trigger,
  title,
  description,
  submitLabel,
  form,
  values,
  onSubmit,
  children,
}: {
  trigger: ReactNode
  title: string
  description?: ReactNode
  submitLabel: string
  form: UseFormReturn<T>
  values?: DefaultValues<T>
  onSubmit: (values: T) => Promise<unknown>
  children: ReactNode
}) {
  const [open, setOpen] = useState(false)
  const serverError = form.formState.errors.root?.server?.message

  const submit = form.handleSubmit(async (data) => {
    try {
      await onSubmit(data)
      setOpen(false)
    } catch (error) {
      form.setError('root.server', { message: message(error) })
    }
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (next) {
          form.reset(values)
        }
        setOpen(next)
      }}
    >
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="max-h-[calc(100svh-2rem)] overflow-y-auto sm:max-w-lg">
        <form onSubmit={submit} noValidate className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            {description && <DialogDescription>{description}</DialogDescription>}
          </DialogHeader>
          <FieldGroup>{children}</FieldGroup>
          {serverError && (
            <Alert variant="destructive">
              <CircleAlert />
              <AlertDescription>{serverError}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline">
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={form.formState.isSubmitting}>
              {form.formState.isSubmitting && <Spinner />}
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
