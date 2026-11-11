import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { ImageUp, RotateCcw, Send, Trash2 } from 'lucide-react'
import { useId, useRef, type ChangeEvent } from 'react'
import { Controller, useForm, useWatch, type Control } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import {
  assetUrl,
  brandingCalls,
  brandingQuery,
  MAX_ASSET_SIZE,
  useChange,
  type Branding,
  type BrandingAssetName,
  type BrandingScope,
  type BrandingSettings,
} from '@/api/queries'
import { ConfirmButton } from '@/components/confirm'
import { LoginPreview, type PreviewAssets } from '@/components/login-preview'
import { QueryView } from '@/components/page'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldError, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { CheckboxField, SelectField } from '@/forms/fields'
import { formatDateTime } from '@/lib/format'

const color = z.string().regex(/^#[0-9a-fA-F]{6}$/, 'A color like #1d4ed8')

const brandingSchema = z.object({
  primary_color: color,
  background_color: color,
  warn_color: color,
  font_color: color,
  primary_color_dark: color,
  background_color_dark: color,
  warn_color_dark: color,
  font_color_dark: color,
  hide_login_name_suffix: z.boolean(),
  disable_watermark: z.boolean(),
  theme_mode: z.enum(['auto', 'light', 'dark']),
}) satisfies z.ZodType<BrandingSettings>

type ColorName = 'primary' | 'background' | 'warn' | 'font'
const colorNames: Array<{ name: ColorName; label: string }> = [
  { name: 'primary', label: 'Primary color' },
  { name: 'background', label: 'Background color' },
  { name: 'warn', label: 'Warning color' },
  { name: 'font', label: 'Font color' },
]

const themeModes = [
  { value: 'auto', label: 'Follow the browser' },
  { value: 'light', label: 'Always light' },
  { value: 'dark', label: 'Always dark' },
]

const assetLabels: Record<BrandingAssetName, { label: string; accept: string; image: boolean }> = {
  logo: { label: 'Logo', accept: 'image/png,image/jpeg,image/gif,image/webp,image/svg+xml', image: true },
  'logo-dark': { label: 'Logo (dark)', accept: 'image/png,image/jpeg,image/gif,image/webp,image/svg+xml', image: true },
  icon: { label: 'Icon', accept: 'image/png,image/jpeg,image/gif,image/webp,image/svg+xml', image: true },
  'icon-dark': { label: 'Icon (dark)', accept: 'image/png,image/jpeg,image/gif,image/webp,image/svg+xml', image: true },
  font: { label: 'Font', accept: '.ttf,.otf,.woff,.woff2,font/ttf,font/otf,font/woff,font/woff2', image: false },
}

const assetKeys = { logo: 'logo', 'logo-dark': 'logoDark', icon: 'icon', 'icon-dark': 'iconDark', font: 'font' } as const

function previewAssets(scope: BrandingScope, branding: Branding, version: 'active' | 'draft', stamp: number) {
  const assets: PreviewAssets = {}
  for (const [asset, key] of Object.entries(assetKeys) as Array<[BrandingAssetName, keyof PreviewAssets]>) {
    if (branding.assets[asset]) {
      assets[key] = assetUrl(scope, asset, version, stamp)
    }
  }
  return assets
}

function sameSettings(a: BrandingSettings, b: BrandingSettings) {
  return (Object.keys(a) as Array<keyof BrandingSettings>).every((key) => a[key] === b[key])
}

function sameAssets(a: Branding['assets'], b: Branding['assets']) {
  return (Object.keys(a) as BrandingAssetName[]).every((key) => a[key] === b[key])
}

// BrandingEditor edits the draft of one scope's login branding and publishes
// it. Saving and uploading change only the draft; the login shows the active
// version until "Publish".
export function BrandingEditor({ scope, name }: { scope: BrandingScope; name: string }) {
  const query = useQuery(brandingQuery(scope))
  return (
    <QueryView query={query}>
      {(state) => <Editor scope={scope} name={name} state={state} stamp={query.dataUpdatedAt} />}
    </QueryView>
  )
}

function Editor({
  scope,
  name,
  state,
  stamp,
}: {
  scope: BrandingScope
  name: string
  state: { inherited: boolean; active: Branding; draft: Branding }
  stamp: number
}) {
  const calls = brandingCalls(scope)
  const form = useForm<BrandingSettings>({ resolver: zodResolver(brandingSchema), values: state.draft.settings })
  const draft = useWatch({ control: form.control }) as BrandingSettings
  const save = useChange((values: BrandingSettings) => calls.save(values), { success: 'Draft saved' })
  const publish = useChange(() => calls.publish(), { success: `Branding of ${name} published` })
  const reset = useChange(() => (calls.reset ? calls.reset() : Promise.resolve()), {
    success: `${name} uses the platform branding again`,
  })

  const unpublished =
    !state.inherited &&
    (!sameSettings(state.active.settings, state.draft.settings) || !sameAssets(state.active.assets, state.draft.assets))
  const dirty = form.formState.isDirty
  const draftAssets = previewAssets(scope, state.draft, 'draft', stamp)

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Login branding</CardTitle>
          <CardDescription>
            What {name}&apos;s users see on the Zitadel login. Changes go to a draft; the login shows them after
            publishing.
          </CardDescription>
          <CardAction className="flex flex-wrap gap-2">
            {calls.reset && !state.inherited && (
              <ConfirmButton
                label="Reset to platform branding"
                icon={<RotateCcw />}
                destructive
                title={`Reset the branding of ${name}?`}
                description="Its own branding, published and draft, is removed with its logo, icon and font. Its login shows the platform branding again."
                confirmLabel="Reset"
                onConfirm={() => reset.mutateAsync()}
              />
            )}
            <ConfirmButton
              label="Publish"
              icon={<Send />}
              title={`Publish the branding of ${name}?`}
              description="The login shows the draft from now on."
              disabled={state.inherited || dirty || !unpublished}
              disabledReason={
                state.inherited
                  ? 'Save a draft first.'
                  : dirty
                    ? 'Save the draft first.'
                    : !unpublished
                      ? 'The draft is already published.'
                      : undefined
              }
              onConfirm={() => publish.mutateAsync()}
            />
          </CardAction>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-2 text-sm">
          {state.inherited ? (
            <Badge variant="secondary">Inherits the platform branding</Badge>
          ) : (
            <Badge variant="outline">{scope === 'platform' ? 'Platform branding' : 'Own branding'}</Badge>
          )}
          {unpublished && <Badge>Unpublished changes</Badge>}
          {state.draft.changed_at && (
            <span className="text-muted-foreground">Draft changed {formatDateTime(state.draft.changed_at)}</span>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,26rem)]">
        <div className="flex flex-col gap-6">
          <Card>
            <CardHeader>
              <CardTitle>Draft</CardTitle>
              <CardDescription>
                {state.inherited
                  ? 'The platform values. Saving gives this organization branding of its own.'
                  : 'Colors and options of the draft.'}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <form
                aria-label="Branding draft"
                className="flex flex-col gap-6"
                onSubmit={form.handleSubmit((values) => save.mutateAsync(values).catch(() => {}))}
              >
                <div className="grid gap-6 md:grid-cols-2">
                  {(['light', 'dark'] as const).map((mode) => (
                    <FieldSet key={mode}>
                      <FieldLegend>{mode === 'light' ? 'Light' : 'Dark'}</FieldLegend>
                      {colorNames.map((c) => (
                        <ColorField
                          key={c.name}
                          control={form.control}
                          name={(mode === 'light' ? `${c.name}_color` : `${c.name}_color_dark`) as keyof BrandingSettings}
                          label={`${c.label} (${mode})`}
                        />
                      ))}
                    </FieldSet>
                  ))}
                </div>
                <SelectField control={form.control} name="theme_mode" label="Theme mode" options={themeModes} />
                <CheckboxField
                  control={form.control}
                  name="hide_login_name_suffix"
                  label="Hide login name suffix"
                  description="The login shows ann instead of ann@tenant.example."
                />
                <CheckboxField
                  control={form.control}
                  name="disable_watermark"
                  label="Disable watermark"
                  description="The login hides “Powered by ZITADEL”."
                />
                <div className="flex gap-2">
                  <Button type="submit" disabled={form.formState.isSubmitting}>
                    {form.formState.isSubmitting && <Spinner />} Save draft
                  </Button>
                  <Button type="button" variant="outline" disabled={!dirty} onClick={() => form.reset()}>
                    Discard changes
                  </Button>
                </div>
              </form>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Logo, icon and font</CardTitle>
              <CardDescription>
                PNG, JPEG, GIF, WebP or SVG images and TTF, OTF, WOFF or WOFF2 fonts, up to 512 KiB. Uploads go to the
                draft.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <ul className="flex flex-col divide-y">
                {(Object.keys(assetLabels) as BrandingAssetName[]).map((asset) => (
                  <AssetRow
                    key={asset}
                    scope={scope}
                    asset={asset}
                    present={state.draft.assets[asset]}
                    stamp={stamp}
                    upload={calls.upload}
                    remove={calls.remove}
                  />
                ))}
              </ul>
            </CardContent>
          </Card>
        </div>

        <Card className="self-start">
          <CardHeader>
            <CardTitle>Preview</CardTitle>
            <CardDescription>The draft as you edit it, before saving.</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <LoginPreview settings={draft} assets={draftAssets} mode="light" />
            <LoginPreview settings={draft} assets={draftAssets} mode="dark" />
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Published</CardTitle>
          <CardDescription>
            {state.inherited ? 'The platform branding, which the login shows now.' : 'What the login shows now.'}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Swatches settings={state.active.settings} />
        </CardContent>
      </Card>
    </div>
  )
}

function ColorField({ control, name, label }: { control: Control<BrandingSettings>; name: keyof BrandingSettings; label: string }) {
  const id = useId()
  return (
    <Controller
      control={control}
      name={name}
      render={({ field, fieldState }) => {
        const value = typeof field.value === 'string' ? field.value : ''
        return (
          <Field data-invalid={fieldState.invalid}>
            <FieldLabel htmlFor={id}>{label}</FieldLabel>
            <div className="flex items-center gap-2">
              <input
                type="color"
                aria-label={`Pick ${label.toLowerCase()}`}
                value={/^#[0-9a-fA-F]{6}$/.test(value) ? value.toLowerCase() : '#000000'}
                onChange={(event) => field.onChange(event.target.value)}
                className="size-8 shrink-0 cursor-pointer rounded-md border bg-transparent p-0.5"
              />
              <Input
                id={id}
                name={field.name}
                ref={field.ref}
                value={value}
                onChange={field.onChange}
                onBlur={field.onBlur}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                aria-invalid={fieldState.invalid}
              />
            </div>
            {fieldState.invalid && <FieldError errors={[fieldState.error]} />}
          </Field>
        )
      }}
    />
  )
}

function Swatches({ settings }: { settings: BrandingSettings }) {
  return (
    <dl className="grid gap-4 sm:grid-cols-2">
      {(['light', 'dark'] as const).map((mode) => (
        <div key={mode} className="flex flex-col gap-2">
          <dt className="text-xs font-medium text-muted-foreground">{mode === 'light' ? 'Light' : 'Dark'}</dt>
          <dd className="flex flex-wrap gap-3">
            {colorNames.map((c) => {
              const value = settings[(mode === 'light' ? `${c.name}_color` : `${c.name}_color_dark`) as keyof BrandingSettings] as string
              return (
                <span key={c.name} className="flex items-center gap-1.5 text-xs" title={`${c.label} (${mode})`}>
                  <span className="size-4 rounded border" style={{ backgroundColor: value }} />
                  <span className="font-mono">{value || '—'}</span>
                </span>
              )
            })}
          </dd>
        </div>
      ))}
    </dl>
  )
}

function AssetRow({
  scope,
  asset,
  present,
  stamp,
  upload,
  remove,
}: {
  scope: BrandingScope
  asset: BrandingAssetName
  present: boolean
  stamp: number
  upload: (asset: BrandingAssetName, file: File) => Promise<unknown>
  remove: (asset: BrandingAssetName) => Promise<unknown>
}) {
  const { label, accept, image } = assetLabels[asset]
  const input = useRef<HTMLInputElement>(null)
  const send = useChange((file: File) => upload(asset, file), { success: `${label} uploaded to the draft` })
  const drop = useChange(() => remove(asset), { success: `${label} removed from the draft` })

  function choose(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) {
      return
    }
    // The server checks again, by content.
    if (file.size > MAX_ASSET_SIZE) {
      toast.error(`${file.name} is larger than 512 KiB.`)
      return
    }
    send.mutate(file)
  }

  return (
    <li className="flex items-center gap-3 py-3 first:pt-0 last:pb-0">
      <div className="flex size-12 shrink-0 items-center justify-center rounded-md border bg-muted/40">
        {present && image ? (
          <img src={assetUrl(scope, asset, 'draft', stamp)} alt={`${label} in the draft`} className="max-h-10 max-w-10 object-contain" />
        ) : (
          <span className="text-[10px] text-muted-foreground">{present ? 'Font' : 'None'}</span>
        )}
      </div>
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">{label}</div>
        <div className="text-xs text-muted-foreground">{present ? 'In the draft' : 'Not set'}</div>
      </div>
      <input
        ref={input}
        type="file"
        accept={accept}
        className="sr-only"
        aria-label={`Upload ${label.toLowerCase()}`}
        tabIndex={-1}
        onChange={choose}
      />
      <Button type="button" variant="outline" size="sm" disabled={send.isPending} onClick={() => input.current?.click()}>
        {send.isPending ? <Spinner /> : <ImageUp />} Upload
      </Button>
      {present && (
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={`Remove ${label.toLowerCase()}`}
          disabled={drop.isPending}
          onClick={() => drop.mutate()}
        >
          <Trash2 />
        </Button>
      )}
    </li>
  )
}
