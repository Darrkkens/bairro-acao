import { useEffect, useState } from 'react'
import { enqueue } from './outbox'
import type { Place } from './places'
import { navigate, useRoute } from './route'
import { CaptureScreen } from './screens/CaptureScreen'
import { ReportScreen } from './screens/ReportScreen'
import { WalkScreen } from './screens/WalkScreen'
import { load, save } from './storage'
import { clearTrack, loadTrack, thin, useTracker } from './track'
import type { Walk } from './types'
import { uuid } from './uuid'

const ACTIVE_WALK = 'bairro.activeWalk'

export function App() {
  const route = useRoute()
  // The walk in progress lives on the phone, so it survives a reload or a closed tab.
  const [active, setActive] = useState<Walk | null>(() => load(ACTIVE_WALK, null))
  const [draft, setDraft] = useState<{ file: File; key: number } | null>(null)
  // Lives here, not in a screen, so the route keeps recording on the capture screen.
  const tracker = useTracker(active?.id ?? null)

  const capturing = route.name === 'capture' && active !== null && draft !== null
  useEffect(() => {
    if (route.name === 'capture' && !capturing) navigate('#/', { replace: true })
  }, [route.name, capturing])

  function updateActive(walk: Walk | null) {
    setActive(walk)
    save(ACTIVE_WALK, walk)
  }

  async function start(place: Place) {
    const walk: Walk = { id: uuid(), ...place, started_at: new Date().toISOString(), finished_at: null }
    await enqueue({ kind: 'walk', walk })
    updateActive(walk)
  }

  async function finish() {
    if (!active) return
    // The route travels with the finish, so it reaches the server in one piece.
    const track = thin(await loadTrack(active.id).catch(() => []))
    await enqueue({ kind: 'finish', walkId: active.id, finishedAt: new Date().toISOString(), track })
    await clearTrack(active.id).catch(() => {})
    updateActive(null)
    navigate(`#/caminhadas/${active.id}`)
  }

  function photoTaken(file: File) {
    setDraft({ file, key: Date.now() })
    if (route.name !== 'capture') navigate('#/registrar')
  }

  function closeCapture() {
    setDraft(null)
    history.back()
  }

  if (route.name === 'walk') return <ReportScreen key={route.id} id={route.id} />
  if (capturing && active && draft) return <CaptureScreen key={draft.key} file={draft.file} walk={active} onRetake={photoTaken} onClose={closeCapture} />
  return <WalkScreen walk={active} tracker={tracker} onStart={start} onPhoto={photoTaken} onFinish={finish} />
}
