// The walk happens outdoors, often with weak or no signal, and the AI runs on
// a computer at home. Everything recorded goes into this IndexedDB queue first
// and is sent in order whenever the server answers. The server accepts the same
// ID twice, so a retry after a dropped response is harmless.
import { useEffect, useState } from 'react'
import { api, ApiError } from './api'
import { withStore } from './db'
import type { LocalOccurrence, TrackPoint, Walk } from './types'

type Payload =
  | { kind: 'walk'; walk: Walk }
  | { kind: 'occurrence'; occurrence: LocalOccurrence }
  | { kind: 'finish'; walkId: string; finishedAt: string; track?: TrackPoint[] }

/** error is set when the server rejected the item; it then waits for the person. */
export type Op = Payload & { seq: number; error?: string }

const tx = <T,>(mode: IDBTransactionMode, run: (store: IDBObjectStore) => IDBRequest<T>) => withStore<T>('outbox', mode, run)

const listeners = new Set<() => void>()
function changed() {
  listeners.forEach((fn) => fn())
}
export function subscribe(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export const list = () => tx<Op[]>('readonly', (s) => s.getAll() as IDBRequest<Op[]>)

export async function enqueue(payload: Payload): Promise<void> {
  await tx('readwrite', (s) => s.add(payload))
  // Ask the browser not to evict queued photos under storage pressure.
  void navigator.storage?.persist?.().catch(() => false)
  changed()
  void sync()
}

export async function discard(seq: number): Promise<void> {
  await tx('readwrite', (s) => s.delete(seq))
  changed()
}

function send(op: Op): Promise<unknown> {
  switch (op.kind) {
    case 'walk':
      return api.startWalk(op.walk)
    case 'occurrence':
      return api.record(op.occurrence)
    case 'finish':
      return api.finishWalk(op.walkId, op.finishedAt, op.track ?? [])
  }
}

async function drain() {
  for (const op of await list()) {
    if (op.error) continue
    try {
      await send(op)
      await tx('readwrite', (s) => s.delete(op.seq))
    } catch (e) {
      if (e instanceof ApiError && !e.retryable) {
        await tx('readwrite', (s) => s.put({ ...op, error: e.message }))
        continue
      }
      return // offline or server busy: keep the order and try later
    } finally {
      changed()
    }
  }
}

let running: Promise<void> | null = null
let again = false

export function sync(): Promise<void> {
  if (running) {
    again = true
    return running
  }
  running = (async () => {
    do {
      again = false
      await drain()
    } while (again)
  })().finally(() => {
    running = null
  })
  return running
}

export function startBackgroundSync(): void {
  void sync()
  window.addEventListener('online', () => void sync())
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') void sync()
  })
  setInterval(() => {
    void list().then((ops) => {
      if (ops.some((op) => !op.error)) void sync()
    })
  }, 15000)
}

/** Items still on the phone, refreshed whenever the queue changes. */
export function useOutbox(): Op[] {
  const [ops, setOps] = useState<Op[]>([])
  useEffect(() => {
    let alive = true
    const refresh = () =>
      void list()
        .then((next) => alive && setOps(next))
        .catch(() => {})
    refresh()
    const unsubscribe = subscribe(refresh)
    return () => {
      alive = false
      unsubscribe()
    }
  }, [])
  return ops
}

export function belongsTo(op: Op, walkId: string): boolean {
  return op.kind === 'walk' ? op.walk.id === walkId : op.kind === 'occurrence' ? op.occurrence.walk_id === walkId : op.walkId === walkId
}
