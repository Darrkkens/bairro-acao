import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import { useEffect, useRef, useState } from 'react'
import { routeOf } from '../geo'
import type { Occurrence, TrackPoint } from '../types'

// OpenStreetMap's standard tiles; attribution is required by its tile policy.
const TILES = 'https://tile.openstreetmap.org/{z}/{x}/{y}.png'
const ATTRIBUTION = '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
const MARKER_SPACING = 32 // px between marker centers before they are fanned out

const touch = () => window.matchMedia('(pointer: coarse)').matches

interface Pin {
  latlng: L.LatLng
  number: number
  category: string
}

/**
 * Route plus numbered photo points; tapping a number scrolls to its review card.
 * Inline, one finger keeps scrolling the page (two fingers move and zoom the
 * map); full screen hands every gesture to the map.
 */
export function WalkMap({ occurrences, track }: { occurrences: Occurrence[]; track: TrackPoint[] }) {
  const container = useRef<HTMLDivElement>(null)
  const map = useRef<L.Map | null>(null)
  const layers = useRef<L.LayerGroup | null>(null)
  const fitted = useRef(false)
  const [expanded, setExpanded] = useState(false)

  useEffect(() => {
    const m = L.map(container.current!, { dragging: !touch(), scrollWheelZoom: !touch(), zoomSnap: 0.5, maxZoom: 19, zoomControl: false })
    L.control.zoom({ zoomInTitle: 'Aproximar', zoomOutTitle: 'Afastar' }).addTo(m)
    L.tileLayer(TILES, { maxZoom: 19, attribution: ATTRIBUTION }).addTo(m)
    layers.current = L.layerGroup().addTo(m)
    map.current = m
    return () => {
      m.remove()
      map.current = null
      fitted.current = false
    }
  }, [])

  useEffect(() => {
    const m = map.current
    const group = layers.current
    if (!m || !group) return
    group.clearLayers()
    const route = routeOf(track, occurrences).map((p): L.LatLngTuple => [p.latitude, p.longitude])
    if (route.length > 1) {
      L.polyline(route, { color: '#ffffff', weight: 8, opacity: 0.9, interactive: false }).addTo(group)
      L.polyline(route, { color: '#1f6f4a', weight: 4.5, interactive: false }).addTo(group)
      L.circleMarker(route[0], { radius: 6, color: '#1f6f4a', weight: 3, fillColor: '#ffffff', fillOpacity: 1 }).bindTooltip('Início').addTo(group)
    }
    const pins: Pin[] = occurrences.flatMap((o, i) => (o.location ? [{ latlng: L.latLng(o.location.latitude, o.location.longitude), number: i + 1, category: o.category || o.ai?.category || '' }] : []))

    // Frame the walk once; later refreshes (AI results) must not undo the person's zoom.
    const bounds = L.latLngBounds(route)
    pins.forEach((p) => bounds.extend(p.latlng))
    if (!bounds.isValid()) return
    if (!fitted.current) {
      m.fitBounds(bounds, { padding: [36, 36], maxZoom: 18 })
      fitted.current = true
    }

    // Pins are laid out in screen space, so only after the map has a view.
    const markers = L.layerGroup().addTo(group)
    const draw = () => drawPins(m, markers, pins)
    draw()
    // Points that overlap at one zoom separate at another, so lay them out again.
    m.on('zoomend', draw)
    return () => {
      m.off('zoomend', draw)
    }
  }, [occurrences, track])

  // Full screen: every gesture goes to the map; the phone's back button closes it.
  useEffect(() => {
    const m = map.current
    if (!m) return
    if (expanded || !touch()) m.dragging.enable()
    else m.dragging.disable()
    if (expanded) m.scrollWheelZoom.enable()
    else if (touch()) m.scrollWheelZoom.disable()
    requestAnimationFrame(() => m.invalidateSize())
    if (!expanded) return
    document.documentElement.style.overflow = 'hidden'
    history.pushState({ walkMap: true }, '')
    const onPop = () => setExpanded(false)
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && history.back()
    window.addEventListener('popstate', onPop)
    window.addEventListener('keydown', onKey)
    return () => {
      document.documentElement.style.overflow = ''
      window.removeEventListener('popstate', onPop)
      window.removeEventListener('keydown', onKey)
    }
  }, [expanded])

  return (
    <div className={`map-frame${expanded ? ' expanded' : ''}`}>
      <div className="walk-map" ref={container} role="region" aria-label="Mapa do trajeto com os pontos fotografados" />
      <button type="button" className="map-toggle" onClick={() => (expanded ? history.back() : setExpanded(true))}>
        {expanded ? 'Fechar mapa' : 'Tela cheia'}
      </button>
    </div>
  )
}

const fanRadius = (k: number) => (k < 2 ? 0 : Math.max(26, (k * MARKER_SPACING) / (2 * Math.PI)))

/**
 * Draws numbered pins. Pins that would cover each other at this zoom are fanned
 * out around their center with a line to the real spot; groups whose fans would
 * touch are merged until none overlap (mirrors spread in backend/internal/report).
 */
function drawPins(m: L.Map, layer: L.LayerGroup, pins: Pin[]) {
  layer.clearLayers()
  const placed = pins.map((pin) => ({ pin, at: m.latLngToLayerPoint(pin.latlng) }))
  let groups = placed.map((p) => ({ members: [p], center: p.at }))
  for (let merged = true; merged; ) {
    merged = false
    outer: for (let a = 0; a < groups.length; a++) {
      for (let b = a + 1; b < groups.length; b++) {
        const ga = groups[a]
        const gb = groups[b]
        if (ga.center.distanceTo(gb.center) >= fanRadius(ga.members.length) + fanRadius(gb.members.length) + MARKER_SPACING) continue
        const members = [...ga.members, ...gb.members].sort((x, y) => x.pin.number - y.pin.number)
        const center = L.point(members.reduce((s, p) => s + p.at.x, 0) / members.length, members.reduce((s, p) => s + p.at.y, 0) / members.length)
        groups = [...groups.slice(0, a), { members, center }, ...groups.slice(a + 1, b), ...groups.slice(b + 1)]
        merged = true
        break outer
      }
    }
  }
  for (const { members, center } of groups) {
    const k = members.length
    members.forEach(({ pin, at }, i) => {
      const angle = -Math.PI / 2 + (2 * Math.PI * i) / k
      // Offset from the pin's real location to its place in the fan.
      const dx = k > 1 ? Math.round(center.x + fanRadius(k) * Math.cos(angle) - at.x) : 0
      const dy = k > 1 ? Math.round(center.y + fanRadius(k) * Math.sin(angle) - at.y) : 0
      const leader = k > 1 ? `<svg class="leader" width="1" height="1" aria-hidden="true"><line x1="0" y1="0" x2="${dx}" y2="${dy}"/><circle r="3"/></svg>` : ''
      const marker = L.marker(pin.latlng, {
        icon: L.divIcon({ className: 'map-marker', html: `${leader}<span data-category="${pin.category}" style="transform: translate(${dx}px, ${dy}px)">${pin.number}</span>`, iconSize: [30, 30], iconAnchor: [15, 15] }),
        title: `Ponto ${pin.number}`,
        alt: `Ponto ${pin.number}`,
        riseOnHover: true,
      })
      marker.on('click', () => {
        // Leave full screen first so the card can scroll into view.
        if (history.state?.walkMap) history.back()
        setTimeout(() => document.getElementById(`ponto-${pin.number}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 50)
      })
      marker.addTo(layer)
    })
  }
}
