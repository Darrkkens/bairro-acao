// Records the walk's route while the app is open. Browsers stop location for
// pages in the background, so with the screen off the map falls back to the
// places where photos were taken (see routeOf in geo.ts).
import { useEffect, useRef, useState } from 'react'
import { withStore } from './db'
import { distance, routeLength } from './geo'
import type { TrackPoint } from './types'

const MAX_ACCURACY_M = 50 // worse fixes zigzag across the street
const MIN_STEP_M = 12
const MAX_POINTS = 5000 // backend limit per walk

interface Row {
  walkId: string
  point: TrackPoint
}

export async function loadTrack(walkId: string): Promise<TrackPoint[]> {
  const rows = await withStore<Row[]>('track', 'readonly', (s) => s.index('walkId').getAll(walkId) as IDBRequest<Row[]>)
  return rows.map((r) => r.point)
}

export function clearTrack(walkId: string): Promise<void> {
  return withStore<void>('track', 'readwrite', (s) => {
    const keys = s.index('walkId').getAllKeys(walkId)
    keys.onsuccess = () => keys.result.forEach((key) => s.delete(key))
  })
}

function append(walkId: string, point: TrackPoint) {
  return withStore('track', 'readwrite', (s) => s.add({ walkId, point } satisfies Row))
}

/** Keeps the shape of very long walks within the upload limit. */
export function thin(points: TrackPoint[]): TrackPoint[] {
  if (points.length <= MAX_POINTS) return points
  const step = Math.ceil(points.length / (MAX_POINTS - 1))
  return [...points.filter((_, i) => i % step === 0), points[points.length - 1]]
}

export type GpsState = 'starting' | 'on' | 'searching' | 'paused' | 'denied' | 'unavailable'

export function useTracker(walkId: string | null): { gps: GpsState; meters: number } {
  const [gps, setGps] = useState<GpsState>('starting')
  const [meters, setMeters] = useState(0)
  const last = useRef<TrackPoint | null>(null)

  useEffect(() => {
    if (!walkId) return
    let alive = true
    let watch: number | null = null
    last.current = null
    setMeters(0)
    setGps('starting')
    // Resume after a reload or a closed tab.
    void loadTrack(walkId)
      .then((points) => {
        if (!alive || points.length === 0) return
        last.current ??= points[points.length - 1]
        setMeters(routeLength(points))
      })
      .catch(() => {})
    if (!window.isSecureContext || !('geolocation' in navigator)) {
      setGps('unavailable')
      return
    }
    const start = () => {
      if (watch !== null) return
      watch = navigator.geolocation.watchPosition(
        (p) => {
          setGps('on')
          if (p.coords.accuracy > MAX_ACCURACY_M) return
          const point: TrackPoint = { latitude: p.coords.latitude, longitude: p.coords.longitude, accuracy_m: Math.round(p.coords.accuracy), recorded_at: new Date(p.timestamp).toISOString() }
          const prev = last.current
          const step = prev ? distance(prev, point) : 0
          if (prev && step < MIN_STEP_M) return
          last.current = point
          setMeters((m) => m + step)
          void append(walkId, point).catch(() => {})
        },
        (e) => setGps(e.code === e.PERMISSION_DENIED ? 'denied' : 'searching'),
        { enableHighAccuracy: true, maximumAge: 5000, timeout: 30000 },
      )
    }
    const stop = () => {
      if (watch !== null) navigator.geolocation.clearWatch(watch)
      watch = null
    }
    const onVisibility = () => {
      if (document.visibilityState === 'visible') {
        start()
      } else {
        stop()
        setGps((state) => (state === 'denied' ? state : 'paused'))
      }
    }
    start()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      alive = false
      stop()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [walkId])

  return { gps, meters }
}
