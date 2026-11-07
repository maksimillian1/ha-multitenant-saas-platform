import { BrandingEditor } from '@/components/branding-editor'
import { PageHeader } from '@/components/page'

// PlatformBrandingPage edits the instance's login branding: the default of
// every organization without its own, and the admin app's own look.
export function PlatformBrandingPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Platform branding"
        description="The login branding of every organization without its own. The admin app takes its colors, logo and icon from the published version."
      />
      <BrandingEditor scope="platform" name="the platform" />
    </div>
  )
}
