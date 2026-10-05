import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { api } from '../api'
import { Autocomplete, type Results } from '../components/Autocomplete'
import { CaptureButton } from '../components/CaptureButton'
import { AIChip, Spinner } from '../components/Status'
import { formatClock, formatShortDate, plural } from '../format'
import { formatDistance, locate } from '../geo'
import { useNow, useObjectURL, useOnline, useWalkDetail } from '../hooks'
import { CameraIcon, PinIcon, RetryIcon } from '../icons'
import { discard, useOutbox, type Op } from '../outbox'
import { confirmCity, DEFAULT_PLACE, listNeighborhoods, matchesWords, normalize, reverseGeocode, searchCities, searchNeighborhoods, STATES, type City, type Neighborhood, type Place } from '../places'
import type { GpsState } from '../track'
import type { LocalOccurrence, Occurrence, Walk, WalkSummary } from '../types'

interface Props {
  walk: Walk | null
  tracker: { gps: GpsState; meters: number }
  onStart: (place: Place) => Promise<void>
  onPhoto: (file: File) => void
  onFinish: () => Promise<void>
}

export function WalkScreen({ walk, tracker, onStart, onPhoto, onFinish }: Props) {
  return walk ? <ActiveWalk walk={walk} tracker={tracker} onPhoto={onPhoto} onFinish={onFinish} /> : <StartWalk onStart={onStart} />
}

export function Brand() {
  return (
    <div className="brand">
      <img src="/icon.svg" alt="" width={28} height={28} />
      <span>Bairro em Ação</span>
    </div>
  )
}

const cityKey = (city: string, state: string) => `${normalize(city)}|${state}`

export function placeLabel(w: { city: string; state: string }): string {
  return w.city ? `${w.city}${w.state ? `/${w.state}` : ''}` : ''
}

type Detection =
  | { status: 'idle' }
  | { status: 'locating' }
  | { status: 'found' }
  | { status: 'no-neighborhood'; where: string }
  | { status: 'elsewhere'; where: string; found: Place }
  | { status: 'failed'; reason: string }

type Field = keyof Place

type ListState = { key: string; status: 'idle' | 'loading' | 'ready' | 'empty' | 'unavailable'; names: string[] }

// More would make a list nobody scrolls; typing filters the rest.
const MAX_LISTED = 300

function StartWalk({ onStart }: { onStart: Props['onStart'] }) {
  const [place, setPlace] = useState<Place>({ neighborhood: '', ...DEFAULT_PLACE })
  const placeRef = useRef(place)
  placeRef.current = place
  // Fields the person edited are never overwritten by detection.
  const touched = useRef<Record<Field, boolean>>({ neighborhood: false, city: false, state: false })
  const [detection, setDetection] = useState<Detection>({ status: 'locating' })
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({})
  const [busy, setBusy] = useState(false)
  const [walks, setWalks] = useState<WalkSummary[]>([])
  const neighborhoodInput = useRef<HTMLInputElement>(null)
  // The city the neighborhood was found in (detection or a picked suggestion); null when typed by hand.
  const neighborhoodOf = useRef<string | null>(null)
  const [notice, setNotice] = useState('')
  const [list, setList] = useState<ListState>({ key: '', status: 'idle', names: [] })
  const [listAttempt, setListAttempt] = useState(0)
  const run = useRef(0)

  const city = place.city.trim()
  const currentKey = cityKey(city, place.state)
  const listReady = list.status === 'ready' && list.key === currentKey

  const detect = useCallback(async () => {
    const call = ++run.current
    setDetection({ status: 'locating' })
    try {
      const found = await reverseGeocode(await locate(15000))
      let detectedCity = found.city ?? ''
      if (detectedCity && found.state) detectedCity = (await confirmCity(detectedCity, found.state).catch(() => null)) ?? detectedCity
      if (call !== run.current) return
      const where = placeLabel({ city: detectedCity, state: found.state ?? '' })
      const current = placeRef.current
      // The default city always wins; another detected city is only offered.
      if (current.city.trim() && cityKey(detectedCity, found.state ?? '') !== cityKey(current.city.trim(), current.state)) {
        setDetection(detectedCity ? { status: 'elsewhere', where, found: { neighborhood: found.neighborhood ?? '', city: detectedCity, state: found.state ?? '' } } : { status: 'failed', reason: 'A localização não indicou uma cidade.' })
        return
      }
      setPlace((p) => ({
        neighborhood: touched.current.neighborhood ? p.neighborhood : (found.neighborhood ?? ''),
        city: touched.current.city || !detectedCity ? p.city : detectedCity,
        state: touched.current.state || !found.state ? p.state : found.state,
      }))
      if (found.neighborhood && !touched.current.neighborhood) neighborhoodOf.current = cityKey(detectedCity, found.state ?? '')
      if (found.neighborhood) {
        setDetection({ status: 'found' })
      } else {
        setDetection({ status: 'no-neighborhood', where })
        neighborhoodInput.current?.focus()
      }
    } catch (e) {
      if (call === run.current) setDetection({ status: 'failed', reason: (e as Error).message })
    }
  }, [])

  useEffect(() => {
    void detect()
    api.walks().then(setWalks, () => {})
    return () => {
      run.current++ // ignore a lookup that finishes after the screen closed
    }
  }, [detect])

  // Load the city's neighborhoods as soon as city and UF are known, so the list is ready on focus.
  useEffect(() => {
    const key = cityKey(city, place.state)
    if (city.length < 2 || !place.state) {
      setList({ key, status: 'idle', names: [] })
      return
    }
    const controller = new AbortController()
    const timer = setTimeout(() => {
      setList({ key, status: 'loading', names: [] })
      listNeighborhoods(city, place.state, controller.signal).then(
        (result) => {
          if (result.status === 'ok') setList({ key, status: result.names.length > 0 ? 'ready' : 'empty', names: result.names })
          else setList({ key, status: result.status === 'unknown-city' ? 'idle' : 'unavailable', names: [] })
        },
        () => {},
      )
    }, 400)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [city, place.state, listAttempt])

  function update(field: Field, value: string) {
    touched.current[field] = true
    if (field === 'neighborhood') {
      neighborhoodOf.current = null
      setNotice('')
    }
    // The banner described what detection filled in; after an edit it no longer applies.
    if (detection.status === 'found' || detection.status === 'no-neighborhood') setDetection({ status: 'idle' })
    setPlace((p) => ({ ...p, [field]: value }))
    setErrors((e) => ({ ...e, [field]: undefined }))
  }

  function applyDetected(found: Place) {
    touched.current = { neighborhood: Boolean(found.neighborhood), city: true, state: true }
    neighborhoodOf.current = found.neighborhood ? cityKey(found.city, found.state) : null
    setPlace(found)
    setErrors({})
    setNotice('')
    setDetection(found.neighborhood ? { status: 'found' } : { status: 'no-neighborhood', where: placeLabel(found) })
  }

  // A neighborhood brings its city and UF along.
  function pickNeighborhood(n: Neighborhood) {
    touched.current = { neighborhood: true, city: true, state: true }
    neighborhoodOf.current = cityKey(n.city, n.state)
    setPlace({ neighborhood: n.name, city: n.city, state: n.state })
    setErrors({})
    setNotice('')
    if (detection.status === 'no-neighborhood') setDetection({ status: 'idle' })
  }

  // A different city makes a neighborhood found for the old one meaningless.
  function pickCity(picked: City) {
    touched.current.city = touched.current.state = true
    const changed = cityKey(picked.name, picked.state) !== currentKey
    const stale = neighborhoodOf.current !== null && neighborhoodOf.current !== cityKey(picked.name, picked.state)
    setPlace((p) => ({ ...p, city: picked.name, state: picked.state, neighborhood: stale ? '' : p.neighborhood }))
    setErrors((e) => ({ ...e, city: undefined, state: undefined }))
    if (stale) {
      neighborhoodOf.current = null
      setNotice(`O bairro anterior era de outra cidade.`)
    }
    // Next step: the neighborhood, whose list is loading now.
    if (changed && (stale || !place.neighborhood.trim())) setTimeout(() => neighborhoodInput.current?.focus(), 0)
  }

  // Typed in full without tapping a suggestion: accept it when the match is unambiguous.
  function commitNeighborhood(items: Neighborhood[]) {
    const exact = items.filter((n) => !n.custom && normalize(n.name) === normalize(place.neighborhood))
    if (exact.length > 0 && new Set(exact.map((n) => cityKey(n.city, n.state))).size === 1 && (!city || currentKey === cityKey(exact[0].city, exact[0].state))) {
      pickNeighborhood(exact[0])
    }
  }

  function commitCity(items: City[]) {
    const exact = items.filter((c) => normalize(c.name) === normalize(place.city) && (!place.state || c.state === place.state))
    if (exact.length === 1 && (exact[0].name !== place.city || exact[0].state !== place.state)) pickCity(exact[0])
  }

  async function searchNeighborhood(q: string, signal: AbortSignal): Promise<Results<Neighborhood>> {
    const typed = q.trim()
    if (listReady) {
      const exact = list.names.find((n) => normalize(n) === normalize(typed))
      // An exact name shows the whole list again, so another one can be chosen.
      const shown = !typed || exact ? list.names : list.names.filter((n) => matchesWords(n, typed))
      const items: Neighborhood[] = shown.slice(0, MAX_LISTED).map((name) => ({ name, city, state: place.state }))
      if (typed.length >= 2 && !exact) items.push({ name: typed, city, state: place.state, custom: true })
      let note: string | undefined
      if (shown.length === 0 && typed) note = `Nenhum bairro de ${city} na lista começa assim.`
      else if (shown.length > MAX_LISTED) note = `Mostrando ${MAX_LISTED} de ${shown.length}. Digite para filtrar.`
      return { items, note }
    }
    // No list for this city (or no city yet): search by name, which also finds the city.
    const { items, elsewhere } = await searchNeighborhoods(q, place.city, place.state, signal)
    return { items, note: elsewhere && items.length > 0 ? `Nenhum bairro com esse nome em ${city}. Em outras cidades:` : undefined }
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    const clean = (s: string) => s.trim().replace(/\s+/g, ' ')
    const next: Place = { neighborhood: clean(place.neighborhood), city: clean(place.city), state: place.state }
    const problems: Partial<Record<Field, string>> = {}
    if (next.city.length < 2) problems.city = 'Informe a cidade.'
    if (!next.state) problems.state = 'Escolha a UF.'
    if (next.neighborhood.length < 2) problems.neighborhood = 'Escolha ou digite o bairro.'
    setErrors(problems)
    if (Object.keys(problems).length > 0) return
    setBusy(true)
    try {
      await onStart(next)
    } catch {
      setErrors({ neighborhood: 'Não foi possível guardar a caminhada neste aparelho. Verifique se o navegador permite armazenamento.' })
      setBusy(false)
    }
  }

  let listStatus = ''
  if (list.key === currentKey) {
    if (list.status === 'loading') listStatus = `Buscando os bairros de ${city}…`
    if (list.status === 'ready') listStatus = `${plural(list.names.length, 'bairro', 'bairros')} de ${city}. Escolha na lista ou digite outro.`
    if (list.status === 'empty') listStatus = `O OpenStreetMap ainda não tem bairros cadastrados em ${city}. Digite o nome do bairro.`
    if (list.status === 'unavailable') listStatus = `Não foi possível carregar a lista de bairros de ${city} agora. Digite o nome do bairro ou tente de novo.`
  }

  let placeholder = 'Busque pelo nome, ex.: Vila Mariana'
  if (detection.status === 'locating') placeholder = 'Procurando…'
  else if (listReady) placeholder = 'Toque para ver a lista ou digite'
  else if (city) placeholder = 'Digite o nome do bairro'

  return (
    <main className="screen start">
      <Brand />
      <section className="intro">
        <h1>Caminhe pelo bairro. Registre o que precisa de cuidado.</h1>
        <p>Fotografe os problemas pelo caminho e guarde o celular. Depois, uma IA aberta rodando no seu computador organiza tudo em um relatório com mapa, pronto para enviar.</p>
      </section>

      <form className="card start-form" onSubmit={submit} noValidate>
        <DetectionStatus detection={detection} onRetry={() => void detect()} onUse={applyDetected} current={placeLabel({ city, state: place.state })} />

        <div className="field-row">
          <Autocomplete<City>
            id="city"
            label="Cidade"
            value={place.city}
            onChange={(v) => update('city', v)}
            search={async (q, signal) => {
              const items = await searchCities(q, place.state, signal)
              if (items.length > 0 || !place.state) return { items }
              // Typed a city from another state: offer it and let the pick change the UF.
              const elsewhere = await searchCities(q, '', signal)
              return { items: elsewhere, note: elsewhere.length > 0 ? `Nenhuma cidade com esse nome em ${place.state}. Em outros estados:` : undefined }
            }}
            itemKey={(c) => cityKey(c.name, c.state)}
            renderItem={(c) => (
              <>
                <span className="option-main">{c.name}</span>
                <span className="option-sub">{c.state}</span>
              </>
            )}
            onPick={pickCity}
            onCommit={commitCity}
            error={errors.city}
            className="city-field"
            inputProps={{ placeholder: 'Ex.: Joaçaba', autoCapitalize: 'words', enterKeyHint: 'next', maxLength: 80 }}
          />
          <div className="field state-field">
            <label htmlFor="state">UF</label>
            <select id="state" value={place.state} onChange={(e) => update('state', e.target.value)} aria-invalid={errors.state ? true : undefined}>
              <option value="">—</option>
              {STATES.map((s) => (
                <option key={s.uf} value={s.uf}>
                  {s.uf}
                </option>
              ))}
            </select>
            {errors.state && (
              <p className="field-error" role="alert">
                {errors.state}
              </p>
            )}
          </div>
        </div>

        <Autocomplete<Neighborhood>
          id="neighborhood"
          label="Bairro"
          value={place.neighborhood}
          onChange={(v) => update('neighborhood', v)}
          search={searchNeighborhood}
          refreshKey={`${list.key}:${list.status}`}
          itemKey={(n) => (n.custom ? 'custom:' : '') + cityKey(n.city, n.state) + normalize(n.name)}
          renderItem={(n) =>
            n.custom ? (
              <>
                <span className="option-main option-custom">Usar “{n.name}”</span>
                <span className="option-sub">fora da lista</span>
              </>
            ) : (
              <>
                <span className="option-main">{n.name}</span>
                {!listReady && <span className="option-sub">{placeLabel(n)}</span>}
              </>
            )
          }
          onPick={pickNeighborhood}
          onCommit={commitNeighborhood}
          error={errors.neighborhood}
          notice={
            (notice || listStatus) && (
              <p className={`field-note${list.status === 'unavailable' || notice ? ' warn-text' : ''}`} role="status">
                {list.status === 'loading' && list.key === currentKey && <Spinner />} {notice} {listStatus}
                {list.status === 'unavailable' && list.key === currentKey && (
                  <button type="button" className="link-button" onClick={() => setListAttempt((n) => n + 1)}>
                    <RetryIcon width={16} height={16} /> Tentar de novo
                  </button>
                )}
              </p>
            )
          }
          inputRef={neighborhoodInput}
          listClassName="wide tall"
          inputProps={{
            placeholder,
            autoCapitalize: 'words',
            enterKeyHint: 'done',
            maxLength: 80,
            'aria-invalid': detection.status === 'no-neighborhood' ? true : undefined,
          }}
        />

        <button className="button primary large" disabled={busy}>
          Começar caminhada
        </button>
      </form>

      <ol className="steps">
        <li>
          <p>
            <strong>Caminhe e fotografe.</strong> Um toque abre a câmera; funciona sem sinal. O trajeto é gravado enquanto o app está aberto.
          </p>
        </li>
        <li>
          <p>
            <strong>A IA organiza.</strong> Gemma 3 sugere categoria, título e descrição, sem mandar suas fotos para nenhuma nuvem.
          </p>
        </li>
        <li>
          <p>
            <strong>Revise e compartilhe.</strong> Você confirma cada ponto e gera o relatório com o mapa do trajeto.
          </p>
        </li>
      </ol>

      {walks.length > 0 && (
        <section className="history">
          <h2>Caminhadas anteriores</h2>
          <ul>
            {walks.map((w) => (
              <li key={w.id}>
                <a href={`#/caminhadas/${w.id}`}>
                  <span className="history-name">
                    {w.neighborhood}
                    {w.city && <span className="history-city"> · {placeLabel(w)}</span>}
                  </span>
                  <span className="history-meta">
                    {formatShortDate(w.started_at)} · {plural(w.occurrences, 'ponto', 'pontos')}
                    {w.occurrences > 0 && w.reviewed < w.occurrences ? ` · ${w.occurrences - w.reviewed} para revisar` : ''}
                  </span>
                </a>
              </li>
            ))}
          </ul>
        </section>
      )}
    </main>
  )
}

function DetectionStatus({ detection, onRetry, onUse, current }: { detection: Detection; onRetry: () => void; onUse: (p: Place) => void; current: string }) {
  const retry = (label: string) => (
    <button type="button" className="link-button" onClick={onRetry}>
      <RetryIcon width={16} height={16} /> {label}
    </button>
  )
  switch (detection.status) {
    case 'idle':
      return <div className="detect-link">{retry('Usar minha localização')}</div>
    case 'locating':
      return (
        <p className="detect" role="status">
          <Spinner /> Procurando onde você está…
        </p>
      )
    case 'found':
      return (
        <div className="detect" role="status">
          <PinIcon width={18} height={18} />
          <p>
            Preenchido pela sua localização. Confira antes de começar.
            <br />
            {retry('Detectar de novo')}
          </p>
        </div>
      )
    case 'elsewhere':
      return (
        <div className="detect" role="status">
          <PinIcon width={18} height={18} />
          <p>
            Sua localização indica {detection.found.neighborhood ? `${detection.found.neighborhood}, ` : ''}
            {detection.where}. A caminhada está em {current || 'outra cidade'}.
            <br />
            <button type="button" className="link-button" onClick={() => onUse(detection.found)}>
              <PinIcon width={16} height={16} /> Usar {detection.where}
            </button>
          </p>
        </div>
      )
    case 'no-neighborhood':
      return (
        <div className="detect warn" role="alert">
          <PinIcon width={18} height={18} />
          <p>
            {detection.where ? `Encontramos ${detection.where}, mas não o bairro.` : 'Não encontramos o bairro pela localização.'} <strong>Escolha ou digite o bairro.</strong>
          </p>
        </div>
      )
    case 'failed':
      return (
        <div className="detect warn" role="status">
          <PinIcon width={18} height={18} />
          <p>
            Não conseguimos detectar onde você está. {detection.reason} {current ? `Escolha o bairro de ${current} na lista.` : 'Preencha cidade, UF e bairro.'}
            <br />
            {retry('Tentar de novo')}
          </p>
        </div>
      )
  }
}

type LocalOp = Op & { kind: 'occurrence' }
type Point = { kind: 'local'; op: LocalOp; at: string } | { kind: 'remote'; occurrence: Occurrence; at: string }

const gpsText: Partial<Record<GpsState, string>> = {
  searching: 'procurando sinal de GPS',
  denied: 'GPS sem permissão: o mapa usará só os pontos fotografados',
  unavailable: 'trajeto indisponível neste navegador (precisa de HTTPS)',
}

function ActiveWalk({ walk, tracker, onPhoto, onFinish }: { walk: Walk; tracker: Props['tracker']; onPhoto: Props['onPhoto']; onFinish: Props['onFinish'] }) {
  const ops = useOutbox()
  const { data } = useWalkDetail(walk.id)
  const now = useNow()
  const online = useOnline()
  const [confirming, setConfirming] = useState(false)

  useEffect(() => {
    if (!confirming) return
    const timer = setTimeout(() => setConfirming(false), 4000)
    return () => clearTimeout(timer)
  }, [confirming])

  const remote = data?.occurrences ?? []
  const stored = new Set(remote.map((o) => o.id))
  const local = ops.filter((op): op is LocalOp => op.kind === 'occurrence' && op.occurrence.walk_id === walk.id && !stored.has(op.occurrence.id))
  const points: Point[] = [
    ...local.map((op): Point => ({ kind: 'local', op, at: op.occurrence.captured_at })),
    ...remote.map((occurrence): Point => ({ kind: 'remote', occurrence, at: occurrence.captured_at })),
  ].sort((a, b) => b.at.localeCompare(a.at))
  const waiting = local.filter((op) => !op.error).length

  let sync = 'tudo enviado ao servidor'
  if (waiting > 0) sync = online ? `enviando ${plural(waiting, 'foto', 'fotos')}…` : `sem sinal: ${plural(waiting, 'foto guardada', 'fotos guardadas')} no aparelho`
  else if (points.length === 0) sync = online ? 'pronto para registrar' : 'sem sinal: tudo bem, as fotos ficam no aparelho'

  return (
    <main className="screen walking">
      <header className="walk-header">
        <div>
          <p className="eyebrow">Caminhando em</p>
          <h1>{walk.neighborhood}</h1>
          {walk.city && <p className="walk-city">{placeLabel(walk)}</p>}
        </div>
        <p className="clock" aria-label="Tempo de caminhada">
          {formatClock(now - Date.parse(walk.started_at))}
        </p>
      </header>

      <p className="walk-status" aria-live="polite">
        <strong>{plural(points.length, 'ponto', 'pontos')}</strong>
        {tracker.meters > 0 && <> · {formatDistance(tracker.meters)}</>} · {sync}
      </p>
      {gpsText[tracker.gps] && <p className="gps-note">{gpsText[tracker.gps]}</p>}

      <CaptureButton onPhoto={onPhoto} className="capture-hero">
        <CameraIcon width={44} height={44} strokeWidth={1.6} />
        <span>Registrar ponto</span>
      </CaptureButton>
      <p className="hint">Foto, uma palavra se quiser, e siga caminhando. A IA analisa em segundo plano e você revisa tudo no final, com o mapa do trajeto.</p>

      {points.length > 0 && (
        <ol className="points" aria-label="Pontos desta caminhada">
          {points.map((p) => (p.kind === 'local' ? <LocalPoint key={p.op.occurrence.id} op={p.op} /> : <RemotePoint key={p.occurrence.id} occurrence={p.occurrence} />))}
        </ol>
      )}

      <div className="finish-bar">
        <button className={`button large ${confirming ? 'danger' : 'secondary'}`} onClick={() => (confirming ? void onFinish() : setConfirming(true))}>
          {confirming ? 'Toque de novo para finalizar' : 'Finalizar caminhada'}
        </button>
      </div>
    </main>
  )
}

function LocalPoint({ op }: { op: LocalOp }) {
  const o: LocalOccurrence = op.occurrence
  const url = useObjectURL(o.photo)
  return (
    <li className="point">
      <img src={url} alt="" />
      <div>
        <p className="point-title">{o.note || 'Sem observação'}</p>
        {op.error ? <span className="chip danger">{op.error}</span> : <span className="chip muted">No aparelho, aguardando envio</span>}
      </div>
      {op.error && (
        <button className="button ghost small" onClick={() => void discard(op.seq)}>
          Descartar
        </button>
      )}
    </li>
  )
}

function RemotePoint({ occurrence: o }: { occurrence: Occurrence }) {
  return (
    <li className="point">
      <img src={api.photoURL(o.photo)} alt="" loading="lazy" />
      <div>
        <p className="point-title">{o.title || o.ai?.title || o.note || 'Sem observação'}</p>
        <AIChip occurrence={o} />
      </div>
    </li>
  )
}
