import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api'
import { subscribe } from './outbox'
import type { Health, WalkDetail } from './types'

/**
 * Server view of a walk. Polls quickly while the AI still has photos to read
 * and refreshes whenever the outbox delivers something.
 */
// Last server view per walk, so returning from the capture screen without
// signal still shows the points already sent.
const seen = new Map<string, WalkDetail>()

export function useWalkDetail(id: string) {
  const [data, setData] = useState<WalkDetail | null>(() => seen.get(id) ?? null)
  const [error, setError] = useState<ApiError | null>(null)
  const latest = useRef(0)

  const reload = useCallback(async () => {
    const call = ++latest.current
    try {
      const next = await api.walk(id)
      if (call === latest.current) {
        setData(next)
        setError(null)
      }
    } catch (e) {
      if (call === latest.current) setError(e instanceof ApiError ? e : new ApiError(String(e), 0))
    }
  }, [id])

  useEffect(() => {
    if (data) seen.set(id, data)
  }, [id, data])

  useEffect(() => {
    setData(seen.get(id) ?? null)
    void reload()
    return subscribe(() => void reload())
  }, [reload])

  const busy = data?.occurrences.some((o) => o.ai_status === 'pending' || o.ai_status === 'running') ?? false
  useEffect(() => {
    const timer = setInterval(() => {
      if (document.visibilityState === 'visible') void reload()
    }, busy ? 4000 : 20000)
    return () => clearInterval(timer)
  }, [busy, reload])

  return { data, error, reload, setData }
}

/** Current time, ticking every `ms`. */
export function useNow(ms = 1000): number {
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), ms)
    return () => clearInterval(timer)
  }, [ms])
  return now
}

export function useHealth(enabled: boolean): Health | null {
  const [health, setHealth] = useState<Health | null>(null)
  useEffect(() => {
    if (!enabled) return
    let alive = true
    const check = () =>
      api.health().then(
        (h) => alive && setHealth(h),
        () => alive && setHealth(null),
      )
    void check()
    const timer = setInterval(check, 30000)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [enabled])
  return health
}

/** Object URL for a Blob, revoked when the blob changes or the component unmounts. */
export function useObjectURL(blob: Blob | null | undefined): string | undefined {
  const [url, setUrl] = useState<string>()
  useEffect(() => {
    if (!blob) {
      setUrl(undefined)
      return
    }
    const next = URL.createObjectURL(blob)
    setUrl(next)
    return () => URL.revokeObjectURL(next)
  }, [blob])
  return url
}

export function useOnline(): boolean {
  const [online, setOnline] = useState(navigator.onLine)
  useEffect(() => {
    const update = () => setOnline(navigator.onLine)
    window.addEventListener('online', update)
    window.addEventListener('offline', update)
    return () => {
      window.removeEventListener('online', update)
      window.removeEventListener('offline', update)
    }
  }, [])
  return online
}
