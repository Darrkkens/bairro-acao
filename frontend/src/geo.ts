import type { GeoPoint, TrackPoint } from './types'

/** One location fix for the spot where the photo was taken. */
export function locate(timeoutMs = 12000): Promise<GeoPoint> {
  return new Promise((resolve, reject) => {
    if (!window.isSecureContext || !('geolocation' in navigator)) {
      reject(new Error('Localização exige HTTPS (use npm run phone).'))
      return
    }
    navigator.geolocation.getCurrentPosition(
      (p) => resolve({ latitude: p.coords.latitude, longitude: p.coords.longitude, accuracy_m: Math.round(p.coords.accuracy) }),
      (e) => reject(new Error(e.code === e.PERMISSION_DENIED ? 'Permissão de localização negada.' : 'Não foi possível obter a localização.')),
      { enableHighAccuracy: true, timeout: timeoutMs, maximumAge: 15000 },
    )
  })
}

type LatLng = { latitude: number; longitude: number }

/** Great-circle distance in meters. */
export function distance(a: LatLng, b: LatLng): number {
  const rad = Math.PI / 180
  const dLat = (b.latitude - a.latitude) * rad
  const dLng = (b.longitude - a.longitude) * rad
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(a.latitude * rad) * Math.cos(b.latitude * rad) * Math.sin(dLng / 2) ** 2
  return 2 * 6371000 * Math.asin(Math.min(1, Math.sqrt(h)))
}

export function routeLength(points: LatLng[]): number {
  let total = 0
  for (let i = 1; i < points.length; i++) total += distance(points[i - 1], points[i])
  return total
}

/** GPS track merged with photo locations in time order (mirrors walk.Route in the backend). */
export function routeOf(track: TrackPoint[], occurrences: { location: GeoPoint | null; captured_at: string }[]): TrackPoint[] {
  const photos = occurrences.flatMap((o) => (o.location ? [{ ...o.location, recorded_at: o.captured_at }] : []))
  return [...track, ...photos].sort((a, b) => Date.parse(a.recorded_at) - Date.parse(b.recorded_at))
}

export function formatDistance(meters: number): string {
  if (meters < 1000) return `${Math.round(meters / 10) * 10} m`
  return `${(meters / 1000).toLocaleString('pt-BR', { maximumFractionDigits: 1, minimumFractionDigits: 1 })} km`
}
