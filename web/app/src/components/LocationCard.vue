<template>
  <!-- Hover lifts with shadow only, deliberately NOT a transform: translating
       the card by a fractional device pixel (any browser zoom does that) puts
       it on a composited layer and every label on the card goes blurry. -->
  <Card class="location h-full flex flex-col transition-[box-shadow,border-color] duration-200 ease-out hover:shadow-xl dark:hover:border-gray-700">
    <CardHeader class="px-3 sm:px-5 pt-3 sm:pt-4 pb-2 space-y-0">
      <div class="flex items-start justify-between gap-2">
        <CardTitle class="text-base sm:text-lg truncate">
          <span :data-tooltip="name" class="block truncate">{{ name }}</span>
        </CardTitle>
        <div class="flex-shrink-0 flex items-center gap-1">
          <CardSettingsMenu :name="name" :rows="settingsRows" />
          <span v-if="isSimulated" class="text-[9px] font-bold uppercase tracking-wide px-1 py-0.5 rounded bg-amber-500 text-white" data-tooltip="Simulated (not real)">SIM</span>
          <StatusBadge :status="currentStatus" />
        </div>
      </div>
    </CardHeader>

    <CardContent class="loc-content flex-1 pb-3 sm:pb-4 px-3 sm:px-5 pt-1">
      <!-- loc-dense: five or more rows (a site with UniFi) tightens the rhythm
           and thins the fullscreen bars so the card doesn't outgrow its grid. -->
      <div class="loc-rows space-y-1.5" :class="{ 'loc-dense': displayRows.length > 4 }">
        <div v-for="(row, rowIdx) in displayRows" :key="row.key" class="loc-row flex items-center gap-2"
          :class="{ 'opacity-50': row.paused }">
          <!-- Row label -->
          <div class="loc-rowlabel w-16 sm:w-[68px] shrink-0">
            <component
              :is="row.to ? 'a' : 'span'"
              :href="row.to"
              @click="row.to && navigate($event, row.to)"
              :data-tooltip="row.tooltip"
              :class="[
                'block truncate text-[11px] sm:text-xs font-medium',
                row.isOverall
                  ? (row.to ? 'text-foreground hover:text-primary cursor-pointer' : 'text-foreground')
                  : row.to ? 'text-muted-foreground hover:text-primary cursor-pointer'
                  : row.segmented ? 'text-muted-foreground'
                  : 'text-muted-foreground/40'
              ]"
            >
              {{ row.label }}
            </component>
            <!-- ISP + IP (shown only in fullscreen) -->
            <div v-if="!row.isOverall && (row.isp || row.ip)" class="loc-meta leading-tight mt-0.5">
              <div v-if="row.isp" class="truncate text-[11px] text-muted-foreground">{{ row.isp }}</div>
              <div v-if="row.ip" class="truncate text-[11px] font-mono text-muted-foreground/70">{{ row.ip }}</div>
            </div>
          </div>

          <!-- Status bars -->
          <div class="loc-bars flex-1 flex gap-0.5">
            <template v-for="(cell, cellIdx) in row.cells" :key="cellIdx">
              <!-- Sliced bar: one segment per underlying endpoint, stacked
                   inside the SAME bar footprint, so the row keeps the height
                   and rhythm of every other row while still saying which of
                   the three is the one that went away. -->
              <div
                v-if="row.segmented"
                class="ping-cell bar-appear loc-slices flex-1 h-6 sm:h-8 rounded-sm overflow-hidden flex flex-col"
                :style="{ '--i': cellIdx }"
              >
                <div
                  v-for="(seg, segIdx) in cell.segments"
                  :key="segIdx"
                  :class="sliceClass(effectiveToken(seg.token, rowIdx, cellIdx * 4 + segIdx), `${rowIdx}:${cellIdx}:${segIdx}` === selectedKey)"
                  :data-tooltip="seg.label"
                  @mouseenter="seg.result && handleMouseEnter(seg.result, $event)"
                  @mouseleave="seg.result && handleMouseLeave($event)"
                  @click.stop="seg.result && handleClick(seg.result, $event, rowIdx, `${cellIdx}:${segIdx}`)"
                />
              </div>
              <div
                v-else
                :class="[cellClass(effectiveToken(cell.token, rowIdx, cellIdx), `${rowIdx}:${cellIdx}` === selectedKey), 'ping-cell bar-appear']"
                :style="{ '--i': cellIdx }"
                @mouseenter="cell.result && handleMouseEnter(cell.result, $event)"
                @mouseleave="cell.result && handleMouseLeave($event)"
                @click.stop="cell.result && handleClick(cell.result, $event, rowIdx, cellIdx)"
              />
            </template>
          </div>

          <!-- Trailing metric: latency on the link rows, live counts on the
               UniFi rows, best-of latency on Overall. Same reserved width on
               every row so all the bar sets stay perfectly aligned. -->
          <div
            class="loc-rowvalue w-[62px] sm:w-[70px] shrink-0 text-right overflow-hidden"
            :data-tooltip="row.isOverall ? 'Current best latency across WANs' : null"
          >
            <div
              :class="[
                'truncate text-[10px] sm:text-[11px] tabular-nums leading-tight',
                row.valueBad ? 'text-destructive font-medium' : 'text-muted-foreground'
              ]"
            >{{ row.value }}</div>
            <div v-if="row.valueSub" class="loc-meta truncate text-[10px] text-muted-foreground/70 tabular-nums leading-tight">
              {{ row.valueSub }}
            </div>
          </div>
        </div>
      </div>

      <!-- Data-age label (updates live) -->
      <div class="loc-age text-[11px] text-muted-foreground mt-2 pl-[72px] sm:pl-[76px]">
        <span>{{ oldestResultTime }}</span>
      </div>
    </CardContent>
  </Card>
</template>

<script setup>
import { computed, ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'
import StatusBadge from '@/components/StatusBadge.vue'
import { generatePrettyTimeAgo } from '@/utils/time'
import CardSettingsMenu from '@/components/CardSettingsMenu.vue'
import { now, simulations, unifiSnapshots, isMonitored } from '@/store'

const router = useRouter()

const props = defineProps({
  name: { type: String, required: true },
  endpoints: { type: Array, default: () => [] },
  maxResults: { type: Number, default: 50 },
})

const emit = defineEmits(['showTooltip'])

// Latency thresholds (ms) for the Overall Health row.
const LATENCY_GOOD = 100
const LATENCY_WARN = 250

const selectedKey = ref(null)

// --- Classify endpoints into the card's rows by their group ---
const classify = (group) => {
  const g = (group || '').toLowerCase()
  if (/wan\s*1|primary/.test(g)) return 'wan1'
  if (/wan\s*2|backup|secondary/.test(g)) return 'wan2'
  if (/firewall|gateway|edge/.test(g)) return 'firewall'
  if (/wireless|wi-?fi|wlan|access\s*point/.test(g)) return 'wireless'
  if (/phone|voip|sip/.test(g)) return 'phones'
  // DNS groups are collected rather than slotted: several of them collapse into
  // one row (see pushSegmentedRow), so the group carries which resolver it is —
  // "DNS MS-DC01" — and the prefix is stripped off for the segment label.
  if (/^dns\b/.test(g)) return 'dns'
  return 'other'
}

// "DNS MS-DC01" -> "MS-DC01". What is left identifies the slice.
const segmentLabel = (group) => (group || '').replace(/^dns[\s:_-]*/i, '').trim() || 'DNS'

// ISP name is the text in parentheses of the group, e.g. "WAN 1 (Comcast Fiber)".
const ispFromGroup = (group) => {
  const m = (group || '').match(/\(([^)]+)\)/)
  return m ? m[1].trim() : ''
}

// IP is the ping/target host from the most recent result.
const ipOf = (endpoint) => {
  if (!endpoint || !endpoint.results || endpoint.results.length === 0) return ''
  const r = endpoint.results[endpoint.results.length - 1]
  return r && r.hostname ? r.hostname : ''
}

const shortLabel = (group) => {
  switch (classify(group)) {
    case 'wan1': return 'WAN 1'
    case 'wan2': return 'WAN 2'
    case 'firewall': return 'Firewall'
    case 'wireless': return 'Wireless'
    case 'phones': return 'Phones'
    default: return group || '—'
  }
}

const slots = computed(() => {
  const s = { wan1: null, wan2: null, firewall: null, wireless: null, phones: null, dns: [], others: [] }
  for (const ep of props.endpoints) {
    const c = classify(ep.group)
    if (c === 'other') s.others.push(ep)
    else if (c === 'dns') s.dns.push(ep)
    else if (!s[c]) s[c] = ep
    else s.others.push(ep)
  }
  return s
})

const hasWanLayout = computed(() =>
  !!(slots.value.wan1 || slots.value.wan2 || slots.value.firewall || slots.value.wireless || slots.value.phones))

// Pad an endpoint's results to maxResults (nulls at the front), like EndpointCard.
const padResults = (endpoint) => {
  const results = [...(endpoint?.results || [])]
  while (results.length < props.maxResults) results.unshift(null)
  return results.slice(-props.maxResults)
}

// "Nothing reported" is a distinct failure from "reported and failing": a site
// with 0 phones registered, or a UniFi console we can't read, has no health
// signal at all, and painting that red makes it look like an outage. Both
// collectors flag it with a fixed error prefix — "no phones reporting" and
// "no unifi reporting" — and it renders BLACK. Keep this in step with the
// prefixes in collector/phone_collector.py and collector/unifi_collector.py.
const NOT_REPORTING = /^no (phones|unifi) reporting\b/i
const isNotReporting = (result) =>
  !!result && !result.success && (result.errors || []).some((e) => NOT_REPORTING.test(e))

// A collector that reports three states (healthy / degraded / down) pushes
// degraded as a PASS carrying its reason, so a partial problem doesn't fire a
// down alert. A successful result with errors therefore means "passed, with a
// warning" — 1 of 2 WAN uplinks down, 4 of 23 APs offline — and reads AMBER.
// Painting it green would hide exactly the problems this dashboard exists for.
const isWarning = (result) => !!result && result.success && (result.errors || []).length > 0

const endpointRowCells = (endpoint) => {
  const padded = padResults(endpoint)
  return padded.map((result) => {
    if (!result) return { token: 'none', result: null }
    if (result.success) return { token: isWarning(result) ? 'amber' : 'green', result }
    return { token: isNotReporting(result) ? 'nodata' : 'red', result }
  })
}

// Paused endpoints are excluded from the site's rollup: the Overall row and the
// header badge. Their own row still renders (muted) so you can see it is paused
// rather than wondering where it went.
const activeEndpoints = computed(() => props.endpoints.filter((ep) => isMonitored(ep.key)))

// Overall Health row: best (lowest) latency across the location's WANs per slice.
const overallCells = computed(() => {
  const padded = activeEndpoints.value.map(padResults)
  const cells = []
  for (let i = 0; i < props.maxResults; i++) {
    const slice = padded.map((p) => p[i]).filter(Boolean)
    if (slice.length === 0) {
      cells.push({ token: 'none', result: null })
      continue
    }
    const up = slice.filter((r) => r.success && r.duration)
    if (up.length === 0) {
      // Everything in this slice failed — but if the ONLY thing that failed was
      // a not-reporting feed, there is no outage to call red.
      const token = slice.every(isNotReporting) ? 'nodata' : 'red'
      cells.push({ token, result: slice[0] })
      continue
    }
    const best = up.reduce((m, r) => (r.duration < m.duration ? r : m))
    const ms = best.duration / 1000000
    const token = ms <= LATENCY_GOOD ? 'green' : ms <= LATENCY_WARN ? 'amber' : 'red'
    cells.push({ token, result: best })
  }
  return cells
})

const currentLatencyLabel = computed(() => {
  for (let i = overallCells.value.length - 1; i >= 0; i--) {
    const c = overallCells.value[i]
    if (c.result && c.result.duration) {
      return `~${Math.round(c.result.duration / 1000000)}ms`
    }
  }
  return 'N/A'
})

// --- Trailing metrics -----------------------------------------------------
// Every row carries a value at its right edge, not just Overall. The column was
// already reserved on Overall, so filling it on the rows above costs no layout
// and turns dead width into the numbers you'd otherwise have to drill in for.
const latestResult = (endpoint) => {
  const r = endpoint && endpoint.results
  return r && r.length ? r[r.length - 1] : null
}

// Latency of the most recent check, or "down" when that check failed.
const latencyOf = (endpoint) => {
  const r = latestResult(endpoint)
  if (!r) return { value: '', bad: false }
  if (!r.success) return { value: 'down', bad: true }
  return { value: r.duration ? `${Math.round(r.duration / 1000000)}ms` : '', bad: false }
}

// Firewall / Wireless read from the UniFi side channel, which carries counts an
// external-endpoint's pass/fail cannot. Falls back to latency until the
// collector has reported.
const unifiCounts = (endpoint) => {
  const snap = endpoint ? unifiSnapshots.value[endpoint.key] : null
  return snap && snap.counts ? snap.counts : null
}
const unifiDetail = (endpoint) => {
  const snap = endpoint ? unifiSnapshots.value[endpoint.key] : null
  return snap && snap.detail ? snap.detail : null
}

const firewallMetric = (endpoint) => {
  const c = unifiCounts(endpoint)
  if (!c || c.wansTotal == null) return latencyOf(endpoint)
  // The primary uplink's link type is the one extra fact worth the fullscreen
  // sub-line: it tells you whether the site is on fibre or a coax backup.
  const wans = (unifiDetail(endpoint) || {}).wans || []
  const primary = wans[0] || {}
  return {
    value: `${c.wansUp}/${c.wansTotal} WAN`,
    bad: c.wansUp < c.wansTotal,
    sub: primary.speedType || '',
    tooltip: `${c.wansUp} of ${c.wansTotal} WAN uplinks up`
      + (primary.ip ? ` · ${primary.ip}` : ''),
  }
}
const wirelessMetric = (endpoint) => {
  const c = unifiCounts(endpoint)
  if (!c || c.apsTotal == null) return latencyOf(endpoint)
  return {
    value: `${c.apsOnline}/${c.apsTotal} AP`,
    bad: c.apsOnline < c.apsTotal,
    sub: c.clients != null ? `${c.clients} cl` : '',
    tooltip: `${c.apsOnline} of ${c.apsTotal} access points online`
      + (c.clients != null ? ` · ${c.clients} clients` : ''),
  }
}

// A row backed by several endpoints reports a tally instead of a latency, and
// names the offenders on the fullscreen sub-line — a 10px slice is enough to
// tell you something is wrong from across the room, but not which one.
const segmentedMetric = (endpoints) => {
  const reported = endpoints
    .map((ep) => ({ ep, r: latestResult(ep) }))
    .filter((x) => x.r)
  if (!reported.length) return { value: '', bad: false, tooltip: 'No data yet' }
  const down = reported.filter((x) => !x.r.success).map((x) => segmentLabel(x.ep.group))
  const up = reported.length - down.length
  return {
    value: `${up}/${endpoints.length}`,
    bad: down.length > 0,
    sub: down.join(' '),
    tooltip: down.length
      ? `Not resolving: ${down.join(', ')}`
      : `All ${endpoints.length} resolvers answering`,
  }
}

// Transpose several endpoints' padded results into one cell per time slice,
// each carrying a segment per endpoint. Same time axis as every other row, so
// slice N here lines up with slice N above it.
const segmentedRowCells = (endpoints) => {
  const perEndpoint = endpoints.map(endpointRowCells)
  const cells = []
  for (let i = 0; i < props.maxResults; i++) {
    cells.push({
      segments: perEndpoint.map((cellsForEp, si) => ({
        ...cellsForEp[i],
        label: segmentLabel(endpoints[si].group),
      })),
    })
  }
  return cells
}

const metricFor = (kind, endpoint) => {
  if (!endpoint) return { value: '', bad: false }
  if (kind === 'firewall') return firewallMetric(endpoint)
  if (kind === 'wireless') return wirelessMetric(endpoint)
  return latencyOf(endpoint)
}

const displayRows = computed(() => {
  const rows = []
  const s = slots.value

  const pushEndpointRow = (label, endpoint, keyName) => {
    const metric = metricFor(keyName, endpoint)
    const paused = !!endpoint && !isMonitored(endpoint.key)
    rows.push({
      key: keyName,
      label,
      endpointKey: endpoint ? endpoint.key : null,
      to: endpoint ? `/endpoints/${endpoint.key}` : null,
      tooltip: paused
        ? `${label}: monitoring paused`
        : endpoint ? (metric.tooltip || endpoint.group || endpoint.name) : `${label}: no data`,
      isp: endpoint ? ispFromGroup(endpoint.group) : '',
      ip: ipOf(endpoint),
      cells: endpoint ? endpointRowCells(endpoint) : Array.from({ length: props.maxResults }, () => ({ token: 'none', result: null })),
      isOverall: false,
      paused,
      // A paused row shows why it is quiet instead of a number nobody is watching.
      value: paused ? 'paused' : metric.value,
      valueBad: paused ? false : metric.bad,
      valueSub: paused ? '' : (metric.sub || ''),
    })
  }

  // One row, several endpoints, bars cut into a slice each.
  const pushSegmentedRow = (label, endpoints, keyName) => {
    const metric = segmentedMetric(endpoints)
    // Only "paused" once every slice is paused — otherwise the row is still
    // reporting something and dimming the whole thing would hide it.
    const paused = endpoints.every((ep) => !isMonitored(ep.key))
    rows.push({
      key: keyName,
      label,
      segmented: true,
      // No single endpoint to drill into, so the label isn't a link and the
      // settings menu gets one switch per slice instead of one for the row.
      endpointKey: null,
      to: null,
      segmentRows: endpoints.map((ep) => ({
        key: `${keyName}-${ep.key}`,
        label: `${label} · ${segmentLabel(ep.group)}`,
        endpointKey: ep.key,
      })),
      tooltip: paused ? `${label}: monitoring paused` : metric.tooltip,
      isp: '',
      ip: '',
      cells: segmentedRowCells(endpoints),
      isOverall: false,
      paused,
      value: paused ? 'paused' : metric.value,
      valueBad: paused ? false : metric.bad,
      valueSub: paused ? '' : (metric.sub || ''),
    })
  }

  if (hasWanLayout.value) {
    pushEndpointRow('WAN 1', s.wan1, 'wan1')
    pushEndpointRow('WAN 2', s.wan2, 'wan2')
    // Firewall and Wireless appear only where a UniFi console is wired up, so
    // sites without one don't grow two permanently empty rows.
    if (s.firewall) pushEndpointRow('Firewall', s.firewall, 'firewall')
    if (s.wireless) pushEndpointRow('Wireless', s.wireless, 'wireless')
    pushEndpointRow('Phones', s.phones, 'phones')
    s.others.forEach((ep, i) => pushEndpointRow(shortLabel(ep.group), ep, `other-${i}`))
  } else {
    // No WAN layout (e.g. a standalone monitor) — one row per endpoint. The
    // fall-back to props.endpoints is only for a card with nothing classified
    // at all; DNS endpoints have their own row below and must not double up.
    const list = (s.others.length || s.dns.length) ? s.others : props.endpoints
    list.forEach((ep, i) => pushEndpointRow(shortLabel(ep.group) || ep.name, ep, `ep-${i}`))
  }

  if (s.dns.length) pushSegmentedRow('DNS', s.dns, 'dns')

  rows.push({
    key: 'overall',
    label: 'Overall',
    endpointKey: null,
    // Overall drills into the whole-site view rather than any one endpoint.
    to: `/sites/${encodeURIComponent(props.name)}`,
    tooltip: `Open the ${props.name} site overview`,
    cells: overallCells.value,
    isOverall: true,
    value: currentLatencyLabel.value,
    valueBad: false,
    valueSub: '',
  })

  return rows
})

// Rows the card's settings menu can switch, in the order they appear. Overall is
// a rollup of the others, not a monitor of its own, so it isn't switchable.
const settingsRows = computed(() =>
  displayRows.value
    .filter((row) => !row.isOverall)
    .flatMap((row) =>
      row.segmentRows
        ? row.segmentRows
        : [{ key: row.key, label: row.label, endpointKey: row.endpointKey }]
    )
)

// --- Current status for the badge (a simulation overrides the real status) ---
const isSimulated = computed(() => !!simulations[props.name])
const currentStatus = computed(() => {
  if (simulations[props.name]) return simulations[props.name]
  const latest = activeEndpoints.value
    .map((ep) => (ep.results && ep.results.length ? ep.results[ep.results.length - 1] : null))
    .filter(Boolean)
  if (latest.length === 0) return 'unknown'
  const upCount = latest.filter((r) => r.success).length
  if (upCount === 0) return 'unhealthy'
  if (upCount < latest.length) return 'degraded'
  // Everything passed, but a pass carrying a reason is a warning (see isWarning),
  // and the badge has to say so or the card claims healthy while a row is amber.
  if (latest.some(isWarning)) return 'degraded'
  return 'healthy'
})

// --- Shared time axis (based on the endpoint with the most results) ---
const primaryEndpoint = computed(() => {
  return props.endpoints.reduce((best, ep) => {
    const n = ep.results ? ep.results.length : 0
    return n > (best?.results?.length || 0) ? ep : best
  }, null)
})

const oldestResultTime = computed(() => {
  const ep = primaryEndpoint.value
  if (!ep || !ep.results || ep.results.length === 0) return ''
  const idx = Math.max(0, ep.results.length - props.maxResults)
  return generatePrettyTimeAgo(ep.results[idx].timestamp, now.value)
})

// Stable pseudo-random 0..1 so the simulated pattern doesn't flicker on re-render.
const hash01 = (n) => {
  const x = Math.sin(n * 12.9898) * 43758.5453
  return x - Math.floor(x)
}

// When a location is simulated, recolor a REALISTIC fraction of its bars — like
// a flapping/degraded connection — not every bar the same colour. Empty slots
// stay grey. Recent bars (right side) skew worse for a "down" site.
const effectiveToken = (token, rowIdx, cellIdx) => {
  if (!isSimulated.value || token === 'none' || token === 'nodata') return token
  const status = simulations[props.name]
  if (status === 'healthy') return 'green'
  const n = props.maxResults || 20
  const r = hash01(props.name.length * 7 + rowIdx * 131 + cellIdx * 17 + 1)
  if (status === 'unhealthy') {
    // Mostly red over the recent ~2/3, with occasional drops earlier.
    const recent = cellIdx >= n * 0.35
    return r < (recent ? 0.82 : 0.2) ? 'red' : 'green'
  }
  // Degraded — flapping mix of green / amber / red.
  if (r < 0.15) return 'red'
  if (r < 0.45) return 'amber'
  return 'green'
}

// --- Cell styling ---
// Slices share the colour vocabulary but not the geometry: the parent bar owns
// the height and the rounded corners, each slice just takes an equal share of
// it. The hairline border-bottom is the "cut" — it reads as one bar divided,
// not as three bars that happen to touch.
const sliceClass = (token, selected) =>
  cellClass(token, selected, 'flex-1 min-h-0 transition-all loc-slice')

const cellClass = (token, selected, base = 'flex-1 h-6 sm:h-8 rounded-sm transition-all') => {
  if (token === 'none') return `${base} bg-gray-200 dark:bg-gray-700`
  const cursor = ' cursor-pointer'
  const sel = selected ? ' sel' : ''
  switch (token) {
    case 'green': return `${base}${cursor} stbar-up${sel}`
    case 'red': return `${base}${cursor} stbar-down${sel}`
    case 'amber': return `${base}${cursor} stbar-degraded${sel}`
    case 'nodata': return `${base}${cursor} stbar-nodata${sel}`
    default: return `${base} bg-gray-200 dark:bg-gray-700`
  }
}

// --- Interaction (reuses the app-wide rich tooltip) ---
const navigate = (event, path) => {
  event.preventDefault()
  router.push(path)
}

const handleMouseEnter = (result, event) => emit('showTooltip', result, event, 'hover')
const handleMouseLeave = (event) => emit('showTooltip', null, event, 'hover')

const handleClick = (result, event, rowIdx, cellIdx) => {
  window.dispatchEvent(new CustomEvent('clear-data-point-selection'))
  const key = `${rowIdx}:${cellIdx}`
  if (selectedKey.value === key) {
    selectedKey.value = null
    emit('showTooltip', null, event, 'click')
  } else {
    selectedKey.value = key
    emit('showTooltip', result, event, 'click')
  }
}

const handleClearSelection = () => { selectedKey.value = null }

onMounted(() => window.addEventListener('clear-data-point-selection', handleClearSelection))
onUnmounted(() => window.removeEventListener('clear-data-point-selection', handleClearSelection))
</script>
