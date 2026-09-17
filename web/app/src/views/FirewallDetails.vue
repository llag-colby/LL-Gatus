<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 space-y-4 firewall-panel">

      <!-- Toolbar -->
      <div class="flex items-end justify-between gap-4 flex-wrap">
        <div class="flex items-center gap-3">
          <router-link to="/" class="back-link text-muted-foreground hover:text-foreground transition-colors mb-1"
            data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-5 w-5" />
          </router-link>
          <div>
            <div class="eyebrow">Edge · {{ uplinkSummary }}</div>
            <h1 class="text-2xl font-bold tracking-tight leading-none mt-0.5">
              {{ siteName }} <span class="text-muted-foreground font-normal">firewall</span>
            </h1>
          </div>
        </div>
        <div class="flex items-center gap-3 mb-0.5">
          <span class="live-ind" :class="{ on: reportingFresh }"><span class="ldot"></span>{{ updatedLabel }}</span>
          <Button variant="ghost" size="icon" class="refresh h-9 w-9" @click="refreshAll"
            data-tooltip="Refresh" data-tip-pos="bottom">
            <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
          </Button>
          <MonitorToggle :endpoint-key="routeKey" />
        </div>
      </div>

      <!-- Paused: not an error, so it never wears the error styling -->
      <div v-if="!monitored" class="notice notice-paused">
        <div class="notice-title flex items-center gap-2"><Pause class="h-4 w-4" /> Monitoring is paused</div>
        <p class="notice-body">
          Gatus isn't running this check and isn't accepting collector pushes for
          <code>{{ routeKey }}</code>. The history below is frozen at the last recorded result.
          Resume with the switch above and the same timeline picks back up.
        </p>
      </div>

      <!-- Loading -->
      <div v-if="!loaded" class="notice">
        <div class="notice-title">Reading the gateway…</div>
        <p class="notice-body">Fetching the latest uplink snapshot and the recorded result history.</p>
      </div>

      <!-- Never reported -->
      <div v-else-if="notReported" class="notice">
        <div class="notice-title">No gateway snapshot for this key yet</div>
        <p class="notice-body">
          <code>unifi-collector</code> pushes the gateway and its uplinks to <code>{{ routeKey }}</code>,
          and nothing has arrived. Check that the <code>unifi-collector</code> container is running and that
          <code>UNIFI_API_KEY</code> and <code>UNIFI_PUSH_TOKEN</code> are set — a snapshot lands within one sweep.
        </p>
      </div>

      <template v-else>
        <!-- Status band -->
        <div class="flex flex-wrap items-center gap-3">
          <span class="status-pill" :class="statusMeta.cls">{{ statusMeta.label }}</span>
          <span class="band-counts">{{ uplinkSummary }}</span>
          <span v-if="gateway.state && gateway.state !== 'ONLINE'" class="band-warn">
            gateway {{ gateway.state.toLowerCase() }} in UniFi
          </span>
        </div>

        <!-- Gateway identity strip -->
        <section class="idstrip">
          <div class="idcell">
            <div class="eyebrow">Model</div>
            <div class="idval">{{ gateway.model || '—' }}</div>
          </div>
          <div class="idcell">
            <div class="eyebrow">Hardware</div>
            <div class="idval mono">{{ gateway.shortname || '—' }}</div>
          </div>
          <div class="idcell">
            <div class="eyebrow">Firmware</div>
            <div class="idval mono">{{ gateway.version || '—' }}</div>
          </div>
          <div class="idcell">
            <div class="eyebrow">Public IP</div>
            <div class="idval mono">{{ gateway.ip || '—' }}</div>
          </div>
          <div class="idcell">
            <div class="eyebrow">UniFi cloud</div>
            <div class="idval mono" :class="gateway.state === 'ONLINE' ? 'ok' : 'bad'">
              {{ (gateway.state || 'unknown').toLowerCase() }}
            </div>
          </div>
        </section>

        <!-- THE UPLINK LADDER — each WAN is a path from the gateway to the internet -->
        <section class="ladder-sec">
          <h2 class="sec-title">Uplinks</h2>

          <!-- Nothing to draw, for two unrelated reasons. Leading with the wrong one
               sends someone into UniFi to fix a WAN that was never broken. -->
          <div v-if="!wans.length && unreadable" class="notice notice-error">
            <div class="notice-title flex items-center gap-2">
              <AlertTriangle class="h-4 w-4" /> Couldn't read UniFi for this gateway
            </div>
            <p class="notice-body">
              The last snapshot arrived empty, so <code>unifi-collector</code> reached Gatus but could not
              read the UniFi cloud API — the gateway itself may be fine. Check the
              <code>unifi-collector</code> container log for <code>{{ routeKey }}</code>; the reason it
              recorded is shown below. The uplinks come back on the first sweep that reads.
            </p>
          </div>

          <div v-else-if="!wans.length" class="notice notice-error">
            <div class="notice-title flex items-center gap-2"><AlertTriangle class="h-4 w-4" /> No enabled uplinks</div>
            <p class="notice-body">
              The gateway reports no enabled WAN ports, so there is no path to the internet to draw.
              Enable a WAN in the UniFi network settings.
            </p>
          </div>

          <div v-else class="ladder-wrap">
            <div class="ladder">
              <div class="ladder-head">
                <span class="eyebrow">Uplink</span>
                <span class="head-ends"><span class="eyebrow">Gateway</span><span class="eyebrow">Internet</span></span>
                <span class="eyebrow head-state">State</span>
              </div>

              <ul class="rungs" role="list">
                <li v-for="(w, i) in wans" :key="w.id + ':' + w.interface" class="rung" :class="'s-' + wanState(w)">
                  <div class="rung-id">
                    <span class="wan-id">{{ w.id }}</span>
                    <span class="wan-rank">{{ i === 0 ? 'primary' : 'backup' }}</span>
                  </div>

                  <div class="wire">
                    <span class="node node-gw" aria-hidden="true"></span>
                    <span class="span">
                      <span class="chip chip-if">
                        {{ w.interface || 'unnamed port' }}
                        <span v-if="w.port !== null && w.port !== undefined" class="chip-dim">port {{ w.port }}</span>
                      </span>
                      <span v-if="w.speedType" class="chip chip-speed">{{ w.speedType }}</span>
                      <i v-if="wanState(w) === 'down'" class="break" aria-hidden="true"></i>
                      <span class="chip chip-ip">{{ w.ip || 'no address' }}</span>
                    </span>
                    <span class="node node-net" aria-hidden="true"></span>
                  </div>

                  <div class="rung-state">{{ stateLabel(w) }}</div>
                </li>
              </ul>
            </div>
          </div>

          <div class="legend">
            <span class="lg"><i class="key k-up"></i>up</span>
            <span class="lg"><i class="key k-linked"></i>link up, no address</span>
            <span class="lg"><i class="key k-down"></i>no link</span>
          </div>
        </section>

        <!-- What the last check reported. A degraded firewall passes WITH its reason
             in errors[], so this only wears the error styling when the check failed. -->
        <div v-if="latestErrors.length" class="notice" :class="latestFailed ? 'notice-error' : 'notice-warn'">
          <div class="notice-title flex items-center gap-2">
            <AlertTriangle class="h-4 w-4" /> {{ reportTitle }}
          </div>
          <pre class="err-pre" :class="{ warn: !latestFailed }">{{ latestErrors.join('\n') }}</pre>
          <p v-if="!latestFailed" class="notice-body mt-2">
            A working path remains, so Gatus records the check as a pass and the bar stays amber
            rather than red. Chase the uplink marked above.
          </p>
        </div>
        <div v-else-if="status === 'degraded' && notCarrying.length" class="partial">
          <AlertTriangle class="h-3.5 w-3.5" />
          {{ notCarrying.join(', ') }} {{ notCarrying.length === 1 ? 'is' : 'are' }} down. A working
          path remains, so Gatus records the check as a pass. Chase the uplink marked above.
        </div>

        <!-- Result history -->
        <section class="hist">
          <h2 class="sec-title">Recorded checks <span class="sec-dim">· last {{ paddedResults.length }}</span></h2>
          <div class="bars-wrap">
            <div class="bars">
              <span v-for="(r, i) in paddedResults" :key="i" class="stcell" :class="barClass(r)"
                :data-tooltip="barTip(r)" data-tip-pos="top"></span>
            </div>
          </div>
          <div class="axis">
            <span>{{ oldestLabel }}</span>
            <span>{{ monitored ? 'now' : 'paused' }}</span>
          </div>
        </section>

        <!-- History over time. One picker scopes every chart under it, and the
             choice is shared with the other drill-ins so the window follows you. -->
        <section class="hist-sec">
          <div class="hist-head">
            <h2 class="sec-title">History</h2>
            <RangeSelector v-model="range" label="History range" />
          </div>
          <div class="chart-grid">
            <HistoryChart
              title="WAN uplinks up"
              :subtitle="uplinkChartSub"
              :series="wansUpSeries"
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
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft, RefreshCw, AlertTriangle, Pause } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import MonitorToggle from '@/components/MonitorToggle.vue'
import RangeSelector from '@/components/RangeSelector.vue'
import HistoryChart from '@/components/HistoryChart.vue'
import { generatePrettyTimeAgo, prettifyTimestamp } from '@/utils/time'
import { isMonitored, now, historyRange, setHistoryRange } from '@/store'

const route = useRoute()
const routeKey = computed(() => route.params.key || '')

const MAX_BARS = 40
const POLL_MS = 20000
// The snapshot and the bars are live, so they poll hard. A range of history is a
// far bigger read and barely moves inside twenty seconds, so it refreshes on its
// own slower clock, on a range change and on the refresh button.
const HISTORY_POLL_MS = 300000

// Both collectors flag "we could not read the source at all" with a fixed error
// prefix. That is an ABSENCE of a health signal, not a reported failure, so it
// paints grey here exactly as it does on LocationCard and the wireless drill-in.
// Keep in step with collector/unifi_collector.py and collector/phone_collector.py.
const NOT_REPORTING = /^no (phones|unifi) reporting\b/i
const isNotReporting = (r) =>
  !!r && !r.success && (Array.isArray(r.errors) ? r.errors : []).some(e => NOT_REPORTING.test(e))

const loading = ref(false)
const loaded = ref(false)
const notReported = ref(false)
const updatedAt = ref(null)
const status = ref('')
const counts = ref({})
const detail = ref({})
const site = ref('')
const results = ref([])

const monitored = computed(() => isMonitored(routeKey.value))

// "firewall_decatur-gmc" -> "Decatur Gmc", used until the collector names the site.
const siteName = computed(() => {
  if (site.value) return site.value
  const slug = routeKey.value.replace(/^firewall_/, '')
  return slug.split(/[-_]/).filter(Boolean).map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ') || 'Site'
})

const gateway = computed(() => detail.value.gateway || {})
// The collector already sorts WAN before WAN2 before WAN3, so the primary reads first.
const wans = computed(() => (Array.isArray(detail.value.wans) ? detail.value.wans : []))

// A WAN is only up when the port has link AND an address on it.
const wanState = (w) => (w.up ? 'up' : w.plugged ? 'linked' : 'down')
const STATE_LABELS = { up: 'up', linked: 'linked, no address', down: 'no link' }
const stateLabel = (w) => STATE_LABELS[wanState(w)]
const notCarrying = computed(() => wans.value.filter(w => !w.up).map(w => w.id))

const uplinkSummary = computed(() => {
  const total = counts.value.wansTotal != null ? counts.value.wansTotal : wans.value.length
  const up = counts.value.wansUp != null ? counts.value.wansUp : wans.value.filter(w => w.up).length
  if (!total) return 'no uplinks'
  return `${up} of ${total} ${total === 1 ? 'uplink' : 'uplinks'} up`
})

const STATUS_META = {
  healthy: { label: 'Healthy', cls: 'st-up' },
  degraded: { label: 'Degraded', cls: 'st-degraded' },
  down: { label: 'Down', cls: 'st-down' },
}
const statusMeta = computed(() => STATUS_META[status.value] || { label: 'Unknown', cls: 'st-none' })

// The collector re-reports every sweep, so a snapshot older than ~90s is stale.
const reportingFresh = computed(() => {
  if (!updatedAt.value) return false
  const t = Date.parse(updatedAt.value)
  return !Number.isNaN(t) && now.value - t < 90000
})
const updatedLabel = computed(() => {
  if (!updatedAt.value) return 'never reported'
  try { return 'reported ' + generatePrettyTimeAgo(updatedAt.value, now.value) } catch (e) { return '—' }
})

// --- Result history ------------------------------------------------------
const paddedResults = computed(() => {
  const list = [...results.value]
  while (list.length < MAX_BARS) list.unshift(null)
  return list.slice(-MAX_BARS)
})
const latest = computed(() => (results.value.length ? results.value[results.value.length - 1] : null))
const latestFailed = computed(() => !!latest.value && !latest.value.success)
// A degraded firewall passes and carries its reason in errors[], so read errors
// off every result — the panel decides how to dress them, not whether to show them.
const latestErrors = computed(() => {
  const r = latest.value
  return r && Array.isArray(r.errors) ? r.errors.filter(Boolean) : []
})
const reportTitle = computed(() => {
  if (!latestFailed.value) return 'The last check passed with a warning'
  return isNotReporting(latest.value) ? 'The last check read nothing' : 'The last check failed'
})

// The collector's fatal path pushes {status: "down", counts: {}, detail: {}} — a
// snapshot DID arrive, it just carries nothing. A gateway that genuinely has no
// enabled WAN still ships wansUp/wansTotal and a gateway block, so an empty pair
// on a down row means "we could not read UniFi". The check's own not-reporting
// error says the same thing; either signal is enough.
const emptySnapshot = computed(() => (
  status.value === 'down'
  && !Object.keys(counts.value).length
  && !Object.keys(detail.value).length
))
const unreadable = computed(() => emptySnapshot.value || isNotReporting(latest.value))
const oldestLabel = computed(() => {
  const first = paddedResults.value.find(Boolean)
  if (!first) return 'no history yet'
  try { return generatePrettyTimeAgo(first.timestamp, now.value) } catch (e) { return '—' }
})

// Same bar vocabulary as the dashboard card, so the time axis reads the same:
// grey where nothing was read, red where the gateway reported a failure, amber
// where it passed carrying a warning, sage where it passed clean.
const barClass = (r) => {
  if (!r) return 'empty'
  if (!r.success) return isNotReporting(r) ? 'stbar-nodata' : 'stbar-down'
  if (Array.isArray(r.errors) && r.errors.length) return 'stbar-degraded'
  return 'stbar-up'
}
const barVerdict = (r) => {
  if (!r.success) return isNotReporting(r) ? 'nothing read' : 'fail'
  return Array.isArray(r.errors) && r.errors.length ? 'pass, with a warning' : 'pass'
}
const barTip = (r) => {
  if (!r) return 'No check recorded in this slot'
  const parts = []
  try { parts.push(prettifyTimestamp(r.timestamp)) } catch (e) { /* leave the timestamp out */ }
  parts.push(barVerdict(r))
  if (r.duration) parts.push(`${Math.round(r.duration / 1000000)}ms`)
  const errs = (Array.isArray(r.errors) ? r.errors : []).filter(Boolean)
  if (errs.length) parts.push(errs.join(' · '))
  return parts.join(' · ')
}

// --- History charts ------------------------------------------------------
// The range lives in the store, so picking 7d here and opening the wireless
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

const seriesOf = (name) => metricSeries.value[name] || EMPTY_SERIES
const wansUpSeries = computed(() => seriesOf('wansUp'))

// The y-scale is whatever this gateway actually has, so Alabaster tops out at 2
// and Decatur at 1. A flat line at the top is the healthy normal in both cases,
// and the subtitle names the denominator so a single-WAN site can't read as a
// site that lost an uplink.
const lastValue = (s) => {
  const vals = (s && s.values) || []
  for (let i = vals.length - 1; i >= 0; i--) {
    if (vals[i] !== null && vals[i] !== undefined) return vals[i]
  }
  return null
}
const uplinkChartSub = computed(() => {
  const recorded = lastValue(seriesOf('wansTotal'))
  const total = recorded !== null
    ? recorded
    : (counts.value.wansTotal != null ? counts.value.wansTotal : wans.value.length)
  if (!total) return 'This gateway reports no enabled uplinks.'
  return `${total} enabled ${total === 1 ? 'uplink' : 'uplinks'} here, so a flat line at ${total} is healthy.`
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
      { cache: 'no-store' }
    )
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
      { cache: 'no-store' }
    )
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

// loading is passed to both charts, so the previous render is held at reduced
// opacity while a new range arrives rather than collapsing to a skeleton.
const loadHistory = async () => {
  historyLoading.value = true
  await Promise.allSettled([fetchMetricHistory(), fetchUptimeSeries()])
  historyLoading.value = false
}

watch(historyRange, () => { loadHistory() })

// --- Fetching ------------------------------------------------------------
const fetchSnapshot = async () => {
  const res = await fetch(`/api/v1/unifi/${encodeURIComponent(routeKey.value)}`, { cache: 'no-store' })
  if (res.status === 404) {
    notReported.value = true
    return
  }
  if (!res.ok) return
  const data = await res.json()
  notReported.value = false
  updatedAt.value = data.updatedAt || null
  status.value = data.status || ''
  site.value = data.site || ''
  counts.value = data.counts || {}
  detail.value = data.detail || {}
}

const fetchResults = async () => {
  const res = await fetch(
    `/api/v1/endpoints/${encodeURIComponent(routeKey.value)}/statuses?page=1&pageSize=${MAX_BARS}`,
    { cache: 'no-store' }
  )
  if (!res.ok) return
  const data = await res.json()
  results.value = Array.isArray(data.results) ? data.results : []
}

const load = async () => {
  loading.value = true
  // Keep the last good view if one call fails — a stale panel beats a blank one.
  await Promise.allSettled([fetchSnapshot(), fetchResults()])
  loading.value = false
  loaded.value = true
}

// The refresh button means "everything on this page", charts included.
const refreshAll = () => Promise.allSettled([load(), loadHistory()])

let poll = null
let historyPoll = null
onMounted(() => {
  refreshAll()
  poll = setInterval(load, POLL_MS)
  historyPoll = setInterval(loadHistory, HISTORY_POLL_MS)
})
onUnmounted(() => {
  if (poll) clearInterval(poll)
  if (historyPoll) clearInterval(historyPoll)
})
</script>

<style scoped>
.firewall-panel { width: 100%; }

/* Equipment-panel micro-label */
.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: hsl(var(--muted-foreground));
  font-weight: 700;
}
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; }
.back-link:focus-visible, .refresh:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; border-radius: 8px; }

.sec-title { font-size: 0.8rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em; color: hsl(var(--muted-foreground)); margin-bottom: 0.55rem; }
.sec-dim { font-weight: 500; opacity: 0.6; letter-spacing: 0.02em; }

/* --- live indicator (same idiom as the Jira board) --- */
.live-ind { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.72rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: hsl(var(--muted-foreground)); text-transform: uppercase; letter-spacing: 0.08em; }
.live-ind .ldot { width: 7px; height: 7px; border-radius: 999px; background: hsl(var(--muted-foreground) / 0.5); }
.live-ind.on { color: hsl(var(--foreground)); }
.live-ind.on .ldot { background: #5aa06b; }

/* --- states --- */
.notice { border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 1.25rem 1.35rem; }
.notice-error { border-style: solid; border-color: hsl(var(--destructive) / 0.4); background: hsl(var(--destructive) / 0.05); }
/* A pass carrying a warning is amber, never red — same rule as the paused notice. */
.notice-warn { border-style: solid; border-color: rgb(224 160 88 / 0.4); background: rgb(224 160 88 / 0.07); }
.notice-paused { border-style: solid; border-color: rgb(138 143 152 / 0.45); background: hsl(var(--muted) / 0.35); }
.notice-title { font-weight: 700; margin-bottom: 0.35rem; }
.notice-body { font-size: 0.875rem; color: hsl(var(--muted-foreground)); }
.notice code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em; }
.err-pre { font-size: 12px; white-space: pre-wrap; word-break: break-word; color: hsl(var(--destructive)); background: hsl(var(--destructive) / 0.08); border-radius: 6px; padding: 0.6rem 0.75rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; margin: 0; }
.err-pre.warn { color: #e0a458; background: rgb(224 160 88 / 0.1); }
.partial { display: flex; align-items: flex-start; gap: 0.45rem; font-size: 0.78rem; line-height: 1.45; color: #e0a458; background: rgb(224 160 88 / 0.09); border: 1px solid rgb(224 160 88 / 0.28); border-radius: 8px; padding: 0.5rem 0.7rem; }
.partial svg { flex-shrink: 0; margin-top: 0.15rem; }

/* --- status band --- */
.status-pill { font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em; padding: 0.2rem 0.65rem; border-radius: 6px; color: #fff; }
.status-pill.st-up { background: var(--status-up); }
.status-pill.st-degraded { background: var(--status-degraded); }
.status-pill.st-down { background: var(--status-down); }
.status-pill.st-none { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }
.band-counts { font-size: 0.8rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); }
.band-warn { font-size: 0.75rem; color: #e0a458; }

/* --- gateway identity strip: quiet, dense, one row on desktop --- */
.idstrip { display: flex; flex-wrap: wrap; border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--card)); padding: 0.7rem 0.35rem; }
.idcell { flex: 1 1 9rem; min-width: 0; padding: 0.1rem 0.85rem; border-left: 1px solid hsl(var(--border)); }
.idcell:first-child { border-left: 0; }
.idval { font-size: 0.86rem; font-weight: 600; margin-top: 0.15rem; font-variant-numeric: tabular-nums; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.idval.ok { color: #7bbd8a; }
.idval.bad { color: #ef6b53; }

/* ================================================================= */
/*  THE UPLINK LADDER                                                 */
/*  Every WAN is drawn as a path from the gateway to the internet.     */
/*  Solid sage = up      · dashed amber = linked but no address ·     */
/*  cut terracotta = no link. Facts ride on the wire itself.           */
/* ================================================================= */
.ladder-wrap { overflow-x: auto; }
.ladder { min-width: 20rem; border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--card)); padding: 0.5rem 0.9rem 0.75rem; }

.ladder-head, .rung { display: grid; grid-template-columns: 5.5rem minmax(0, 1fr) 8.5rem; align-items: center; gap: 0.75rem; }
.ladder-head { padding: 0.35rem 0 0.2rem; }
.head-ends { display: flex; justify-content: space-between; }
.head-state { text-align: right; }

.rungs { list-style: none; margin: 0; padding: 0; }
.rung { padding: 0.3rem 0; border-top: 1px solid hsl(var(--border) / 0.7); }

.rung-id { display: flex; flex-direction: column; gap: 0.05rem; min-width: 0; }
.wan-id { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.92rem; font-weight: 700; letter-spacing: 0.02em; }
.wan-rank { font-size: 9px; letter-spacing: 0.1em; text-transform: uppercase; font-weight: 700; color: hsl(var(--muted-foreground)); }

/* the path itself */
.wire { display: flex; align-items: center; min-width: 0; }
.node { flex: 0 0 auto; width: 9px; height: 9px; border-radius: 999px; background: hsl(var(--muted-foreground) / 0.75); }
.node-net { box-shadow: none; background: transparent; box-sizing: border-box; border: 2px solid hsl(var(--muted-foreground) / 0.6); }

.span { position: relative; flex: 1 1 auto; min-width: 0; display: flex; align-items: center; gap: 0.4rem; padding: 1.35rem 0.5rem; }
.span::before {
  content: ''; position: absolute; left: 0; right: 0; top: 50%; height: 2px;
  transform: translateY(-50%); border-radius: 2px;
  background: hsl(var(--muted-foreground) / 0.4);
}

/* facts laid along the wire — card background so each one cuts the line */
.chip { position: relative; z-index: 1; background: hsl(var(--card)); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.72rem; font-variant-numeric: tabular-nums; color: hsl(var(--foreground)); padding: 0 0.4rem; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.chip-dim { color: hsl(var(--muted-foreground)); margin-left: 0.4rem; }
.chip-speed { flex: 0 0 auto; border: 1px solid hsl(var(--border)); border-radius: 999px; padding: 0.05rem 0.5rem; font-size: 0.66rem; letter-spacing: 0.03em; color: hsl(var(--muted-foreground)); }
.chip-ip { flex: 0 0 auto; margin-left: auto; font-weight: 600; }

/* the cut, for a wire with no link */
.break { position: relative; z-index: 1; flex: 0 0 auto; margin-left: auto; width: 18px; height: 14px; background: hsl(var(--card)); }
.break::before, .break::after { content: ''; position: absolute; top: 0; width: 2px; height: 14px; border-radius: 2px; background: #ef6b53; transform: rotate(24deg); }
.break::before { left: 4px; }
.break::after { left: 11px; }
.break + .chip-ip { margin-left: 0.4rem; }

.rung-state { text-align: right; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.68rem; letter-spacing: 0.06em; text-transform: uppercase; font-weight: 700; color: hsl(var(--muted-foreground)); }

/* up: link and an address */
.rung.s-up .span::before { background: #5aa06b; }
.rung.s-up .node-gw { background: #5aa06b; }
.rung.s-up .node-net { background: #5aa06b; border-color: #5aa06b; box-shadow: 0 0 8px -1px #5aa06b; }
.rung.s-up .rung-state { color: #7bbd8a; }
.rung.s-up .chip-ip { color: #7bbd8a; }

/* linked, but not carrying an address */
.rung.s-linked .span::before { background: repeating-linear-gradient(90deg, #e0a458 0 8px, transparent 8px 15px); }
.rung.s-linked .node-gw { background: #e0a458; }
.rung.s-linked .node-net { border-color: #e0a458; }
.rung.s-linked .rung-state, .rung.s-linked .chip-ip { color: #e0a458; }

/* no link at all */
.rung.s-down .span::before { background: #ef6b53; opacity: 0.85; }
.rung.s-down .node-gw { background: #ef6b53; }
.rung.s-down .node-net { border-color: hsl(var(--muted-foreground) / 0.45); }
.rung.s-down .rung-state, .rung.s-down .chip-ip { color: #ef6b53; }
.rung.s-down .chip, .rung.s-down .chip-speed { opacity: 0.85; }

.legend { display: flex; flex-wrap: wrap; gap: 0.3rem 1.1rem; font-size: 0.7rem; color: hsl(var(--muted-foreground)); margin-top: 0.55rem; }
.lg { display: inline-flex; align-items: center; gap: 0.4rem; }
.key { width: 18px; height: 2px; border-radius: 2px; display: inline-block; }
.key.k-up { background: #5aa06b; }
.key.k-linked { background: repeating-linear-gradient(90deg, #e0a458 0 5px, transparent 5px 9px); }
.key.k-down { background: #ef6b53; }

/* --- result history bars --- */
.bars-wrap { overflow-x: auto; }
.bars { display: flex; gap: 2px; min-width: 15rem; }
.stcell { flex: 1 1 0; min-width: 3px; height: 28px; border-radius: 3px; }
.stcell.empty { background: hsl(var(--muted) / 0.7); }
.axis { display: flex; justify-content: space-between; margin-top: 0.3rem; font-size: 0.68rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); }

/* --- history charts: one picker on top, small multiples beneath --- */
.hist-head { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 0.6rem; margin-bottom: 0.55rem; }
.hist-head .sec-title { margin-bottom: 0; }
.chart-grid { display: grid; gap: 0.75rem; grid-template-columns: repeat(auto-fit, minmax(min(100%, 20rem), 1fr)); }


/* --- mobile: the wire gets its own full-width row --- */
@media (max-width: 719px) {
  .ladder-head { display: none; }
  .rung { grid-template-columns: minmax(0, 1fr) auto; gap: 0.15rem 0.6rem; padding: 0.55rem 0; }
  .rung-id { flex-direction: row; align-items: baseline; gap: 0.5rem; }
  .wire { grid-column: 1 / -1; }
  .span { padding: 1rem 0.4rem; }
  .chip-speed { display: none; }
  .idcell { flex: 1 1 7rem; border-left: 0; padding: 0.25rem 0.85rem; }
}
</style>
