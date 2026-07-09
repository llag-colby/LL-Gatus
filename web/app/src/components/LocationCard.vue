<template>
  <Card class="location h-full flex flex-col transition hover:shadow-lg dark:hover:border-gray-700">
    <CardHeader class="px-3 sm:px-5 pt-3 sm:pt-4 pb-2 space-y-0">
      <div class="flex items-start justify-between gap-2">
        <CardTitle class="text-base sm:text-lg truncate">
          <span :data-tooltip="name" class="block truncate">{{ name }}</span>
        </CardTitle>
        <div class="flex-shrink-0 flex items-center gap-1">
          <span v-if="isSimulated" class="text-[9px] font-bold uppercase tracking-wide px-1 py-0.5 rounded bg-amber-500 text-white" data-tooltip="Simulated (not real)">SIM</span>
          <StatusBadge :status="currentStatus" />
        </div>
      </div>
    </CardHeader>

    <CardContent class="loc-content flex-1 pb-3 sm:pb-4 px-3 sm:px-5 pt-1">
      <div class="loc-rows space-y-2">
        <div v-for="(row, rowIdx) in displayRows" :key="row.key" class="loc-row flex items-center gap-2">
          <!-- Row label -->
          <div class="loc-rowlabel w-16 sm:w-[68px] shrink-0">
            <component
              :is="row.endpointKey ? 'a' : 'span'"
              :href="row.endpointKey ? `/endpoints/${row.endpointKey}` : undefined"
              @click="row.endpointKey && navigate($event, row.endpointKey)"
              :data-tooltip="row.tooltip"
              :class="[
                'block truncate text-[11px] sm:text-xs font-medium',
                row.isOverall ? 'text-foreground' : (row.endpointKey ? 'text-muted-foreground hover:text-primary cursor-pointer' : 'text-muted-foreground/40')
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
            <div
              v-for="(cell, cellIdx) in row.cells"
              :key="cellIdx"
              :class="cellClass(cell.token, `${rowIdx}:${cellIdx}` === selectedKey)"
              @mouseenter="cell.result && handleMouseEnter(cell.result, $event)"
              @mouseleave="cell.result && handleMouseLeave($event)"
              @click.stop="cell.result && handleClick(cell.result, $event, rowIdx, cellIdx)"
            />
          </div>

          <!-- Trailing latency value (overall row only) -->
          <span
            v-if="row.isOverall"
            class="w-12 shrink-0 text-right text-[11px] sm:text-xs text-muted-foreground tabular-nums"
            :data-tooltip="'Current best latency across WANs'"
          >{{ row.latencyLabel }}</span>
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
import { now, simulations } from '@/store'

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

// --- Classify endpoints into WAN 1 / WAN 2 / Phones by their group ---
const classify = (group) => {
  const g = (group || '').toLowerCase()
  if (/wan\s*1|primary/.test(g)) return 'wan1'
  if (/wan\s*2|backup|secondary/.test(g)) return 'wan2'
  if (/phone|voip|sip/.test(g)) return 'phones'
  return 'other'
}

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
    case 'phones': return 'Phones'
    default: return group || '—'
  }
}

const slots = computed(() => {
  const s = { wan1: null, wan2: null, phones: null, others: [] }
  for (const ep of props.endpoints) {
    const c = classify(ep.group)
    if (c === 'other') s.others.push(ep)
    else if (!s[c]) s[c] = ep
    else s.others.push(ep)
  }
  return s
})

const hasWanLayout = computed(() => !!(slots.value.wan1 || slots.value.wan2 || slots.value.phones))

// Pad an endpoint's results to maxResults (nulls at the front), like EndpointCard.
const padResults = (endpoint) => {
  const results = [...(endpoint?.results || [])]
  while (results.length < props.maxResults) results.unshift(null)
  return results.slice(-props.maxResults)
}

const endpointRowCells = (endpoint) => {
  const padded = padResults(endpoint)
  return padded.map((result) => {
    if (!result) return { token: 'none', result: null }
    return { token: result.success ? 'green' : 'red', result }
  })
}

// Overall Health row: best (lowest) latency across the location's WANs per slice.
const overallCells = computed(() => {
  const padded = props.endpoints.map(padResults)
  const cells = []
  for (let i = 0; i < props.maxResults; i++) {
    const slice = padded.map((p) => p[i]).filter(Boolean)
    if (slice.length === 0) {
      cells.push({ token: 'none', result: null })
      continue
    }
    const up = slice.filter((r) => r.success && r.duration)
    if (up.length === 0) {
      cells.push({ token: 'red', result: slice[0] })
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

const displayRows = computed(() => {
  const rows = []
  const s = slots.value

  const pushEndpointRow = (label, endpoint, keyName) => {
    rows.push({
      key: keyName,
      label,
      endpointKey: endpoint ? endpoint.key : null,
      tooltip: endpoint ? (endpoint.group || endpoint.name) : `${label}: no data`,
      isp: endpoint ? ispFromGroup(endpoint.group) : '',
      ip: ipOf(endpoint),
      cells: endpoint ? endpointRowCells(endpoint) : Array.from({ length: props.maxResults }, () => ({ token: 'none', result: null })),
      isOverall: false,
    })
  }

  if (hasWanLayout.value) {
    pushEndpointRow('WAN 1', s.wan1, 'wan1')
    pushEndpointRow('WAN 2', s.wan2, 'wan2')
    pushEndpointRow('Phones', s.phones, 'phones')
    s.others.forEach((ep, i) => pushEndpointRow(shortLabel(ep.group), ep, `other-${i}`))
  } else {
    // No WAN layout (e.g. a standalone monitor) — one row per endpoint.
    const list = s.others.length ? s.others : props.endpoints
    list.forEach((ep, i) => pushEndpointRow(shortLabel(ep.group) || ep.name, ep, `ep-${i}`))
  }

  rows.push({
    key: 'overall',
    label: 'Overall',
    endpointKey: null,
    tooltip: 'Overall Health — best latency across WANs',
    cells: overallCells.value,
    isOverall: true,
    latencyLabel: currentLatencyLabel.value,
  })

  return rows
})

// --- Current status for the badge (a simulation overrides the real status) ---
const isSimulated = computed(() => !!simulations[props.name])
const currentStatus = computed(() => {
  if (simulations[props.name]) return simulations[props.name]
  const latest = props.endpoints
    .map((ep) => (ep.results && ep.results.length ? ep.results[ep.results.length - 1] : null))
    .filter(Boolean)
  if (latest.length === 0) return 'unknown'
  const upCount = latest.filter((r) => r.success).length
  if (upCount === 0) return 'unhealthy'
  if (upCount < latest.length) return 'degraded'
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

// --- Cell styling ---
const cellClass = (token, selected) => {
  const base = 'flex-1 h-6 sm:h-8 rounded-sm transition-all'
  if (token === 'none') return `${base} bg-gray-200 dark:bg-gray-700`
  const cursor = ' cursor-pointer'
  switch (token) {
    case 'green': return `${base}${cursor} ${selected ? 'bg-green-700' : 'bg-green-500 hover:bg-green-700'}`
    case 'red': return `${base}${cursor} ${selected ? 'bg-red-700' : 'bg-red-500 hover:bg-red-700'}`
    case 'amber': return `${base}${cursor} ${selected ? 'bg-amber-600' : 'bg-amber-500 hover:bg-amber-600'}`
    default: return `${base} bg-gray-200 dark:bg-gray-700`
  }
}

// --- Interaction (reuses the app-wide rich tooltip) ---
const navigate = (event, key) => {
  event.preventDefault()
  router.push(`/endpoints/${key}`)
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
