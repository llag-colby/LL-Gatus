<template>
  <div class="dashboard-container detail-page s1-view bg-background" :class="{ wall: isFullscreen }">
    <div class="s1-shell">

      <!-- RAIL -->
      <header class="rail">
        <div class="rail-id">
          <router-link v-if="!isFullscreen" to="/" class="rail-back"
            data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-5 w-5" />
          </router-link>
          <span class="rail-mark" :style="{ backgroundImage: `url(${s1Icon})` }"></span>
          <div class="rail-name">
            <span class="rail-title">SentinelOne</span>
            <span class="rail-sub">{{ railSub }}</span>
          </div>
        </div>

        <nav v-if="!isFullscreen" class="tabbar" role="tablist" aria-label="View">
          <button v-for="tb in TABS" :key="tb.id" role="tab" :aria-selected="shown === tb.id"
            class="tab" :class="{ on: shown === tb.id }" @click="tab = tb.id">
            <component :is="tb.icon" class="h-4 w-4" />{{ tb.label }}
          </button>
        </nav>

        <div class="rail-state">
          <span class="livedot" :class="{ on: live }"
            :data-tooltip="live ? 'Streaming live' : 'Polling every 30s'" data-tip-pos="bottom"></span>
          <span class="feed">{{ feedLabel }}</span>
          <span class="clock">{{ clockLabel }}</span>
          <Button v-if="!isFullscreen" variant="ghost" size="icon" class="h-9 w-9 shrink-0"
            @click="fetchMetrics" data-tooltip="Refresh" data-tip-pos="bottom">
            <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
          </Button>
        </div>
      </header>

      <div v-if="loaded && !snap.configured" class="notice">
        <div class="notice-title">SentinelOne not connected</div>
        <p class="notice-body">
          Set <code>S1_BASE_URL</code> and <code>S1_API_TOKEN</code> in <code>.env</code>.
        </p>
      </div>
      <div v-else-if="loaded && !snap.ok" class="notice notice-error">
        <div class="notice-title"><AlertTriangle class="h-4 w-4" /> Cannot reach SentinelOne</div>
        <pre class="err-pre">{{ snap.error }}</pre>
      </div>
      <div v-else-if="!loaded" class="notice"><div class="notice-title">Reading estate…</div></div>

      <!-- ================================================================
           THE WALL.
           Alert list dominant on the left, charts banked down the right, one
           thin KPI strip across the top. The list is the focal point because
           the threats are the work; the charts are context around it.
           ================================================================ -->
      <template v-else-if="shown === 'wall'">
        <!-- Thin strip. Compact on purpose - these are reference figures, not
             the thing the eye should land on. -->
        <div class="strip">
          <div v-for="k in kpis" :key="k.l" class="kv" :class="k.tone" :style="{ '--kc': k.c }">
            <span class="kv-n">{{ k.v }}<i v-if="k.unit">{{ k.unit }}</i></span>
            <span class="kv-l">{{ k.l }}</span>
          </div>
        </div>

        <div class="canvas">
          <!-- ALERTS. The focal point: biggest area, severity down the left
               edge, oldest first because age is the pressure on this queue. -->
          <section class="panel-x alerts">
            <header class="ph">
              <h3 class="pt"><Siren class="h-3.5 w-3.5" /> Active threats</h3>
              <div class="sevkey">
                <span v-for="sv in SEVS" :key="sv.k" class="sk" :class="'sev-' + sv.k">
                  <i></i>{{ sv.l }} {{ sevCounts[sv.k] }}
                </span>
              </div>
            </header>
            <div class="alist">
              <table class="atab">
                <thead>
                  <tr>
                    <th class="c-sev">Sev</th>
                    <th class="c-age">Age</th>
                    <th class="c-site">Site</th>
                    <th class="c-host">Endpoint</th>
                    <th>Threat</th>
                    <th class="c-vd">Verdict</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="th in alerts" :key="th.id" @click="openThreat = th">
                    <td class="c-sev"><span class="sevbar" :class="'sev-' + sevOf(th).k">{{ sevOf(th).l }}</span></td>
                    <td class="c-age" :class="ageTone(th)">{{ ageShort(th.createdAt) }}</td>
                    <td class="c-site"><span class="sitetag">{{ siteOf(th.endpoint) }}</span></td>
                    <td class="c-host">{{ th.endpoint || '—' }}</td>
                    <td class="c-file"><span class="fname">{{ th.name }}</span></td>
                    <td class="c-vd">
                      <span class="pill" :class="'vd-' + slug(th.verdict)">{{ th.verdictLabel || 'Undefined' }}</span>
                    </td>
                  </tr>
                  <tr v-if="!alerts.length"><td colspan="6" class="empty">No active threats.</td></tr>
                </tbody>
              </table>
            </div>
          </section>

          <!-- CHART BANK -->
          <section class="panel-x">
            <header class="ph">
              <h3 class="pt">Detections per day</h3>
              <span class="ps">{{ trend.length }}d · bars = that day, line = 7-day average</span>
            </header>
            <div class="chart"><Bar :data="trendData" :options="trendOpts" /></div>
          </section>

          <section class="panel-x">
            <header class="ph">
              <h3 class="pt">Open by site</h3>
              <span class="ps">from endpoint naming</span>
            </header>
            <div class="chart"><Bar :data="siteData" :options="hBarOpts" /></div>
          </section>

          <section class="panel-x">
            <header class="ph">
              <h3 class="pt">Verdict mix</h3>
              <span class="ps">{{ fmt(t.notResolved) }} open</span>
            </header>
            <div class="chart"><Doughnut :data="verdictData" :options="donutOpts" /></div>
          </section>
        </div>

        <section v-if="(snap.byAction || []).length" class="failstrip">
          <AlertTriangle class="h-4 w-4" />
          <span class="fs-l">Mitigation did not succeed</span>
          <ul class="chips">
            <li v-for="r in snap.byAction" :key="r.name" class="chip">{{ r.name }} <b>{{ fmt(r.count) }}</b></li>
          </ul>
        </section>
      </template>

      <!-- ======================= FULL QUEUE ======================= -->
      <section v-else-if="shown === 'queue'" class="queue">
        <div class="q-tools">
          <input v-model="search" class="q-search" type="search"
            :placeholder="`Filter ${fmt(open.length)} open — file, endpoint, site, hash…`" />
          <div class="q-filters">
            <button v-for="f in QUEUE_FILTERS" :key="f.id" class="qf"
              :class="{ on: queueFilter === f.id }" @click="queueFilter = f.id">
              {{ f.label }}<span class="qf-n">{{ fmt(queueCount(f.id)) }}</span>
            </button>
          </div>
          <a v-if="snap.baseUrl" class="q-open" :href="`${snap.baseUrl}/incidents/threats`"
            target="_blank" rel="noopener">SentinelOne<ExternalLink class="h-3.5 w-3.5" /></a>
        </div>
        <div v-if="sortedQueue.length" class="table">
          <table class="atab full">
            <thead>
              <tr>
                <th v-for="col in QUEUE_COLS" :key="col.id">
                  <button class="sortable" :class="{ on: sortBy === col.id }" @click="sortQueue(col.id)">
                    {{ col.label }}
                  </button>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="th in sortedQueue" :key="th.id" @click="openThreat = th">
                <td class="c-sev"><span class="sevbar" :class="'sev-' + sevOf(th).k">{{ sevOf(th).l }}</span></td>
                <td class="c-age" :class="ageTone(th)">{{ ageShort(th.createdAt) }}</td>
                <td class="c-site"><span class="sitetag">{{ siteOf(th.endpoint) }}</span></td>
                <td class="c-host">{{ th.endpoint || '—' }}</td>
                <td class="c-file">
                  <span class="fname" :title="th.name">{{ th.name }}</span>
                  <span class="fpath" :title="th.path">{{ shortPath(th.path) }}</span>
                </td>
                <td><span class="pill" :class="'cls-' + slug(th.classification)">{{ th.classification || '—' }}</span></td>
                <td class="c-vd"><span class="pill" :class="'vd-' + slug(th.verdict)">{{ th.verdictLabel || 'Undefined' }}</span></td>
                <td><span class="pill" :class="th.needsAction ? 'mt-bad' : 'mt-ok'">{{ th.mitigationLabel || '—' }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="notice"><div class="notice-title">Nothing matches</div></div>
      </section>

      <!-- ======================= ANALYSIS ======================= -->
      <section v-else class="analysis">
        <div class="an-grid">
          <S1Donut title="Open by verdict" :sub="`${fmt(t.notResolved)} open`" centre-label="open"
            :rows="snap.openVerdict || []" :palette="VERDICT_P" />
          <S1Donut title="Estate mitigation" sub="all time" centre-label="threats"
            :rows="snap.allMitigation || []" :palette="MITIGATION_P" />
          <S1Donut title="Classification" sub="all time" centre-label="threats"
            :rows="snap.allClassification || []" :palette="CLASS_P" />
          <S1Donut title="Confidence" sub="all time" centre-label="threats"
            :rows="snap.allConfidence || []" :palette="CONF_P" />
          <S1Donut title="Analyst verdict" sub="all time" centre-label="threats"
            :rows="snap.allVerdict || []" :palette="VERDICT_P" />
          <S1Donut title="Fleet check-in" :sub="`${fmt(a.total)} agents`" centre-label="agents"
            :rows="fleetRows" :palette="FLEET_P" />
          <section class="panel-x an-wide">
            <header class="ph"><h3 class="pt">Open by site</h3></header>
            <div class="chart h-fix"><Bar :data="siteData" :options="hBarOpts" /></div>
          </section>
          <section class="panel-x an-wide">
            <header class="ph"><h3 class="pt">Open by OS</h3></header>
            <div class="chart h-fix"><Bar :data="osData" :options="hBarOpts" /></div>
          </section>
        </div>
      </section>
    </div>

    <!-- DRILL-IN -->
    <aside v-if="openThreat" class="panel" @click.self="closeThreat">
      <div class="panel-card">
        <header class="p-head">
          <div class="p-id">
            <span class="sevbar" :class="'sev-' + sevOf(openThreat).k">{{ sevOf(openThreat).l }}</span>
            <h2 class="p-title">{{ openThreat.name }}</h2>
          </div>
          <Button variant="ghost" size="icon" class="h-9 w-9" @click="closeThreat">
            <X class="h-5 w-5" />
          </Button>
        </header>
        <dl class="p-facts">
          <div><dt>Site</dt><dd>{{ siteOf(openThreat.endpoint) }}</dd></div>
          <div><dt>Endpoint</dt><dd>{{ openThreat.endpoint || '—' }}</dd></div>
          <div><dt>OS</dt><dd>{{ openThreat.os || '—' }}</dd></div>
          <div><dt>Open for</dt><dd>{{ ageLong(openThreat.createdAt) }}</dd></div>
          <div><dt>Status</dt><dd>{{ openThreat.statusLabel || '—' }}</dd></div>
          <div><dt>Verdict</dt><dd>{{ openThreat.verdictLabel || '—' }}</dd></div>
          <div><dt>Confidence</dt><dd>{{ openThreat.confidence || '—' }}</dd></div>
          <div><dt>Mitigation</dt><dd>{{ openThreat.mitigationLabel || '—' }}</dd></div>
          <div><dt>Classification</dt><dd>{{ openThreat.classification || '—' }}</dd></div>
          <div><dt>Detection</dt><dd>{{ openThreat.detectionType || '—' }}{{ enginesText }}</dd></div>
          <div><dt>Publisher</dt><dd>{{ openThreat.publisher || '—' }}</dd></div>
          <div><dt>First seen</dt><dd>{{ stamp(openThreat.createdAt) }}</dd></div>
        </dl>
        <div v-if="(openThreat.failedMitigations || []).length" class="p-alarm">
          <AlertTriangle class="h-4 w-4" />
          <span>Action failed: <b>{{ openThreat.failedMitigations.join(', ') }}</b></span>
        </div>
        <div class="p-block" v-if="openThreat.path">
          <h4 class="p-h4">Path</h4><code class="p-code">{{ openThreat.path }}</code>
        </div>
        <div class="p-block" v-if="openThreat.sha1">
          <h4 class="p-h4">SHA1</h4><code class="p-code">{{ openThreat.sha1 }}</code>
        </div>
        <div class="p-block">
          <h4 class="p-h4">Activity</h4>
          <p v-if="detailLoading" class="gnote">Loading…</p>
          <pre v-else-if="detailError" class="err-pre">{{ detailError }}</pre>
          <ol v-else-if="timeline.length" class="tl">
            <li v-for="(e, i) in timeline" :key="i">
              <span class="tl-at">{{ stamp(e.at) }}</span>
              <span class="tl-p">{{ e.primary }}</span>
              <span v-if="e.secondary" class="tl-s">{{ e.secondary }}</span>
            </li>
          </ol>
          <p v-else class="gnote">No activity recorded.</p>
        </div>
        <a v-if="snap.baseUrl" class="p-link"
          :href="`${snap.baseUrl}/incidents/threats/${openThreat.id}/overview`"
          target="_blank" rel="noopener">Open in SentinelOne<ExternalLink class="h-3.5 w-3.5" /></a>
      </div>
    </aside>

    <Settings v-if="!isFullscreen" @refreshData="fetchMetrics" />
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import {
  ArrowLeft, RefreshCw, AlertTriangle, Siren,
  LayoutGrid, Rows3, ChartPie, ExternalLink, X,
} from 'lucide-vue-next'
import { Bar, Doughnut } from 'vue-chartjs'
import {
  Chart as ChartJS, BarElement, BarController, LineElement, PointElement, LineController,
  ArcElement, DoughnutController, CategoryScale, LinearScale, Tooltip, Legend,
} from 'chart.js'
import { Button } from '@/components/ui/button'
import Settings from '@/components/Settings.vue'
import S1Donut from '@/components/S1Donut.vue'
import { isFullscreen, now as serverNow } from '@/store'
import s1Icon from '@/assets/sentinelone.png'

// Prints each bar's value on the bar, but only where the label fits the bar
// it belongs to - drawing unconditionally is what made numbers overlap.
const valueLabels = {
  id: 'valueLabels',
  afterDatasetsDraw(chart, _args, opts) {
    const { ctx } = chart
    const size = opts.size || 10
    const horizontal = chart.options.indexAxis === 'y'
    chart.data.datasets.forEach((ds, di) => {
      if (ds.type === 'line') return
      const meta = chart.getDatasetMeta(di)
      if (meta.hidden) return
      meta.data.forEach((el, i) => {
        const v = ds.data[i]
        if (!v) return
        const text = v.toLocaleString()
        ctx.save()
        ctx.font = `800 ${size}px ui-monospace, SFMono-Regular, Menlo, monospace`
        const fits = horizontal
          ? (el.height || 0) >= size + 3
          : (el.width || 0) >= ctx.measureText(text).width + 3
        if (!fits) { ctx.restore(); return }
        ctx.fillStyle = opts.color || '#888'
        if (horizontal) {
          ctx.textAlign = 'left'; ctx.textBaseline = 'middle'
          ctx.fillText(text, el.x + 6, el.y)
        } else {
          ctx.textAlign = 'center'; ctx.textBaseline = 'bottom'
          ctx.fillText(text, el.x, el.y - 4)
        }
        ctx.restore()
      })
    })
  },
}
ChartJS.register(
  BarElement, BarController, LineElement, PointElement, LineController,
  ArcElement, DoughnutController, CategoryScale, LinearScale, Tooltip, Legend, valueLabels,
)

const LIGHT = {
  crit: '#dc2626', rose: '#e11d48', warn: '#d97706', ok: '#16a34a',
  teal: '#0d9488', info: '#2563eb', cyan: '#0891b2', violet: '#7c3aed',
  fuchsia: '#a21caf', idle: '#64748b', slate: '#94a3b8',
}
const DARK = {
  crit: '#f87171', rose: '#fb7185', warn: '#fbbf24', ok: '#4ade80',
  teal: '#2dd4bf', info: '#60a5fa', cyan: '#22d3ee', violet: '#a78bfa',
  fuchsia: '#e879f9', idle: '#94a3b8', slate: '#cbd5e1',
}
const dark = ref(document.documentElement.classList.contains('dark'))
let themeObserver = null
const C = computed(() => (dark.value ? DARK : LIGHT))
const VERDICT_P = computed(() => [C.value.warn, C.value.slate, C.value.crit, C.value.info])
const MITIGATION_P = computed(() => [C.value.ok, C.value.idle, C.value.crit])
const CLASS_P = computed(() => [C.value.info, C.value.fuchsia, C.value.crit])
const CONF_P = computed(() => [C.value.rose, C.value.warn])
const FLEET_P = computed(() => [C.value.teal, C.value.idle])

const snap = ref({ configured: false, ok: false, status: 'unknown', threats: {}, agents: {} })
const loaded = ref(false)
const loading = ref(false)
const live = ref(false)
const search = ref('')
const openThreat = ref(null)
const timeline = ref([])
const detailLoading = ref(false)
const detailError = ref('')

const t = computed(() => snap.value.threats || {})
const a = computed(() => snap.value.agents || {})
const open = computed(() => snap.value.open || [])
const trend = computed(() => snap.value.trend || [])

const TABS = [
  { id: 'wall', label: 'Wall', icon: LayoutGrid },
  { id: 'queue', label: 'Queue', icon: Rows3 },
  { id: 'analysis', label: 'Analysis', icon: ChartPie },
]
const savedTab = localStorage.getItem('gatus.s1.tab')
const tab = ref(['wall', 'queue', 'analysis'].includes(savedTab) ? savedTab : 'wall')
watch(tab, (v) => localStorage.setItem('gatus.s1.tab', v))
const shown = computed(() => (isFullscreen.value ? 'wall' : tab.value))

// --- severity ------------------------------------------------------------
// SentinelOne has no severity field, so it is derived from the two things
// that actually decide urgency: whether anything is still sitting on the
// machine, and how confident the engine was.
const SEVS = [
  { k: 'crit', l: 'CRIT' },
  { k: 'high', l: 'HIGH' },
  { k: 'med', l: 'MED' },
  { k: 'low', l: 'LOW' },
]
const sevOf = (th) => {
  if (!th) return SEVS[3]
  if (th.needsAction) return SEVS[0]
  if (th.confidence === 'malicious') return SEVS[1]
  if (th.confidence === 'suspicious') return SEVS[2]
  return SEVS[3]
}
const SEV_ORDER = { crit: 0, high: 1, med: 2, low: 3 }
const sevCounts = computed(() => {
  const out = { crit: 0, high: 0, med: 0, low: 0 }
  for (const th of open.value) out[sevOf(th).k]++
  return out
})

// --- site, derived from the endpoint name -------------------------------
// This tenant has ONE SentinelOne site holding all 956 agents, so the
// console's own site field cannot group anything. The hostnames do: the
// segment before the first dash is the location (LLTU, LLRR, PARTS, IT…).
// Derived rather than configured, so a new site appears on its own.
const siteOf = (host) => {
  const h = String(host || '').trim()
  if (!h) return 'UNKNOWN'
  return (h.split(/[-_]/)[0] || 'UNKNOWN').toUpperCase()
}

// --- alerts, the focal point ---------------------------------------------
// Severity first, then oldest: the top of this list is what someone should
// pick up next.
const alerts = computed(() => {
  const rows = [...open.value].sort((x, y) => {
    const s = SEV_ORDER[sevOf(x).k] - SEV_ORDER[sevOf(y).k]
    if (s !== 0) return s
    return String(x.createdAt).localeCompare(String(y.createdAt))
  })
  return rows.slice(0, isFullscreen.value ? 18 : 12)
})

// --- age ------------------------------------------------------------------
const DAY = 86400000
const ageDays = (iso) => {
  if (!iso) return 0
  const ms = serverNow.value - new Date(iso).getTime()
  return isFinite(ms) && ms > 0 ? ms / DAY : 0
}
const ageTone = (th) => {
  const d = ageDays(th.createdAt)
  return d >= 30 ? 'a3' : d >= 7 ? 'a2' : d >= 2 ? 'a1' : 'a0'
}
const ageShort = (iso) => {
  const d = ageDays(iso)
  return d < 1 ? `${Math.max(1, Math.round(d * 24))}h` : `${Math.round(d)}d`
}
const ageLong = (iso) => {
  const d = ageDays(iso)
  return d < 1 ? `${Math.round(d * 24)} hours` : `${Math.round(d)} days`
}
const oldestDays = computed(() =>
  open.value.reduce((m, th) => Math.max(m, Math.round(ageDays(th.createdAt))), 0))

// --- the thin strip ------------------------------------------------------
// Reference figures only. Deliberately small: the alert list is the focal
// point, and licence counts are not here because they are not operational.
const kpis = computed(() => {
  const th = t.value
  return [
    { l: 'Open', v: fmt(th.notResolved), c: C.value.violet },
    { l: 'Critical', v: fmt(sevCounts.value.crit), c: C.value.crit,
      tone: sevCounts.value.crit ? '' : 'calm' },
    { l: 'Malicious', v: fmt(th.maliciousNotResolved), c: C.value.rose,
      tone: th.maliciousNotResolved ? '' : 'calm' },
    { l: 'Oldest', v: oldestDays.value, unit: 'd', c: oldestDays.value >= 14 ? C.value.crit : C.value.warn },
    { l: 'Infected', v: fmt(a.value.infected), c: C.value.crit,
      tone: a.value.infected ? '' : 'calm' },
    { l: 'Agents', v: fmt(a.value.online), c: C.value.teal,
      tone: 'calm' },
    { l: 'Silent', v: fmt(a.value.offline), c: C.value.warn,
      tone: a.value.offline ? '' : 'calm' },
    { l: 'New today', v: fmt(th.newToday), c: C.value.info, tone: 'calm' },
  ]
})

// --- charts --------------------------------------------------------------
const grid = () => (dark.value ? 'rgba(148,163,184,0.16)' : 'rgba(100,116,139,0.16)')
const ink = () => (dark.value ? 'rgba(226,232,240,0.88)' : 'rgba(51,65,85,0.80)')
const fs = (small, big) => (isFullscreen.value ? big : small)

const rollingMean = (series, w = 7) => series.map((_, i) => {
  const slice = series.slice(Math.max(0, i - w + 1), i + 1)
  return slice.reduce((s, n) => s + n, 0) / slice.length
})

const trendData = computed(() => {
  const counts = trend.value.map(p => p.created || 0)
  const peak = Math.max(...counts, 1)
  return {
    labels: trend.value.map(p => p.date.slice(5)),
    datasets: [
      {
        type: 'line', label: '7-day average', data: rollingMean(counts),
        borderColor: C.value.cyan, backgroundColor: C.value.cyan, borderWidth: 3,
        pointRadius: 2.5, pointBackgroundColor: C.value.cyan,
        pointBorderColor: dark.value ? '#1b1f27' : '#ffffff', pointBorderWidth: 1,
        tension: 0.35, order: 0,
      },
      {
        type: 'bar', label: 'Detections that day', data: counts,
        backgroundColor: counts.map(n => (n >= peak ? C.value.warn : C.value.violet)),
        borderRadius: 2, order: 1,
      },
    ],
  }
})
const trendOpts = computed(() => ({
  responsive: true, maintainAspectRatio: false, animation: { duration: 0 },
  layout: { padding: { top: fs(12, 18) } },
  plugins: {
    legend: {
      display: true, position: 'top', align: 'end',
      labels: {
        color: ink(), boxWidth: 10, boxHeight: 10, padding: 8,
        usePointStyle: true, pointStyle: 'rectRounded',
        font: { size: fs(9, 12), weight: '700' },
      },
    },
    tooltip: { callbacks: { label: (c) => ` ${c.dataset.label}: ${Math.round(c.parsed.y).toLocaleString()}` } },
    valueLabels: { color: ink(), size: fs(9, 12) },
  },
  scales: {
    // Linear, zero-based: on a log axis a bar's length is no longer
    // proportional to its value and the chart stops meaning anything.
    x: { grid: { display: false }, ticks: { color: ink(), font: { size: fs(9, 11) }, maxRotation: 0 } },
    y: {
      beginAtZero: true, grace: '14%', grid: { color: grid() },
      ticks: { color: ink(), font: { size: fs(9, 11) }, precision: 0, maxTicksLimit: 4 },
    },
  },
}))

// Top sites, tail folded into one row so a long list of ones does not crowd
// out the sites that actually carry the backlog.
const siteRows = computed(() => {
  const tally = {}
  for (const th of open.value) {
    const k = siteOf(th.endpoint)
    tally[k] = (tally[k] || 0) + 1
  }
  const rows = Object.entries(tally).map(([name, count]) => ({ name, count }))
    .sort((x, y) => y.count - x.count || x.name.localeCompare(y.name))
  const top = rows.slice(0, isFullscreen.value ? 9 : 7)
  const rest = rows.slice(top.length).reduce((s, r) => s + r.count, 0)
  if (rest > 0) top.push({ name: `+${rows.length - top.length} more`, count: rest })
  return top
})
const siteData = computed(() => ({
  labels: siteRows.value.map(r => r.name),
  datasets: [{ data: siteRows.value.map(r => r.count), backgroundColor: C.value.violet, borderRadius: 2 }],
}))
const osData = computed(() => ({
  labels: (snap.value.byOs || []).map(r => r.name),
  datasets: [{ data: (snap.value.byOs || []).map(r => r.count), backgroundColor: C.value.info, borderRadius: 2 }],
}))
const hBarOpts = computed(() => ({
  indexAxis: 'y', responsive: true, maintainAspectRatio: false, animation: { duration: 0 },
  layout: { padding: { right: fs(30, 40) } },
  plugins: {
    legend: { display: false },
    tooltip: { callbacks: { label: (c) => ` ${c.parsed.x.toLocaleString()} open` } },
    valueLabels: { color: ink(), size: fs(9, 12) },
  },
  scales: {
    x: { grid: { color: grid() }, ticks: { color: ink(), font: { size: fs(9, 11) }, precision: 0, maxTicksLimit: 4 } },
    y: { grid: { display: false }, ticks: { color: ink(), font: { size: fs(10, 13), weight: '700' }, autoSkip: false } },
  },
}))

const verdictData = computed(() => {
  const rows = snap.value.openVerdict || []
  return {
    labels: rows.map(r => r.name),
    datasets: [{
      data: rows.map(r => r.count),
      backgroundColor: rows.map((_, i) => VERDICT_P.value[i % VERDICT_P.value.length]),
      borderWidth: 0, spacing: 2,
    }],
  }
})
const donutOpts = computed(() => ({
  responsive: true, maintainAspectRatio: false, animation: { duration: 0 },
  cutout: '58%',
  plugins: {
    legend: {
      display: true, position: 'right',
      labels: {
        color: ink(), boxWidth: 10, boxHeight: 10, padding: 8,
        usePointStyle: true, pointStyle: 'rectRounded',
        font: { size: fs(10, 13), weight: '700' },
      },
    },
    tooltip: {
      callbacks: {
        label: (c) => {
          const sum = c.dataset.data.reduce((x, y) => x + y, 0)
          return ` ${c.label}: ${c.parsed.toLocaleString()}${sum ? ` (${Math.round((c.parsed / sum) * 100)}%)` : ''}`
        },
      },
    },
  },
}))

const fleetRows = computed(() => [
  { name: 'Online', count: a.value.online || 0 },
  { name: 'Silent', count: a.value.offline || 0 },
])

// --- full queue ----------------------------------------------------------
const QUEUE_FILTERS = [
  { id: 'all', label: 'All' }, { id: 'crit', label: 'Critical' },
  { id: 'aged', label: 'Over 7 days' }, { id: 'malicious', label: 'Malicious' },
  { id: 'undefined', label: 'No verdict' },
]
const queueFilter = ref('all')
const QUEUE_COLS = [
  { id: 'sev', label: 'Sev' }, { id: 'createdAt', label: 'Age' },
  { id: 'site', label: 'Site' }, { id: 'endpoint', label: 'Endpoint' },
  { id: 'name', label: 'Threat' }, { id: 'classification', label: 'Class' },
  { id: 'verdict', label: 'Verdict' }, { id: 'mitigation', label: 'Mitigation' },
]
const sortBy = ref('sev')
const sortDir = ref('asc')
const sortQueue = (id) => {
  if (sortBy.value === id) sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  else { sortBy.value = id; sortDir.value = 'asc' }
}
const matchesFilter = (th, id) => {
  if (id === 'crit') return sevOf(th).k === 'crit'
  if (id === 'aged') return ageDays(th.createdAt) >= 7
  if (id === 'malicious') return th.confidence === 'malicious'
  if (id === 'undefined') return !th.verdict || th.verdict === 'undefined'
  return true
}
const queueCount = (id) => open.value.filter(th => matchesFilter(th, id)).length
const sortedQueue = computed(() => {
  const q = search.value.trim().toLowerCase()
  const rows = open.value.filter(th => {
    if (!matchesFilter(th, queueFilter.value)) return false
    if (!q) return true
    return [th.name, th.endpoint, siteOf(th.endpoint), th.path, th.sha1, th.classification, th.publisher]
      .some(v => (v || '').toLowerCase().includes(q))
  })
  const dir = sortDir.value === 'desc' ? -1 : 1
  return rows.sort((x, y) => {
    if (sortBy.value === 'sev') {
      const d = SEV_ORDER[sevOf(x).k] - SEV_ORDER[sevOf(y).k]
      return (d !== 0 ? d : String(x.createdAt).localeCompare(String(y.createdAt))) * dir
    }
    if (sortBy.value === 'site') return siteOf(x.endpoint).localeCompare(siteOf(y.endpoint)) * dir
    const xv = x[sortBy.value] || '', yv = y[sortBy.value] || ''
    if (xv === yv) return (x.name || '').localeCompare(y.name || '')
    return (xv > yv ? 1 : -1) * dir
  })
})

// --- drill-in ------------------------------------------------------------
watch(openThreat, async (th) => {
  timeline.value = []
  detailError.value = ''
  if (!th) return
  detailLoading.value = true
  try {
    const res = await fetch(`/api/v1/s1/threat/${encodeURIComponent(th.id)}`, { cache: 'no-store' })
    const body = await res.json()
    if (body.ok) timeline.value = body.timeline || []
    else detailError.value = body.error || 'Could not read this threat.'
  } catch (e) {
    detailError.value = 'Could not reach the dashboard.'
  } finally {
    detailLoading.value = false
  }
})
const closeThreat = () => { openThreat.value = null }
const enginesText = computed(() => {
  const e = openThreat.value?.engines
  return e && e.length ? ` · ${e.join(', ')}` : ''
})
const onKey = (e) => { if (e.key === 'Escape' && openThreat.value) closeThreat() }

// --- formatting ----------------------------------------------------------
const fmt = (n) => (n || 0).toLocaleString()
const slug = (v) => String(v || 'none').toLowerCase().replace(/[^a-z0-9]+/g, '-')
const shortPath = (p) => {
  if (!p) return ''
  const parts = String(p).split('\\').filter(Boolean)
  return parts.length > 3 ? '…\\' + parts.slice(-3).join('\\') : p
}
const stamp = (s) => {
  if (!s) return '—'
  const d = new Date(s)
  return isNaN(d) ? s : d.toLocaleString([], { hourCycle: 'h23' })
}
const railSub = computed(() => {
  const parts = []
  if (snap.value.console) parts.push(snap.value.console)
  if (a.value.total) parts.push(`${fmt(a.value.total)} endpoints`)
  return parts.join('  ·  ')
})
const clockLabel = computed(() => new Date(serverNow.value)
  .toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }))
const feedLabel = computed(() => {
  if (!snap.value.updatedAt) return 'waiting'
  const ms = new Date(snap.value.updatedAt).getTime()
  if (!ms) return 'just now'
  const secs = Math.max(0, Math.round((serverNow.value - ms) / 1000))
  return secs < 60 ? `${secs}s ago` : `${Math.floor(secs / 60)}m ago`
})

// --- data ----------------------------------------------------------------
const fetchMetrics = async () => {
  loading.value = true
  try {
    const res = await fetch('/api/v1/s1/metrics', { cache: 'no-store' })
    if (res.ok) snap.value = await res.json()
  } catch (e) {
    // keep the last good snapshot; the age label carries the staleness
  } finally {
    loading.value = false
    loaded.value = true
  }
}
let es = null
const connectLive = () => {
  try {
    es = new EventSource('/api/v1/s1/live')
    es.onmessage = (e) => {
      try { snap.value = JSON.parse(e.data); loaded.value = true; live.value = true } catch (err) { /* malformed frame */ }
    }
    es.onerror = () => { live.value = false }
  } catch (e) { live.value = false }
}
let fallback = null
onMounted(() => {
  document.title = 'SentinelOne'
  fetchMetrics()
  connectLive()
  window.addEventListener('keydown', onKey)
  themeObserver = new MutationObserver(() => {
    dark.value = document.documentElement.classList.contains('dark')
  })
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  fallback = setInterval(() => { if (!live.value) fetchMetrics() }, 30000)
})
onUnmounted(() => {
  if (themeObserver) themeObserver.disconnect()
  if (es) es.close()
  if (fallback) clearInterval(fallback)
  window.removeEventListener('keydown', onKey)
})
</script>

<style scoped>
/* ====================================================================
   SOC CONSOLE

   Alert list dominant, charts banked down the right, one thin strip of
   reference figures across the top. The list is the largest thing on the
   screen because the threats are the work; everything else is context.

   Severity is a coloured block at the left edge of every row, which is
   the convention every security console uses and the thing that makes a
   list like this scannable without reading it.

   No animation on data: this repaints on every poll and lives on a
   monitor in peripheral vision all day.
   ==================================================================== */
.s1-view {
  --ok: #16a34a; --warn: #d97706; --crit: #dc2626; --idle: #64748b;
  --info: #2563eb; --accent: #7c3aed; --rose: #e11d48;
  --mono: var(--j-mono, ui-monospace, "SF Mono", Menlo, monospace);
  --hair: hsl(var(--border));
  --tint: 12%;
}
:global(.dark) .s1-view {
  --ok: #4ade80; --warn: #fbbf24; --crit: #f87171; --idle: #94a3b8;
  --info: #60a5fa; --accent: #a78bfa; --rose: #fb7185;
  --tint: 16%;
}

.s1-shell { width: 100%; padding: 0.8rem 0.9rem 1.1rem; display: flex; flex-direction: column; gap: 0.55rem; }
/* One viewport, never scrolls. min-height:0 on every shrinkable descendant,
   without which a grid child refuses to go below its content height. */
.s1-view.wall .s1-shell { height: 100vh; overflow: hidden; padding: 0.5rem 0.6rem; gap: 0.45rem; }

/* ------------------------------------------------------------------ rail */
.rail { display: flex; align-items: center; gap: 1rem; flex-wrap: wrap; flex: none; }
.rail-id { display: flex; align-items: center; gap: 0.6rem; min-width: 0; }
.rail-back { display: grid; place-items: center; width: 2.25rem; height: 2.25rem; border-radius: 8px; color: hsl(var(--muted-foreground)); }
.rail-back:hover { background: hsl(var(--muted)); color: hsl(var(--foreground)); }
/* A background-image span, not an <img>: the header stylesheet repaints
   <img> for the wordmark and would turn this into a white block. */
.rail-mark { width: 1.9rem; height: 1.9rem; flex: none; background-size: contain; background-repeat: no-repeat; background-position: center; }
.rail-name { display: flex; flex-direction: column; min-width: 0; }
.rail-title { font-weight: 800; font-size: 1.1rem; line-height: 1.1; letter-spacing: -0.01em; }
.rail-sub { font-family: var(--mono); font-size: 0.7rem; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rail-state { display: flex; align-items: center; gap: 0.6rem; margin-left: auto; }
.feed, .clock { font-family: var(--mono); font-variant-numeric: tabular-nums; white-space: nowrap; }
.feed { font-size: 0.72rem; color: hsl(var(--muted-foreground)); }
.clock { font-size: 1rem; font-weight: 700; }
.s1-view.wall .clock { font-size: 1.3rem; }
.livedot { width: 0.5rem; height: 0.5rem; border-radius: 50%; background: var(--idle); flex: none; }
.livedot.on { background: var(--ok); animation: pulse 2.4s ease-in-out infinite; }
@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.3; } }
.tabbar { display: flex; gap: 0.2rem; }
.tab {
  display: inline-flex; align-items: center; gap: 0.35rem; padding: 0.35rem 0.7rem;
  font-size: 0.82rem; font-weight: 600; border-radius: 7px;
  color: hsl(var(--muted-foreground)); border: 1px solid transparent;
}
.tab:hover { background: hsl(var(--muted)); color: hsl(var(--foreground)); }
.tab.on { color: hsl(var(--foreground)); background: hsl(var(--card)); border-color: var(--hair); }

/* ------------------------------------------------------------- thin strip */
/* Reference figures. Small by design - the alert list is the focal point,
   and a row of big boxes competes with it for attention. */
.strip {
  flex: none; display: grid; gap: 0.3rem;
  grid-template-columns: repeat(auto-fit, minmax(5.2rem, 1fr));
}
.kv {
  display: flex; align-items: baseline; gap: 0.35rem; min-width: 0;
  padding: 0.3rem 0.5rem; border-radius: 7px;
  border: 1px solid color-mix(in srgb, var(--kc) 30%, var(--hair));
  background: color-mix(in srgb, var(--kc) var(--tint), hsl(var(--card)));
}
.kv.calm { background: hsl(var(--card)); border-color: var(--hair); }
.kv-n {
  font-family: var(--mono); font-variant-numeric: tabular-nums;
  font-size: 1.1rem; font-weight: 800; line-height: 1.1; letter-spacing: -0.04em;
  color: var(--kc);
}
.s1-view.wall .kv-n { font-size: 1.5rem; }
.kv.calm .kv-n { color: hsl(var(--foreground)); }
.kv-n i { font-style: normal; font-size: 0.6em; opacity: 0.7; }
.kv-l {
  font-family: var(--mono); font-size: 8px; font-weight: 800; letter-spacing: 0.1em;
  text-transform: uppercase; color: hsl(var(--muted-foreground));
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.s1-view.wall .kv-l { font-size: 10px; }

/* ---------------------------------------------------------------- canvas */
/* Alert list across roughly 60%, three charts banked down the right. */
.canvas {
  display: grid; gap: 0.5rem; min-width: 0;
  grid-template-columns: minmax(0, 1.45fr) minmax(0, 1fr);
  grid-template-areas: "alerts c1" "alerts c2" "alerts c3";
  grid-template-rows: repeat(3, minmax(0, 1fr));
}
.s1-view.wall .canvas { flex: 1 1 auto; min-height: 0; overflow: hidden; }
.canvas > .panel-x:nth-of-type(2) { grid-area: c1; }
.canvas > .panel-x:nth-of-type(3) { grid-area: c2; }
.canvas > .panel-x:nth-of-type(4) { grid-area: c3; }
.alerts { grid-area: alerts; }
@media (max-width: 1100px) {
  .canvas { grid-template-columns: 1fr; grid-template-areas: "alerts" "c1" "c2" "c3"; grid-template-rows: auto; }
  .alerts { min-height: 22rem; }
  .canvas > .panel-x { min-height: 13rem; }
}

.panel-x {
  display: flex; flex-direction: column; min-width: 0; min-height: 0;
  border: 1px solid var(--hair); border-radius: 10px;
  padding: 0.45rem 0.6rem 0.5rem; background: hsl(var(--card)); overflow: hidden;
}
.ph {
  display: flex; align-items: baseline; justify-content: space-between; gap: 0.5rem;
  padding-bottom: 0.3rem; margin-bottom: 0.3rem; border-bottom: 1px solid var(--hair); flex: none;
}
.pt {
  display: flex; align-items: center; gap: 0.3rem; margin: 0;
  font-family: var(--mono); font-size: 10px; font-weight: 800; letter-spacing: 0.14em;
  text-transform: uppercase; color: hsl(var(--foreground)); white-space: nowrap;
}
.s1-view.wall .pt { font-size: 12px; }
.ps { font-size: 0.64rem; color: hsl(var(--muted-foreground)); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.s1-view.wall .ps { font-size: 0.78rem; }
.chart { position: relative; flex: 1 1 auto; min-height: 0; }
.h-fix { height: 12rem; flex: none; }

/* ----------------------------------------------------------- severity key */
.sevkey { display: flex; gap: 0.5rem; flex-wrap: wrap; }
.sk {
  display: inline-flex; align-items: center; gap: 0.25rem;
  font-family: var(--mono); font-size: 0.64rem; font-weight: 700;
  font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground));
}
.s1-view.wall .sk { font-size: 0.78rem; }
.sk i { width: 0.5rem; height: 0.5rem; border-radius: 2px; display: inline-block; }
.sk.sev-crit i { background: var(--crit); }
.sk.sev-high i { background: var(--rose); }
.sk.sev-med i { background: var(--warn); }
.sk.sev-low i { background: var(--idle); }

/* ---------------------------------------------------------------- alerts */
.alist { flex: 1 1 auto; min-height: 0; overflow: hidden; }
.atab { width: 100%; border-collapse: collapse; font-family: var(--mono); font-size: 0.76rem; font-variant-numeric: tabular-nums; }
.s1-view.wall .atab { font-size: 0.95rem; }
.atab th {
  font-size: 8px; font-weight: 800; letter-spacing: 0.1em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); text-align: left; padding: 0 0.35rem 0.25rem; white-space: nowrap;
}
.s1-view.wall .atab th { font-size: 10px; }
.atab td { padding: 0 0.35rem; height: 1.7rem; white-space: nowrap; }
.s1-view.wall .atab td { height: 2.15rem; }
.atab tbody tr { cursor: pointer; border-top: 1px solid hsl(var(--border) / 0.5); }
.atab tbody tr:hover { background: hsl(var(--muted)); }

/* The severity block. Every security console puts this at the left edge,
   and it is what lets a list this dense be scanned rather than read. */
.c-sev { width: 3.1rem; }
.sevbar {
  display: inline-block; width: 100%; text-align: center;
  font-size: 0.6rem; font-weight: 800; letter-spacing: 0.06em;
  border-radius: 3px; padding: 0.1rem 0; color: #fff;
}
.s1-view.wall .sevbar { font-size: 0.72rem; }
.sevbar.sev-crit { background: var(--crit); }
.sevbar.sev-high { background: var(--rose); }
.sevbar.sev-med { background: var(--warn); color: hsl(20 14% 12%); }
.sevbar.sev-low { background: var(--idle); }

.c-age { width: 3rem; font-weight: 700; }
.c-age.a1 { color: hsl(var(--foreground)); }
.c-age.a2 { color: var(--warn); }
.c-age.a3 { color: var(--crit); font-weight: 800; }
.c-site { width: 4.6rem; }
.sitetag {
  display: inline-block; font-size: 0.62rem; font-weight: 800; letter-spacing: 0.04em;
  border-radius: 3px; padding: 0.05rem 0.3rem;
  border: 1px solid color-mix(in srgb, var(--accent) 35%, transparent);
  background: color-mix(in srgb, var(--accent) 12%, transparent);
  color: var(--accent);
}
.s1-view.wall .sitetag { font-size: 0.75rem; }
.c-host { width: 10.5rem; font-weight: 700; overflow: hidden; text-overflow: ellipsis; }
.c-file { max-width: 0; width: 40%; overflow: hidden; }
.fname { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.fpath { display: block; font-size: 0.66rem; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.c-vd { width: 7.5rem; }
.empty { color: hsl(var(--muted-foreground)); text-align: center; height: 4rem; }

.pill { display: inline-block; font-size: 0.66rem; font-weight: 700; border-radius: 4px; padding: 0.06rem 0.3rem; border: 1px solid var(--hair); }
.s1-view.wall .pill { font-size: 0.8rem; }
.cls-ransomware { color: var(--crit); border-color: color-mix(in srgb, var(--crit) 45%, transparent); background: color-mix(in srgb, var(--crit) 14%, transparent); }
.cls-malware { color: var(--warn); border-color: color-mix(in srgb, var(--warn) 45%, transparent); background: color-mix(in srgb, var(--warn) 14%, transparent); }
.vd-true-positive { color: var(--crit); border-color: color-mix(in srgb, var(--crit) 40%, transparent); }
.vd-false-positive { color: hsl(var(--muted-foreground)); }
.vd-suspicious { color: var(--warn); border-color: color-mix(in srgb, var(--warn) 40%, transparent); }
.vd-undefined { color: hsl(var(--muted-foreground)); border-style: dashed; }
.mt-ok { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 40%, transparent); }
.mt-bad { color: var(--crit); border-color: color-mix(in srgb, var(--crit) 45%, transparent); background: color-mix(in srgb, var(--crit) 14%, transparent); }

/* ------------------------------------------------------------ fail strip */
.failstrip {
  display: flex; align-items: center; gap: 0.6rem; flex: none; flex-wrap: wrap;
  border: 1px solid color-mix(in srgb, var(--crit) 50%, transparent);
  background: color-mix(in srgb, var(--crit) 12%, transparent);
  border-radius: 10px; padding: 0.4rem 0.7rem; color: var(--crit);
}
.fs-l { font-family: var(--mono); font-size: 0.72rem; font-weight: 800; letter-spacing: 0.1em; text-transform: uppercase; }
.chips { list-style: none; display: flex; flex-wrap: wrap; gap: 0.3rem; margin: 0; padding: 0; }
.chip { font-family: var(--mono); font-size: 0.72rem; border-radius: 5px; padding: 0.1rem 0.4rem; border: 1px solid color-mix(in srgb, var(--crit) 40%, transparent); }

/* -------------------------------------------------------- queue/analysis */
.queue, .analysis { display: flex; flex-direction: column; gap: 0.6rem; }
.q-tools { display: flex; align-items: center; gap: 0.6rem; flex-wrap: wrap; }
.q-search { flex: 1; min-width: 14rem; font-size: 0.85rem; background: hsl(var(--background)); border: 1px solid var(--hair); border-radius: 8px; padding: 0.4rem 0.65rem; }
.q-filters { display: flex; gap: 0.2rem; }
.qf { display: inline-flex; align-items: center; gap: 0.3rem; font-size: 0.78rem; font-weight: 600; padding: 0.35rem 0.6rem; border-radius: 8px; color: hsl(var(--muted-foreground)); border: 1px solid transparent; }
.qf:hover { background: hsl(var(--muted)); }
.qf.on { color: hsl(var(--foreground)); border-color: var(--hair); background: hsl(var(--card)); }
.qf-n { font-family: var(--mono); font-size: 0.68rem; font-variant-numeric: tabular-nums; opacity: 0.7; }
.q-open { display: inline-flex; align-items: center; gap: 0.3rem; font-size: 0.78rem; color: hsl(var(--muted-foreground)); }
.q-open:hover { color: hsl(var(--foreground)); }
.table { overflow-x: auto; border: 1px solid var(--hair); border-radius: 10px; background: hsl(var(--card)); }
.atab.full td { height: 2.2rem; }
.sortable { font: inherit; letter-spacing: inherit; text-transform: inherit; color: inherit; padding: 0; border: 0; background: none; }
.sortable:hover { color: hsl(var(--foreground)); }
.sortable.on { color: hsl(var(--foreground)); text-decoration: underline; text-underline-offset: 0.2em; }
.an-grid { display: grid; gap: 0.6rem; grid-template-columns: repeat(auto-fit, minmax(15rem, 1fr)); }
.an-wide { grid-column: span 1; }
@media (min-width: 1100px) { .an-wide { grid-column: span 2; } }

/* --------------------------------------------------------------- panel */
.panel { position: fixed; inset: 0; z-index: 60; display: flex; justify-content: flex-end; background: hsl(0 0% 0% / 0.5); }
.panel-card { width: min(34rem, 100%); height: 100%; overflow-y: auto; background: hsl(var(--background)); border-left: 1px solid var(--hair); padding: 1rem 1.1rem 2rem; display: flex; flex-direction: column; gap: 0.9rem; }
.p-head { display: flex; align-items: flex-start; gap: 0.6rem; }
.p-id { display: flex; flex-direction: column; gap: 0.4rem; min-width: 0; align-items: flex-start; }
.p-id .sevbar { width: auto; padding: 0.1rem 0.5rem; }
.p-title { margin: 0; font-size: 1rem; font-weight: 700; word-break: break-all; }
.p-facts { display: grid; grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr)); gap: 0.5rem 0.9rem; margin: 0; }
.p-facts > div { min-width: 0; }
.p-facts dt { font-family: var(--mono); font-size: 9px; font-weight: 700; letter-spacing: 0.12em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
.p-facts dd { margin: 0.1rem 0 0; font-size: 0.82rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.p-alarm { display: flex; align-items: center; gap: 0.5rem; font-size: 0.8rem; color: var(--crit); border: 1px solid color-mix(in srgb, var(--crit) 40%, transparent); background: color-mix(in srgb, var(--crit) 10%, transparent); border-radius: 8px; padding: 0.5rem 0.65rem; }
.p-block { display: flex; flex-direction: column; gap: 0.3rem; }
.p-h4 { margin: 0; font-family: var(--mono); font-size: 9px; font-weight: 800; letter-spacing: 0.14em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
.p-code { font-family: var(--mono); font-size: 0.72rem; word-break: break-all; background: hsl(var(--muted)); border-radius: 6px; padding: 0.4rem 0.5rem; }
.tl { list-style: none; margin: 0; padding: 0; display: grid; gap: 0.5rem; }
.tl li { display: grid; gap: 0.1rem; border-left: 2px solid var(--accent); padding-left: 0.6rem; }
.tl-at { font-family: var(--mono); font-size: 0.66rem; color: hsl(var(--muted-foreground)); font-variant-numeric: tabular-nums; }
.tl-p { font-size: 0.8rem; }
.tl-s { font-size: 0.72rem; color: hsl(var(--muted-foreground)); word-break: break-all; }
.p-link { display: inline-flex; align-items: center; gap: 0.35rem; margin-top: auto; font-size: 0.8rem; color: hsl(var(--muted-foreground)); }
.p-link:hover { color: hsl(var(--foreground)); }

/* ------------------------------------------------------------- notices */
.notice { border: 1px dashed var(--hair); border-radius: 10px; padding: 1rem 1.1rem; background: hsl(var(--card)); }
.notice-error { border-style: solid; border-color: color-mix(in srgb, var(--crit) 45%, transparent); background: color-mix(in srgb, var(--crit) 8%, transparent); }
.notice-title { display: flex; align-items: center; gap: 0.4rem; font-weight: 700; font-size: 0.9rem; }
.notice-body { font-size: 0.82rem; color: hsl(var(--muted-foreground)); margin-top: 0.35rem; }
.notice code { font-family: var(--mono); background: hsl(var(--muted)); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em; }
.gnote { margin: 0; font-size: 0.74rem; color: hsl(var(--muted-foreground)); }
.err-pre { font-family: var(--mono); font-size: 12px; white-space: pre-wrap; word-break: break-word; color: var(--crit); background: color-mix(in srgb, var(--crit) 9%, transparent); border-radius: 6px; padding: 0.6rem 0.75rem; margin: 0; }

@media (prefers-reduced-motion: reduce) {
  .animate-spin, .livedot.on { animation: none; }
}
</style>
