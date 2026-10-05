import { categoryLabel } from '../categories'
import { SparkIcon } from '../icons'
import type { CategoryId, Occurrence } from '../types'

export function CategoryChip({ id }: { id: CategoryId | '' }) {
  return (
    <span className="chip category" data-category={id}>
      <span className="dot" aria-hidden="true" />
      {categoryLabel(id)}
    </span>
  )
}

export function AIChip({ occurrence: o }: { occurrence: Occurrence }) {
  if (o.reviewed_at) return <CategoryChip id={o.category} />
  switch (o.ai_status) {
    case 'pending':
      return <span className="chip muted">Na fila da IA</span>
    case 'running':
      return (
        <span className="chip muted">
          <span className="spinner" aria-hidden="true" /> IA analisando
        </span>
      )
    case 'done':
      return (
        <span className="chip ai">
          <SparkIcon width={14} height={14} /> {categoryLabel(o.ai?.category ?? '')}
        </span>
      )
    case 'needs_info':
      return <span className="chip warn">IA pediu detalhes</span>
    case 'failed':
      return <span className="chip danger">Falha na análise</span>
  }
}

export function Spinner() {
  return <span className="spinner" aria-hidden="true" />
}
