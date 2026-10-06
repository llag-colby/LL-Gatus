<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 space-y-4 smb-panel">

      <!-- Toolbar -->
      <div class="flex items-end justify-between gap-4 flex-wrap">
        <div class="flex items-center gap-3">
          <router-link to="/" class="back-link text-muted-foreground hover:text-foreground transition-colors mb-1"
            data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-5 w-5" />
          </router-link>
          <div class="min-w-0">
            <div class="eyebrow">SMB share · {{ serverName }}</div>
            <h1 class="text-2xl font-bold tracking-tight leading-none mt-0.5 truncate">
              {{ drive }}
              <span class="text-muted-foreground font-normal">{{ shareName }}</span>
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

      <!-- Paused -->
      <div v-if="!monitored" class="notice notice-paused">
        <div class="notice-title flex items-center gap-2"><Pause class="h-4 w-4" /> Monitoring is paused</div>
        <p class="notice-body">
          Gatus isn't accepting collector pushes for <code>{{ routeKey }}</code>, so this share is not being
          opened. The history below is frozen at the last recorded result.
        </p>
      </div>

      <!-- Loading -->
      <div v-if="!loaded" class="notice">
        <div class="notice-title">Reading this share's checks…</div>
        <p class="notice-body">Fetching the latest mount result and the recorded history.</p>
      </div>

      <template v-else>
        <!-- NOT CONFIGURED. Distinct from an outage and has to read that way:
             the collector pushed "no smb reporting" because it has no account
             to connect with, so it never touched the share. -->
        <div v-if="notConfigured" class="notice notice-nodata">
          <div class="notice-title flex items-center gap-2">
            <KeyRound class="h-4 w-4" /> No credentials, so the share is not being checked
          </div>
          <p class="notice-body">
            Opening an SMB share needs an account, there is no anonymous read on these shares.
            <code>smb-collector</code> is running and reporting, but it has no
            <code>SMB_USER</code> / <code>SMB_PASS</code>, so it is not connecting to anything.
            This row is black rather than red on purpose: nothing is known to be broken.
          </p>
          <p class="notice-body mt-2">
            Add a <strong>read-only</strong> service account to <code>.env</code> and restart the collector:
          </p>
          <pre class="env-pre">SMB_USER=longlewis\svc-gatus
SMB_PASS=...</pre>
          <p class="notice-body mt-2">
            Then <code>docker compose up -d smb-collector</code>. It needs Read on
            <code>{{ unc || 'this share' }}</code> and nothing else, it lists the root and reads free
            space, and never writes.
          </p>
        </div>

        <!-- Nothing has ever reported -->
        <div v-else-if="notReported" class="notice">
          <div class="notice-title">No mount result for this share yet</div>
          <p class="notice-body">
            <code>smb-collector</code> pushes to <code>{{ routeKey }}</code> and nothing has arrived.
            Check that the <code>smb-collector</code> container is running
            (<code>docker logs smb-collector</code>) and that <code>PHONES_PUSH_TOKEN</code> is set ,
            a result lands within one sweep, about 45 to 90 seconds.
          </p>
        </div>

        <template v-else>
          <!-- Status band -->
          <div class="flex flex-wrap items-center gap-3">
            <span class="status-pill" :class="statusMeta.cls">{{ statusMeta.label }}</span>
            <span class="band-counts">{{ bandSummary }}</span>
            <span v-if="reason" class="band-warn">{{ reason }}</span>
          </div>

          <!-- Identity -->
          <section class="idstrip">
            <div class="idcell">
              <div class="eyebrow">Drive</div>
              <div class="idval mono">{{ drive || ',' }}</div>
            </div>
            <div class="idcell idcell-wide">
              <div class="eyebrow">Share path</div>
              <div class="idval mono" :title="unc">{{ unc || 'unknown' }}</div>
            </div>
            <div class="idcell">
              <div class="eyebrow">Mounted as</div>
              <div class="idval mono" :title="detail.account">{{ detail.account || ',' }}</div>
            </div>
            <div class="idcell">
              <div class="eyebrow">Free space</div>
              <div class="idval mono" :class="freeCls">{{ freeLabel }}</div>
            </div>
            <div class="idcell">
              <div class="eyebrow">Root entries</div>
              <div class="idval mono">{{ entriesLabel }}</div>
            </div>
          </section>

          <!-- THE STAGE LADDER. The whole reason this page is worth opening:
               a red row says WHICH part broke, and "the share is gone" goes to
               a different person than "the password expired". -->
          <section>
            <h2 class="sec-title">What the collector did, in order</h2>
            <div class="ladder">
              <div v-for="(st, i) in ladder" :key="st.id" class="rung" :class="`rung-${st.state}`">
                <div class="rung-mark">
                  <Check v-if="st.state === 'pass'" class="h-3.5 w-3.5" />
                  <X v-else-if="st.state === 'fail'" class="h-3.5 w-3.5" />
                  <Minus v-else class="h-3.5 w-3.5" />
                </div>
                <div class="rung-body">
                  <div class="rung-top">
                    <span class="rung-label">{{ st.label }}</span>
                    <span class="rung-ms mono">{{ st.ms !== null ? `${st.ms} ms` : stateWord(st.state) }}</span>
                  </div>
                  <div class="rung-blurb">{{ st.blurb }}</div>
                  <div v-if="st.error" class="rung-err">{{ st.error }}</div>
                </div>
                <div v-if="i < ladder.length - 1" class="rung-line" :class="{ dead: st.state !== 'pass' }"></div>
              </div>
            </div>
          </section>

          <!-- Free space, shown as a bar because a percentage alone doesn't
               convey how much room is actually left on a 12 TB volume. -->
          <section v-if="counts.totalGB">
            <h2 class="sec-title">Capacity</h2>
            <div class="cap">
              <div class="cap-bar">
                <div class="cap-used" :class="freeCls" :style="{ width: usedPct + '%' }"></div>
              </div>
              <div class="cap-legend">
                <span><strong :class="freeCls">{{ fmtGB(counts.freeGB) }}</strong> free</span>
                <span class="cap-dim">{{ fmtGB(counts.totalGB - counts.freeGB) }} used of {{ fmtGB(counts.totalGB) }}</span>
                <span class="cap-dim" v-if="counts.freePct !== undefined">{{ counts.freePct }}% free</span>
              </div>
              <p class="cap-note">
                Space available to the service account, so a per-user quota is reflected here.
                Amber below {{ warnPct }}% and under {{ warnGB }} GB free, both required.
              </p>
            </div>
          </section>

          <!-- Proof it read the real share. A wrong-share mix-up is otherwise
               invisible: every number looks healthy. -->
          <section v-if="detail.sample && detail.sample.length">
            <h2 class="sec-title">
              What's in the root
              <span class="sec-dim">· first {{ detail.sample.length }} of {{ entriesLabel }}</span>
            </h2>
            <div class="sample">
              <span v-for="n in detail.sample" :key="n" class="sample-item mono">{{ n }}</span>
            </div>
          </section>

          <!-- Siblings. Now that the check is per-share, these are genuinely
               independent signals, which is the thing the port check could not
               give us. -->
          <section v-if="siblings.length">
            <h2 class="sec-title">The other watched shares</h2>
            <div class="sib-list">
              <router-link v-for="s in siblings" :key="s.key" :to="`/endpoints/${s.key}`" class="sib">
                <span class="sib-drive mono">{{ s.drive }}</span>
                <span class="sib-unc mono">{{ s.unc }}</span>
                <span v-if="s.sameServer" class="sib-tag">same server</span>
                <span class="sib-state" :class="s.stateCls">{{ s.stateLabel }}</span>
                <span class="sib-ms mono">{{ s.free }}</span>
                <ChevronRight class="h-4 w-4 sib-chev" />
              </router-link>
            </div>
          </section>

          <!-- Recorded checks -->
          <section>
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

          <!-- History -->
          <section>
            <div class="hist-head">
              <h2 class="sec-title mb-0">History</h2>
              <RangeSelector v-model="range" label="History range" />
            </div>
            <div class="grid gap-3 lg:grid-cols-3">
              <HistoryChart
                title="Free space"
                subtitle="Percent of the volume still available to the service account."
                :series="freeSeries" kind="area" unit="%"
                :loading="historyLoading" :empty-text="metricEmptyText" :note="metricNote"
              />
              <HistoryChart
                title="Root listing time"
                subtitle="How long it took to read the top of the share."
                :series="listSeries" kind="area" unit="ms"
                :loading="historyLoading" :empty-text="metricEmptyText" :note="metricNote"
              />
              <HistoryChart
                title="Uptime"
                subtitle="Share of sweeps that mounted. A degraded sweep counts as a pass."
                :series="uptimeSeries" kind="column" ratio
                :warn-below="UPTIME_WARN" :down-below="UPTIME_DOWN"
                :loading="historyLoading" :empty-text="uptimeEmptyText" :note="uptimeNote"
              />
            </div>
          </section>
        </template>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft, RefreshCw, Pause, Check, X, Minus, ChevronRight, KeyRound } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import MonitorToggle from '@/components/MonitorToggle.vue'
import RangeSelector from '@/components/RangeSelector.vue'
import HistoryChart from '@/components/HistoryChart.vue'
import { generatePrettyTimeAgo, prettifyTimestamp } from '@/utils/time'
import { isMonitored, now, historyRange, setHistoryRange } from '@/store'
import {
  SMB_SHARES, shareFor, uncPath, driveFromKey, stageLadder, isNotReporting, fmtGB, keyPath,
} from '@/utils/smbShares'

const route = useRoute()
const routeKey = computed(() => route.params.key || '')

const MAX_BARS = 40
const POLL_MS = 20000
const HISTORY_POLL_MS = 300000

const loading = ref(false)
const loaded = ref(false)
const notReported = ref(false)
const snapshot = ref({})
const snapshots = ref({})
const results = ref([])

const monitored = computed(() => isMonitored(routeKey.value))

// --- Identity: the snapshot is authoritative, the catalog is the cold-start
// fallback. See utils/smbShares.js.
const catalog = computed(() => shareFor(routeKey.value))
const detail = computed(() => snapshot.value.detail || {})
const counts = computed(() => snapshot.value.counts || {})
const status = computed(() => snapshot.value.status || '')

const drive = computed(() =>
  snapshot.value.drive || (catalog.value ? catalog.value.drive : driveFromKey(routeKey.value)))
const unc = computed(() =>
  detail.value.unc || snapshot.value.path || uncPath(catalog.value))
const serverName = computed(() =>
  detail.value.server || (catalog.value ? catalog.value.server : ','))
const shareName = computed(() =>
  detail.value.share || (catalog.value ? catalog.value.share : ''))

// --- Latest result -------------------------------------------------------
const latest = computed(() => (results.value.length ? results.value[results.value.length - 1] : null))
const latestErrors = computed(() => {
  const r = latest.value
  return r && Array.isArray(r.errors) ? r.errors.filter(Boolean) : []
})
// The collector sends the reason as the result's error, for a degraded pass as
// well as a failure, so this one field covers both.
const reason = computed(() => latestErrors.value.join(' · '))

const notConfigured = computed(() => isNotReporting(latest.value))

const STATUS_META = {
  healthy: { label: 'Share is usable', cls: 'st-up' },
  degraded: { label: 'Usable, needs attention', cls: 'st-degraded' },
  down: { label: 'Share is not usable', cls: 'st-down' },
}
const statusMeta = computed(() => STATUS_META[status.value] || { label: 'Unknown', cls: 'st-none' })

const ladder = computed(() => stageLadder(detail.value.steps))
const stateWord = (state) => (state === 'skipped' ? 'not reached' : state === 'unknown' ? ',' : '')

const totalMs = computed(() =>
  ladder.value.reduce((sum, s) => sum + (s.ms || 0), 0))
const bandSummary = computed(() => {
  if (!snapshot.value.status) return 'nothing reported yet'
  return `mounted in ${Math.round(totalMs.value)} ms · ${entriesLabel.value} in the root`
})

const warnPct = computed(() => 10)
const warnGB = computed(() => 20)
const freeCls = computed(() => {
  const c = counts.value
  if (c.freePct === undefined) return ''
  if (c.freePct < warnPct.value && c.freeGB < warnGB.value) return 'bad'
  if (c.freePct < 20) return 'warn'
  return 'ok'
})
const freeLabel = computed(() => {
  const c = counts.value
  if (c.freeGB === undefined) return ','
  return c.freePct !== undefined ? `${fmtGB(c.freeGB)} · ${c.freePct}%` : fmtGB(c.freeGB)
})
const usedPct = computed(() => {
  const c = counts.value
  if (!c.totalGB) return 0
  return Math.min(100, Math.max(0, ((c.totalGB - (c.freeGB || 0)) / c.totalGB) * 100))
})
const entriesLabel = computed(() => {
  const c = counts.value
  if (c.entries === undefined) return ','
  return detail.value.entriesTruncated ? `${c.entries}+ entries` : `${c.entries} entries`
})

const reportingFresh = computed(() => {
  if (!snapshot.value.updatedAt) return false
  const t = Date.parse(snapshot.value.updatedAt)
  // Sweeps are 45-90s jittered, so allow a little over two of them.
  return !Number.isNaN(t) && now.value - t < 200000
})
const updatedLabel = computed(() => {
  if (!snapshot.value.updatedAt) return 'never reported'
  try { return 'checked ' + generatePrettyTimeAgo(snapshot.value.updatedAt, now.value) } catch (e) { return ',' }
})

const paddedResults = computed(() => {
  const list = [...results.value]
  while (list.length < MAX_BARS) list.unshift(null)
  return list.slice(-MAX_BARS)
})
const oldestLabel = computed(() => {
  const first = paddedResults.value.find(Boolean)
  if (!first) return 'no history yet'
  try { return generatePrettyTimeAgo(first.timestamp, now.value) } catch (e) { return ',' }
})

// Same bar vocabulary as the dashboard card: grey where nothing was recorded,
// black where the collector could not even try, red for a real failure, amber
// for a pass carrying a warning, sage for a clean pass.
const barClass = (r) => {
  if (!r) return 'empty'
  if (!r.success) return isNotReporting(r) ? 'stbar-nodata' : 'stbar-down'
  if (Array.isArray(r.errors) && r.errors.length) return 'stbar-degraded'
  return 'stbar-up'
}
const barTip = (r) => {
  if (!r) return 'No sweep recorded in this slot'
  const parts = []
  try { parts.push(prettifyTimestamp(r.timestamp)) } catch (e) { /* leave the timestamp out */ }
  if (!r.success) parts.push(isNotReporting(r) ? 'not checked' : 'share not usable')
  else parts.push((r.errors || []).length ? 'usable, with a warning' : 'share usable')
  if (r.duration) parts.push(`${Math.round(r.duration / 1000000)}ms`)
  const errs = (Array.isArray(r.errors) ? r.errors : []).filter(Boolean)
  if (errs.length) parts.push(errs.join(' · '))
  return parts.join(' · ')
}

// --- Siblings ------------------------------------------------------------
const SIB_STATE = {
  healthy: { cls: 'sib-up', label: 'usable' },
  degraded: { cls: 'sib-warn', label: 'warning' },
  down: { cls: 'sib-down', label: 'not usable' },
}
const siblings = computed(() =>
  Object.keys(SMB_SHARES)
    .filter((k) => k !== routeKey.value)
    .map((k) => {
      const snap = snapshots.value[k] || {}
      const d = snap.detail || {}
      const c = snap.counts || {}
      const meta = SMB_SHARES[k]
      const st = SIB_STATE[snap.status] || { cls: 'sib-none', label: 'no data' }
      return {
        key: k,
        drive: snap.drive || meta.drive,
        unc: d.unc || snap.path || uncPath(meta),
        sameServer: (d.server || meta.server) === serverName.value,
        stateCls: st.cls,
        stateLabel: st.label,
        free: c.freeGB !== undefined ? fmtGB(c.freeGB) + ' free' : ',',
      }
    })
    .sort((a, b) => a.drive.localeCompare(b.drive)))

// --- History -------------------------------------------------------------
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
const freeSeries = computed(() => seriesOf('freePct'))
const listSeries = computed(() => seriesOf('listMs'))

// Sweeps land every 45-90s, so an hour holds roughly 40 to 80 of them and one
// bad sweep is a little over 1% of a bucket. Warn from two, and reserve 90% for
// an hour that lost real minutes. Same reasoning as the firewall panel.
const UPTIME_WARN = 0.98
const UPTIME_DOWN = 0.9

const METRIC_NOTES = {
  raw: 'One point per sweep, roughly every 45 to 90 seconds.',
  hour: "Hourly averages. The shaded band is each hour's low and high.",
}
const metricNote = computed(() => METRIC_NOTES[metricResolution.value] || '')
const metricEmptyText = computed(() => metricError.value
  || 'No history in this range yet. Recording starts at the first sweep after a deploy and earlier periods cannot be filled in.')

const UPTIME_NOTES = {
  hour: 'One column per hour.',
  day: 'One column per day. Gatus compacts uptime older than about 48 hours into daily buckets.',
  mixed: 'One column per hour for about the last 48 hours, one per day before that, because Gatus compacts older uptime into daily buckets.',
}
const uptimeNote = computed(() => UPTIME_NOTES[uptimeResolution.value] || '')
const uptimeEmptyText = computed(() => uptimeError.value
  || 'No uptime buckets in this range yet. They fill in as sweeps are recorded.')

const fetchMetricHistory = async () => {
  try {
    const res = await fetch(`/api/v1/history/${keyPath(routeKey.value)}?range=${range.value}`,
      { cache: 'no-store' })
    if (!res.ok) {
      metricSeries.value = {}
      metricResolution.value = ''
      metricError.value = 'Gatus could not read the recorded history for this range.'
      return
    }
    const data = await res.json()
    metricSeries.value = (data && data.series) || {}
    metricResolution.value = (data && data.resolution) || ''
    metricError.value = ''
  } catch (e) {
    metricSeries.value = {}
    metricResolution.value = ''
    metricError.value = 'Could not reach Gatus for the recorded history.'
  }
}

const fetchUptimeSeries = async () => {
  try {
    const res = await fetch(
      `/api/v1/endpoints/${keyPath(routeKey.value)}/uptime-series?range=${range.value}`,
      { cache: 'no-store' })
    if (!res.ok) {
      uptimeSeries.value = { timestamps: [], values: [] }
      uptimeResolution.value = ''
      uptimeError.value = res.status === 404
        ? 'Gatus has no recorded sweeps under this key.'
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

const loadHistory = async () => {
  historyLoading.value = true
  await Promise.allSettled([fetchMetricHistory(), fetchUptimeSeries()])
  historyLoading.value = false
}

watch(historyRange, () => { loadHistory() })

// --- Fetching ------------------------------------------------------------
const fetchSnapshot = async () => {
  try {
    const res = await fetch(`/api/v1/smb/${keyPath(routeKey.value)}`, { cache: 'no-store' })
    if (res.status === 404) {
      notReported.value = true
      snapshot.value = {}
      return
    }
    if (!res.ok) return
    snapshot.value = await res.json()
    notReported.value = false
  } catch (e) { /* leave the previous snapshot in place */ }
}

// One call covers every share, which is what the sibling list needs.
const fetchSnapshots = async () => {
  try {
    const res = await fetch('/api/v1/smb', { cache: 'no-store' })
    if (!res.ok) return
    snapshots.value = await res.json()
  } catch (e) { /* leave the previous snapshots in place */ }
}

const fetchResults = async () => {
  try {
    const res = await fetch(
      `/api/v1/endpoints/${keyPath(routeKey.value)}/statuses?page=1&pageSize=${MAX_BARS}`,
      { cache: 'no-store' })
    if (!res.ok) return
    const data = await res.json()
    results.value = Array.isArray(data.results) ? data.results : []
  } catch (e) { /* leave the previous results in place */ }
}

const tick = async () => {
  await Promise.allSettled([fetchSnapshot(), fetchSnapshots(), fetchResults()])
  loaded.value = true
}

const refreshAll = async () => {
  loading.value = true
  await Promise.allSettled([tick(), loadHistory()])
  loading.value = false
}

let poll = null
let historyPoll = null

onMounted(async () => {
  await tick()
  await loadHistory()
  poll = setInterval(tick, POLL_MS)
  historyPoll = setInterval(loadHistory, HISTORY_POLL_MS)
})

onUnmounted(() => {
  if (poll) clearInterval(poll)
  if (historyPoll) clearInterval(historyPoll)
})

// Clicking a sibling keeps this component mounted and only swaps the param.
watch(routeKey, async () => {
  loaded.value = false
  notReported.value = false
  snapshot.value = {}
  results.value = []
  await tick()
  await loadHistory()
})
</script>

<style scoped>
.smb-panel { max-width: 1400px; margin: 0 auto; }

.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px; letter-spacing: 0.1em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); font-weight: 700;
}
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; }
.back-link:focus-visible, .refresh:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; border-radius: 8px; }

.sec-title { font-size: 0.8rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em; color: hsl(var(--muted-foreground)); margin-bottom: 0.55rem; }
.sec-dim { font-weight: 500; opacity: 0.6; letter-spacing: 0.02em; }

.live-ind { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.72rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: hsl(var(--muted-foreground)); text-transform: uppercase; letter-spacing: 0.08em; }
.live-ind .ldot { width: 7px; height: 7px; border-radius: 999px; background: hsl(var(--muted-foreground) / 0.5); }
.live-ind.on { color: hsl(var(--foreground)); }
.live-ind.on .ldot { background: #5aa06b; }

.notice { border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 1.1rem 1.25rem; }
.notice-paused { border-style: solid; border-color: rgb(138 143 152 / 0.45); background: hsl(var(--muted) / 0.35); }
/* "Not configured" is its own state, and wears the same black the bars use for
   it rather than borrowing the red of a real failure. */
.notice-nodata { border-style: solid; border-color: hsl(var(--foreground) / 0.35); background: hsl(var(--foreground) / 0.04); }
.notice-title { font-weight: 700; margin-bottom: 0.35rem; }
.notice-body { font-size: 0.875rem; color: hsl(var(--muted-foreground)); }
.notice code, .cap-note code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em; }
.env-pre { margin-top: 0.6rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.78rem; background: hsl(var(--muted) / 0.7); border: 1px solid hsl(var(--border)); border-radius: 6px; padding: 0.55rem 0.7rem; overflow-x: auto; user-select: all; }

.status-pill { font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em; padding: 0.2rem 0.65rem; border-radius: 6px; color: #fff; }
.status-pill.st-up { background: var(--status-up); }
.status-pill.st-degraded { background: var(--status-degraded); }
.status-pill.st-down { background: var(--status-down); }
.status-pill.st-none { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }
.band-counts { font-size: 0.8rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); }
.band-warn { font-size: 0.75rem; color: #e0a458; }

.idstrip { display: flex; flex-wrap: wrap; border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--card)); padding: 0.7rem 0.35rem; }
.idcell { flex: 1 1 9rem; min-width: 0; padding: 0.1rem 0.85rem; border-left: 1px solid hsl(var(--border)); }
.idcell-wide { flex: 2 1 16rem; }
.idcell:first-child { border-left: 0; }
.idval { font-size: 0.86rem; font-weight: 600; margin-top: 0.15rem; font-variant-numeric: tabular-nums; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.idval.ok { color: #7bbd8a; }
.idval.warn { color: #e0a458; }
.idval.bad { color: #ef6b53; }

/* --- the stage ladder --- */
.ladder { border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--card)); padding: 0.4rem 0.9rem; }
.rung { position: relative; display: flex; gap: 0.75rem; padding: 0.7rem 0; }
.rung-mark { flex-shrink: 0; width: 1.5rem; height: 1.5rem; border-radius: 999px; display: flex; align-items: center; justify-content: center; z-index: 1; }
.rung-pass .rung-mark { background: rgb(123 189 138 / 0.18); color: #7bbd8a; }
.rung-fail .rung-mark { background: rgb(239 107 83 / 0.18); color: #ef6b53; }
.rung-skipped .rung-mark, .rung-unknown .rung-mark { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }
/* The connector is drawn from each rung down to the next, dead-grey once a
   stage has failed, because nothing after it actually ran. */
.rung-line { position: absolute; left: 0.75rem; top: 2.2rem; bottom: -0.7rem; width: 1px; background: rgb(123 189 138 / 0.35); }
.rung-line.dead { background: hsl(var(--border)); }
.rung-body { min-width: 0; flex: 1; }
.rung-top { display: flex; align-items: baseline; justify-content: space-between; gap: 0.75rem; }
.rung-label { font-size: 0.86rem; font-weight: 600; }
.rung-ms { font-size: 0.75rem; color: hsl(var(--muted-foreground)); flex-shrink: 0; }
.rung-blurb { font-size: 0.78rem; color: hsl(var(--muted-foreground)); line-height: 1.45; margin-top: 0.1rem; }
.rung-err { margin-top: 0.35rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.75rem; color: #ef6b53; background: rgb(239 107 83 / 0.09); border-radius: 6px; padding: 0.35rem 0.55rem; }
.rung-skipped .rung-label, .rung-unknown .rung-label { color: hsl(var(--muted-foreground)); }
.ladder-foot { font-size: 0.78rem; color: hsl(var(--muted-foreground)); margin-top: 0.6rem; line-height: 1.5; }

/* --- capacity --- */
.cap { border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--card)); padding: 0.9rem 1.1rem; }
.cap-bar { height: 10px; border-radius: 999px; background: hsl(var(--muted) / 0.8); overflow: hidden; }
.cap-used { height: 100%; border-radius: 999px; background: hsl(var(--muted-foreground) / 0.5); transition: width 0.3s; }
.cap-used.ok { background: #7bbd8a; }
.cap-used.warn { background: #e0a458; }
.cap-used.bad { background: #ef6b53; }
.cap-legend { display: flex; flex-wrap: wrap; gap: 0.25rem 1.1rem; margin-top: 0.5rem; font-size: 0.82rem; font-variant-numeric: tabular-nums; }
.cap-legend strong.ok { color: #7bbd8a; }
.cap-legend strong.warn { color: #e0a458; }
.cap-legend strong.bad { color: #ef6b53; }
.cap-dim { color: hsl(var(--muted-foreground)); }
.cap-note { font-size: 0.75rem; color: hsl(var(--muted-foreground)); margin-top: 0.6rem; line-height: 1.45; }

/* --- root sample --- */
.sample { display: flex; flex-wrap: wrap; gap: 0.3rem; }
.sample-item { font-size: 0.75rem; padding: 0.2rem 0.5rem; border: 1px solid hsl(var(--border)); border-radius: 6px; background: hsl(var(--card)); color: hsl(var(--muted-foreground)); }

/* --- siblings --- */
.sib-list { display: flex; flex-direction: column; gap: 2px; }
.sib { display: flex; align-items: center; gap: 0.75rem; padding: 0.55rem 0.8rem; border: 1px solid hsl(var(--border)); border-radius: 8px; background: hsl(var(--card)); transition: background 0.12s, border-color 0.12s; }
.sib:hover { background: hsl(var(--muted) / 0.45); border-color: hsl(var(--muted-foreground) / 0.3); }
.sib-drive { font-weight: 700; font-size: 0.9rem; width: 2.1rem; flex-shrink: 0; }
.sib-unc { flex: 1 1 auto; min-width: 0; font-size: 0.8rem; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sib-tag { font-size: 10px; text-transform: uppercase; letter-spacing: 0.06em; color: hsl(var(--muted-foreground)); background: hsl(var(--muted) / 0.7); padding: 0.1rem 0.35rem; border-radius: 4px; flex-shrink: 0; }
.sib-state { font-size: 10px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.07em; padding: 0.1rem 0.4rem; border-radius: 4px; flex-shrink: 0; }
.sib-state.sib-up { color: #7bbd8a; background: rgb(123 189 138 / 0.14); }
.sib-state.sib-warn { color: #e0a458; background: rgb(224 160 88 / 0.14); }
.sib-state.sib-down { color: #ef6b53; background: rgb(239 107 83 / 0.14); }
.sib-state.sib-none { color: hsl(var(--muted-foreground)); background: hsl(var(--muted) / 0.6); }
.sib-ms { font-size: 0.75rem; color: hsl(var(--muted-foreground)); width: 5.5rem; text-align: right; flex-shrink: 0; }
.sib-chev { color: hsl(var(--muted-foreground) / 0.5); flex-shrink: 0; }
.sib-foot { font-size: 0.78rem; color: hsl(var(--muted-foreground)); margin-top: 0.6rem; line-height: 1.5; }

/* --- bars --- */
.bars-wrap { overflow-x: auto; }
.bars { display: flex; gap: 2px; min-width: 15rem; }
.stcell { flex: 1 1 0; min-width: 3px; height: 28px; border-radius: 3px; }
.stcell.empty { background: hsl(var(--muted) / 0.7); }
.axis { display: flex; justify-content: space-between; margin-top: 0.3rem; font-size: 0.68rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); }

.hist-head { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 0.6rem; margin-bottom: 0.55rem; }
</style>
