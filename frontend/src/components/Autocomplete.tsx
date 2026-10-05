import { useEffect, useRef, useState, type InputHTMLAttributes, type ReactNode, type Ref } from 'react'

export interface Results<T> {
  items: T[]
  /** Shown above the list, e.g. when the matches are from another city. */
  note?: string
}

interface Props<T> {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  search: (query: string, signal: AbortSignal) => Promise<Results<T>>
  itemKey: (item: T) => string
  renderItem: (item: T) => ReactNode
  onPick: (item: T) => void
  /** Runs on blur with the latest results, to accept a name typed in full without tapping it. */
  onCommit?: (items: T[]) => void
  /** Search again when this changes, e.g. when the data behind the suggestions finishes loading. */
  refreshKey?: string
  error?: string
  notice?: ReactNode
  inputRef?: Ref<HTMLInputElement>
  className?: string
  listClassName?: string
  inputProps?: InputHTMLAttributes<HTMLInputElement>
}

/** Text input with suggestions (ARIA combobox): arrows move, Enter picks, Escape closes. */
export function Autocomplete<T>({ id, label, value, onChange, search, itemKey, renderItem, onPick, onCommit, refreshKey, error, notice, inputRef, className, listClassName, inputProps }: Props<T>) {
  const [open, setOpen] = useState(false)
  const [results, setResults] = useState<Results<T>>({ items: [] })
  const [active, setActive] = useState(-1)
  // The search function changes on every render of the parent; keep the latest without re-running the effect.
  const searchRef = useRef(search)
  searchRef.current = search

  useEffect(() => {
    if (!open) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      searchRef.current(value, controller.signal).then(
        (next) => {
          setResults(next)
          setActive(-1)
        },
        (e: Error) => e.name !== 'AbortError' && setResults({ items: [] }),
      )
    }, 300)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [value, open, refreshKey])

  function pick(item: T) {
    onPick(item)
    setOpen(false)
    setResults({ items: [] })
  }

  const listId = `${id}-list`
  const showList = open && results.items.length > 0
  return (
    <div className={`field autocomplete ${className ?? ''}`}>
      <label htmlFor={id}>{label}</label>
      <div className="combo">
      <input
        {...inputProps}
        id={id}
        ref={inputRef}
        value={value}
        onChange={(e) => {
          onChange(e.target.value)
          setOpen(true)
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => {
          setOpen(false)
          onCommit?.(results.items)
        }}
        onKeyDown={(e) => {
          if (e.key === 'Escape') setOpen(false)
          if (!showList) return
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            e.preventDefault()
            const step = e.key === 'ArrowDown' ? 1 : -1
            setActive((i) => (i + step + results.items.length) % results.items.length)
          } else if (e.key === 'Enter' && active >= 0) {
            e.preventDefault() // pick instead of submitting the form
            pick(results.items[active])
          }
        }}
        autoComplete="off"
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={showList}
        aria-controls={listId}
        aria-activedescendant={showList && active >= 0 ? `${listId}-${active}` : undefined}
        aria-invalid={error ? true : inputProps?.['aria-invalid']}
      />
      {showList && (
        <ul className={`suggestions ${listClassName ?? ''}`} id={listId} role="listbox" aria-label={`Sugestões de ${label.toLowerCase()}`}>
          {results.note && (
            <li className="suggestions-note" role="presentation">
              {results.note}
            </li>
          )}
          {results.items.map((item, i) => (
            <li key={itemKey(item)} id={`${listId}-${i}`} role="option" aria-selected={i === active} className={i === active ? 'active' : undefined}>
              {/* pointerdown keeps focus in the input, so blur does not close the list before the tap lands */}
              <button type="button" tabIndex={-1} onPointerDown={(e) => e.preventDefault()} onClick={() => pick(item)}>
                {renderItem(item)}
              </button>
            </li>
          ))}
        </ul>
      )}
      </div>
      {notice}
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
    </div>
  )
}
