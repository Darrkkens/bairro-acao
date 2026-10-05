import { useEffect, useRef, useState } from 'react'
import { CaptureButton } from '../components/CaptureButton'
import { Spinner } from '../components/Status'
import { locate } from '../geo'
import { useObjectURL } from '../hooks'
import { BackIcon, CameraIcon, PinIcon } from '../icons'
import { preparePhoto } from '../image'
import { enqueue } from '../outbox'
import { load, save } from '../storage'
import type { GeoPoint, Walk } from '../types'
import { uuid } from '../uuid'

// Typing on the street is slow; one tap fills the usual cases.
const QUICK_NOTES = ['Buraco', 'Lixo acumulado', 'Calçada quebrada', 'Passagem bloqueada', 'Sinalização danificada', 'Banco quebrado', 'Brinquedo quebrado', 'Iluminação']

type Fix = { state: 'off' } | { state: 'locating' } | { state: 'ok'; point: GeoPoint } | { state: 'error'; message: string }

interface Props {
  file: File
  walk: Walk
  onRetake: (file: File) => void
  onClose: () => void
}

export function CaptureScreen({ file, walk, onRetake, onClose }: Props) {
  const preview = useObjectURL(file)
  const [capturedAt] = useState(() => new Date().toISOString())
  const photo = useRef<Promise<Blob> | null>(null)
  const [photoError, setPhotoError] = useState('')
  const [note, setNote] = useState('')
  const [withLocation, setWithLocation] = useState(() => load('bairro.useLocation', true))
  const [fix, setFix] = useState<Fix>({ state: 'off' })
  const located = useRef<Promise<GeoPoint | null>>(Promise.resolve(null))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const prepared = preparePhoto(file)
    photo.current = prepared
    prepared.catch(() => setPhotoError('Não foi possível ler esta foto. Tente tirar outra.'))
  }, [file])

  // The person is standing at the spot right now, so locate while they type.
  useEffect(() => {
    save('bairro.useLocation', withLocation)
    if (!withLocation) {
      located.current = Promise.resolve(null)
      setFix({ state: 'off' })
      return
    }
    let alive = true
    setFix({ state: 'locating' })
    located.current = locate().then(
      (point) => {
        if (alive) setFix({ state: 'ok', point })
        return point
      },
      (e: Error) => {
        if (alive) setFix({ state: 'error', message: e.message })
        return null
      },
    )
    return () => {
      alive = false
    }
  }, [withLocation])

  function addQuickNote(text: string) {
    setNote((current) => (current.trim() ? `${current.trim()}, ${text.toLowerCase()}` : text))
  }

  async function submit() {
    if (!photo.current) return
    setSaving(true)
    setError('')
    try {
      const [blob, location] = await Promise.all([photo.current, located.current])
      await enqueue({ kind: 'occurrence', occurrence: { id: uuid(), walk_id: walk.id, photo: blob, note: note.trim(), location, captured_at: capturedAt } })
      onClose()
    } catch {
      setError('Não foi possível guardar o ponto neste aparelho. Tente de novo.')
      setSaving(false)
    }
  }

  let locationText = 'Desligada'
  if (fix.state === 'locating') locationText = 'Obtendo localização…'
  if (fix.state === 'ok') locationText = fix.point.accuracy_m !== undefined ? `Obtida (±${fix.point.accuracy_m} m)` : 'Obtida'
  if (fix.state === 'error') locationText = fix.message

  return (
    <main className="screen capture">
      <header className="bar">
        <button className="icon-button" onClick={onClose} aria-label="Cancelar e voltar">
          <BackIcon />
        </button>
        <h1>Novo ponto</h1>
        <span className="bar-meta">{walk.neighborhood}</span>
      </header>

      <figure className="photo-frame">{preview && <img src={preview} alt="Foto que você acabou de tirar" />}</figure>
      {photoError && (
        <p className="field-error" role="alert">
          {photoError}
        </p>
      )}

      <label htmlFor="note" className="field-label">
        O que você viu? <span>opcional</span>
      </label>
      <textarea
        id="note"
        rows={3}
        value={note}
        onChange={(e) => setNote(e.target.value)}
        placeholder="Ex.: buraco grande perto da faixa de pedestres"
        maxLength={1000}
        enterKeyHint="done"
      />
      <div className="quick-notes" aria-label="Observações rápidas">
        {QUICK_NOTES.map((text) => (
          <button key={text} type="button" className="chip-button" onClick={() => addQuickNote(text)}>
            {text}
          </button>
        ))}
      </div>

      <label className="location-row">
        <PinIcon />
        <span className="location-text">
          <strong>Salvar localização</strong>
          <span className={fix.state === 'error' ? 'warn-text' : undefined}>{locationText}</span>
        </span>
        <input type="checkbox" role="switch" className="switch" checked={withLocation} onChange={(e) => setWithLocation(e.target.checked)} />
      </label>

      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}

      <div className="action-bar">
        <CaptureButton className="button secondary" onPhoto={onRetake}>
          <CameraIcon /> Outra foto
        </CaptureButton>
        <button className="button primary" onClick={() => void submit()} disabled={saving || photoError !== ''}>
          {saving ? (
            <>
              <Spinner /> {fix.state === 'locating' ? 'Aguardando localização…' : 'Salvando…'}
            </>
          ) : (
            'Salvar ponto'
          )}
        </button>
      </div>
    </main>
  )
}
