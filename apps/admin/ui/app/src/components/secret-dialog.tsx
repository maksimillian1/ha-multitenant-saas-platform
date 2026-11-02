import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import type { Schemas } from '@/api/queries'

function CopyField({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false)
  const id = `secret-${label.toLowerCase().replace(/\s+/g, '-')}`
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <div className="flex gap-2">
        <Input id={id} readOnly value={value} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
        <Button
          type="button"
          variant="outline"
          size="icon"
          aria-label={`Copy ${label}`}
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(value)
              setCopied(true)
            } catch {
              setCopied(false)
            }
          }}
        >
          {copied ? <Check /> : <Copy />}
        </Button>
      </div>
    </Field>
  )
}

// SecretDialog shows a new client secret. Zitadel cannot show it again, so
// the dialog says so and only closes when the admin is done.
export function SecretDialog({ secret, onClose }: { secret?: Schemas['MachineSecret']; onClose: () => void }) {
  return (
    <Dialog open={secret !== undefined} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg" onInteractOutside={(e) => e.preventDefault()}>
        <DialogHeader>
          <DialogTitle>Client credentials</DialogTitle>
          <DialogDescription>For the client credentials grant against Zitadel's token endpoint.</DialogDescription>
        </DialogHeader>
        {secret && (
          <div className="flex flex-col gap-4">
            <CopyField label="Client ID" value={secret.client_id} />
            <CopyField label="Client secret" value={secret.client_secret} />
            <Alert>
              <AlertDescription>Copy the secret now: it is not shown again.</AlertDescription>
            </Alert>
          </div>
        )}
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
