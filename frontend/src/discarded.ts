// Photos the server discarded as off-topic (a car, a profile picture). They are
// gone from the server; the walk screen tells the person so they can retake it.
import { useEffect, useState } from 'react'
import { subscribe } from './outbox'
import { load, save } from './storage'

export interface Discarded {
  id: string
  at: string
  looks: string
  note: string
}

const key = (walkId: string) => `bairro.discarded.${walkId}`

export function rememberDiscarded(walkId: string, item: Discarded): void {
  const list = load<Discarded[]>(key(walkId), [])
  if (!list.some((d) => d.id === item.id)) save(key(walkId), [...list, item])
}

export function forgetDiscarded(walkId: string, id: string): void {
  save(
    key(walkId),
    load<Discarded[]>(key(walkId), []).filter((d) => d.id !== id),
  )
}

export function useDiscarded(walkId: string): [Discarded[], (id: string) => void] {
  const [items, setItems] = useState<Discarded[]>(() => load(key(walkId), []))
  useEffect(() => {
    const refresh = () => setItems(load(key(walkId), []))
    refresh()
    return subscribe(refresh)
  }, [walkId])
  const dismiss = (id: string) => {
    forgetDiscarded(walkId, id)
    setItems(load(key(walkId), []))
  }
  return [items, dismiss]
}
