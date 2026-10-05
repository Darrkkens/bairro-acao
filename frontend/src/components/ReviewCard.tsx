import { useEffect, useId, useState, type FormEvent } from 'react'
import { api, ApiError } from '../api'
import { categories } from '../categories'
import { formatTime, mapURL } from '../format'
import { CheckIcon, PencilIcon, PinIcon, RetryIcon, SparkIcon, TrashIcon } from '../icons'
import type { CategoryId, Occurrence, Review, Suggestion } from '../types'
import { CategoryChip, Spinner } from './Status'

const confidenceLabel = { alta: 'alta', media: 'média', baixa: 'baixa' }

interface Props {
  index: number
  occurrence: Occurrence
  onChange: (o: Occurrence) => void
  onRemove: (id: string) => void
}

export function ReviewCard({ index, occurrence: o, onChange, onRemove }: Props) {
  const reviewed = o.reviewed_at !== null
  const [editing, setEditing] = useState(false)
  const [manual, setManual] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)

  useEffect(() => {
    if (!confirmDelete) return
    const timer = setTimeout(() => setConfirmDelete(false), 4000)
    return () => clearTimeout(timer)
  }, [confirmDelete])

  async function run(action: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await action()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Algo deu errado. Tente de novo.')
    } finally {
      setBusy(false)
    }
  }

  const confirm = (r: Review) =>
    run(async () => {
      onChange(await api.review(o.id, r))
      setEditing(false)
      setManual(false)
    })
  const reanalyze = (note: string) =>
    run(async () => {
      onChange(await api.reanalyze(o.id, note))
      setManual(false)
    })
  const remove = () =>
    run(async () => {
      await api.remove(o.id)
      onRemove(o.id)
    })

  let body
  if (reviewed && !editing) {
    body = (
      <div className="confirmed">
        <div className="confirmed-tags">
          <CategoryChip id={o.category} />
          {o.ai && o.ai.category !== o.category && <span className="tag">categoria corrigida</span>}
        </div>
        <h3>{o.title}</h3>
        {o.description && <p>{o.description}</p>}
        <button className="link-button" onClick={() => setEditing(true)}>
          <PencilIcon width={16} height={16} /> Editar
        </button>
      </div>
    )
  } else if (reviewed || manual || o.ai_status === 'done') {
    const suggestion = !reviewed && !manual ? o.ai : null
    const initial: Review = reviewed
      ? { category: o.category as CategoryId, title: o.title, description: o.description }
      : { category: o.ai?.category ?? 'outros', title: o.ai?.title ?? '', description: o.ai?.description || o.note }
    body = (
      <ReviewForm
        // A fresh AI result replaces untouched defaults.
        key={`${o.ai_status}:${o.ai?.title ?? ''}:${manual}`}
        initial={initial}
        suggestion={suggestion}
        busy={busy}
        onSubmit={confirm}
        onCancel={reviewed ? () => setEditing(false) : manual ? () => setManual(false) : undefined}
      />
    )
  } else {
    body = <AIState occurrence={o} busy={busy} onReanalyze={reanalyze} onManual={() => setManual(true)} />
  }

  return (
    <article className={`review-card${reviewed && !editing ? ' is-reviewed' : ''}`} id={`ponto-${index}`}>
      <div className="review-media">
        <a href={api.photoURL(o.photo)} target="_blank" rel="noopener">
          <img src={api.photoURL(o.photo)} alt={`Foto do ponto ${index}`} loading="lazy" />
        </a>
        {/* Same number and color as the marker on the map. */}
        <span className="photo-number" data-category={o.category || o.ai?.category || undefined} aria-hidden="true">
          {index}
        </span>
        <p className="review-meta">
          <span className="index">Ponto {index}</span> {formatTime(o.captured_at)}
          {o.location ? (
            <a href={mapURL(o.location)} target="_blank" rel="noopener">
              <PinIcon width={14} height={14} /> mapa
            </a>
          ) : (
            <span className="subtle">sem local</span>
          )}
        </p>
      </div>
      <div className="review-body">
        {o.note && !(reviewed && !editing) && <p className="note">“{o.note}”</p>}
        {body}
        {error && (
          <p className="field-error" role="alert">
            {error}
          </p>
        )}
        {!(reviewed && !editing) && (
          <button className={`link-button danger${confirmDelete ? ' armed' : ''}`} disabled={busy} onClick={() => (confirmDelete ? void remove() : setConfirmDelete(true))}>
            <TrashIcon width={16} height={16} /> {confirmDelete ? 'Toque de novo para excluir' : 'Excluir ponto'}
          </button>
        )}
      </div>
    </article>
  )
}

function AIState({ occurrence: o, busy, onReanalyze, onManual }: { occurrence: Occurrence; busy: boolean; onReanalyze: (note: string) => void; onManual: () => void }) {
  const [extra, setExtra] = useState(o.note)
  const manual = (
    <button className="button ghost" onClick={onManual} disabled={busy}>
      <PencilIcon width={16} height={16} /> Preencher manualmente
    </button>
  )

  if (o.ai_status === 'pending' || o.ai_status === 'running') {
    return (
      <div className="ai-state">
        <p className="ai-status">
          <Spinner /> {o.ai_status === 'running' ? 'A IA está lendo a foto…' : 'Na fila da IA, aguardando a vez…'}
        </p>
        <div className="form-actions">{manual}</div>
      </div>
    )
  }
  if (o.ai_status === 'failed') {
    return (
      <div className="ai-state">
        <p className="ai-status warn-text">{o.ai_error || 'A análise falhou.'}</p>
        <div className="form-actions">
          <button className="button secondary" onClick={() => onReanalyze(o.note)} disabled={busy}>
            <RetryIcon width={16} height={16} /> Tentar de novo
          </button>
          {manual}
        </div>
      </div>
    )
  }
  // needs_info: the photo alone was not enough.
  const id = `extra-${o.id}`
  return (
    <form
      className="ai-state"
      onSubmit={(e) => {
        e.preventDefault()
        onReanalyze(extra)
      }}
    >
      <p className="ai-question">
        <SparkIcon width={18} height={18} />
        <span>{o.ai?.question}</span>
      </p>
      <label htmlFor={id} className="visually-hidden">
        Descrição complementar
      </label>
      <textarea id={id} rows={3} value={extra} onChange={(e) => setExtra(e.target.value)} placeholder="Descreva o problema que você viu" maxLength={1000} />
      <div className="form-actions">
        <button className="button primary" disabled={busy || extra.trim() === ''}>
          <SparkIcon width={16} height={16} /> Analisar de novo
        </button>
        {manual}
      </div>
    </form>
  )
}

function ReviewForm({ initial, suggestion, busy, onSubmit, onCancel }: { initial: Review; suggestion: Suggestion | null; busy: boolean; onSubmit: (r: Review) => void; onCancel?: () => void }) {
  const id = useId()
  const [category, setCategory] = useState<CategoryId>(initial.category)
  const [title, setTitle] = useState(initial.title)
  const [description, setDescription] = useState(initial.description)
  const [error, setError] = useState('')

  function submit(e: FormEvent) {
    e.preventDefault()
    if (!title.trim()) {
      setError('Dê um título curto ao problema.')
      return
    }
    onSubmit({ category, title: title.trim(), description: description.trim() })
  }

  return (
    <form className="review-form" onSubmit={submit} noValidate>
      {suggestion && (
        <p className="ai-badge">
          <SparkIcon width={16} height={16} /> Sugestão da IA, confiança {confidenceLabel[suggestion.confidence]}. Confira e corrija se precisar.
        </p>
      )}
      <fieldset className="category-picker">
        <legend>Categoria</legend>
        {categories.map((c) => (
          <label key={c.id} className="category-option" data-category={c.id}>
            <input type="radio" name={`${id}-category`} value={c.id} checked={category === c.id} onChange={() => setCategory(c.id)} />
            <span className="dot" aria-hidden="true" />
            {c.label}
            {suggestion?.category === c.id && category !== c.id && (
              <span className="suggested" title="Sugerida pela IA">
                <SparkIcon width={14} height={14} />
                <span className="visually-hidden">sugerida pela IA</span>
              </span>
            )}
          </label>
        ))}
      </fieldset>
      <label htmlFor={`${id}-title`} className="field-label">
        Título
      </label>
      <input
        id={`${id}-title`}
        value={title}
        onChange={(e) => {
          setTitle(e.target.value)
          setError('')
        }}
        maxLength={80}
        aria-invalid={error !== ''}
        enterKeyHint="next"
      />
      <label htmlFor={`${id}-description`} className="field-label">
        Descrição
      </label>
      <textarea id={`${id}-description`} rows={3} value={description} onChange={(e) => setDescription(e.target.value)} maxLength={1000} />
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      <div className="form-actions">
        <button className="button primary" disabled={busy}>
          {busy ? <Spinner /> : <CheckIcon width={18} height={18} />} Confirmar
        </button>
        {onCancel && (
          <button type="button" className="button ghost" onClick={onCancel} disabled={busy}>
            Cancelar
          </button>
        )}
      </div>
    </form>
  )
}
