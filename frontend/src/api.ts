import type { Health, LocalOccurrence, Occurrence, Review, TrackPoint, Walk, WalkDetail, WalkSummary } from './types'

const base = (import.meta.env.VITE_API_URL ?? '').replace(/\/$/, '')

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message)
  }

  /** No connection or a server-side hiccup: worth trying again later. */
  get retryable(): boolean {
    return this.status === 0 || this.status === 408 || this.status === 429 || this.status >= 500
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${base}/api${path}`, init)
  } catch {
    throw new ApiError('Sem conexão com o servidor.', 0)
  }
  if (res.status === 204) return undefined as T
  const body = await res.json().catch(() => null)
  if (!res.ok) throw new ApiError(body?.error ?? `O servidor respondeu com erro ${res.status}.`, res.status)
  return body as T
}

function json(method: string, body: unknown): RequestInit {
  return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }
}

export const api = {
  health: () => request<Health>('/health'),
  walks: () => request<WalkSummary[]>('/walks'),
  walk: (id: string) => request<WalkDetail>(`/walks/${id}`),
  startWalk: (w: Walk) => request<Walk>(`/walks/${w.id}`, json('PUT', { neighborhood: w.neighborhood, city: w.city, state: w.state, started_at: w.started_at })),
  finishWalk: (id: string, finishedAt: string, track: TrackPoint[]) => request<Walk>(`/walks/${id}/finish`, json('POST', { finished_at: finishedAt, track })),
  track: (id: string) => request<TrackPoint[]>(`/walks/${id}/track`),
  record(o: LocalOccurrence) {
    const form = new FormData()
    form.set('photo', o.photo, `${o.id}.jpg`)
    form.set('note', o.note)
    form.set('captured_at', o.captured_at)
    if (o.location) {
      form.set('latitude', String(o.location.latitude))
      form.set('longitude', String(o.location.longitude))
      if (o.location.accuracy_m !== undefined) form.set('accuracy_m', String(o.location.accuracy_m))
    }
    return request<Occurrence>(`/walks/${o.walk_id}/occurrences/${o.id}`, { method: 'PUT', body: form })
  },
  review: (id: string, r: Review) => request<Occurrence>(`/occurrences/${id}`, json('PATCH', r)),
  reanalyze: (id: string, note: string) => request<Occurrence>(`/occurrences/${id}/analyze`, json('POST', { note })),
  remove: (id: string) => request<void>(`/occurrences/${id}`, { method: 'DELETE' }),
  group: (id: string, withId: string) => request<void>(`/occurrences/${id}/group`, json('POST', { with: withId })),
  ungroup: (id: string) => request<void>(`/occurrences/${id}/ungroup`, { method: 'POST' }),
  keepSeparate: (id: string) => request<void>(`/occurrences/${id}/keep-separate`, { method: 'POST' }),
  neighborhoods: (code: string, city: string) => request<{ names: string[]; fetched_at: string }>(`/places/neighborhoods/${code}?city=${encodeURIComponent(city)}`),
  photoURL: (name: string) => `${base}/api/photos/${encodeURIComponent(name)}`,
  reportURL: (id: string, download = false) => `${base}/api/walks/${id}/report.html${download ? '?download' : ''}`,
}
