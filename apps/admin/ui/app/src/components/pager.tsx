import { ChevronLeft, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'

// Pager moves through a list page by page: "21–40 of 57".
export function Pager({
  offset,
  limit,
  total,
  loading,
  onOffset,
}: {
  offset: number
  limit: number
  total: number
  loading?: boolean
  onOffset: (offset: number) => void
}) {
  const first = total === 0 ? 0 : offset + 1
  const last = Math.min(offset + limit, total)
  return (
    <div className="flex items-center justify-between gap-2 text-sm text-muted-foreground">
      <span className="flex items-center gap-2">
        {first}–{last} of {total.toLocaleString()}
        {loading && <Spinner />}
      </span>
      <div className="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={offset === 0}
          onClick={() => onOffset(Math.max(0, offset - limit))}
        >
          <ChevronLeft /> Previous
        </Button>
        <Button variant="outline" size="sm" disabled={offset + limit >= total} onClick={() => onOffset(offset + limit)}>
          Next <ChevronRight />
        </Button>
      </div>
    </div>
  )
}
