import type { ReactNode } from 'react'

/** Opens the rear camera straight away; no permission prompt, works on plain HTTP. */
export function CaptureButton({ onPhoto, className, children }: { onPhoto: (file: File) => void; className: string; children: ReactNode }) {
  return (
    <label className={className}>
      <input
        type="file"
        accept="image/*"
        capture="environment"
        className="visually-hidden"
        onChange={(e) => {
          const file = e.currentTarget.files?.[0]
          e.currentTarget.value = ''
          if (file) onPhoto(file)
        }}
      />
      {children}
    </label>
  )
}
