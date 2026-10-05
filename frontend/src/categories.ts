import type { CategoryId } from './types'

// Mirrors walk.Categories in the backend so the app works offline.
export const categories: { id: CategoryId; label: string; examples: string }[] = [
  { id: 'limpeza', label: 'Limpeza', examples: 'Lixo acumulado, descarte irregular' },
  { id: 'calcadas', label: 'Calçadas e acessibilidade', examples: 'Piso danificado, passagem obstruída' },
  { id: 'via_publica', label: 'Via pública', examples: 'Buraco, sinalização danificada' },
  { id: 'lazer', label: 'Espaços de lazer', examples: 'Banco quebrado, equipamento deteriorado' },
  { id: 'outros', label: 'Outros', examples: 'Situações que precisam de descrição manual' },
]

export function categoryLabel(id: CategoryId | ''): string {
  return categories.find((c) => c.id === id)?.label ?? 'Sem categoria'
}
