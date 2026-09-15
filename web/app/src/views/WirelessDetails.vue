<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 space-y-4 wl-page">

      <!-- Toolbar -->
      <div class="flex items-end justify-between gap-4 flex-wrap">
        <div class="flex items-center gap-3">
          <router-link to="/" class="text-muted-foreground hover:text-foreground transition-colors mb-1"
            data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-5 w-5" />
          </router-link>
          <div>
            <div class="eyebrow">
              Wireless panel<template v-if="apsTotal"> · {{ apsTotal }} access {{ apsTotal === 1 ? 'point' : 'points' }}</template>
            </div>
            <h1 class="text-2xl font-bold tracking-tight leading-none mt-0.5">
              {{ siteName }} <span class="text-muted-foreground font-normal">wireless</span>
            </h1>
          </div>
        </div>
        <div class="flex items-center gap-2 mb-0.5">
          <span class="live-ind" :class="{ on: fresh }"><span class="ldot"></span>{{ fresh ? 'live' : updatedLabel }}</span>
          <input v-model="search" type="text" placeholder="filter ap / model / ip"
            class="wl-filter" aria-label="Filter access points" />
          <Button variant="ghost" size="icon" class="h-9 w-9" @click="refreshAll"
            data-tooltip="Refresh" data-tip-pos="bottom">
            <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
          </Button>
          <MonitorToggle :endpoint-key="routeKey" />
        </div>
      </div>

      <!-- Monitoring paused: a state, not a failure -->
      <div v-if="!monitored" class="paused">
        <PauseCircle class="h-4 w-4 shrink-0" />
        <div>
          <span class="paused-title">Monitoring is paused for {{ routeKey }}.</span>
          The bars below are frozen and the collector's pushes are rejected while it stays paused.
          Recorded history is kept — resume with the toggle above and the same timeline continues.
        </div>
      </div>

      <!-- Loading (first pass only; a poll never blanks the page) -->
      <div v-if="!loaded" class="notice">
        <div class="notice-title">Loading the wireless snapshot…</div>
        <p class="notice-body">Reading the last push for <code>{{ routeKey }}</code>.</p>
      </div>

      <!-- Nothing ever reported for this key -->
      <div v-else-if="!snapshot" class="notice">
        <div class="notice-title">No wireless snapshot for this key yet</div>
        <p class="notice-body">
          <code>unifi-collector</code> hasn't pushed <code>{{ routeKey }}</code>. Snapshots are held in
          memory, so a restarted Gatus is blank until the next sweep — that lands within a minute.
          If it stays blank, check the <code>unifi-collector</code> container log for
          <code>{{ routeKey }}</code>, and check that <code>UNIFI_API_KEY</code> and
          <code>UNIFI_PUSH_TOKEN</code> are set.
        </p>
      </div>

      <template v-else>
        <!-- Status band -->
        <div class="flex flex-wrap items-center gap-3">
          <span class="status-pill" :class="statusMeta.cls">{{ statusMeta.label }}</span>
          <span class="band-facts">
            <b>{{ apsOnline }}</b>/{{ apsTotal }} APs online · <b>{{ fmt(counts.clients) }}</b> wifi clients
            <template v-if="controller"> · {{ controller }}</template>
          </span>
        </div>

        <!-- Counts strip -->
        <div class="strip">
          <div v-for="cell in cells" :key="cell.label" class="cell" :class="cell.tone">
            <div class="cell-label">{{ cell.label }}</div>
            <div class="cell-value">{{ cell.value }}</div>
            <div class="cell-sub">{{ cell.sub }}</div>
          </div>
        </div>

        <!-- The last check failed: the only red panel on this page -->
        <div v-if="latestErrors.length" class="notice notice-error">
          <div class="notice-title flex items-center gap-2">
            <AlertTriangle class="h-4 w-4" /> The last check failed
          </div>
          <pre class="err-pre">{{ latestErrors.join('\n') }}</pre>
          <p class="notice-body mt-2">
            A site reporting nothing at all fails with <code>no unifi reporting</code> and leaves
            the bars grey — that is a collector that couldn't read the console, not a wifi outage.
          </p>
        </div>

        <!-- Passed, carrying a reason: amber, because a partial problem is not an outage -->
        <div v-else-if="latestWarnings.length" class="notice notice-warn">
          <div class="notice-title flex items-center gap-2">
            <AlertTriangle class="h-4 w-4" /> The last check passed, with something worth reading
          </div>
          <pre class="warn-pre">{{ latestWarnings.join('\n') }}</pre>
          <p class="notice-body mt-2">
            The collector calls this <code>degraded</code> and pushes it as a pass, so a partial
            problem doesn't page anyone. Wifi is still serving clients — the line above names what
            isn't.
          </p>
        </div>

        <!-- Check history -->
        <section v-if="results.length" class="panel">
          <div class="panel-head">
            <div class="eyebrow">Check history · last {{ results.length }} results</div>
            <span class="panel-note">{{ uptimeLabel }}</span>
          </div>
          <div class="bars">
            <span v-for="(r, i) in results" :key="i" class="bar" :class="barClass(r)"
              :data-tooltip="barTip(r)" data-tip-pos="bottom"></span>
          </div>
          <div class="bars-axis">
            <span class="font-mono">{{ oldestLabel }}</span>
            <span class="font-mono">now</span>
          </div>
        </section>

        <!-- History over time. One picker scopes every chart under it, and the
             choice is shared with the other drill-ins so the window follows you. -->
        <section class="hist-sec">
          <div class="hist-head">
            <div class="eyebrow">History</div>
            <RangeSelector v-model="range" label="History range" />
          </div>
          <div class="chart-grid">
            <HistoryChart
              title="Access points online"
              :subtitle="apChartSub"
              :series="apsOnlineSeries"
              kind="area"
              :loading="historyLoading"
              :empty-text="metricEmptyText"
              :note="metricNote"
            />
            <HistoryChart
              title="Wi-Fi clients"
              subtitle="Clients associated to this site's APs"
              :series="clientsSeries"
              kind="area"
              :loading="historyLoading"
              :empty-text="metricEmptyText"
              :note="metricNote"
            />
            <!-- Only drawn when the console actually reported it: some firmwares
                 never send tx retry, and an empty plot reads as a broken radio. -->
            <HistoryChart
              v-if="hasTxRetry"
              title="Tx retry"
              subtitle="Site average. Worth reading over 20%."
              unit="%"
              :series="txRetrySeries"
              kind="area"
              :loading="historyLoading"
              :empty-text="metricEmptyText"
              :note="metricNote"
            />
            <HistoryChart
              title="Uptime"
              subtitle="Share of checks that passed. A degraded sweep counts as a pass."
              :series="uptimeSeries"
              kind="column"
              ratio
              :warn-below="UPTIME_WARN"
              :down-below="UPTIME_DOWN"
              :loading="historyLoading"
              :empty-text="uptimeEmptyText"
              :note="uptimeNote"
            />
          </div>
        </section>

        <!-- THE ROSTER MAP -->
        <section v-if="aps.length" class="panel">
          <div class="panel-head">
            <div>
              <div class="eyebrow">Access point map</div>
              <div class="panel-title">{{ apsOnline }} of {{ apsTotal }} online</div>
            </div>
            <button v-if="selected" type="button" class="clear-sel" @click="clearSelection">
              clear selection
            </button>
          </div>
          <div class="roster-scroll">
            <div class="roster">
              <button v-for="ap in aps" :key="ap.name + ap.ip" type="button" class="tile"
                :class="[tileClass(ap), { sel: selected === ap.name }]" :aria-pressed="selected === ap.name"
                :data-tooltip="tileTip(ap)" data-tip-pos="bottom" @click="pick(ap)">
                <span class="tile-name">{{ ap.name || '—' }}</span>
                <span class="tile-sub">{{ ap.model || 'unknown model' }}</span>
              </button>
            </div>
          </div>
          <div class="legend">
            <span class="lg"><i class="sw sw-on"></i>online ({{ onlineCount }})</span>
            <span class="lg"><i class="sw sw-pend"></i>update pending ({{ pendingCount }})</span>
            <span class="lg"><i class="sw sw-off"></i>offline ({{ offlineCount }})</span>
            <span class="lg lg-dim">click a tile to filter the table</span>
          </div>
          <p v-if="rosterNote" class="panel-note mt-2">{{ rosterNote }}</p>
        </section>

        <!-- No APs at all: honest, not a failure -->
        <div v-else class="notice">
          <div class="notice-title">No access points on this site</div>
          <p class="notice-body">
            <code>unifi-collector</code> read {{ controller || 'this console' }}
            {{ updatedLabel }} and found no adopted wireless devices. If APs belong here, adopt them
            in UniFi — they appear on the next sweep. If they never will, drop the
            <code>{{ routeKey }}</code> row from <code>config.yaml</code> instead of watching an empty site.
          </p>
        </div>

        <!-- AP table -->
        <section v-if="aps.length" ref="tableSection">
          <div class="sec-head">
            <div class="eyebrow">
              {{ showAllDevices ? 'All adopted devices' : 'Access points' }}
              · {{ apsOnline }} of {{ apsTotal }} online
            </div>
            <div class="flex items-center gap-3">
              <button
                v-if="nonWirelessCount"
                type="button"
                class="panel-note underline underline-offset-2 hover:text-foreground"
                @click="showAllDevices = !showAllDevices"
              >
                {{ showAllDevices
                  ? 'radios only'
                  : `show ${nonWirelessCount} switch/other device${nonWirelessCount === 1 ? '' : 's'}` }}
              </button>
              <span class="panel-note">showing {{ sortedAps.length }} of {{ aps.length }} listed</span>
            </div>
          </div>
          <div class="overflow-x-auto rounded-lg border">
            <table class="w-full text-sm roster-table">
              <thead>
                <tr>
                  <th v-for="col in COLUMNS" :key="col.key" :class="['sortable', { active: sortKey === col.key }]">
                    <button type="button" class="th-btn" @click="sortBy(col.key)"
                      :aria-label="'Sort by ' + col.label">
                      {{ col.label }}<span class="arrow">{{ arrow(col.key) }}</span>
                    </button>
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="ap in sortedAps" :key="'row-' + ap.name + ap.ip"
                  :class="{ 'row-sel': selected === ap.name, 'row-off': !isOnline(ap) }">
                  <td>
                    <span class="lamp" :class="isOnline(ap) ? 'up' : 'down'"></span>
                    <span :class="isOnline(ap) ? 'st-text-up' : 'st-text-down'">{{ stateLabel(ap) }}</span>
                  </td>
                  <td class="strong">{{ ap.name || '—' }}</td>
                  <td class="mono dim">{{ ap.model || '—' }}</td>
                  <td class="mono">{{ ap.ip || '—' }}</td>
                  <td class="mono dim">{{ ap.version || '—' }}</td>
                  <td>
                    <span class="pill" :class="upToDate(ap) ? 'pill-ok' : 'pill-pend'">{{ firmwareLabel(ap) }}</span>
                  </td>
                  <td class="dim">{{ sinceLabel(ap) }}</td>
                </tr>
                <tr v-if="!sortedAps.length">
                  <td colspan="7" class="py-10 text-center text-muted-foreground">
                    No access points match “{{ search }}”.
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft, RefreshCw, AlertTriangle, PauseCircle } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import MonitorToggle from '@/components/MonitorToggle.vue'
import RangeSelector from '@/components/RangeSelector.vue'
import HistoryChart from '@/components/HistoryChart.vue'
import { generatePrettyTimeAgo, prettifyTimestamp } from '@/utils/time'
import { isMonitored, now, historyRange, setHistoryRange } from '@/store'

const route = useRoute()
const routeKey = computed(() => route.params.key || '')
const monitored = computed(() => isMonitored(routeKey.value))

const RESULT_WINDOW = 50
const POLL_MS = 20000
// The snapshot and the bars are live, so they poll hard. A range of history is a
// far bigger read and barely moves inside twenty seconds, so it refreshes on its
// own slower clock, on a range change and on the refresh button.
const HISTORY_POLL_MS = 300000
// The collector's own prefix for "nothing was read at all". A failure carrying it
// is an absence of data, not a red outage — the bars stay grey, matching LocationCard.
const NOT_REPORTING = /^no unifi reporting\b/i

const loading = ref(false)
const loaded = ref(false)
const snapshot = ref(null)
const results = ref([])
const search = ref('')
const selected = ref('')
const tableSection = ref(null)

// --- Snapshot shape (see collect_wireless in collector/unifi_collector.py) ---
const counts = computed(() => (snapshot.value && snapshot.value.counts) || {})
const detail = computed(() => (snapshot.value && snapshot.value.detail) || {})
const wlan = computed(() => detail.value.wlan || {})
// Everything adopted to the console, radios and otherwise. Absent on snapshots
// pushed by a collector older than the auto-discovery change, hence the
// fall-back to detail.aps below, which keeps this page working mid-rollout.
const allDevices = computed(() =>
  (Array.isArray(detail.value.devices) ? detail.value.devices : []))
const showAllDevices = ref(false)
const aps = computed(() => {
  if (allDevices.value.length) {
    return showAllDevices.value
      ? allDevices.value
      : allDevices.value.filter((d) => d.wireless)
  }
  return Array.isArray(detail.value.aps) ? detail.value.aps : []
})
const nonWirelessCount = computed(() =>
  allDevices.value.filter((d) => !d.wireless).length)
const controller = computed(() => detail.value.controller || '')

const siteName = computed(() => {
  const named = detail.value.site || (snapshot.value && snapshot.value.site)
  if (named) return named
  const slug = routeKey.value.replace(/^wireless_/, '')
  return slug.split('-').map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ') || 'Site'
})

const isOnline = (ap) => String(ap.state || '').toUpperCase() === 'ONLINE'
const upToDate = (ap) => String(ap.firmware || '').toLowerCase().replace(/[^a-z]/g, '') === 'uptodate'

// The site statistics are the truth for totals; the device list can name fewer
// devices than the counts do, so never derive one from the other.
const apsTotal = computed(() => (counts.value.apsTotal != null ? counts.value.apsTotal : aps.value.length))
const apsOnline = computed(() => (
  counts.value.apsOnline != null ? counts.value.apsOnline : aps.value.filter(isOnline).length
))
const onlineCount = computed(() => aps.value.filter(a => isOnline(a) && upToDate(a)).length)
const pendingCount = computed(() => aps.value.filter(a => isOnline(a) && !upToDate(a)).length)
const offlineCount = computed(() => aps.value.filter(a => !isOnline(a)).length)
const rosterNote = computed(() => {
  if (!aps.value.length || aps.value.length === apsTotal.value) return ''
  return `The console listed ${aps.value.length} devices this sweep but its site statistics count `
    + `${apsTotal.value}. The map shows what was listed; the counts above are the site's own numbers.`
})

const STATUS_META = {
  healthy: { label: 'Healthy', cls: 'st-up' },
  degraded: { label: 'Degraded', cls: 'st-degraded' },
  down: { label: 'Down', cls: 'st-down' },
}
const statusMeta = computed(() => STATUS_META[(snapshot.value || {}).status] || { label: 'Unknown', cls: 'st-none' })

// --- Counts strip -------------------------------------------------------
const fmt = (v) => (v == null ? '—' : String(v))
const txTone = computed(() => {
  const v = counts.value.txRetryPct
  if (v == null) return ''
  if (v > 35) return 'tone-bad'
  if (v > 20) return 'tone-warn'
  return ''
})
const cells = computed(() => [
  {
    label: 'APs online', value: `${apsOnline.value}/${apsTotal.value}`,
    sub: offlineCount.value ? `${offlineCount.value} offline` : 'all up',
    tone: apsOnline.value < apsTotal.value ? 'tone-bad' : '',
  },
  { label: 'Wifi clients', value: fmt(counts.value.clients), sub: 'associated now', tone: '' },
  {
    label: 'Guests', value: fmt(counts.value.guests),
    sub: counts.value.guests ? 'on the guest network' : 'none on guest', tone: '',
  },
  { label: 'Wired clients', value: fmt(counts.value.wiredClients), sub: 'same site', tone: '' },
  {
    label: 'Tx retry',
    value: counts.value.txRetryPct == null ? '—' : `${counts.value.txRetryPct}%`,
    sub: counts.value.txRetryPct == null ? 'not reported' : 'warn over 20%',
    tone: txTone.value,
  },
  { label: 'SSIDs', value: fmt(wlan.value.ssids), sub: 'broadcast here', tone: '' },
  {
    label: 'Updates pending', value: fmt(wlan.value.pendingUpdate),
    sub: 'all site devices', tone: wlan.value.pendingUpdate ? 'tone-warn' : '',
  },
])

// --- Check history bars -------------------------------------------------
const latest = computed(() => (results.value.length ? results.value[results.value.length - 1] : null))
const errorsOf = (r) => ((r && Array.isArray(r.errors) ? r.errors : []).filter(Boolean))

// An external endpoint result only carries a bool, but the collector reports three
// states. It pushes `degraded` as a PASS carrying its reason, so a partial problem
// never fires a down alert — which makes "passed with errors" a WARNING, not an
// outage. Same rule as isWarning() in LocationCard.vue; keep the two in step.
const isWarning = (r) => !!r && r.success && errorsOf(r).length > 0

// The warning text and the failure text are never both shown: one result is
// either a pass carrying a reason (amber) or a failure (red).
const latestWarnings = computed(() => (isWarning(latest.value) ? errorsOf(latest.value) : []))
const latestErrors = computed(() => (
  latest.value && !latest.value.success ? errorsOf(latest.value) : []
))

// Four bar states, in the dashboard's own vocabulary:
//   pass, clean            → green
//   pass, carrying a reason → amber (degraded: 4 of 23 APs offline)
//   fail, nothing read      → grey (an absence of data, not an outage)
//   fail                    → red
const barClass = (r) => {
  if (r.success) return isWarning(r) ? 'stbar-degraded' : 'stbar-up'
  return errorsOf(r).some(e => NOT_REPORTING.test(e)) ? 'stbar-nodata' : 'stbar-down'
}
const barTip = (r) => {
  const state = r.success ? (isWarning(r) ? 'passed with a warning' : 'check passed') : 'check failed'
  const head = `${prettifyTimestamp(r.timestamp)} · ${state}`
  const errs = errorsOf(r).join(' · ')
  return errs ? `${head} · ${errs}` : head
}
const uptimeLabel = computed(() => {
  if (!results.value.length) return ''
  const ok = results.value.filter(r => r.success).length
  return `${Math.round((ok / results.value.length) * 100)}% passed in this window`
})
const oldestLabel = computed(() => {
  const first = results.value[0]
  if (!first) return ''
  try { return generatePrettyTimeAgo(first.timestamp, now.value) } catch (e) { return '' }
})

// --- Timestamps ---------------------------------------------------------
// startupTime arrives as RFC3339 from the site-manager API, but epoch seconds
// from some firmwares — accept either rather than printing a wrong date.
const asDate = (v) => {
  if (v == null || v === '') return null
  if (typeof v === 'number' || /^\d+$/.test(String(v))) {
    const n = Number(v)
    const ms = n < 1e12 ? n * 1000 : n
    return Number.isFinite(ms) ? new Date(ms) : null
  }
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? null : d
}
const updatedAt = computed(() => asDate(snapshot.value && snapshot.value.updatedAt))
const updatedLabel = computed(() => {
  if (!updatedAt.value) return 'never'
  try { return generatePrettyTimeAgo(updatedAt.value, now.value) } catch (e) { return '—' }
})
const fresh = computed(() => !!updatedAt.value && now.value - updatedAt.value.getTime() < 180000)
const sinceLabel = (ap) => {
  const d = asDate(ap.since)
  if (!d) return 'unknown'
  try { return generatePrettyTimeAgo(d, now.value) } catch (e) { return 'unknown' }
}
const stateLabel = (ap) => (ap.state ? String(ap.state).toLowerCase() : 'unknown')
const firmwareLabel = (ap) => {
  if (upToDate(ap)) return 'up to date'
  const raw = String(ap.firmware || '').trim()
  if (!raw) return 'unknown'
  return raw.replace(/([a-z])([A-Z])/g, '$1 $2').toLowerCase()
}

// --- Roster tiles -------------------------------------------------------
const tileClass = (ap) => {
  if (!isOnline(ap)) return 'tile-off'
  return upToDate(ap) ? 'tile-on' : 'tile-pend'
}
const tileTip = (ap) => [
  ap.name || 'unnamed',
  ap.model || 'unknown model',
  ap.ip || 'no ip',
  stateLabel(ap),
  upToDate(ap) ? `firmware ${ap.version || '—'}` : `firmware ${firmwareLabel(ap)}`,
].join(' · ')

const pick = (ap) => {
  if (selected.value === ap.name) { clearSelection(); return }
  selected.value = ap.name || ''
  search.value = ap.name || ''
  nextTick(() => {
    const el = tableSection.value
    if (!el || !el.scrollIntoView) return
    const still = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    el.scrollIntoView({ behavior: still ? 'auto' : 'smooth', block: 'center' })
  })
}
const clearSelection = () => { selected.value = ''; search.value = '' }

// --- Table --------------------------------------------------------------
const COLUMNS = [
  { key: 'state', label: 'State' },
  { key: 'name', label: 'Access point' },
  { key: 'model', label: 'Model' },
  { key: 'ip', label: 'IP address' },
  { key: 'version', label: 'Version' },
  { key: 'firmware', label: 'Firmware' },
  { key: 'since', label: 'Up since' },
]

// Offline first: the reason anyone opens this page.
const sortKey = ref('state')
const sortDir = ref('asc')
const sortBy = (key) => {
  if (sortKey.value === key) sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  else { sortKey.value = key; sortDir.value = 'asc' }
}
const arrow = (key) => (sortKey.value !== key ? '' : sortDir.value === 'asc' ? '▲' : '▼')

const filteredAps = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return aps.value
  return aps.value.filter(a => [a.name, a.model, a.ip, a.version, a.firmware, a.state]
    .some(v => (v || '').toString().toLowerCase().includes(q)))
})

const ipKey = (ip) => String(ip || '').split('.')
  .map(p => String(Number(p) || 0).padStart(3, '0')).join('.')

const sortValue = (ap, key) => {
  switch (key) {
    case 'state': return isOnline(ap) ? 1 : 0
    case 'firmware': return upToDate(ap) ? 1 : 0
    case 'ip': return ipKey(ap.ip)
    case 'since': {
      const d = asDate(ap.since)
      return d ? d.getTime() : 0
    }
    default: return (ap[key] == null ? '' : ap[key])
  }
}

const sortedAps = computed(() => {
  const dir = sortDir.value === 'asc' ? 1 : -1
  return [...filteredAps.value].sort((a, b) => {
    const av = sortValue(a, sortKey.value)
    const bv = sortValue(b, sortKey.value)
    let cmp
    if (typeof av === 'number' && typeof bv === 'number') cmp = av - bv
    else cmp = String(av).localeCompare(String(bv), undefined, { numeric: true, sensitivity: 'base' })
    if (cmp !== 0) return cmp * dir
    // Name is the tiebreak, so equal states keep one stable, readable order.
    return String(a.name || '').localeCompare(String(b.name || ''), undefined, { numeric: true })
  })
})

// --- History charts -----------------------------------------------------
// The range lives in the store, so picking 7d here and opening the firewall
// page next keeps 7d. Writing through setHistoryRange is what persists it.
const range = computed({
  get: () => historyRange.value,
  set: (v) => setHistoryRange(v),
})

const EMPTY_SERIES = Object.freeze({ timestamps: [], values: [] })
const metricSeries = ref({})
const metricResolution = ref('')
const metricError = ref('')
const uptimeSeries = ref({ timestamps: [], values: [] })
const uptimeResolution = ref('')
const uptimeError = ref('')
const historyLoading = ref(false)

// APs online run 0 to about 23 and wifi clients run to sixty or more, so they get
// a chart each. Sharing one plot would flatten the AP line into the axis.
const seriesOf = (name) => metricSeries.value[name] || EMPTY_SERIES
const apsOnlineSeries = computed(() => seriesOf('apsOnline'))
const clientsSeries = computed(() => seriesOf('clients'))
const txRetrySeries = computed(() => seriesOf('txRetryPct'))

// txRetryPct is optional: the key can be missing from the response, or present
// with nothing but nulls. Either way there is no chart to draw.
const hasTxRetry = computed(() => {
  const vals = txRetrySeries.value.values
  return Array.isArray(vals) && vals.some(v => v !== null && v !== undefined)
})

const lastValue = (s) => {
  const vals = (s && s.values) || []
  for (let i = vals.length - 1; i >= 0; i--) {
    if (vals[i] !== null && vals[i] !== undefined) return vals[i]
  }
  return null
}
// The y-scale is whatever this site adopted, so a flat line at the top is the
// healthy normal. The subtitle names the denominator so it cannot be misread.
const apChartSub = computed(() => {
  const recorded = lastValue(seriesOf('apsTotal'))
  const total = recorded !== null ? recorded : apsTotal.value
  if (!total) return 'No access points adopted on this site.'
  return `${total} adopted ${total === 1 ? 'AP' : 'APs'} here, so a flat line at ${total} is healthy.`
})

// Sweeps land every 30 to 60 seconds, so an hour holds roughly 60 to 120 checks
// and one flaky sweep is about 1.5% of a bucket. Warning below 98% therefore
// ignores a single missed sweep and lights up from two, and 90% is reserved for
// an hour that lost six minutes or more, which is a real outage rather than noise.
// Degraded is recorded as a pass, so healthy buckets sit at 100% and a genuine
// dip stands out without either threshold firing on ordinary jitter.
const UPTIME_WARN = 0.98
const UPTIME_DOWN = 0.9

const METRIC_NOTES = {
  raw: 'One point per sweep, roughly every 30 to 60 seconds.',
  hour: "Hourly averages. The shaded band is each hour's low and high.",
}
const metricNote = computed(() => METRIC_NOTES[metricResolution.value] || '')

const UPTIME_NOTES = {
  hour: 'One column per hour.',
  day: 'One column per day. Gatus compacts uptime older than about 48 hours into daily buckets.',
  mixed: 'One column per hour for about the last 48 hours, one per day before that, because Gatus compacts older uptime into daily buckets.',
}
const uptimeNote = computed(() => UPTIME_NOTES[uptimeResolution.value] || '')

// Metric history starts at the first sweep after a deploy and cannot be filled
// in backwards, so an empty chart on a long-lived endpoint is normal.
const metricEmptyText = computed(() => metricError.value
  || 'No metric history in this range yet. Recording starts at the first sweep after a deploy and earlier periods cannot be filled in.')
const uptimeEmptyText = computed(() => uptimeError.value
  || 'No uptime buckets in this range yet. They fill in as checks are recorded.')

const fetchMetricHistory = async () => {
  try {
    const res = await fetch(
      `/api/v1/history/${encodeURIComponent(routeKey.value)}?range=${range.value}`,
      { credentials: 'include', cache: 'no-store' })
    if (!res.ok) {
      metricSeries.value = {}
      metricResolution.value = ''
      metricError.value = 'Gatus could not read the metric history for this range.'
      return
    }
    const data = await res.json()
    metricSeries.value = (data && data.series) || {}
    metricResolution.value = (data && data.resolution) || ''
    metricError.value = ''
  } catch (e) {
    metricSeries.value = {}
    metricResolution.value = ''
    metricError.value = 'Could not reach Gatus for the metric history.'
  }
}

const fetchUptimeSeries = async () => {
  try {
    const res = await fetch(
      `/api/v1/endpoints/${encodeURIComponent(routeKey.value)}/uptime-series?range=${range.value}`,
      { credentials: 'include', cache: 'no-store' })
    if (!res.ok) {
      uptimeSeries.value = { timestamps: [], values: [] }
      uptimeResolution.value = ''
      uptimeError.value = res.status === 404
        ? 'Gatus has no recorded checks under this key.'
        : 'Gatus could not read the uptime history for this range.'
      return
    }
    const data = await res.json()
    uptimeSeries.value = {
      timestamps: Array.isArray(data.timestamps) ? data.timestamps : [],
      values: Array.isArray(data.values) ? data.values : [],
    }
    uptimeResolution.value = data.resolution || ''
    uptimeError.value = ''
  } catch (e) {
    uptimeSeries.value = { timestamps: [], values: [] }
    uptimeResolution.value = ''
    uptimeError.value = 'Could not reach Gatus for the uptime history.'
  }
}

// loading is passed to every chart, so the previous render is held at reduced
// opacity while a new range arrives rather than collapsing to a skeleton.
const loadHistory = async () => {
  historyLoading.value = true
  await Promise.allSettled([fetchMetricHistory(), fetchUptimeSeries()])
  historyLoading.value = false
}

watch(historyRange, () => { loadHistory() })

// --- Fetching -----------------------------------------------------------
const fetchSnapshot = async () => {
  try {
    const res = await fetch(`/api/v1/unifi/${routeKey.value}`, { cache: 'no-store' })
    if (res.ok) {
      const data = await res.json()
      snapshot.value = data && data.kind ? data : null
    } else if (res.status === 404) {
      snapshot.value = null
    }
  } catch (e) {
    // non-fatal — keep the last good snapshot rather than blanking the page
  }
}

const fetchResults = async () => {
  try {
    const res = await fetch(
      `/api/v1/endpoints/${routeKey.value}/statuses?page=1&pageSize=${RESULT_WINDOW}`,
      { credentials: 'include', cache: 'no-store' })
    if (res.ok) {
      const data = await res.json()
      results.value = Array.isArray(data.results) ? data.results : []
    }
  } catch (e) {
    // non-fatal — the snapshot alone still answers "is the wifi up"
  }
}

const refresh = async () => {
  loading.value = true
  try { await Promise.all([fetchSnapshot(), fetchResults()]) } finally {
    loading.value = false
    loaded.value = true
  }
}

// The refresh button means "everything on this page", charts included.
const refreshAll = () => Promise.allSettled([refresh(), loadHistory()])

let poll = null
let historyPoll = null
onMounted(() => {
  refreshAll()
  poll = setInterval(refresh, POLL_MS)
  historyPoll = setInterval(loadHistory, HISTORY_POLL_MS)
})
onUnmounted(() => {
  if (poll) clearInterval(poll)
  if (historyPoll) clearInterval(historyPoll)
})
</script>

<style scoped>
.wl-page { width: 100%; }

/* Equipment-panel micro-label */
.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px; letter-spacing: 0.1em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); font-weight: 700;
}

.wl-filter {
  font-size: 0.85rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  background: hsl(var(--background)); color: hsl(var(--foreground));
  border: 1px solid hsl(var(--border)); border-radius: 8px;
  padding: 0.4rem 0.6rem; width: 12rem;
}
.wl-filter:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }

/* Live / updated indicator — same vocabulary as the Jira board */
.live-ind {
  display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.72rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: hsl(var(--muted-foreground)); text-transform: uppercase; letter-spacing: 0.08em;
}
.live-ind .ldot { width: 7px; height: 7px; border-radius: 999px; background: hsl(var(--muted-foreground) / 0.5); }
.live-ind.on { color: hsl(var(--foreground)); }
.live-ind.on .ldot { background: #5aa06b; }

/* --- States ----------------------------------------------------------- */
.notice { border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 1.5rem; }
.notice-error { border-style: solid; border-color: hsl(var(--destructive) / 0.4); background: hsl(var(--destructive) / 0.05); }
.notice-title { font-weight: 700; margin-bottom: 0.35rem; }
.notice-body { font-size: 0.875rem; color: hsl(var(--muted-foreground)); line-height: 1.5; }
.notice code, .paused code {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em;
}
.err-pre {
  font-size: 12px; white-space: pre-wrap; word-break: break-word;
  color: hsl(var(--destructive)); background: hsl(var(--destructive) / 0.08);
  border-radius: 6px; padding: 0.6rem 0.75rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

/* A pass carrying a reason is amber, the same attention colour as a pending
   firmware tile or a paused endpoint — never the destructive red. */
.notice-warn { border-style: solid; border-color: rgb(224 160 88 / 0.4); background: rgb(224 160 88 / 0.07); }
.notice-warn .notice-title { color: #e0a458; }
.warn-pre {
  font-size: 12px; white-space: pre-wrap; word-break: break-word;
  color: #e0a458; background: rgb(224 160 88 / 0.1);
  border-radius: 6px; padding: 0.6rem 0.75rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

/* Paused is a state, not an error — amber, never red. */
.paused {
  display: flex; align-items: flex-start; gap: 0.5rem;
  font-size: 0.8rem; line-height: 1.5; color: #e0a458;
  background: rgb(224 160 88 / 0.09); border: 1px solid rgb(224 160 88 / 0.28);
  border-radius: 10px; padding: 0.6rem 0.75rem;
}
.paused-title { font-weight: 700; }

/* --- Status band ------------------------------------------------------ */
.status-pill {
  font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em;
  padding: 0.2rem 0.65rem; border-radius: 6px; color: #fff;
}
.status-pill.st-up { background: var(--status-up); }
.status-pill.st-degraded { background: var(--status-degraded); }
.status-pill.st-down { background: var(--status-down); }
.status-pill.st-none { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }
.band-facts { font-size: 0.8rem; color: hsl(var(--muted-foreground)); font-variant-numeric: tabular-nums; }
.band-facts b { color: hsl(var(--foreground)); font-weight: 700; }

/* --- Counts strip ----------------------------------------------------- */
.strip {
  display: grid; gap: 1px; background: hsl(var(--border));
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  border: 1px solid hsl(var(--border)); border-radius: 12px; overflow: hidden;
}
.cell { background: hsl(var(--card)); padding: 0.6rem 0.75rem; }
.cell-label {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px; letter-spacing: 0.1em; text-transform: uppercase; font-weight: 600;
  color: hsl(var(--muted-foreground));
}
.cell-value {
  font-size: 1.15rem; font-weight: 700; line-height: 1.2; margin-top: 0.15rem;
  font-variant-numeric: tabular-nums; color: hsl(var(--foreground));
}
.cell-sub { font-size: 0.68rem; color: hsl(var(--muted-foreground)); opacity: 0.8; margin-top: 0.1rem; }
.cell.tone-warn .cell-value { color: #e0a458; }
.cell.tone-bad .cell-value { color: #ef6b53; }

/* --- Panels ----------------------------------------------------------- */
.panel { border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--card)); padding: 0.85rem 0.9rem; }
.panel-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 0.75rem; margin-bottom: 0.6rem; }
.panel-title { font-size: 0.95rem; font-weight: 700; font-variant-numeric: tabular-nums; margin-top: 0.15rem; }
.panel-note { font-size: 0.72rem; color: hsl(var(--muted-foreground)); font-variant-numeric: tabular-nums; }
.sec-head { display: flex; align-items: baseline; justify-content: space-between; gap: 0.75rem; margin-bottom: 0.4rem; }
.clear-sel {
  font-size: 0.7rem; letter-spacing: 0.06em; text-transform: uppercase; font-weight: 700;
  color: hsl(var(--muted-foreground)); border: 1px solid hsl(var(--border));
  border-radius: 8px; padding: 0.2rem 0.5rem; background: transparent;
}
.clear-sel:hover { color: hsl(var(--foreground)); border-color: hsl(var(--muted-foreground) / 0.5); }
.clear-sel:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }

/* --- Check history bars ----------------------------------------------- */
.bars { display: flex; gap: 1px; }
.bar { flex: 1 1 0; min-width: 0; height: 22px; border-radius: 1px; background: hsl(var(--muted-foreground) / 0.12); }
.bars-axis {
  display: flex; justify-content: space-between; margin-top: 0.3rem;
  font-size: 10px; letter-spacing: 0.06em; color: hsl(var(--muted-foreground));
}

/* --- History charts: one picker on top, small multiples beneath -------- */
.hist-head { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 0.6rem; margin-bottom: 0.55rem; }
.chart-grid { display: grid; gap: 0.75rem; grid-template-columns: repeat(auto-fit, minmax(min(100%, 20rem), 1fr)); }

/* --- The roster map: one tile per AP, read as a fleet ------------------ */
.roster-scroll { overflow-x: auto; }
.roster {
  display: grid; gap: 5px;
  grid-template-columns: repeat(auto-fill, minmax(118px, 1fr));
}
.tile {
  display: flex; flex-direction: column; gap: 0.1rem; text-align: left;
  min-width: 0; padding: 0.4rem 0.5rem; border-radius: 9px;
  border: 1px solid hsl(var(--border)); background: hsl(var(--muted) / 0.25);
  position: relative; overflow: hidden; cursor: pointer;
}
.tile::before { content: ''; position: absolute; inset: 0 0 auto 0; height: 2px; background: #8a8f98; }
.tile-name {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px; font-weight: 700; line-height: 1.25;
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
  color: hsl(var(--foreground));
}
.tile-sub {
  font-size: 9.5px; letter-spacing: 0.06em; text-transform: uppercase;
  color: hsl(var(--muted-foreground));
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.tile-on { background: rgb(90 160 107 / 0.1); border-color: rgb(90 160 107 / 0.32); }
.tile-on::before { background: #5aa06b; }
.tile-pend { background: rgb(224 160 88 / 0.13); border-color: rgb(224 160 88 / 0.4); }
.tile-pend::before { background: #e0a458; }
.tile-pend .tile-sub { color: #e0a458; }
/* Offline is the one thing that must jump out of the grid. */
.tile-off { background: rgb(239 107 83 / 0.17); border-color: rgb(239 107 83 / 0.55); }
.tile-off::before { background: #ef6b53; height: 3px; }
.tile-off .tile-name { color: #ef8b74; }
.tile-off .tile-sub { color: #ef6b53; }
.tile:hover { border-color: hsl(var(--muted-foreground) / 0.55); }
.tile:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }
.tile.sel { box-shadow: inset 0 0 0 1px hsl(var(--foreground) / 0.55); }

.legend { display: flex; flex-wrap: wrap; gap: 0.3rem 1rem; margin-top: 0.6rem; font-size: 0.7rem; color: hsl(var(--muted-foreground)); }
.lg { display: inline-flex; align-items: center; gap: 0.4rem; font-variant-numeric: tabular-nums; }
.lg .sw { width: 10px; height: 10px; border-radius: 3px; display: inline-block; }
.sw-on { background: #5aa06b; }
.sw-pend { background: #e0a458; }
.sw-off { background: #ef6b53; }
.lg-dim { opacity: 0.6; }

/* --- AP table (same idiom as the phone directory) --------------------- */
.roster-table thead th {
  text-align: left; padding: 0; white-space: nowrap;
  border-bottom: 1px solid hsl(var(--border)); background: hsl(var(--muted) / 0.4);
}
.roster-table .th-btn {
  display: inline-flex; align-items: center; gap: 0.3rem; width: 100%;
  padding: 0.6rem 0.75rem; background: transparent; cursor: pointer;
  font-size: 10px; letter-spacing: 0.12em; text-transform: uppercase; font-weight: 600;
  color: hsl(var(--muted-foreground));
}
.roster-table .th-btn:hover { color: hsl(var(--foreground)); background: hsl(var(--muted) / 0.7); }
.roster-table .th-btn:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: -2px; }
.roster-table thead th.active .th-btn { color: hsl(var(--foreground)); }
.roster-table .arrow { font-size: 8px; line-height: 1; }
.roster-table tbody td {
  padding: 0.5rem 0.75rem; border-bottom: 1px solid hsl(var(--border) / 0.6);
  font-variant-numeric: tabular-nums;
}
.roster-table tbody tr:last-child td { border-bottom: 0; }
.roster-table tbody tr:hover td { background: hsl(var(--accent) / 0.4); }
.roster-table .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.roster-table .strong { font-weight: 700; }
.roster-table .dim { color: hsl(var(--muted-foreground)); }
.roster-table tr.row-sel td { background: hsl(var(--accent) / 0.65); }
.roster-table tr.row-off td:first-child { box-shadow: inset 2px 0 0 #ef6b53; }

.lamp { width: 8px; height: 8px; border-radius: 999px; display: inline-block; margin-right: 0.45rem; }
.lamp.up { background: var(--status-up); box-shadow: 0 0 7px -1px var(--status-up); }
.lamp.down { background: var(--status-down); box-shadow: 0 0 7px -1px var(--status-down); }

.pill {
  font-size: 10px; letter-spacing: 0.04em; text-transform: uppercase;
  padding: 0.1rem 0.4rem; border-radius: 5px; font-weight: 700; white-space: nowrap;
}
.pill-ok { background: rgb(90 160 107 / 0.16); color: #7bbd8a; }
.pill-pend { background: rgb(224 160 88 / 0.18); color: #e0a458; }

@media (max-width: 640px) {
  .roster { grid-template-columns: repeat(auto-fill, minmax(96px, 1fr)); }
  .wl-filter { width: 9rem; }
  .bar { height: 18px; }
}

@media (prefers-reduced-motion: no-preference) {
  .tile { transition: border-color 0.14s ease, transform 0.14s ease, box-shadow 0.14s ease; }
  .tile:hover { transform: translateY(-1px); }
  .clear-sel, .wl-filter { transition: color 0.14s ease, border-color 0.14s ease; }
  .bar { transition: opacity 0.12s ease; }
  .bars:hover .bar:hover { opacity: 0.72; }
}
</style>
