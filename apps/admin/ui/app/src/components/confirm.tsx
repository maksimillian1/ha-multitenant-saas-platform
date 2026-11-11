import { useId, useState, type ReactNode } from 'react'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'

// ConfirmButton asks before it runs an action. With confirmText the admin
// must type that text first, for actions that cannot be undone. The dialog
// stays open while the action runs and closes when it succeeds; a failure
// is reported by the action itself (a toast).
export function ConfirmButton({
  label,
  icon,
  title,
  description,
  confirmLabel,
  confirmText,
  destructive,
  disabled,
  disabledReason,
  iconOnly,
  onConfirm,
}: {
  label: string
  icon?: ReactNode
  title: string
  description: ReactNode
  confirmLabel?: string
  confirmText?: string
  destructive?: boolean
  disabled?: boolean
  disabledReason?: string
  // iconOnly shows just the icon, with the label for screen readers.
  iconOnly?: boolean
  onConfirm: () => Promise<unknown>
}) {
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const [running, setRunning] = useState(false)
  const inputId = useId()
  const ready = !confirmText || typed === confirmText

  async function confirm() {
    setRunning(true)
    try {
      await onConfirm()
      setOpen(false)
    } catch {
      // The action's own error handling tells the admin.
    } finally {
      setRunning(false)
    }
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        setTyped('')
      }}
    >
      <AlertDialogTrigger asChild>
        <Button
          variant={iconOnly ? 'ghost' : destructive ? 'destructive' : 'outline'}
          size={iconOnly ? 'icon-sm' : 'default'}
          aria-label={iconOnly ? label : undefined}
          disabled={disabled}
          title={disabled ? disabledReason : iconOnly ? label : undefined}
        >
          {icon}
          {!iconOnly && label}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription asChild>
            <div>{description}</div>
          </AlertDialogDescription>
        </AlertDialogHeader>
        {confirmText && (
          <Field>
            <FieldLabel htmlFor={inputId}>Type {confirmText} to confirm</FieldLabel>
            <Input id={inputId} value={typed} autoComplete="off" onChange={(e) => setTyped(e.target.value)} />
            <FieldDescription>This cannot be undone.</FieldDescription>
          </Field>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={running}>Cancel</AlertDialogCancel>
          <Button variant={destructive ? 'destructive' : 'default'} disabled={!ready || running} onClick={confirm}>
            {running && <Spinner />}
            {confirmLabel ?? label}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
