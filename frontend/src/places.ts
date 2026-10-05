// Where the walk happens.
// - Detection: OpenStreetMap's Nominatim (reverse geocoding, one request per walk).
// - Cities: BrasilAPI (CPTEC search, then the IBGE municipality list, which keeps
//   working when CPTEC is down; it returned 500 for every query when this was
//   written), with IBGE's own API as a fallback.
// - A city's neighborhoods: the Overpass API over OpenStreetMap, by IBGE code.
// - Typed searches: Photon, an open-source geocoder over OpenStreetMap built for
//   search-as-you-type, which Nominatim's usage policy forbids.
import { api, ApiError } from './api'
import { load, save } from './storage'
import type { GeoPoint } from './types'

const BRASILAPI = 'https://brasilapi.com.br/api'
const PHOTON = 'https://photon.komoot.io/api/'
const IBGE = 'https://servicodados.ibge.gov.br/api/v1/localidades'
// Only used when the Bairro em Ação server is out of reach; it queries (and caches) these itself.
const OVERPASS = ['https://overpass-api.de/api/interpreter', 'https://maps.mail.ru/osm/tools/overpass/api/interpreter']
const BRAZIL_BBOX = '-74.1,-33.8,-34.7,5.3'

export const STATES: { uf: string; name: string }[] = [
  { uf: 'AC', name: 'Acre' }, { uf: 'AL', name: 'Alagoas' }, { uf: 'AP', name: 'Amapá' }, { uf: 'AM', name: 'Amazonas' },
  { uf: 'BA', name: 'Bahia' }, { uf: 'CE', name: 'Ceará' }, { uf: 'DF', name: 'Distrito Federal' }, { uf: 'ES', name: 'Espírito Santo' },
  { uf: 'GO', name: 'Goiás' }, { uf: 'MA', name: 'Maranhão' }, { uf: 'MT', name: 'Mato Grosso' }, { uf: 'MS', name: 'Mato Grosso do Sul' },
  { uf: 'MG', name: 'Minas Gerais' }, { uf: 'PA', name: 'Pará' }, { uf: 'PB', name: 'Paraíba' }, { uf: 'PR', name: 'Paraná' },
  { uf: 'PE', name: 'Pernambuco' }, { uf: 'PI', name: 'Piauí' }, { uf: 'RJ', name: 'Rio de Janeiro' }, { uf: 'RN', name: 'Rio Grande do Norte' },
  { uf: 'RS', name: 'Rio Grande do Sul' }, { uf: 'RO', name: 'Rondônia' }, { uf: 'RR', name: 'Roraima' }, { uf: 'SC', name: 'Santa Catarina' },
  { uf: 'SP', name: 'São Paulo' }, { uf: 'SE', name: 'Sergipe' }, { uf: 'TO', name: 'Tocantins' },
]

/** Where every walk starts by default; set VITE_DEFAULT_CITY / VITE_DEFAULT_STATE to change it. */
export const DEFAULT_PLACE = {
  city: (import.meta.env.VITE_DEFAULT_CITY as string | undefined) || 'Joaçaba',
  state: (import.meta.env.VITE_DEFAULT_STATE as string | undefined) || 'SC',
}

export interface Place {
  neighborhood: string
  city: string
  state: string
}

export interface City {
  name: string
  state: string
  /** IBGE municipality code, when known. */
  code?: string
}

export interface Neighborhood {
  name: string
  city: string
  state: string
  /** A name typed by the person that is not in the city's list. */
  custom?: boolean
}

export function normalize(s: string): string {
  return s.normalize('NFD').replace(/\p{Diacritic}/gu, '').toLowerCase().replace(/\s+/g, ' ').trim()
}

const LOWER = new Set(['de', 'da', 'do', 'das', 'dos', 'e'])

/** "SÃO JOSÉ DOS CAMPOS" → "São José dos Campos"; IBGE lists names in capitals. */
export function titleCase(name: string): string {
  return name
    .toLowerCase()
    .split(' ')
    .map((word, i) => {
      if (i > 0 && LOWER.has(word)) return word
      if (word.startsWith("d'")) return "d'" + word.charAt(2).toUpperCase() + word.slice(3)
      return word.replace(/(^|-)(\p{L})/gu, (_, sep: string, letter: string) => sep + letter.toUpperCase())
    })
    .join(' ')
}

/**
 * Neighborhood, city and UF for a location. Coordinates are rounded to about
 * 100 m before leaving the phone: enough for the neighborhood, not the house.
 */
export async function reverseGeocode(p: GeoPoint, signal?: AbortSignal): Promise<Partial<Place>> {
  const params = new URLSearchParams({ format: 'jsonv2', zoom: '16', addressdetails: '1', 'accept-language': 'pt-BR', lat: p.latitude.toFixed(3), lon: p.longitude.toFixed(3) })
  let res: Response
  try {
    res = await fetch(`https://nominatim.openstreetmap.org/reverse?${params}`, { signal: signal ?? AbortSignal.timeout(10000) })
  } catch {
    throw new Error('Sem internet para consultar o mapa.')
  }
  if (!res.ok) throw new Error('O serviço de mapas não respondeu.')
  const { address = {} } = (await res.json()) as { address?: Record<string, string> }
  const uf = /^BR-([A-Z]{2})$/.exec(address['ISO3166-2-lvl4'] ?? '')?.[1] ?? ''
  const city = address.city ?? address.town ?? address.village ?? address.municipality ?? ''
  // Rural areas often report the municipality itself as the district: that is not a neighborhood.
  const neighborhood = [address.suburb, address.neighbourhood, address.quarter, address.city_district].find((n) => n && normalize(n) !== normalize(city)) ?? ''
  return { neighborhood, city, state: STATES.some((s) => s.uf === uf) ? uf : '' }
}

function ufFromName(stateName: string | undefined): string {
  return STATES.find((s) => normalize(s.name) === normalize(stateName ?? ''))?.uf ?? ''
}

/** Every typed word starts a word of the name: "vila mar" finds "Jardim Vila Mariana". */
export function matchesWords(name: string, query: string): boolean {
  const words = normalize(name).split(/[\s-]+/)
  return normalize(query)
    .split(' ')
    .every((t) => words.some((w) => w.startsWith(t)))
}

interface PhotonFeature {
  properties: { name?: string; city?: string; state?: string; countrycode?: string; osm_value?: string }
}

const photonCache = new Map<string, PhotonFeature[]>()

async function photon(params: Record<string, string | string[]>, signal?: AbortSignal): Promise<PhotonFeature[]> {
  const query = new URLSearchParams({ limit: '15', bbox: BRAZIL_BBOX })
  for (const [key, value] of Object.entries(params)) for (const v of [value].flat()) query.append(key, v)
  const key = query.toString()
  const cached = photonCache.get(key)
  if (cached) return cached
  let res: Response
  try {
    res = await fetch(`${PHOTON}?${key}`, { signal })
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e
    throw new Error('Sem internet para buscar endereços.')
  }
  if (!res.ok) throw new Error('A busca de endereços não respondeu.')
  const features = ((await res.json()) as { features?: PhotonFeature[] }).features ?? []
  photonCache.set(key, features)
  return features
}

function uniqueBy<T>(list: T[], key: (item: T) => string): T[] {
  const seen = new Set<string>()
  return list.filter((item) => !seen.has(key(item)) && Boolean(seen.add(key(item))))
}

function rankByName<T extends { name: string }>(list: T[], query: string): T[] {
  const q = normalize(query)
  return list
    .filter((item) => matchesWords(item.name, query))
    .sort((a, b) => Number(normalize(b.name).startsWith(q)) - Number(normalize(a.name).startsWith(q)) || a.name.length - b.name.length || a.name.localeCompare(b.name, 'pt-BR'))
    .slice(0, 6)
}

async function photonNeighborhoods(q: string, signal?: AbortSignal): Promise<Neighborhood[]> {
  const features = await photon({ q, osm_tag: ['place:suburb', 'place:neighbourhood', 'place:quarter'] }, signal)
  return features.flatMap(({ properties: p }) => {
    const state = ufFromName(p.state)
    return p.countrycode === 'BR' && p.name && p.city && state ? [{ name: p.name, city: p.city, state }] : []
  })
}

/**
 * Neighborhoods matching what was typed, each with its city and UF. With a city
 * chosen, only that city's neighborhoods; if it has none by that name, other
 * cities' are offered (`elsewhere`) so picking one moves the walk there.
 */
export async function searchNeighborhoods(query: string, city: string, state: string, signal?: AbortSignal): Promise<{ items: Neighborhood[]; elsewhere: boolean }> {
  if (normalize(query).length < 3) return { items: [], elsewhere: false }
  const key = (n: Neighborhood) => `${normalize(n.name)}|${normalize(n.city)}|${n.state}`
  const stateName = STATES.find((s) => s.uf === state)?.name
  if (normalize(city).length >= 2 && stateName) {
    const inCity = (await photonNeighborhoods(`${query.trim()}, ${city.trim()}, ${stateName}`, signal)).filter((n) => n.state === state && normalize(n.city) === normalize(city))
    const items = rankByName(uniqueBy(inCity, key), query)
    if (items.length > 0) return { items, elsewhere: false }
  }
  const items = rankByName(uniqueBy(await photonNeighborhoods(query.trim(), signal), key), query)
  return { items, elsewhere: Boolean(stateName && normalize(city).length >= 2) }
}

/** Cities anywhere in Brazil, used to find the UF when none is chosen yet. */
async function photonCities(query: string, signal?: AbortSignal): Promise<City[]> {
  const features = await photon({ q: query.trim(), layer: 'city' }, signal)
  const cities = features.flatMap(({ properties: p }) => {
    const state = ufFromName(p.state)
    return p.countrycode === 'BR' && p.name && state && (p.osm_value === 'municipality' || p.osm_value === 'city') ? [{ name: p.name, state }] : []
  })
  return rankByName(
    uniqueBy(cities, (c) => `${normalize(c.name)}|${c.state}`),
    query,
  )
}

let cptecDown = false
const ibge = new Map<string, City[]>()

// A list whose names repeat is broken: BrasilAPI with ?providers=dados-abertos-br
// returned every Santa Catarina city as "ORD" when this was written.
const plausible = (list: City[]) => list.length > 0 && new Set(list.map((c) => c.name)).size >= list.length * 0.95

async function ibgeCities(uf: string, signal?: AbortSignal): Promise<City[]> {
  const key = `bairro.cities.v2.${uf}` // v1 may hold the broken list
  const cached = ibge.get(uf) ?? load<City[] | null>(key, null)
  if (cached) {
    ibge.set(uf, cached)
    return cached
  }
  let list: City[] = []
  try {
    const res = await fetch(`${BRASILAPI}/ibge/municipios/v1/${uf}`, { signal })
    if (res.ok) list = ((await res.json()) as { nome: string; codigo_ibge: string }[]).map((c) => ({ name: titleCase(c.nome), state: uf, code: String(c.codigo_ibge) }))
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e
  }
  if (!plausible(list)) {
    const res = await fetch(`${IBGE}/estados/${uf}/municipios`, { signal })
    if (!res.ok) throw new Error('Lista de cidades indisponível.')
    list = ((await res.json()) as { id: number; nome: string }[]).map((c) => ({ name: c.nome, state: uf, code: String(c.id) }))
  }
  ibge.set(uf, list)
  save(key, list) // a state's list rarely changes and makes typing work offline
  return list
}

const neighborhoodLists = new Map<string, string[]>()

export type NeighborhoodList = { status: 'ok'; names: string[] } | { status: 'unknown-city' } | { status: 'failed' }

/**
 * Every neighborhood OpenStreetMap knows in a city, sorted. `unknown-city` while
 * the name does not match an IBGE municipality (still being typed); `failed`
 * when the Overpass API cannot be reached. Cached on the phone for 30 days, so
 * the list also works offline afterwards.
 */
export async function listNeighborhoods(city: string, uf: string, signal?: AbortSignal): Promise<NeighborhoodList> {
  const cities = await ibgeCities(uf, signal).catch((e: Error) => {
    if (e.name === 'AbortError') throw e
    return null
  })
  if (!cities) return { status: 'failed' }
  const code = cities.find((c) => normalize(c.name) === normalize(city))?.code
  if (!code) return { status: 'unknown-city' }
  const key = `bairro.neighborhoods.v1.${code}`
  const memory = neighborhoodLists.get(code)
  if (memory) return { status: 'ok', names: memory }
  const cached = load<{ at: number; names: string[] } | null>(key, null)
  if (cached && Date.now() - cached.at < 30 * 24 * 3600 * 1000) {
    neighborhoodLists.set(code, cached.names)
    return { status: 'ok', names: cached.names }
  }
  let names: string[] | null = null
  try {
    // The server keeps every list it fetches, so a city loads even while Overpass is down.
    names = (await api.neighborhoods(code, city)).names
  } catch (e) {
    if (signal?.aborted) throw e
    // Server reached but Overpass failed there too: asking again from here would not help.
    if (e instanceof ApiError && (e.status === 503 || (e.status >= 400 && e.status < 500))) return { status: 'failed' }
    names = await overpassDirect(code, city, signal)
  }
  if (!names) return { status: 'failed' }
  neighborhoodLists.set(code, names)
  save(key, { at: Date.now(), names })
  return { status: 'ok', names }
}

/** Same query as backend/internal/places, for a phone that cannot reach the server. */
async function overpassDirect(code: string, city: string, signal?: AbortSignal): Promise<string[] | null> {
  // Neighborhoods are mapped as place nodes/areas or as administrative boundaries (levels 9–10).
  const query = `[out:json][timeout:25];
area["IBGE:GEOCODIGO"="${code}"]["boundary"="administrative"]->.city;
(
  nwr(area.city)["place"~"^(suburb|neighbourhood|quarter)$"];
  relation(area.city)["boundary"="administrative"]["admin_level"~"^(9|10)$"];
);
out tags;`
  for (const endpoint of OVERPASS) {
    try {
      const res = await fetch(endpoint, { method: 'POST', body: new URLSearchParams({ data: query }), signal: signal ?? AbortSignal.timeout(30000) })
      if (!res.ok) continue
      const { elements = [], remark = '' } = (await res.json()) as { elements?: { tags?: { name?: string } }[]; remark?: string }
      if (remark.includes('error')) continue
      return [...new Set(elements.map((e) => e.tags?.name?.trim() ?? '').filter((n) => n && normalize(n) !== normalize(city)))].sort((a, b) => a.localeCompare(b, 'pt-BR'))
    } catch (e) {
      if (signal?.aborted) throw e
    }
  }
  return null
}

/** City suggestions for what the person typed, optionally limited to one UF. */
export async function searchCities(query: string, uf: string, signal?: AbortSignal): Promise<City[]> {
  if (normalize(query).length < 2) return []
  if (!cptecDown) {
    try {
      const res = await fetch(`${BRASILAPI}/cptec/v1/cidade/${encodeURIComponent(query.trim())}`, { signal })
      if (res.ok) {
        const found = ((await res.json()) as { nome: string; estado: string }[]).map((c) => ({ name: c.nome, state: c.estado }))
        const matches = rankByName(uf ? found.filter((c) => c.state === uf) : found, query)
        if (matches.length > 0 || !uf) return matches
      } else if (res.status >= 500) {
        cptecDown = true // skip it for the rest of the session instead of waiting on every key
      }
    } catch (e) {
      if ((e as Error).name === 'AbortError') throw e
      cptecDown = true
    }
  }
  // The IBGE list is authoritative but per state; without a UF, ask Photon, which also tells the UF.
  return uf ? rankByName(await ibgeCities(uf, signal), query) : photonCities(query, signal)
}

/** The official spelling of a detected city, or null if BrasilAPI does not know it. */
export async function confirmCity(name: string, uf: string, signal?: AbortSignal): Promise<string | null> {
  const matches = await searchCities(name, uf, signal)
  return matches.find((c) => c.state === uf && normalize(c.name) === normalize(name))?.name ?? null
}
