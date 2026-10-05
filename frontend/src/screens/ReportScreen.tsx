import { useEffect, useState } from 'react'
import { api } from '../api'
import { categories } from '../categories'
import { ErrorBoundary } from '../components/ErrorBoundary'
import { ReviewCard } from '../components/ReviewCard'
import { Spinner } from '../components/Status'
import { WalkMap } from '../components/WalkMap'
import { formatDate, formatDuration, formatTime, mapURL, plural } from '../format'
import { formatDistance, routeLength, routeOf } from '../geo'
import { useHealth, useNow, useWalkDetail } from '../hooks'
import { BackIcon, DownloadIcon, ExternalIcon, ShareIcon, SparkIcon } from '../icons'
import { belongsTo, useOutbox } from '../outbox'
import { navigate } from '../route'
import type { Occurrence, TrackPoint, WalkDetail } from '../types'
import { placeLabel } from './WalkScreen'

const coarsePointer = window.matchMedia('(pointer: coarse)').matches

export function ReportScreen({ id }: { id: string }) {
  const { data, error, reload, setData } = useWalkDetail(id)
  const ops = useOutbox().filter((op) => belongsTo(op, id))
  const waiting = ops.filter((op) => op.kind === 'occurrence' && !op.error).length
  const aiBusy = data?.occurrences.some((o) => o.ai_status === 'pending' || o.ai_status === 'running') ?? false
  const health = useHealth(aiBusy)
  const now = useNow(60000)
  const [track, setTrack] = useState<TrackPoint[] | null>(null)

  // The route arrives with the finish, so look again once the walk is closed.
  const loaded = data !== null
  const finishedAt = data?.finished_at
  useEffect(() => {
    if (!loaded) return
    let alive = true
    api.track(id).then(
      (points) => alive && setTrack(points),
      () => alive && setTrack((t) => t ?? []),
    )
    return () => {
      alive = false
    }
  }, [id, loaded, finishedAt])

  const replace = (o: Occurrence) => setData((d) => d && { ...d, occurrences: d.occurrences.map((x) => (x.id === o.id ? o : x)) })
  const drop = (occurrenceId: string) => setData((d) => d && { ...d, occurrences: d.occurrences.filter((x) => x.id !== occurrenceId) })

  const top = (
    <header className="bar">
      <button className="icon-button" onClick={() => navigate('#/')} aria-label="Voltar ao início">
        <BackIcon />
      </button>
      <h1>Revisão e relatório</h1>
      <span />
    </header>
  )

  if (!data) {
    let content = (
      <p className="empty">
        <Spinner /> Carregando caminhada…
      </p>
    )
    if (ops.length > 0) {
      content = (
        <div className="empty card">
          <h2>Aguardando envio</h2>
          <p>
            {plural(waiting, 'ponto está guardado', 'pontos estão guardados')} neste aparelho. Eles são enviados sozinhos quando o servidor do Bairro em Ação estiver ao alcance (por exemplo, na mesma rede Wi-Fi do
            computador).
          </p>
        </div>
      )
    } else if (error) {
      content = (
        <div className="empty card">
          <h2>{error.status === 404 ? 'Caminhada não encontrada' : 'Sem conexão com o servidor'}</h2>
          <p>{error.message}</p>
          <button className="button secondary" onClick={() => void reload()}>
            Tentar de novo
          </button>
        </div>
      )
    }
    return (
      <main className="screen report">
        {top}
        {content}
      </main>
    )
  }

  const total = data.occurrences.length
  const reviewed = data.occurrences.filter((o) => o.reviewed_at).length
  const end = data.finished_at ? Date.parse(data.finished_at) : now
  const route = routeOf(track ?? [], data.occurrences)
  const located = data.occurrences.filter((o) => o.location).length

  return (
    <main className="screen report">
      {top}
      <header className="report-header">
        <p className="eyebrow">{data.finished_at ? 'Caminhada finalizada' : 'Caminhada em andamento'}</p>
        <h2 className="title">{data.neighborhood}</h2>
        <p className="meta">
          {data.city && <>{placeLabel(data)} · </>}
          {formatDate(data.started_at)} · {formatTime(data.started_at)} · {formatDuration(end - Date.parse(data.started_at))}
        </p>
        {total > 0 && (
          <div className="progress">
            <div className="progress-track">
              <div className="progress-fill" style={{ transform: `scaleX(${reviewed / total})` }} />
            </div>
            <p>
              {reviewed === total ? 'Todos os pontos revisados' : `${reviewed} de ${plural(total, 'ponto revisado', 'pontos revisados')}`}
            </p>
          </div>
        )}
      </header>

      {waiting > 0 && <p className="banner">{plural(waiting, 'ponto ainda está', 'pontos ainda estão')} no aparelho, aguardando envio.</p>}
      {aiBusy && health && !health.ai.available && (
        <p className="banner warn">
          A IA ({health.ai.model}) não está respondendo. Os pontos continuam na fila e serão analisados quando o Ollama voltar. Se preferir, preencha manualmente.
        </p>
      )}
      {total === 0 && waiting === 0 && (
        <div className="empty card">
          <h2>Nenhum ponto registrado</h2>
          <p>Esta caminhada terminou sem registros. Que bom, ou fica para a próxima!</p>
        </div>
      )}

      {track !== null && route.length > 0 && (
        <section className="map-section" aria-labelledby="map-title">
          <div className="section-head">
            <h2 id="map-title">Trajeto</h2>
            <p>
              {route.length > 1 && <>{formatDistance(routeLength(route))} · </>}
              {plural(located, 'ponto no mapa', 'pontos no mapa')}
            </p>
          </div>
          <ErrorBoundary fallback={<p className="banner warn">Não foi possível mostrar o mapa. Os pontos continuam abaixo e no relatório.</p>}>
            <WalkMap occurrences={data.occurrences} track={track} />
          </ErrorBoundary>
          <p className="map-note">
            {track.length === 0 ? 'Sem trajeto gravado pelo GPS: a linha liga os pontos fotografados. ' : ''}
            {coarsePointer ? 'Use dois dedos para mover e dar zoom, ou abra em tela cheia. ' : 'Role para dar zoom e arraste para mover. '}Toque em um número para ir ao ponto.
          </p>
        </section>
      )}

      <section className="review-list" aria-label="Pontos para revisar">
        {data.occurrences.map((o, i) => (
          <ReviewCard key={o.id} index={i + 1} occurrence={o} onChange={replace} onRemove={drop} />
        ))}
      </section>

      {reviewed > 0 && <ReportPanel walk={data} reviewed={reviewed} total={total} />}
    </main>
  )
}

function summaryText(walk: WalkDetail): string {
  const confirmed = walk.occurrences.filter((o) => o.reviewed_at)
  const where = walk.city ? `${walk.neighborhood}, ${placeLabel(walk)}` : walk.neighborhood
  const lines = [`Bairro em Ação: caminhada em ${where}, ${formatDate(walk.started_at)}.`, `${plural(confirmed.length, 'problema registrado', 'problemas registrados')}:`, '']
  for (const c of categories) {
    const items = confirmed.filter((o) => o.category === c.id)
    if (items.length === 0) continue
    lines.push(`${c.label} (${items.length})`)
    for (const o of items) lines.push(`• ${o.title}${o.location ? ` · ${mapURL(o.location)}` : ''}`)
    lines.push('')
  }
  return lines.join('\n').trim()
}

function ReportPanel({ walk, reviewed, total }: { walk: WalkDetail; reviewed: number; total: number }) {
  const confirmed = walk.occurrences.filter((o) => o.reviewed_at)
  const counts = categories.map((c) => ({ ...c, n: confirmed.filter((o) => o.category === c.id).length })).filter((c) => c.n > 0)
  const compared = confirmed.filter((o) => o.ai)
  const matched = compared.filter((o) => o.ai?.category === o.category).length
  const [file, setFile] = useState<File | null>(null)
  const [status, setStatus] = useState('')

  // Fetch the report ahead of time: browsers only allow sharing right after a tap.
  const signature = confirmed.map((o) => o.id + o.reviewed_at).join()
  useEffect(() => {
    const controller = new AbortController()
    const timer = setTimeout(() => {
      fetch(api.reportURL(walk.id, true), { signal: controller.signal })
        .then(async (res) => {
          if (!res.ok) return
          const name = res.headers.get('Content-Disposition')?.match(/filename="?([^";]+)"?/)?.[1] ?? 'relatorio-bairro-em-acao.html'
          setFile(new File([await res.blob()], name, { type: 'text/html' }))
        })
        .catch(() => {})
    }, 600)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [walk.id, signature])

  async function share() {
    const text = summaryText(walk)
    const title = `Bairro em Ação · ${walk.neighborhood}`
    setStatus('')
    try {
      if (file && navigator.canShare?.({ files: [file] })) {
        await navigator.share({ title, text, files: [file] })
      } else if (navigator.share) {
        await navigator.share({ title, text })
      } else {
        await navigator.clipboard.writeText(text)
        setStatus('Resumo copiado. Cole na mensagem e anexe o relatório baixado.')
      }
    } catch (e) {
      if ((e as DOMException).name !== 'AbortError') setStatus('Não foi possível compartilhar daqui. Use “Baixar” e envie o arquivo.')
    }
  }

  return (
    <section className="report-panel card" aria-labelledby="report-title">
      <h2 id="report-title">{reviewed === total ? 'Relatório pronto' : 'Relatório parcial'}</h2>
      <p>{reviewed === total ? 'Fotos, locais e descrições organizados por categoria, em um arquivo que abre em qualquer celular, mesmo sem internet.' : `Os ${reviewed} pontos revisados entram no relatório. Revise os outros para incluí-los.`}</p>
      <ul className="category-counts">
        {counts.map((c) => (
          <li key={c.id} data-category={c.id}>
            <span className="dot" aria-hidden="true" />
            {c.label}
            <strong>{c.n}</strong>
          </li>
        ))}
      </ul>
      <div className="report-actions">
        <button className="button primary" onClick={() => void share()}>
          <ShareIcon /> Compartilhar
        </button>
        <a className="button secondary" href={api.reportURL(walk.id, true)} download>
          <DownloadIcon /> Baixar
        </a>
        <a className="button secondary" href={api.reportURL(walk.id)} target="_blank" rel="noopener">
          <ExternalIcon /> Abrir
        </a>
      </div>
      {status && (
        <p className="status" role="status">
          {status}
        </p>
      )}
      {compared.length > 0 && (
        <p className="ai-score">
          <SparkIcon width={16} height={16} /> A categoria sugerida pela IA foi mantida em {matched} de {plural(compared.length, 'ponto', 'pontos')}.
        </p>
      )}
    </section>
  )
}
