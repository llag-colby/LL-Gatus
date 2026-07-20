<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 space-y-4 phone-panel">

      <!-- Toolbar -->
      <div class="flex items-end justify-between gap-4 flex-wrap">
        <div class="flex items-center gap-3">
          <router-link to="/" class="text-muted-foreground hover:text-foreground transition-colors mb-1"
            data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-5 w-5" />
          </router-link>
          <div>
            <div class="eyebrow">Line panel · {{ phones.length }} extensions</div>
            <h1 class="text-2xl font-bold tracking-tight leading-none mt-0.5">
              {{ locationName }} <span class="text-muted-foreground font-normal">phones</span>
            </h1>
          </div>
        </div>
        <div class="flex items-center gap-2 mb-0.5">
          <span class="flex items-center gap-1.5 text-xs text-muted-foreground">
            <span class="sweep-dot" :class="{ dead: !updatedAt }"></span>
            <span class="font-mono">swept {{ updatedLabel }}</span>
          </span>
          <input v-model="search" type="text" placeholder="filter ext / name / ip / dept"
            class="text-sm font-mono bg-background border rounded-md px-3 py-1.5 w-48 focus:outline-none focus:ring-2 focus:ring-ring" />

          <!-- Health thresholds editor -->
          <div class="relative">
            <Button variant="ghost" size="icon" class="h-9 w-9" @click="toggleSettings"
              data-tooltip="Health thresholds" data-tip-pos="bottom">
              <SlidersHorizontal class="h-5 w-5" />
            </Button>
            <div v-if="settingsOpen" class="fixed inset-0 z-40" @click="settingsOpen = false"></div>
            <div v-if="settingsOpen" class="pop-in absolute right-0 mt-2 w-72 rounded-lg border bg-popover text-popover-foreground shadow-xl p-3 z-50 space-y-3 text-left" style="transform-origin: top right;">
              <div class="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Health thresholds</div>
              <div class="flex gap-1">
                <button class="flex-1 text-xs py-1 rounded-md border transition-colors"
                  :class="scope === 'site' ? 'bg-accent text-accent-foreground' : 'hover:bg-accent/50'" @click="setScope('site')">
                  {{ locationName }}
                </button>
                <button class="flex-1 text-xs py-1 rounded-md border transition-colors"
                  :class="scope === 'global' ? 'bg-accent text-accent-foreground' : 'hover:bg-accent/50'" @click="setScope('global')">
                  Global default
                </button>
              </div>
              <div class="flex items-center justify-between gap-2">
                <span class="text-sm">Degraded when ≥</span>
                <div class="flex items-center gap-1.5">
                  <input type="number" min="1" v-model.number="form.degradedAt"
                    class="w-14 text-sm text-right bg-background border rounded-md px-2 py-1 focus:outline-none focus:ring-2 focus:ring-ring" />
                  <span class="text-xs text-muted-foreground">offline</span>
                </div>
              </div>
              <div class="flex items-center justify-between gap-2">
                <span class="text-sm">Down when ≥</span>
                <div class="flex items-center gap-1.5">
                  <input type="number" min="1" v-model.number="form.downAt"
                    class="w-14 text-sm text-right bg-background border rounded-md px-2 py-1 focus:outline-none focus:ring-2 focus:ring-ring" />
                  <span class="text-xs text-muted-foreground">offline</span>
                </div>
              </div>
              <div class="flex items-center gap-2">
                <Button size="sm" class="flex-1" @click="saveSettings">Save</Button>
                <Button v-if="scope === 'site' && settingsData && settingsData.override" size="sm" variant="ghost"
                  class="text-muted-foreground" @click="useGlobal">Use global</Button>
              </div>
              <div class="text-[11px] text-muted-foreground">
                <template v-if="scope === 'site'">
                  <span v-if="settingsData && settingsData.override">Site override active — overrides the global default.</span>
                  <span v-else>Using the global default. Save here to create a site override.</span>
                </template>
                <template v-else>Applies to every site without its own override.</template>
              </div>
            </div>
          </div>

          <Button variant="ghost" size="icon" class="h-9 w-9" @click="exportCSV"
            data-tooltip="Export CSV" data-tip-pos="bottom">
            <Download class="h-5 w-5" />
          </Button>
          <Button variant="ghost" size="icon" class="h-9 w-9" @click="forceSweep" :disabled="sweeping"
            data-tooltip="Force sweep now" data-tip-pos="bottom">
            <Zap class="h-5 w-5" :class="{ 'animate-pulse text-primary': sweeping }" />
          </Button>
          <Button variant="ghost" size="icon" class="h-9 w-9" @click="fetchInventory"
            data-tooltip="Refresh" data-tip-pos="bottom">
            <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
          </Button>
        </div>
      </div>

      <!-- Status band -->
      <div v-if="status" class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-3">
          <span class="status-pill" :class="statusMeta.cls">{{ statusMeta.label }}</span>
          <span v-if="counts" class="text-sm text-muted-foreground font-mono">
            {{ counts.online }} online · {{ counts.offline }} offline<template v-if="counts.excluded"> · {{ counts.excluded }} excluded</template>
          </span>
        </div>
        <label class="flex items-center gap-1.5 text-xs text-muted-foreground cursor-pointer select-none">
          <input type="checkbox" v-model="showExcluded" />
          show excluded
        </label>
      </div>

      <!-- Empty state -->
      <div v-if="loaded && phones.length === 0" class="empty-state">
        <template v-if="updatedAt">
          <div class="text-base font-semibold mb-1">No phones registered</div>
          <div class="text-sm text-muted-foreground">
            The PBX is reachable and was swept {{ updatedLabel }}, but no desk phones are
            currently registered at {{ locationName }}.
          </div>
        </template>
        <template v-else>
          <div class="text-base font-semibold mb-1">No phones reported yet</div>
          <div class="text-sm text-muted-foreground">
            The collector hasn't pushed inventory for {{ locationName }} yet — it appears
            within one sweep, or hit <span class="font-mono">force sweep</span> above.
          </div>
        </template>
      </div>

      <!-- Directory table -->
      <section v-if="phones.length">
        <div class="overflow-x-auto rounded-lg border">
          <table class="w-full text-sm directory">
            <thead>
              <tr>
                <th v-for="col in COLUMNS" :key="col.key" @click="sortBy(col.key)"
                  :class="['sortable', { active: sortKey === col.key }]">
                  <span class="th-inner">{{ col.label }}<span class="arrow">{{ arrow(col.key) }}</span></span>
                </th>
                <th class="th-center">Monitor</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="p in sortedPhones" :key="'row-' + p.ext + p.mac" :class="{ 'row-excluded': p.excluded }">
                <td><span class="lamp-dot" :class="p.online ? 'up' : 'down'"></span></td>
                <td class="mono strong">{{ p.ext || '—' }}</td>
                <td>{{ p.name || 'unassigned' }}</td>
                <td class="mono dim">{{ formatPhone(p.did) }}</td>
                <td class="dim">{{ p.department || '—' }}</td>
                <td class="dim">{{ p.model || '—' }}</td>
                <td class="mono dim">{{ p.firmware || '—' }}</td>
                <td class="mono">{{ p.ip || '—' }}</td>
                <td class="mono dim">{{ p.mac || '—' }}</td>
                <td>
                  <span class="pill" :class="p.sipStatus === 'registered' ? 'pill-on' : 'pill-off'">{{ p.sipStatus || 'unknown' }}</span>
                </td>
                <td><span :class="p.reachable ? 'st-text-up' : 'st-text-down'">{{ p.reachable ? 'yes' : 'no' }}</span></td>
                <td class="center">
                  <button type="button" class="switch" :class="{ on: !p.excluded }" role="switch"
                    :aria-checked="!p.excluded" @click="toggleExclude(p)"
                    :title="p.excluded ? 'Excluded — click to monitor' : 'Monitored — click to exclude'">
                    <span class="knob"></span>
                  </button>
                </td>
              </tr>
              <tr v-if="sortedPhones.length === 0">
                <td colspan="12" class="py-10 text-center text-muted-foreground">No lines match “{{ search }}”.</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft, RefreshCw, SlidersHorizontal, Download, Zap } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { generatePrettyTimeAgo } from '@/utils/time'
import { addToast } from '@/store'

const route = useRoute()

const locationName = computed(() => {
  const slug = (route.params.key || '').replace(/^phones_/, '')
  return slug.split('-').map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ')
})

const loading = ref(false)
const loaded = ref(false)
const search = ref('')
const updatedAt = ref(null)
const phones = ref([])
const status = ref('')
const counts = ref(null)
const showExcluded = ref(true)

const STATUS_META = {
  healthy:  { label: 'Healthy',  cls: 'st-up' },
  degraded: { label: 'Degraded', cls: 'st-degraded' },
  down:     { label: 'Down',     cls: 'st-down' },
}
const statusMeta = computed(() => STATUS_META[status.value] || { label: '—', cls: 'st-none' })

const COLUMNS = [
  { key: 'online', label: 'Status' },
  { key: 'ext', label: 'Ext' },
  { key: 'name', label: 'Name' },
  { key: 'did', label: 'Direct #' },
  { key: 'department', label: 'Department' },
  { key: 'model', label: 'Model' },
  { key: 'firmware', label: 'Firmware' },
  { key: 'ip', label: 'IP address' },
  { key: 'mac', label: 'MAC' },
  { key: 'sipStatus', label: 'SIP' },
  { key: 'reachable', label: 'Reach' },
]

const filteredPhones = computed(() => {
  const q = search.value.trim().toLowerCase()
  let list = phones.value
  if (!showExcluded.value) list = list.filter(p => !p.excluded)
  if (!q) return list
  return list.filter(p =>
    [p.ext, p.name, p.did, p.department, p.ip, p.mac, p.model].some(v => (v || '').toString().toLowerCase().includes(q)))
})

// --- Column sorting (click a header to toggle asc/desc) ---
const sortKey = ref('ext')
const sortDir = ref('asc')
const sortBy = (key) => {
  if (sortKey.value === key) sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  else { sortKey.value = key; sortDir.value = 'asc' }
}
const arrow = (key) => (sortKey.value !== key ? '' : sortDir.value === 'asc' ? '▲' : '▼')

const sortedPhones = computed(() => {
  const list = [...filteredPhones.value]
  const k = sortKey.value
  const dir = sortDir.value === 'asc' ? 1 : -1
  return list.sort((a, b) => {
    let av = a[k], bv = b[k]
    if (typeof av === 'boolean' || typeof bv === 'boolean') { av = av ? 1 : 0; bv = bv ? 1 : 0 }
    if (av == null) av = ''
    if (bv == null) bv = ''
    if (typeof av === 'number' && typeof bv === 'number') return (av - bv) * dir
    return String(av).localeCompare(String(bv), undefined, { numeric: true }) * dir
  })
})

const updatedLabel = computed(() => {
  if (!updatedAt.value) return 'never'
  try { return generatePrettyTimeAgo(new Date(updatedAt.value)) } catch (e) { return '—' }
})

const fetchInventory = async () => {
  loading.value = true
  try {
    const res = await fetch(`/api/v1/phones/${route.params.key}`, { cache: 'no-store' })
    if (res.ok) {
      const data = await res.json()
      phones.value = Array.isArray(data.phones) ? data.phones : []
      updatedAt.value = data.updatedAt || null
      status.value = data.status || ''
      counts.value = data.counts || null
    } else {
      phones.value = []
      updatedAt.value = null
      status.value = ''
      counts.value = null
    }
  } catch (e) {
    phones.value = []
    updatedAt.value = null
  } finally {
    loading.value = false
    loaded.value = true
  }
}

// Force an immediate collector sweep instead of waiting out its loop. The
// collector claims the request within ~2s and re-reports; poll for the fresh
// push (detected by a changed updatedAt) for up to ~15s.
const sweeping = ref(false)
const forceSweep = async () => {
  if (sweeping.value) return
  sweeping.value = true
  const before = updatedAt.value
  try {
    const res = await fetch(`/api/v1/phones/${route.params.key}/sweep`, { method: 'POST' })
    if (!res.ok) throw new Error()
    addToast('Sweep requested — refreshing…', 'success')
    const started = Date.now()
    const iv = setInterval(async () => {
      await fetchInventory()
      if (updatedAt.value !== before || Date.now() - started > 15000) {
        clearInterval(iv)
        sweeping.value = false
      }
    }, 1500)
  } catch (e) {
    addToast('Couldn’t request a sweep — try again', 'error')
    sweeping.value = false
  }
}

// Toggle a phone in/out of the exclusion list (persisted server-side; the
// collector picks it up next sweep). Optimistic local update for instant feedback.
const toggleExclude = async (p) => {
  const next = !p.excluded
  p.excluded = next
  const who = `${p.ext}${p.name ? ' · ' + p.name : ''}`
  try {
    const res = await fetch(`/api/v1/phones/${route.params.key}/exclusions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ext: p.ext, excluded: next }),
    })
    if (!res.ok) throw new Error('save failed')
    addToast(next ? `Excluded ${who}` : `Now monitoring ${who}`, 'success')
  } catch (e) {
    p.excluded = !next // revert on failure
    addToast(`Couldn't update ${p.ext} — try again`, 'error')
  }
}

// --- Live-editable health thresholds (global default + per-site override) ---
const settingsOpen = ref(false)
const scope = ref('site')
const settingsData = ref(null)
const form = ref({ degradedAt: 2, downAt: 10 })

const applyFormFromScope = () => {
  if (!settingsData.value) return
  const src = scope.value === 'global'
    ? settingsData.value.global
    : (settingsData.value.override || settingsData.value.global)
  form.value = { degradedAt: src.degradedAt, downAt: src.downAt }
}
const fetchSettings = async () => {
  try {
    const res = await fetch(`/api/v1/phones/${route.params.key}/settings`, { cache: 'no-store' })
    if (res.ok) { settingsData.value = await res.json(); applyFormFromScope() }
  } catch (e) { /* non-fatal */ }
}
const toggleSettings = () => {
  settingsOpen.value = !settingsOpen.value
  if (settingsOpen.value) fetchSettings()
}
const setScope = (s) => { scope.value = s; applyFormFromScope() }
const saveSettings = async () => {
  try {
    const res = await fetch(`/api/v1/phones/${route.params.key}/settings`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ scope: scope.value, degradedAt: form.value.degradedAt, downAt: form.value.downAt }),
    })
    if (!res.ok) throw new Error()
    settingsData.value = await res.json()
    applyFormFromScope()
    addToast(scope.value === 'global' ? 'Global thresholds saved' : `${locationName.value} thresholds saved`, 'success')
  } catch (e) { addToast('Couldn’t save thresholds — try again', 'error') }
}
const useGlobal = async () => {
  try {
    const res = await fetch(`/api/v1/phones/${route.params.key}/settings`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ scope: 'site', clear: true }),
    })
    if (!res.ok) throw new Error()
    settingsData.value = await res.json()
    applyFormFromScope()
    addToast(`${locationName.value} now uses the global default`, 'success')
  } catch (e) { addToast('Couldn’t reset — try again', 'error') }
}

// Format a DID like +12562516110 -> +1-256-251-6110.
const formatPhone = (v) => {
  if (!v) return '—'
  const d = String(v).replace(/\D/g, '')
  const ten = d.length === 11 && d[0] === '1' ? d.slice(1) : d
  if (ten.length === 10) return `+1-${ten.slice(0, 3)}-${ten.slice(3, 6)}-${ten.slice(6)}`
  return v
}

// Export the current (filtered + sorted) view as CSV.
const exportCSV = () => {
  const cols = [
    ['ext', 'Ext'], ['name', 'Name'], ['did', 'Direct #'], ['department', 'Department'],
    ['model', 'Model'], ['firmware', 'Firmware'], ['ip', 'IP'], ['mac', 'MAC'],
    ['sipStatus', 'SIP'], ['online', 'Online'], ['reachable', 'Reachable'], ['excluded', 'Excluded'],
  ]
  const esc = (val) => {
    const s = val == null ? '' : String(val)
    return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s
  }
  const cell = (p, k) => {
    if (k === 'did') return formatPhone(p.did) === '—' ? '' : formatPhone(p.did)
    const v = p[k]
    return typeof v === 'boolean' ? (v ? 'yes' : 'no') : v
  }
  const lines = [cols.map(c => c[1]).join(',')]
  for (const p of sortedPhones.value) lines.push(cols.map(([k]) => esc(cell(p, k))).join(','))
  const blob = new Blob([lines.join('\r\n')], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${route.params.key}_phones.csv`
  a.click()
  URL.revokeObjectURL(url)
  addToast(`Exported ${sortedPhones.value.length} phones`, 'success')
}

let poll = null
onMounted(() => {
  fetchInventory()
  poll = setInterval(fetchInventory, 15000)
})
onUnmounted(() => { if (poll) clearInterval(poll) })
</script>

<style scoped>
.phone-panel { width: 100%; }

.empty-state {
  border: 1px dashed hsl(var(--border));
  border-radius: 10px;
  padding: 2.5rem 1.5rem;
  text-align: center;
}

/* Equipment-panel micro-label */
.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: hsl(var(--muted-foreground));
  font-weight: 600;
}

/* Status lamp in the table's first column */
.lamp-dot { width: 8px; height: 8px; border-radius: 999px; display: inline-block; align-self: center; }
.lamp-dot.up   { background: var(--status-up);   box-shadow: 0 0 7px -1px var(--status-up); }
.lamp-dot.down { background: var(--status-down); box-shadow: 0 0 7px -1px var(--status-down); }

/* Sweep indicator dot (static — no pulsing) */
.sweep-dot { width: 7px; height: 7px; border-radius: 999px; background: var(--status-up); box-shadow: 0 0 7px -1px var(--status-up); }
.sweep-dot.dead { background: hsl(var(--muted-foreground) / 0.5); box-shadow: none; }

/* --- Directory table --- */
.directory thead th {
  text-align: left; font-size: 10px; letter-spacing: 0.12em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); font-weight: 600; padding: 0.6rem 0.75rem;
  border-bottom: 1px solid hsl(var(--border)); background: hsl(var(--muted) / 0.4);
  white-space: nowrap;
}
.directory thead th.sortable { cursor: pointer; user-select: none; }
.directory thead th.sortable:hover { color: hsl(var(--foreground)); background: hsl(var(--muted) / 0.7); }
.directory thead th.active { color: hsl(var(--foreground)); }
.directory .th-inner { display: inline-flex; align-items: center; gap: 0.3rem; }
.directory .arrow { font-size: 8px; line-height: 1; }
.directory tbody td { padding: 0.5rem 0.75rem; border-bottom: 1px solid hsl(var(--border) / 0.6); }
.directory tbody tr:last-child td { border-bottom: 0; }
.directory tbody tr:hover td { background: hsl(var(--accent) / 0.4); }
.directory .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.directory .strong { font-weight: 700; }
.directory .dim { color: hsl(var(--muted-foreground)); }

.pill { font-size: 10px; letter-spacing: 0.04em; text-transform: uppercase; padding: 0.1rem 0.4rem; border-radius: 5px; font-weight: 600; }
.pill-on { background: var(--status-up); color: #fff; }
.pill-off { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }

/* Overall status pill in the band */
.status-pill { font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em; padding: 0.2rem 0.65rem; border-radius: 6px; color: #fff; }
.status-pill.st-up { background: var(--status-up); }
.status-pill.st-degraded { background: var(--status-degraded); }
.status-pill.st-down { background: var(--status-down); }
.status-pill.st-none { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }

/* Monitor column + excluded rows */
.directory th.th-center, .directory td.center { text-align: center; }
.directory tr.row-excluded td:not(.center) { opacity: 0.4; }

/* Clean toggle switch (monitor on/off) */
.switch {
  display: inline-block; position: relative; width: 34px; height: 18px; padding: 0;
  border-radius: 999px; border: 1px solid hsl(var(--border));
  background: hsl(var(--muted)); cursor: pointer; vertical-align: middle;
  transition: background 0.15s ease, border-color 0.15s ease;
}
.switch .knob {
  position: absolute; top: 1px; left: 1px; width: 14px; height: 14px; border-radius: 999px;
  background: #fff; box-shadow: 0 1px 2px rgb(0 0 0 / 0.35); transition: transform 0.15s ease;
}
.switch.on { background: var(--status-up); border-color: var(--status-up); }
.switch.on .knob { transform: translateX(16px); }
.switch:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }
@media (prefers-reduced-motion: reduce) { .switch, .switch .knob { transition: none; } }

</style>
