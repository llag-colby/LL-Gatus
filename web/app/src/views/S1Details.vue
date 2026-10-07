<template>
  <div class="dashboard-container detail-page s1-view bg-background" :class="{ wall: isFullscreen }">
    <div class="s1-shell">

      <!-- RAIL. On the wall it keeps only what proves the board is alive. -->
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
          The base URL is the console you log into, not the host named in the token.
        </p>
      </div>
      <div v-else-if="loaded && !snap.ok" class="notice notice-error">
        <div class="notice-title"><AlertTriangle class="h-4 w-4" /> Cannot reach SentinelOne</div>
        <pre class="err-pre">{{ snap.error }}</pre>
      </div>
      <div v-else-if="!loaded" class="notice"><div class="notice-title">Reading estate…</div></div>

      <template v-else>
        <!-- BEACON + KPI BAND. One state word, then the numbers. No sentences:
             an operator reading a wall needs the figures, and a paragraph on a
             monitor is just something nobody reads twice. -->
        <section class="band" :class="'st-' + state.tone">
          <div class="bc">
            <component :is="state.icon" class="bc-i" />
            <span class="bc-w">{{ state.word }}</span>
          </div>
          <div class="kpis">
            <div class="kpi" v-for="k in kpis" :key="k.l"
              :class="[k.tone, { zero: !k.n }]" :style="{ '--kc': k.c }">
              <span class="kpi-n">{{ fmt(k.n) }}</span>
              <span class="kpi-l">{{ k.l }}</span>
            </div>
          </div>
        </section>

        <nav v-if="!isFullscreen" class="tabbar" role="tablist" aria-label="View">
          <button v-for="tb in TABS" :key="tb.id" role="tab" :aria-selected="shown === tb.id"
            class="tab" :class="{ on: shown === tb.id }" @click="tab = tb.id">
            <component :is="tb.icon" class="h-4 w-4" />{{ tb.label }}
            <span v-if="tb.badge" class="tab-n">{{ fmt(tb.badge) }}</span>
          </button>
        </nav>

        <!-- ======================= WALL ======================= -->
        <section v-if="shown === 'overview'" class="grid">
          <!-- Daily detections: bars on a LOG axis plus a 7-day mean line.
               Log because the real series runs 6 to 1200+ in one fortnight - a
               full-disk sweep finds the same file on hundreds of machines at
               once - and on a linear axis every ordinary day is an invisible
               sliver. The line is the trend with those sweeps smoothed out. -->
          <article class="box sp8 tc-violet">
            <header class="bh">
              <h3 class="bt">Detections per day</h3>
              <span class="bsub">{{ trend.length }}d · log axis · 7d mean</span>
            </header>
            <div class="chart h-trend">
              <Bar :data="trendData" :options="trendOpts" />
            </div>
          </article>

          <!-- Licences. 95%+ here, and nothing else on the dashboard would
               tell you before enrolments start failing. -->
          <article class="box sp4 tc-amber" v-if="a.totalLicenses">
            <header class="bh">
              <h3 class="bt">Licences</h3>
              <span class="bsub" :class="{ warnText: licPct >= 90 }">{{ licPct.toFixed(1) }}%</span>
            </header>
            <div class="licbig">
              <span class="lic-n" :class="licTone">{{ fmt(a.activeLicenses) }}</span>
              <span class="lic-d">/ {{ fmt(a.totalLicenses) }}</span>
            </div>
            <div class="gauge">
              <span class="gfill" :class="licTone" :style="{ width: Math.min(100, licPct) + '%' }"></span>
              <span class="gmark" style="left: 90%"></span>
            </div>
            <div class="licfoot">
              <span>{{ fmt(licFree) }} free</span>
              <span>{{ fmt(a.total) }} agents</span>
            </div>
          </article>

          <S1Donut class="sp4" title="Open by verdict" :sub="`${fmt(t.notResolved)} open`"
            centre-label="open" :rows="snap.openVerdict || []" :palette="VERDICT_P" />
          <S1Donut class="sp4" title="Estate mitigation" sub="all time"
            centre-label="threats" :rows="snap.allMitigation || []" :palette="MITIGATION_P" />
          <S1Donut class="sp4" title="Classification" sub="all time"
            centre-label="threats" :rows="snap.allClassification || []" :palette="CLASS_P" />

          <S1Donut class="sp4" title="Fleet check-in" :sub="`${fmt(a.total)} agents`"
            centre-label="agents" :rows="fleetRows" :palette="FLEET_P" />

          <!-- Repeat offenders: one machine carries 18 of the 85 open
               detections, which a total alone would never show. -->
          <article class="box sp4 tc-violet" v-if="(snap.byEndpoint || []).length">
            <header class="bh">
              <h3 class="bt">Repeat offenders</h3>
              <span class="bsub">open detections</span>
            </header>
            <div class="chart h-bars">
              <Bar :data="offenderData" :options="offenderOpts" />
            </div>
          </article>

          <article class="box sp4 tc-info">
            <header class="bh">
              <h3 class="bt">Open by OS</h3>
              <span class="bsub">{{ fmt(t.notResolved) }} open</span>
            </header>
            <div class="chart h-bars">
              <Bar :data="osData" :options="offenderOpts" />
            </div>
          </article>

          <article class="box sp12 alarm tc-crit" v-if="(snap.byAction || []).length">
            <header class="bh">
              <h3 class="bt"><AlertTriangle class="h-3.5 w-3.5" /> Failed mitigation</h3>
            </header>
            <ul class="chips">
              <li v-for="r in snap.byAction" :key="r.name" class="chip bad">
                {{ r.name }} <b>{{ fmt(r.count) }}</b>
              </li>
            </ul>
          </article>

          <!-- Newest open threats. On a wall this is the thing an analyst
               actually acts on, so it earns a full row. -->
          <article class="box sp12 tc-crit">
            <header class="bh">
              <h3 class="bt">Newest open</h3>
              <span class="bsub">{{ fmt(open.length) }} in queue</span>
            </header>
            <table class="t t-wall">
              <tbody>
                <tr v-for="th in newest" :key="th.id" :class="{ act: th.needsAction }"
                  @click="openThreat = th">
                  <td class="c-age">{{ age(th.createdAt) }}</td>
                  <td class="c-host">{{ th.endpoint || '—' }}</td>
                  <td class="c-file"><span class="fname">{{ th.name }}</span></td>
                  <td><span class="pill" :class="'cls-' + slug(th.classification)">{{ th.classification }}</span></td>
                  <td><span class="dot" :class="'cf-' + slug(th.confidence)"></span>{{ th.confidence }}</td>
                  <td><span class="pill" :class="'vd-' + slug(th.verdict)">{{ th.verdictLabel || '—' }}</span></td>
                  <td class="right"><span class="pill" :class="th.needsAction ? 'mt-bad' : 'mt-ok'">{{ th.mitigationLabel || '—' }}</span></td>
                </tr>
              </tbody>
            </table>
          </article>
        </section>

        <!-- ======================= QUEUE ======================= -->
        <section v-else-if="shown === 'queue'" class="queue">
          <div class="q-tools">
            <input v-model="search" class="q-search" type="search"
              :placeholder="`Filter ${fmt(open.length)} open — file, endpoint, hash…`" />
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
            <table class="t t-queue">
              <thead>
                <tr>
                  <th v-for="col in QUEUE_COLS" :key="col.id" :class="{ right: col.right }">
                    <button class="sortable" :class="{ on: sortBy === col.id }" @click="sortQueue(col.id)">
                      {{ col.label }}
                    </button>
                  </th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="th in sortedQueue" :key="th.id" class="qrow"
                  :class="{ act: th.needsAction }" @click="openThreat = th">
                  <td class="c-file">
                    <span class="fname" :title="th.name">{{ th.name }}</span>
                    <span class="fpath" :title="th.path">{{ shortPath(th.path) }}</span>
                  </td>
                  <td class="c-host">{{ th.endpoint || '—' }}</td>
                  <td><span class="pill" :class="'cls-' + slug(th.classification)">{{ th.classification || '—' }}</span></td>
                  <td><span class="pill" :class="'vd-' + slug(th.verdict)">{{ th.verdictLabel || '—' }}</span></td>
                  <td><span class="dot" :class="'cf-' + slug(th.confidence)"></span>{{ th.confidence || '—' }}</td>
                  <td><span class="pill" :class="th.needsAction ? 'mt-bad' : 'mt-ok'">{{ th.mitigationLabel || '—' }}</span></td>
                  <td class="right c-age">{{ age(th.createdAt) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else class="notice"><div class="notice-title">Nothing matches</div></div>
        </section>

        <!-- ======================= FLEET ======================= -->
        <section v-else class="grid">
          <S1Donut class="sp4" title="Check-in" :sub="`${fmt(a.total)} agents`"
            centre-label="agents" :rows="fleetRows" :palette="FLEET_P" />
          <S1Donut class="sp4" title="Agent version" :sub="`${fmt(a.total)} agents`"
            centre-label="agents" :rows="versionRows" :palette="['#22c55e', '#f59e0b']" />
          <article class="box sp4 tc-teal">
            <header class="bh"><h3 class="bt">Fleet</h3></header>
            <table class="t">
              <tbody>
                <tr v-for="r in fleetFacts" :key="r.l">
                  <td class="c-who"><span class="who">{{ r.l }}</span></td>
                  <td class="c-n" :class="r.tone"><span class="n">{{ fmt(r.n) }}</span></td>
                </tr>
              </tbody>
            </table>
          </article>
          <article class="box sp6 tc-amber" v-if="a.totalLicenses">
            <header class="bh">
              <h3 class="bt">Licences</h3>
              <span class="bsub" :class="{ warnText: licPct >= 90 }">{{ licPct.toFixed(1) }}%</span>
            </header>
            <div class="licbig">
              <span class="lic-n" :class="licTone">{{ fmt(a.activeLicenses) }}</span>
              <span class="lic-d">/ {{ fmt(a.totalLicenses) }}</span>
            </div>
            <div class="gauge">
              <span class="gfill" :class="licTone" :style="{ width: Math.min(100, licPct) + '%' }"></span>
              <span class="gmark" style="left: 90%"></span>
            </div>
            <div class="licfoot"><span>{{ fmt(licFree) }} free</span><span>90% mark</span></div>
          </article>
          <article class="box sp6 tc-cyan">
            <header class="bh">
              <h3 class="bt">Open by machine type</h3>
            </header>
            <div class="chart h-bars">
              <Bar :data="machineData" :options="offenderOpts" />
            </div>
          </article>
        </section>
      </template>
    </div>

    <!-- DRILL-IN -->
    <aside v-if="openThreat" class="panel" @click.self="closeThreat">
      <div class="panel-card">
        <header class="p-head">
          <div class="p-id">
            <span class="pill" :class="'cls-' + slug(openThreat.classification)">{{ openThreat.classification || '—' }}</span>
            <h2 class="p-title">{{ openThreat.name }}</h2>
          </div>
          <Button variant="ghost" size="icon" class="h-9 w-9" @click="closeThreat">
            <X class="h-5 w-5" />
          </Button>
        </header>

        <dl class="p-facts">
          <div><dt>Endpoint</dt><dd>{{ openThreat.endpoint || '—' }}</dd></div>
          <div><dt>OS</dt><dd>{{ openThreat.os || '—' }}</dd></div>
          <div><dt>Machine</dt><dd>{{ openThreat.machineType || '—' }}</dd></div>
          <div><dt>Status</dt><dd>{{ openThreat.statusLabel || '—' }}</dd></div>
          <div><dt>Verdict</dt><dd>{{ openThreat.verdictLabel || '—' }}</dd></div>
          <div><dt>Confidence</dt><dd>{{ openThreat.confidence || '—' }}</dd></div>
          <div><dt>Mitigation</dt><dd>{{ openThreat.mitigationLabel || '—' }}</dd></div>
          <div><dt>Detection</dt><dd>{{ openThreat.detectionType || '—' }}{{ enginesText }}</dd></div>
          <div><dt>Publisher</dt><dd>{{ openThreat.publisher || '—' }}</dd></div>
          <div><dt>First seen</dt><dd>{{ stamp(openThreat.createdAt) }}</dd></div>
          <div><dt>Updated</dt><dd>{{ stamp(openThreat.updatedAt) }}</dd></div>
          <div><dt>Agent</dt><dd>{{ openThreat.agentVersion || '—' }}</dd></div>
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
  ArrowLeft, RefreshCw, AlertTriangle, ShieldCheck, ShieldAlert, Shield, ShieldQuestion,
  Gauge, Rows3, MonitorSmartphone, ExternalLink, X,
} from 'lucide-vue-next'
import { Bar } from 'vue-chartjs'
import {
  Chart as ChartJS, BarElement, BarController, LineElement, PointElement, LineController,
  CategoryScale, LinearScale, LogarithmicScale, Tooltip, Legend,
} from 'chart.js'
import { Button } from '@/components/ui/button'
import Settings from '@/components/Settings.vue'
import S1Donut from '@/components/S1Donut.vue'
import { isFullscreen, now as serverNow } from '@/store'
import s1Icon from '@/assets/sentinelone.png'

ChartJS.register(
  BarElement, BarController, LineElement, PointElement, LineController,
  CategoryScale, LinearScale, LogarithmicScale, Tooltip, Legend,
)

// A fixed palette rather than the theme's CSS custom properties: those are
// defined with color-mix(), which getComputedStyle returns unresolved and
// chart.js cannot parse. Same hues as the status tokens, hard-coded.
const C = {
  crit: '#ef4444', rose: '#f43f5e', warn: '#f59e0b', amber: '#d97706',
  ok: '#22c55e', teal: '#14b8a6', info: '#3b82f6', cyan: '#06b6d4',
  violet: '#8b5cf6', fuchsia: '#c026d3', idle: '#64748b', slate: '#94a3b8',
}
// Each state keeps one colour across every chart: "not mitigated" is red
// everywhere, or the wall teaches the wrong reflex.
const VERDICT_P = [C.warn, C.slate, C.crit, C.info]
const MITIGATION_P = [C.ok, C.idle, C.crit]
const CLASS_P = [C.info, C.fuchsia, C.crit]
const FLEET_P = [C.teal, C.idle]

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

const TABS = computed(() => [
  { id: 'overview', label: 'Wall', icon: Gauge },
  { id: 'queue', label: 'Queue', icon: Rows3, badge: t.value.notResolved },
  { id: 'fleet', label: 'Fleet', icon: MonitorSmartphone },
])
const savedTab = localStorage.getItem('gatus.s1.tab')
const tab = ref(['overview', 'queue', 'fleet'].includes(savedTab) ? savedTab : 'overview')
watch(tab, (v) => localStorage.setItem('gatus.s1.tab', v))
// Fullscreen always shows the wall. Someone who left the page on the queue and
// then put it on a monitor wants the dashboard, not a table they cannot click.
const shown = computed(() => (isFullscreen.value ? 'overview' : tab.value))

// --- state word ----------------------------------------------------------
// One or two words. Same grading as the server: an infected endpoint or an
// unmitigated open threat is a problem; a triage backlog is not.
const state = computed(() => {
  const th = t.value, ag = a.value
  if (!snap.value.ok) return { tone: 'unknown', word: 'Unreachable', icon: ShieldQuestion }
  if (ag.infected > 0) return { tone: 'crit', word: 'Infected', icon: ShieldAlert }
  if (th.needsAction > 0) return { tone: 'crit', word: 'Action required', icon: ShieldAlert }
  if (th.notResolved > 0) return { tone: 'warn', word: 'Contained', icon: Shield }
  return { tone: 'ok', word: 'Clear', icon: ShieldCheck }
})

const kpis = computed(() => {
  const th = t.value
  return [
    { l: 'Open', n: th.notResolved, c: C.violet },
    { l: 'Need action', n: th.needsAction, c: C.crit },
    { l: 'Malicious', n: th.maliciousNotResolved, c: C.rose },
    { l: 'Suspicious', n: th.suspiciousNotResolved, c: C.warn },
    { l: 'New today', n: th.newToday, c: C.info },
    { l: 'New 7d', n: th.newThisWeek, c: C.cyan },
    { l: 'Unmitigated', n: th.notMitigated, c: C.amber },
    { l: 'All time', n: th.total, c: C.idle },
  ]
})

// --- charts --------------------------------------------------------------
const isDark = () => document.documentElement.classList.contains('dark')
const grid = () => (isDark() ? 'rgba(148,163,184,0.14)' : 'rgba(100,116,139,0.16)')
const ink = () => (isDark() ? 'rgba(226,232,240,0.72)' : 'rgba(51,65,85,0.75)')

// 7-day trailing mean, so the underlying rate is readable through the sweep
// spikes rather than being hidden by them.
const rollingMean = (series, window = 7) => series.map((_, i) => {
  const from = Math.max(0, i - window + 1)
  const slice = series.slice(from, i + 1)
  return slice.reduce((s, n) => s + n, 0) / slice.length
})

const trendData = computed(() => {
  const counts = trend.value.map(p => p.created || 0)
  return {
    labels: trend.value.map(p => p.date.slice(5)),
    datasets: [
      {
        type: 'bar', label: 'Detections', data: counts,
        backgroundColor: counts.map(n => (n >= Math.max(...counts, 1) ? C.warn : C.violet)),
        borderRadius: 2, order: 2,
      },
      {
        type: 'line', label: '7d mean', data: rollingMean(counts),
        borderColor: C.info, backgroundColor: C.info, borderWidth: 2,
        pointRadius: 0, tension: 0.35, order: 1,
      },
    ],
  }
})
const trendOpts = computed(() => ({
  responsive: true, maintainAspectRatio: false,
  animation: { duration: 0 },
  plugins: {
    legend: { display: false },
    tooltip: { callbacks: { label: (c) => ` ${c.dataset.label}: ${Math.round(c.parsed.y).toLocaleString()}` } },
  },
  scales: {
    x: { grid: { display: false }, ticks: { color: ink(), font: { size: 9 } } },
    y: {
      // Logarithmic: see the comment on the template. A zero-detection day
      // simply draws no bar, which is correct rather than a gap in the data.
      type: 'logarithmic', min: 1,
      grid: { color: grid() },
      ticks: { color: ink(), font: { size: 9 }, callback: (v) => (Number.isInteger(Math.log10(v)) ? v : '') },
    },
  },
}))

// Horizontal bars for the "top N" lists: a long machine name needs a row, and
// a vertical bar chart would turn them all sideways.
const hBar = (rows, color) => ({
  labels: rows.map(r => r.name),
  datasets: [{ data: rows.map(r => r.count), backgroundColor: color, borderRadius: 2, barThickness: 'flex' }],
})
const offenderData = computed(() => hBar((snap.value.byEndpoint || []).slice(0, 10), C.violet))
const osData = computed(() => hBar(snap.value.byOs || [], C.info))
const machineData = computed(() => hBar(snap.value.byMachineType || [], C.info))
const offenderOpts = computed(() => ({
  indexAxis: 'y',
  responsive: true, maintainAspectRatio: false,
  animation: { duration: 0 },
  plugins: { legend: { display: false }, tooltip: { callbacks: { label: (c) => ` ${c.parsed.x.toLocaleString()}` } } },
  scales: {
    x: { grid: { color: grid() }, ticks: { color: ink(), font: { size: 9 }, precision: 0 } },
    y: { grid: { display: false }, ticks: { color: ink(), font: { size: 10 }, autoSkip: false } },
  },
}))

const fleetRows = computed(() => [
  { name: 'Online', count: a.value.online || 0 },
  { name: 'Not checked in', count: a.value.offline || 0 },
])
const versionRows = computed(() => [
  { name: 'Up to date', count: a.value.upToDate || 0 },
  { name: 'Out of date', count: a.value.outOfDate || 0 },
])
const fleetFacts = computed(() => [
  { l: 'Total agents', n: a.value.total },
  { l: 'Online', n: a.value.online },
  { l: 'Not checked in', n: a.value.offline, tone: a.value.offline > 0 ? 'warnText' : '' },
  { l: 'Infected', n: a.value.infected, tone: a.value.infected > 0 ? 'critText' : '' },
  { l: 'Out of date', n: a.value.outOfDate, tone: a.value.outOfDate > 0 ? 'warnText' : '' },
  { l: 'Decommissioned', n: a.value.decommissioned },
])

const licPct = computed(() => {
  const total = a.value.totalLicenses || 0
  return total > 0 ? ((a.value.activeLicenses || 0) / total) * 100 : 0
})
const licFree = computed(() => Math.max(0, (a.value.totalLicenses || 0) - (a.value.activeLicenses || 0)))
const licTone = computed(() => (licPct.value >= 98 ? 'g-crit' : licPct.value >= 90 ? 'g-warn' : 'g-ok'))

const newest = computed(() =>
  [...open.value].sort((x, y) => String(y.createdAt).localeCompare(String(x.createdAt)))
    .slice(0, isFullscreen.value ? 14 : 8))

// --- queue ---------------------------------------------------------------
const QUEUE_FILTERS = [
  { id: 'all', label: 'All' }, { id: 'action', label: 'Needs action' },
  { id: 'malicious', label: 'Malicious' }, { id: 'undefined', label: 'No verdict' },
]
const queueFilter = ref('all')
const QUEUE_COLS = [
  { id: 'name', label: 'File' }, { id: 'endpoint', label: 'Endpoint' },
  { id: 'classification', label: 'Class' }, { id: 'verdict', label: 'Verdict' },
  { id: 'confidence', label: 'Confidence' }, { id: 'mitigation', label: 'Mitigation' },
  { id: 'createdAt', label: 'Age', right: true },
]
const sortBy = ref('createdAt')
const sortDir = ref('desc')
const sortQueue = (id) => {
  if (sortBy.value === id) sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  else { sortBy.value = id; sortDir.value = id === 'createdAt' ? 'desc' : 'asc' }
}
const matchesFilter = (th, id) => {
  if (id === 'action') return !!th.needsAction
  if (id === 'malicious') return th.confidence === 'malicious'
  if (id === 'undefined') return !th.verdict || th.verdict === 'undefined'
  return true
}
const queueCount = (id) => open.value.filter(th => matchesFilter(th, id)).length
const filteredQueue = computed(() => {
  const q = search.value.trim().toLowerCase()
  return open.value.filter(th => {
    if (!matchesFilter(th, queueFilter.value)) return false
    if (!q) return true
    return [th.name, th.endpoint, th.path, th.sha1, th.classification, th.publisher]
      .some(v => (v || '').toLowerCase().includes(q))
  })
})
const sortedQueue = computed(() => {
  const dir = sortDir.value === 'desc' ? -1 : 1
  return [...filteredQueue.value].sort((x, y) => {
    const key = sortBy.value
    const xv = x[key] || '', yv = y[key] || ''
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
const age = (s) => {
  if (!s) return '—'
  const ms = serverNow.value - new Date(s).getTime()
  if (!isFinite(ms) || ms < 0) return '—'
  const m = Math.floor(ms / 60000)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h`
  return `${Math.floor(h / 24)}d`
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
onMounted(() => { document.title = "SentinelOne" })
onMounted(() => {
  fetchMetrics()
  connectLive()
  window.addEventListener('keydown', onKey)
  fallback = setInterval(() => { if (!live.value) fetchMetrics() }, 30000)
})
onUnmounted(() => {
  if (es) es.close()
  if (fallback) clearInterval(fallback)
  window.removeEventListener('keydown', onKey)
})
</script>

<style scoped>
/* ====================================================================
   SOC WALL

   Built for a monitor nobody is sitting at. Three rules:

   1. No prose. Every tile is a label and a figure or a chart. A sentence
      on a wall is read once and then never again, so it is only taking
      up space where a number could be.
   2. Colour is signal. Chrome is achromatic; a hue means a state, and a
      given state keeps the same hue in every chart on the page.
   3. Nothing animates on update. The page repaints on every SSE tick,
      and charts that re-grow each time are movement in peripheral
      vision all day long.
   ==================================================================== */
.s1-view {
  --s1-ok: #22c55e; --s1-warn: #f59e0b; --s1-crit: #ef4444;
  --s1-idle: #64748b; --s1-info: #3b82f6; --s1-accent: #8b5cf6;
  --mono: var(--j-mono, ui-monospace, "SF Mono", Menlo, monospace);
  --hair: hsl(var(--border));
}
.s1-shell { width: 100%; padding: 0.8rem 0.9rem 1.1rem; display: flex; flex-direction: column; gap: 0.6rem; }
.s1-view.wall .s1-shell { padding: 0.6rem 0.7rem; gap: 0.5rem; }

/* ------------------------------------------------------------------ rail */
.rail { display: flex; align-items: center; gap: 1rem; flex-wrap: wrap; }
.rail-id { display: flex; align-items: center; gap: 0.6rem; min-width: 0; }
.rail-back { display: grid; place-items: center; width: 2.25rem; height: 2.25rem; border-radius: 8px; color: hsl(var(--muted-foreground)); }
.rail-back:hover { background: hsl(var(--muted) / 0.6); color: hsl(var(--foreground)); }
/* A background-image span, not an <img>: the header stylesheet repaints
   <img> for the wordmark and would turn this into a white block. */
.rail-mark { width: 1.9rem; height: 1.9rem; flex: none; background-size: contain; background-repeat: no-repeat; background-position: center; }
.rail-name { display: flex; flex-direction: column; min-width: 0; }
.rail-title { font-weight: 700; font-size: 1.05rem; line-height: 1.1; }
.rail-sub { font-family: var(--mono); font-size: 0.7rem; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rail-state { display: flex; align-items: center; gap: 0.6rem; margin-left: auto; }
.feed, .clock { font-family: var(--mono); font-variant-numeric: tabular-nums; white-space: nowrap; }
.feed { font-size: 0.72rem; color: hsl(var(--muted-foreground)); }
.clock { font-size: 0.95rem; font-weight: 700; }
.s1-view.wall .clock { font-size: 1.3rem; }
/* The one piece of motion kept: proof the stream is alive. A still dot
   would be indistinguishable from a frozen page. */
.livedot { width: 0.5rem; height: 0.5rem; border-radius: 50%; background: var(--s1-idle); flex: none; }
.livedot.on { background: var(--s1-ok); animation: pulse 2.4s ease-in-out infinite; }
@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.35; } }

/* ----------------------------------------------------------- beacon band */
.band {
  display: flex; align-items: stretch; gap: 0.6rem; flex-wrap: wrap;
  border: 1px solid var(--hair); border-radius: 12px; overflow: hidden;
  background: hsl(var(--card) / 0.55);
}
/* A solid block of colour, not a tint. This is the one thing on the page that
   has to register from across a room before anything is read. */
.bc {
  display: flex; align-items: center; gap: 0.55rem; padding: 0.7rem 1.1rem;
  background: var(--bcc, var(--s1-idle)); color: #fff; flex: none;
}
.st-ok { --bcc: var(--s1-ok); }
.st-warn { --bcc: var(--s1-warn); }
.st-crit { --bcc: var(--s1-crit); }
.st-unknown { --bcc: var(--s1-idle); }
.bc-i { width: 1.6rem; height: 1.6rem; flex: none; }
.bc-w {
  font-family: var(--mono); font-size: 0.9rem; font-weight: 800; letter-spacing: 0.1em;
  text-transform: uppercase; white-space: nowrap;
}
.s1-view.wall .bc-i { width: 2.2rem; height: 2.2rem; }
.s1-view.wall .bc-w { font-size: 1.25rem; }
.kpis { flex: 1; display: grid; grid-template-columns: repeat(auto-fit, minmax(5.5rem, 1fr)); }
/* Each tile is tinted and top-edged in its own hue. The colour is doing the
   work a label would otherwise have to: you learn the positions once and then
   read the wall by shape and colour rather than by reading words. */
.kpi {
  position: relative; display: flex; flex-direction: column; justify-content: center;
  padding: 0.6rem 0.7rem 0.5rem;
  background: color-mix(in srgb, var(--kc, transparent) 11%, transparent);
  border-left: 1px solid hsl(var(--border) / 0.5);
}
.kpi::before {
  content: ''; position: absolute; inset: 0 0 auto 0; height: 3px;
  background: var(--kc, var(--s1-idle));
}
.kpi:first-child { border-left: 0; }
/* At zero there is nothing to look at, so the tile recedes rather than
   shouting a colour for a figure that needs no attention. */
.kpi.zero { background: transparent; }
.kpi.zero::before { opacity: 0.3; }
.kpi.zero .kpi-n { color: hsl(var(--muted-foreground)); opacity: 0.55; }
.kpi-n {
  font-family: var(--mono); font-variant-numeric: tabular-nums;
  font-size: clamp(1.4rem, 2.4vw, 2rem); font-weight: 800; line-height: 1;
  letter-spacing: -0.045em; color: var(--kc, hsl(var(--foreground)));
}
.s1-view.wall .kpi-n { font-size: clamp(2rem, 3.4vw, 3.1rem); }
.kpi-l {
  font-family: var(--mono); font-size: 9px; font-weight: 700; letter-spacing: 0.14em;
  text-transform: uppercase; color: hsl(var(--muted-foreground)); margin-top: 0.35rem;
  white-space: nowrap;
}
.s1-view.wall .kpi-l { font-size: 11px; }

/* ------------------------------------------------------------------ tabs */
.tabbar { display: flex; gap: 0.25rem; border-bottom: 1px solid var(--hair); }
.tab {
  display: inline-flex; align-items: center; gap: 0.4rem; padding: 0.45rem 0.8rem;
  font-size: 0.85rem; font-weight: 600; color: hsl(var(--muted-foreground));
  border-bottom: 2px solid transparent; margin-bottom: -1px;
}
.tab:hover { color: hsl(var(--foreground)); }
.tab.on { color: hsl(var(--foreground)); border-bottom-color: var(--s1-accent); }
.tab-n { font-family: var(--mono); font-size: 0.68rem; font-variant-numeric: tabular-nums; background: hsl(var(--muted) / 0.8); border-radius: 999px; padding: 0.05rem 0.35rem; }

/* ------------------------------------------------------------------ grid */
.grid { display: grid; grid-template-columns: repeat(12, minmax(0, 1fr)); gap: 0.6rem; align-content: start; }
.box {
  display: flex; flex-direction: column; min-width: 0; grid-column: span 12;
  border: 1px solid var(--hair); border-radius: 10px; padding: 0.55rem 0.65rem 0.5rem;
  background: hsl(var(--card) / 0.55);
}
.sp4, .sp6, .sp8, .sp12 { grid-column: span 12; }
@media (min-width: 820px) { .sp4 { grid-column: span 6; } .sp6 { grid-column: span 6; } .sp8 { grid-column: span 12; } }
@media (min-width: 1200px) {
  .sp4 { grid-column: span 4; } .sp6 { grid-column: span 6; }
  .sp8 { grid-column: span 8; } .sp12 { grid-column: span 12; }
}
.box.alarm { border-color: color-mix(in srgb, var(--s1-crit) 45%, transparent); background: color-mix(in srgb, var(--s1-crit) 7%, transparent); }
.bh { display: flex; align-items: baseline; justify-content: space-between; gap: 0.5rem; padding-bottom: 0.35rem; margin-bottom: 0.4rem; border-bottom: 1px solid hsl(var(--border) / 0.7); }
.bt {
  display: flex; align-items: center; gap: 0.3rem; margin: 0;
  font-family: var(--mono); font-size: 10px; font-weight: 800; letter-spacing: 0.14em;
  text-transform: uppercase; color: hsl(var(--foreground)); white-space: nowrap;
}
/* A hairline of the tile's own colour, so a panel is identifiable before its
   title is read. */
.box { position: relative; }
.box::before {
  content: ''; position: absolute; inset: 0 0 auto 0; height: 2px;
  border-radius: 10px 10px 0 0; background: var(--tc, transparent);
}
.box.tc-violet { --tc: #8b5cf6; }
.box.tc-info { --tc: #3b82f6; }
.box.tc-cyan { --tc: #06b6d4; }
.box.tc-teal { --tc: #14b8a6; }
.box.tc-amber { --tc: #d97706; }
.box.tc-crit { --tc: #ef4444; }
.s1-view.wall .bt { font-size: 12px; }
.bsub { font-size: 0.66rem; color: hsl(var(--muted-foreground)); text-align: right; white-space: nowrap; }
.warnText { color: var(--s1-warn); }
.critText { color: var(--s1-crit); }

/* ---------------------------------------------------------------- charts */
.chart { position: relative; }
.h-trend { height: 10rem; }
.h-bars { height: 11rem; }
.s1-view.wall .h-trend { height: 15rem; }
.s1-view.wall .h-bars { height: 15rem; }

/* -------------------------------------------------------------- licences */
.licbig { display: flex; align-items: baseline; gap: 0.3rem; margin: 0.2rem 0 0.5rem; }
.lic-n { font-family: var(--mono); font-size: clamp(1.8rem, 3vw, 2.6rem); font-weight: 800; line-height: 1; letter-spacing: -0.045em; }
.s1-view.wall .lic-n { font-size: clamp(2.4rem, 4vw, 3.4rem); }
.lic-d { font-family: var(--mono); font-size: 1rem; color: hsl(var(--muted-foreground)); }
.g-ok { color: var(--s1-ok); } .g-warn { color: var(--s1-warn); } .g-crit { color: var(--s1-crit); }
.gauge { position: relative; height: 0.85rem; border-radius: 5px; overflow: hidden; background: hsl(var(--muted) / 0.6); }
.gfill { display: block; height: 100%; }
.gfill.g-ok { background: var(--s1-ok); } .gfill.g-warn { background: var(--s1-warn); } .gfill.g-crit { background: var(--s1-crit); }
/* A hairline where "getting close" is, so the bar says it rather than
   leaving it to be judged by eye. */
.gmark { position: absolute; top: 0; bottom: 0; width: 2px; background: hsl(var(--foreground) / 0.55); }
.licfoot { display: flex; justify-content: space-between; margin-top: 0.4rem; font-family: var(--mono); font-size: 0.68rem; color: hsl(var(--muted-foreground)); }

/* ---------------------------------------------------------------- chips */
.chips { list-style: none; display: flex; flex-wrap: wrap; gap: 0.35rem; margin: 0; padding: 0; }
.chip { font-family: var(--mono); font-size: 0.72rem; border-radius: 6px; padding: 0.18rem 0.45rem; border: 1px solid var(--hair); }
.chip.bad { color: var(--s1-crit); border-color: color-mix(in srgb, var(--s1-crit) 40%, transparent); background: color-mix(in srgb, var(--s1-crit) 10%, transparent); }

/* --------------------------------------------------------------- tables */
.t { width: 100%; border-collapse: collapse; font-family: var(--mono); font-size: 0.74rem; font-variant-numeric: tabular-nums; }
.s1-view.wall .t { font-size: 0.95rem; }
.t th { font-size: 9px; font-weight: 700; letter-spacing: 0.1em; text-transform: uppercase; color: hsl(var(--muted-foreground)); text-align: left; padding: 0 0.3rem 0.3rem; white-space: nowrap; }
.t th.right, .t td.right { text-align: right; }
.t td { padding: 0 0.3rem; height: 1.7rem; white-space: nowrap; }
.s1-view.wall .t td { height: 2.2rem; }
.t tbody tr { border-top: 1px solid hsl(var(--border) / 0.35); }
.t tbody tr:hover { background: hsl(var(--muted) / 0.45); }
.c-who { max-width: 0; width: 60%; overflow: hidden; }
.who { display: inline-block; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; vertical-align: bottom; }
.c-n { text-align: right; width: 5rem; font-weight: 700; }
.t-wall .c-age { width: 3.5rem; color: hsl(var(--muted-foreground)); }
.t-wall .c-host { width: 11rem; font-weight: 700; }
.t-wall tbody tr { cursor: pointer; }
.sortable { font: inherit; letter-spacing: inherit; text-transform: inherit; color: inherit; padding: 0; border: 0; background: none; }
.sortable:hover { color: hsl(var(--foreground)); }
.sortable.on { color: hsl(var(--foreground)); text-decoration: underline; text-underline-offset: 0.2em; }

/* --------------------------------------------------------------- queue */
.queue { display: flex; flex-direction: column; gap: 0.6rem; }
.q-tools { display: flex; align-items: center; gap: 0.6rem; flex-wrap: wrap; }
.q-search { flex: 1; min-width: 14rem; font-size: 0.85rem; background: hsl(var(--background)); border: 1px solid var(--hair); border-radius: 8px; padding: 0.4rem 0.65rem; }
.q-filters { display: flex; gap: 0.2rem; }
.qf { display: inline-flex; align-items: center; gap: 0.3rem; font-size: 0.78rem; font-weight: 600; padding: 0.35rem 0.6rem; border-radius: 8px; color: hsl(var(--muted-foreground)); border: 1px solid transparent; }
.qf:hover { background: hsl(var(--muted) / 0.5); }
.qf.on { color: hsl(var(--foreground)); border-color: var(--hair); background: hsl(var(--card)); }
.qf-n { font-family: var(--mono); font-size: 0.68rem; font-variant-numeric: tabular-nums; opacity: 0.7; }
.q-open { display: inline-flex; align-items: center; gap: 0.3rem; font-size: 0.78rem; color: hsl(var(--muted-foreground)); }
.q-open:hover { color: hsl(var(--foreground)); }
.table { overflow-x: auto; border: 1px solid var(--hair); border-radius: 10px; }
.t-queue td { height: 2.1rem; }
.qrow { cursor: pointer; }
.qrow.act, .t-wall tr.act { background: color-mix(in srgb, var(--s1-crit) 8%, transparent); }
.c-file { max-width: 24rem; }
.fname { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.fpath { display: block; font-size: 0.66rem; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.c-host { font-weight: 700; }
.c-age { color: hsl(var(--muted-foreground)); }
.pill { display: inline-block; font-size: 0.68rem; font-weight: 700; border-radius: 5px; padding: 0.1rem 0.35rem; border: 1px solid hsl(var(--border)); }
.s1-view.wall .pill { font-size: 0.82rem; }
.cls-ransomware { color: var(--s1-crit); border-color: color-mix(in srgb, var(--s1-crit) 45%, transparent); background: color-mix(in srgb, var(--s1-crit) 12%, transparent); }
.cls-malware { color: var(--s1-warn); border-color: color-mix(in srgb, var(--s1-warn) 45%, transparent); background: color-mix(in srgb, var(--s1-warn) 12%, transparent); }
.vd-true-positive { color: var(--s1-crit); }
.vd-false-positive { color: hsl(var(--muted-foreground)); }
.vd-suspicious { color: var(--s1-warn); }
.mt-ok { color: var(--s1-ok); border-color: color-mix(in srgb, var(--s1-ok) 40%, transparent); }
.mt-bad { color: var(--s1-crit); border-color: color-mix(in srgb, var(--s1-crit) 45%, transparent); background: color-mix(in srgb, var(--s1-crit) 12%, transparent); }
.dot { display: inline-block; width: 0.45rem; height: 0.45rem; border-radius: 50%; margin-right: 0.35rem; background: var(--s1-idle); }
.cf-malicious { background: var(--s1-crit); }
.cf-suspicious { background: var(--s1-warn); }

/* --------------------------------------------------------------- panel */
.panel { position: fixed; inset: 0; z-index: 60; display: flex; justify-content: flex-end; background: hsl(0 0% 0% / 0.45); }
.panel-card { width: min(34rem, 100%); height: 100%; overflow-y: auto; background: hsl(var(--background)); border-left: 1px solid var(--hair); padding: 1rem 1.1rem 2rem; display: flex; flex-direction: column; gap: 0.9rem; }
.p-head { display: flex; align-items: flex-start; gap: 0.6rem; }
.p-id { display: flex; flex-direction: column; gap: 0.35rem; min-width: 0; }
.p-title { margin: 0; font-size: 1rem; font-weight: 700; word-break: break-all; }
.p-facts { display: grid; grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr)); gap: 0.5rem 0.9rem; margin: 0; }
.p-facts > div { min-width: 0; }
.p-facts dt { font-family: var(--mono); font-size: 9px; font-weight: 700; letter-spacing: 0.12em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
.p-facts dd { margin: 0.1rem 0 0; font-size: 0.82rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.p-alarm { display: flex; align-items: center; gap: 0.5rem; font-size: 0.8rem; color: var(--s1-crit); border: 1px solid color-mix(in srgb, var(--s1-crit) 40%, transparent); background: color-mix(in srgb, var(--s1-crit) 9%, transparent); border-radius: 8px; padding: 0.5rem 0.65rem; }
.p-block { display: flex; flex-direction: column; gap: 0.3rem; }
.p-h4 { margin: 0; font-family: var(--mono); font-size: 9px; font-weight: 800; letter-spacing: 0.14em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
.p-code { font-family: var(--mono); font-size: 0.72rem; word-break: break-all; background: hsl(var(--muted) / 0.5); border-radius: 6px; padding: 0.4rem 0.5rem; }
.tl { list-style: none; margin: 0; padding: 0; display: grid; gap: 0.5rem; }
.tl li { display: grid; gap: 0.1rem; border-left: 2px solid var(--s1-accent); padding-left: 0.6rem; }
.tl-at { font-family: var(--mono); font-size: 0.66rem; color: hsl(var(--muted-foreground)); font-variant-numeric: tabular-nums; }
.tl-p { font-size: 0.8rem; }
.tl-s { font-size: 0.72rem; color: hsl(var(--muted-foreground)); word-break: break-all; }
.p-link { display: inline-flex; align-items: center; gap: 0.35rem; margin-top: auto; font-size: 0.8rem; color: hsl(var(--muted-foreground)); }
.p-link:hover { color: hsl(var(--foreground)); }

/* ------------------------------------------------------------- notices */
.notice { border: 1px dashed var(--hair); border-radius: 10px; padding: 1rem 1.1rem; background: hsl(var(--card) / 0.4); }
.notice-error { border-style: solid; border-color: color-mix(in srgb, var(--s1-crit) 40%, transparent); background: color-mix(in srgb, var(--s1-crit) 6%, transparent); }
.notice-title { display: flex; align-items: center; gap: 0.4rem; font-weight: 700; font-size: 0.9rem; }
.notice-body { font-size: 0.82rem; color: hsl(var(--muted-foreground)); margin-top: 0.35rem; }
.notice code { font-family: var(--mono); background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em; }
.gnote { margin: 0; font-size: 0.74rem; color: hsl(var(--muted-foreground)); }
.err-pre { font-family: var(--mono); font-size: 12px; white-space: pre-wrap; word-break: break-word; color: var(--s1-crit); background: color-mix(in srgb, var(--s1-crit) 8%, transparent); border-radius: 6px; padding: 0.6rem 0.75rem; margin: 0; }

@media (prefers-reduced-motion: reduce) {
  .animate-spin, .livedot.on { animation: none; }
}
</style>
