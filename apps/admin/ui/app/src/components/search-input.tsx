import { Search } from 'lucide-react'
import { useEffect, useEffectEvent, useRef, useState } from 'react'
import { Input } from '@/components/ui/input'

// SearchInput reports what the admin types after a short pause, so a list
// does not reload on every key.
export function SearchInput({
  value,
  onSearch,
  placeholder,
  label,
}: {
  value: string
  onSearch: (value: string) => void
  placeholder: string
  label: string
}) {
  const [text, setText] = useState(value)
  // The last value reported or received; typing ahead of it is kept.
  const known = useRef(value)

  // A new value from outside (back button, cleared filter) replaces the text.
  useEffect(() => {
    if (value !== known.current) {
      known.current = value
      setText(value)
    }
  }, [value])

  const report = useEffectEvent((next: string) => {
    known.current = next
    onSearch(next)
  })

  useEffect(() => {
    const next = text.trim()
    if (next === known.current) {
      return
    }
    const timer = setTimeout(() => report(next), 300)
    return () => clearTimeout(timer)
  }, [text])

  return (
    <div className="relative w-full sm:max-w-xs">
      <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        type="search"
        aria-label={label}
        placeholder={placeholder}
        className="pl-8"
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
    </div>
  )
}
