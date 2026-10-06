export type CategoryId = 'limpeza' | 'calcadas' | 'via_publica' | 'lazer' | 'outros'
export type AIStatus = 'pending' | 'running' | 'done' | 'needs_info' | 'failed' | 'grouped'

export interface Walk {
  id: string
  neighborhood: string
  city: string
  /** Two-letter UF code. */
  state: string
  started_at: string
  finished_at: string | null
}

export interface WalkSummary extends Walk {
  occurrences: number
  reviewed: number
}

export interface GeoPoint {
  latitude: number
  longitude: number
  accuracy_m?: number
}

export interface TrackPoint {
  latitude: number
  longitude: number
  accuracy_m?: number
  recorded_at: string
}

export interface Suggestion {
  category: CategoryId
  title: string
  description: string
  confidence: 'alta' | 'media' | 'baixa'
  question?: string
  model: string
}

export interface Occurrence {
  id: string
  walk_id: string
  photo: string
  note: string
  location: GeoPoint | null
  captured_at: string
  ai_status: AIStatus
  ai: Suggestion | null
  ai_error?: string
  category: CategoryId | ''
  title: string
  description: string
  reviewed_at: string | null
  /** Set on an extra photo of a point: the id of the point it belongs to. */
  group_id?: string
  /** A near-identical shot grouped automatically, without analysis. */
  duplicate?: boolean
  /** An earlier point this one probably shows again (same category, time, place, similar photo). */
  suggested_group?: { id: string; distance_m: number; seconds: number; similarity: number }
}

export interface WalkDetail extends Walk {
  occurrences: Occurrence[]
}

export interface Review {
  category: CategoryId
  title: string
  description: string
}

/** An occurrence recorded on the phone that may not have reached the server yet. */
export interface LocalOccurrence {
  id: string
  walk_id: string
  photo: Blob
  note: string
  location: GeoPoint | null
  captured_at: string
}

export interface Health {
  database: boolean
  ai: { model: string; available: boolean }
}
