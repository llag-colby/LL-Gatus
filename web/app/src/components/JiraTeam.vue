<template>
  <section class="team" :class="{ wall: isFullscreen }">

    <!-- A loader only when there is genuinely nothing to draw. The server now
         keeps this breakdown warm, so in practice the first paint has data and
         this never appears; when it does it is a one-line strip rather than a
         page-filling panel. -->
    <div v-if="!loaded && !board" class="thinload">
      <RefreshCw class="h-3.5 w-3.5 animate-spin" /> Counting tickets…
    </div>
    <div v-else-if="loaded && !data.ok && !board" class="notice notice-error">
      <div class="notice-title"><AlertTriangle class="h-4 w-4" /> Cannot read the breakdown</div>
      <pre class="err-pre">{{ data.error || 'Jira returned no usable windows.' }}</pre>
    </div>
    <template v-if="board">
      <!-- Thin reference strip. Small on purpose: the people table is the
           focal point and a row of big boxes would compete with it. -->
      <div class="strip">
        <div v-for="k in kpis" :key="k.l" class="kv" :class="k.tone" :style="{ '--kc': k.c }"
          :data-tooltip="k.tip" data-tip-pos="bottom">
          <span class="kv-n">{{ k.v }}<i v-if="k.unit">{{ k.unit }}</i></span>
          <span class="kv-l">{{ k.l }}</span>
        </div>
      </div>

      <div class="canvas">
        <!-- PEOPLE. The dominant panel, and one table rather than the two
             that used to stack: counts and timing for the same person belong
             on the same row. -->
        <section class="px people">
          <header class="ph">
            <h3 class="pt"><Users class="h-3.5 w-3.5" /> People</h3>
            <span class="ps">
              {{ roster.length }} with activity · {{ effort ? `${effort.windowDays}d timing` : 'counts only' }}
            </span>
          </header>
          <div class="plist">
            <table class="ptab">
              <thead>
                <tr>
                  <th class="c-who">
                    <button class="sortable" :class="{ on: sortBy === 'name' }" @click="sort('name')">Assignee</button>
                  </th>
                  <th v-for="c in COLS" :key="c.id" class="c-n">
                    <button class="sortable" :class="{ on: sortBy === c.id }" @click="sort(c.id)"
                      :data-tooltip="c.tip" data-tip-pos="bottom">{{ c.label }}</button>
                  </th>
                  <th class="c-bar">Share of closed</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in roster" :key="r.id">
                  <td class="c-who">
                    <span class="dot" :style="{ background: colourFor(r.id) }"></span>
                    <span class="who" :title="r.name">{{ r.name }}</span>
                  </td>
                  <td v-for="c in COLS" :key="c.id" class="c-n" :class="cellTone(c, r)">
                    <span v-if="c.dur">{{ dur(r[c.id]) }}</span>
                    <span v-else-if="c.pct">{{ pctOf(r, c) }}</span>
                    <span v-else class="n" :class="{ zero: !r[c.id] }">{{ r[c.id] ? fmt(r[c.id]) : '·' }}</span>
                  </td>
                  <td class="c-bar">
                    <div class="barwrap">
                      <div class="mbar">
                        <span class="mfill"
                          :style="{ width: pct(r.resolvedMonth, totals.resolvedMonth) + '%', background: colourFor(r.id) }"></span>
                      </div>
                      <span class="mpct">{{ pctLabel(r.resolvedMonth, totals.resolvedMonth) }}</span>
                    </div>
                  </td>
                </tr>
              </tbody>
              <tfoot>
                <tr>
                  <td class="c-who">Total</td>
                  <td v-for="c in COLS" :key="c.id" class="c-n">
                    <span v-if="c.dur">{{ dur(totals[c.id]) }}</span>
                    <span v-else-if="c.pct">{{ totalPct(c) }}</span>
                    <span v-else class="n">{{ fmt(totals[c.id]) }}</span>
                  </td>
                  <td class="c-bar"><span class="mpct">100%</span></td>
                </tr>
              </tfoot>
            </table>
          </div>
        </section>

        <!-- CHART BAND -->
        <div class="band">
        <section class="px">
          <header class="ph">
            <h3 class="pt">Closed this month</h3>
            <span class="ps">by person</span>
          </header>
          <div class="chart"><Bar :data="throughputData" :options="hBarOpts" /></div>
        </section>

        <section class="px">
          <header class="ph">
            <h3 class="pt">How long tickets take</h3>
            <span class="ps" :data-tooltip="deskTip" data-tip-pos="left">on-desk time ⓘ</span>
          </header>
          <div class="chart"><Bar :data="bucketData" :options="vBarOpts" /></div>
        </section>

        <section class="px">
          <header class="ph">
            <h3 class="pt">When tickets close</h3>
            <span class="ps">local time · peak {{ heatPeak }}/h</span>
          </header>
          <div class="heatwrap">
            <div class="heat">
              <template v-for="(row, d) in heatRows" :key="d">
                <span class="hday">{{ DAYS[d] }}</span>
                <span v-for="(n, h) in row" :key="h" class="hcell"
                  :class="{ none: !n }"
                  :style="n ? { opacity: 0.22 + 0.78 * (n / heatPeak) } : {}"
                  :data-tooltip="`${DAYS[d]} ${String(h).padStart(2, '0')}:00 — ${n} closed`"></span>
              </template>
              <span class="hday"></span>
              <span v-for="h in 24" :key="'h' + h" class="hh">{{ (h - 1) % 6 === 0 ? (h - 1) : '' }}</span>
            </div>
          </div>
        </section>
        </div>
      </div>
    </template>
  </section>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { AlertTriangle, RefreshCw, Users } from 'lucide-vue-next'
import { Bar } from 'vue-chartjs'
import {
  Chart as ChartJS, BarElement, BarController, CategoryScale, LinearScale, Tooltip, Legend,
} from 'chart.js'
import { isFullscreen } from '@/store'

ChartJS.register(BarElement, BarController, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps({
  projectKey: { type: String, default: '' },
  baseUrl: { type: String, default: '' },
})

// Columns of the people table. `dur` renders a duration, `pct` a ratio of two
// of the row's own fields; everything else is a plain count.
const COLS = [
  { id: 'open', label: 'Open', tip: 'Not in a Done status category' },
  { id: 'createdToday', label: 'In', tip: 'Submitted today' },
  { id: 'resolvedToday', label: 'Done', tip: 'Completed today' },
  { id: 'resolvedMonth', label: 'MTD', tip: 'Completed this month to date' },
  { id: 'resolvedLastMonth', label: 'Prev', tip: 'Completed last month, in full' },
  { id: 'workMedianMs', label: 'Median', dur: true, tip: 'Median business-hours time the ticket sat with the desk' },
  { id: 'frtMedianMs', label: '1st reply', dur: true, tip: 'Median time to first response' },
  { id: 'firstTouch', label: '1-touch', pct: true, over: 'measured', tip: 'Resolved at the first reply' },
  { id: 'slaMetRes', label: 'SLA', pct: true, over: 'slaTotal', tip: 'Resolution SLAs met' },
]

const CAT = ['--j-cat-1', '--j-cat-2', '--j-cat-3', '--j-cat-4', '--j-cat-5', '--j-cat-6', '--j-cat-7', '--j-cat-8']

const data = ref({ configured: false, ok: false, projects: [] })
const loaded = ref(false)

const load = async (force = false) => {
  try {
    const r = await fetch(`/api/v1/jira/breakdown${force ? '?refresh=1' : ''}`, { cache: 'no-store' })
    if (r.ok) data.value = await r.json()
  } catch (e) {
    // keep the last good payload on screen
  } finally {
    loaded.value = true
  }
}
// A chain of timeouts, not setInterval: the next delay is chosen from what came
// back, and a slow response can never stack a second poll on the first.
const FAST_MS = 4000
const IDLE_MS = 60000
let timer = null
const pump = async () => {
  await load()
  timer = setTimeout(pump, data.value.computing ? FAST_MS : IDLE_MS)
}
onMounted(pump)
onUnmounted(() => clearTimeout(timer))

const board = computed(() => {
  const list = data.value.projects || []
  return list.find(p => p.key === props.projectKey) || list[0] || null
})
const windows = computed(() => board.value?.windows || [])
const winById = (id) => windows.value.find(w => w.id === id) || null
const total = (id) => winById(id)?.total || 0
const effort = computed(() => board.value?.effort || null)

// --- the roster, counts and timing merged -------------------------------
// One row per person who appears in ANY window or in the timing pass, so
// somebody who closed work but has nothing open still shows up.
const roster = computed(() => {
  const people = new Map()
  const touch = (id, name, accountId) => {
    let p = people.get(id)
    if (!p) {
      p = {
        id, name, accountId,
        open: 0, createdToday: 0, resolvedToday: 0, resolvedMonth: 0, resolvedLastMonth: 0,
        workMedianMs: 0, frtMedianMs: 0, firstTouch: 0, measured: 0,
        slaMetRes: 0, slaBreachRes: 0, slaTotal: 0,
      }
      people.set(id, p)
    }
    return p
  }
  for (const w of windows.value) {
    for (const r of w.rows || []) {
      const id = r.accountId || `name:${r.name}`
      touch(id, r.name, r.accountId)[w.id] = r.count
    }
  }
  for (const r of effort.value?.rows || []) {
    const id = r.accountId || `name:${r.name}`
    const p = touch(id, r.name, r.accountId)
    p.workMedianMs = r.workMedianMs
    p.frtMedianMs = r.frtMedianMs
    p.firstTouch = r.firstTouch
    p.measured = r.measured
    p.slaMetRes = r.slaMetRes
    p.slaBreachRes = r.slaBreachRes
    p.slaTotal = r.slaMetRes + r.slaBreachRes
  }
  const rows = [...people.values()]
  const dir = sortDir.value === 'desc' ? -1 : 1
  rows.sort((x, y) => {
    if (sortBy.value === 'name') return x.name.localeCompare(y.name) * dir
    const d = (x[sortBy.value] || 0) - (y[sortBy.value] || 0)
    // Name as the tie-break so the order does not shuffle between polls.
    return d !== 0 ? d * dir : x.name.localeCompare(y.name)
  })
  return rows
})

const totals = computed(() => {
  const e = effort.value
  const sum = (k) => roster.value.reduce((s, r) => s + (r[k] || 0), 0)
  return {
    open: total('open'),
    createdToday: total('createdToday'),
    resolvedToday: total('resolvedToday'),
    resolvedMonth: total('resolvedMonth'),
    resolvedLastMonth: total('resolvedLastMonth'),
    workMedianMs: e?.workMedianMs || 0,
    frtMedianMs: e?.frtMedianMs || 0,
    firstTouch: e?.firstTouch || 0,
    measured: e?.measured || 0,
    slaMetRes: sum('slaMetRes'),
    slaTotal: sum('slaTotal'),
  }
})

const sortBy = ref('resolvedMonth')
const sortDir = ref('desc')
const sort = (id) => {
  if (sortBy.value === id) sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  else { sortBy.value = id; sortDir.value = id === 'name' ? 'asc' : 'desc' }
}

// --- strip ---------------------------------------------------------------
const net = computed(() => totals.value.createdToday - totals.value.resolvedToday)
const kpis = computed(() => {
  const e = effort.value
  const T = totals.value
  const ft = T.measured > 0 ? Math.round((T.firstTouch / T.measured) * 100) : 0
  return [
    { l: 'Open', v: fmt(T.open), c: v('--j-cat-1'), tip: 'Not in a Done status category' },
    { l: 'In today', v: fmt(T.createdToday), c: v('--j-info'), tip: 'Submitted today' },
    { l: 'Done today', v: fmt(T.resolvedToday), c: v('--j-ok'), tip: 'Completed today' },
    {
      l: 'Net today', v: net.value > 0 ? `+${net.value}` : String(net.value),
      c: net.value > 0 ? v('--j-crit') : v('--j-ok'),
      tone: net.value === 0 ? 'calm' : '',
      tip: 'Submitted minus completed. Positive means the queue grew.',
    },
    { l: 'Done MTD', v: fmt(T.resolvedMonth), c: v('--j-cat-2'), tip: 'Completed this month to date' },
    { l: 'Prev month', v: fmt(T.resolvedLastMonth), c: v('--j-idle'), tone: 'calm', tip: 'Completed last month, in full' },
    {
      l: 'Median', v: dur(T.workMedianMs), c: v('--j-cat-4'),
      tip: 'Median business-hours time a ticket sat with the desk',
    },
    { l: '1-touch', v: `${ft}%`, c: v('--j-ok'), tip: 'Resolved at the first reply' },
    {
      l: 'Breaches', v: fmt(e?.breaches || 0),
      c: v('--j-crit'), tone: (e?.breaches || 0) > 0 ? '' : 'calm',
      tip: 'Resolution SLAs missed in the window',
    },
  ]
})

// --- charts --------------------------------------------------------------
// Chart.js needs real colour strings, and the theme's --j-* tokens are
// color-mix() expressions that getComputedStyle returns unresolved. These are
// read off a probe element, which resolves them.
const v = (token) => {
  try {
    const el = document.createElement('span')
    el.style.color = `var(${token})`
    document.body.appendChild(el)
    const c = getComputedStyle(el).color
    el.remove()
    return c || '#888'
  } catch (e) { return '#888' }
}
const dark = () => document.documentElement.classList.contains('dark')
const ink = () => (dark() ? 'rgba(226,232,240,0.88)' : 'rgba(51,65,85,0.80)')
const gridc = () => (dark() ? 'rgba(148,163,184,0.16)' : 'rgba(100,116,139,0.16)')
const fsz = (small, big) => (isFullscreen.value ? big : small)

const valueLabels = {
  id: 'jteamLabels',
  afterDatasetsDraw(chart, _a, opts) {
    const { ctx } = chart
    const size = opts.size || 10
    const horizontal = chart.options.indexAxis === 'y'
    const meta = chart.getDatasetMeta(0)
    if (!meta || meta.hidden) return
    const ds = chart.data.datasets[0]
    meta.data.forEach((el, i) => {
      const val = ds.data[i]
      if (!val) return
      const text = val.toLocaleString()
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
  },
}
ChartJS.register(valueLabels)

const throughputRows = computed(() =>
  [...roster.value]
    .filter(r => r.resolvedMonth > 0)
    .sort((x, y) => y.resolvedMonth - x.resolvedMonth)
    .slice(0, isFullscreen.value ? 10 : 8))
const throughputData = computed(() => ({
  labels: throughputRows.value.map(r => r.name),
  datasets: [{
    data: throughputRows.value.map(r => r.resolvedMonth),
    backgroundColor: throughputRows.value.map(r => colourFor(r.id)),
    borderRadius: 2,
  }],
}))
const hBarOpts = computed(() => ({
  indexAxis: 'y', responsive: true, maintainAspectRatio: false, animation: { duration: 0 },
  layout: { padding: { right: fsz(30, 40) } },
  plugins: {
    legend: { display: false },
    tooltip: { callbacks: { label: (c) => ` ${c.parsed.x.toLocaleString()} closed` } },
    jteamLabels: { color: ink(), size: fsz(9, 12) },
  },
  scales: {
    x: { grid: { color: gridc() }, ticks: { color: ink(), font: { size: fsz(9, 11) }, precision: 0, maxTicksLimit: 4 } },
    y: { grid: { display: false }, ticks: { color: ink(), font: { size: fsz(10, 12), weight: '700' }, autoSkip: false } },
  },
}))

const bucketData = computed(() => {
  const b = effort.value?.buckets || []
  return {
    labels: b.map(x => x.name),
    datasets: [{
      data: b.map(x => x.count),
      // Left to right is fast to slow, so the colour ramp carries the meaning.
      backgroundColor: [v('--j-ok'), v('--j-ok'), v('--j-cat-2'), v('--j-warn'), v('--j-warn'), v('--j-crit'), v('--j-crit')],
      borderRadius: 2,
    }],
  }
})
const vBarOpts = computed(() => ({
  responsive: true, maintainAspectRatio: false, animation: { duration: 0 },
  layout: { padding: { top: fsz(14, 20) } },
  plugins: {
    legend: { display: false },
    tooltip: { callbacks: { label: (c) => ` ${c.parsed.y.toLocaleString()} tickets` } },
    jteamLabels: { color: ink(), size: fsz(10, 13) },
  },
  scales: {
    x: { grid: { display: false }, ticks: { color: ink(), font: { size: fsz(9, 12), weight: '700' }, maxRotation: 0 } },
    y: { beginAtZero: true, grace: '16%', grid: { color: gridc() }, ticks: { color: ink(), font: { size: fsz(9, 11) }, precision: 0, maxTicksLimit: 4 } },
  },
}))

// --- heatmap -------------------------------------------------------------
const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const heatRows = computed(() => {
  const g = Array.from({ length: 7 }, () => Array(24).fill(0))
  for (const c of effort.value?.heatmap || []) {
    if (c.day >= 0 && c.day < 7 && c.hour >= 0 && c.hour < 24) g[c.day][c.hour] = c.count
  }
  return g
})
const heatPeak = computed(() =>
  (effort.value?.heatmap || []).reduce((m, c) => Math.max(m, c.count), 0) || 1)

// --- formatting ----------------------------------------------------------
const fmt = (n) => (n || 0).toLocaleString()
const pct = (n, of) => (of > 0 ? (n / of) * 100 : 0)
const pctLabel = (n, of) => {
  if (!of) return '—'
  const p = (n / of) * 100
  return p >= 10 || p === 0 ? `${Math.round(p)}%` : `${p.toFixed(1)}%`
}
// Seconds below a minute, whole minutes below an hour, one decimal of an hour
// below a day: "0.31h" tells a human nothing, "18m" does.
const dur = (ms) => {
  if (!ms || ms <= 0) return '—'
  const secs = ms / 1000
  if (secs < 60) return `${Math.round(secs)}s`
  const mins = secs / 60
  if (mins < 60) return `${Math.round(mins)}m`
  const hrs = mins / 60
  if (hrs < 24) return `${hrs.toFixed(hrs < 10 ? 1 : 0)}h`
  return `${(hrs / 24).toFixed(1)}d`
}
const pctOf = (r, c) => {
  const over = r[c.over] || 0
  return over > 0 ? `${Math.round((r[c.id] / over) * 100)}%` : '—'
}
const totalPct = (c) => {
  const T = totals.value
  const over = T[c.over] || 0
  return over > 0 ? `${Math.round((T[c.id] / over) * 100)}%` : '—'
}
// Only the SLA column earns a colour: a miss is the one cell worth spotting.
const cellTone = (c, r) => {
  if (c.id !== 'slaMetRes') return ''
  return r.slaBreachRes > 0 ? 'bad' : ''
}
// Stable colour per person, hashed from identity rather than row position, so
// sorting the table does not recolour everyone.
const colourFor = (id) => {
  let h = 0
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) | 0
  return v(CAT[Math.abs(h) % CAT.length])
}

const deskTip = `Nobody logs labour in this Jira — timespent is zero on every ticket in the instance. `
  + `On-desk time is the Time-to-resolution SLA clock: business hours only, paused while waiting on `
  + `the customer. It measures how long a ticket was the desk's problem, not how long anyone worked on it.`
</script>

<style scoped>
/* ====================================================================
   TEAM BOARD

   Same shape as the SOC console, for the same reason: one dominant panel
   carrying the work, charts banked beside it, a thin strip of reference
   figures on top. The previous version stacked seven panels down the page
   and scrolled forever.

   Everything is height-bounded. The people table scrolls inside its own
   frame rather than growing the page.
   ==================================================================== */
.team {
  display: flex; flex-direction: column; gap: 0.5rem; min-width: 0;
  --hair: hsl(var(--border));
  --mono: var(--j-mono, ui-monospace, Menlo, monospace);
  --tint: 12%;
}
:global(.dark) .team { --tint: 16%; }

/* ------------------------------------------------------------------ strip */
.strip { display: grid; gap: 0.3rem; grid-template-columns: repeat(auto-fit, minmax(5rem, 1fr)); flex: none; }
.kv {
  display: flex; align-items: baseline; gap: 0.35rem; min-width: 0;
  padding: 0.3rem 0.5rem; border-radius: 7px;
  border: 1px solid color-mix(in srgb, var(--kc) 30%, var(--hair));
  background: color-mix(in srgb, var(--kc) var(--tint), hsl(var(--card)));
}
.kv.calm { background: hsl(var(--card)); border-color: var(--hair); }
.kv-n {
  font-family: var(--mono); font-variant-numeric: tabular-nums;
  font-size: 1.05rem; font-weight: 800; line-height: 1.1; letter-spacing: -0.04em; color: var(--kc);
}
.team.wall .kv-n { font-size: 1.45rem; }
.kv.calm .kv-n { color: hsl(var(--foreground)); }
.kv-n i { font-style: normal; font-size: 0.6em; opacity: 0.7; }
.kv-l {
  font-family: var(--mono); font-size: 8px; font-weight: 800; letter-spacing: 0.1em;
  text-transform: uppercase; color: hsl(var(--muted-foreground));
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.team.wall .kv-l { font-size: 10px; }

/* ---------------------------------------------------------------- canvas */
/* The table is wide and short, so it gets the full width and sizes to its
   content; the three charts sit beneath it in equal thirds. The previous
   arrangement put it in a tall left column beside stacked charts, which left
   a large empty block under it - the panel shape did not match the data. */
.canvas { display: grid; gap: 0.5rem; min-width: 0; grid-template-columns: minmax(0, 1fr); }
.people { grid-column: 1 / -1; }
/* Content-sized, with a ceiling so a big roster scrolls in frame instead of
   growing the page. */
.plist { max-height: 26rem; }
.team.wall .plist { max-height: 44vh; }
.band {
  display: grid; gap: 0.5rem; min-width: 0;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  height: 15rem;
}
.team.wall .band { height: 30vh; }
@media (max-width: 1000px) { .band { grid-template-columns: 1fr; height: auto; } .band > .px { height: 13rem; } }

.px {
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
.team.wall .pt { font-size: 12px; }
.ps { font-size: 0.64rem; color: hsl(var(--muted-foreground)); white-space: nowrap; cursor: help; }
.team.wall .ps { font-size: 0.78rem; }
.chart { position: relative; flex: 1 1 auto; min-height: 0; }

/* ---------------------------------------------------------------- people */
/* Bounded and scrolled inside its own frame: this is what used to make the
   page grow one row at a time. */
.plist { flex: 1 1 auto; min-height: 0; overflow: auto; }
.ptab { width: 100%; border-collapse: collapse; font-family: var(--mono); font-size: 0.75rem; font-variant-numeric: tabular-nums; }
.team.wall .ptab { font-size: 0.92rem; }
.ptab th {
  font-size: 8px; font-weight: 800; letter-spacing: 0.1em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); text-align: right; padding: 0 0.3rem 0.25rem; white-space: nowrap;
  position: sticky; top: 0; background: hsl(var(--card)); z-index: 2;
}
.team.wall .ptab th { font-size: 10px; }
.ptab th.c-who { text-align: left; }
.ptab td { padding: 0 0.3rem; height: 1.7rem; white-space: nowrap; }
.team.wall .ptab td { height: 2.1rem; }
.ptab tbody tr { border-top: 1px solid hsl(var(--border) / 0.5); }
.ptab tbody tr:hover { background: hsl(var(--muted)); }
.ptab tfoot td { border-top: 1px solid var(--hair); font-weight: 800; }
.c-who { max-width: 0; width: 20%; overflow: hidden; }
.dot { display: inline-block; width: 0.42rem; height: 0.42rem; border-radius: 50%; margin-right: 0.35rem; vertical-align: 0.04rem; }
.who { display: inline-block; max-width: calc(100% - 0.85rem); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; vertical-align: bottom; }
.c-n { text-align: right; width: auto; min-width: 3.2rem; }
.c-n .n { font-weight: 700; }
.c-n .n.zero { color: hsl(var(--muted-foreground)); opacity: 0.45; font-weight: 400; }
.c-n.bad { color: var(--j-crit); }
.c-bar { width: 22%; min-width: 6rem; }
.barwrap { display: flex; align-items: center; gap: 0.35rem; }
.mbar { flex: 1; height: 0.38rem; min-width: 1.8rem; border-radius: 3px; background: hsl(var(--muted)); overflow: hidden; }
.mfill { display: block; height: 100%; border-radius: 3px; }
.mpct { color: hsl(var(--muted-foreground)); font-size: 0.9em; min-width: 2.4rem; text-align: right; }
.sortable { font: inherit; letter-spacing: inherit; text-transform: inherit; color: inherit; padding: 0; border: 0; background: none; }
.sortable:hover { color: hsl(var(--foreground)); }
.sortable.on { color: hsl(var(--foreground)); text-decoration: underline; text-underline-offset: 0.2em; }

/* --------------------------------------------------------------- heatmap */
.heatwrap { flex: 1 1 auto; min-height: 0; display: flex; align-items: center; }
.heat { display: grid; grid-template-columns: 2.1rem repeat(24, minmax(0, 1fr)); gap: 2px; width: 100%; align-items: center; }
.hday { font-family: var(--mono); font-size: 8px; font-weight: 800; letter-spacing: 0.06em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
.team.wall .hday { font-size: 10px; }
.hh { font-family: var(--mono); font-size: 7px; color: hsl(var(--muted-foreground)); text-align: center; font-variant-numeric: tabular-nums; }
.team.wall .hh { font-size: 9px; }
.hcell { aspect-ratio: 1; min-height: 0.5rem; border-radius: 2px; background: var(--j-info); }
.hcell.none { background: hsl(var(--muted)); opacity: 0.55; }

/* A single line, not a panel: the cache is warm so this is a flicker at
   worst, and a page-filling "loading" block for that is worse than nothing. */
.thinload {
  display: inline-flex; align-items: center; gap: 0.4rem; align-self: flex-start;
  font-family: var(--mono); font-size: 0.72rem; color: hsl(var(--muted-foreground));
  border: 1px solid var(--hair); border-radius: 999px; padding: 0.15rem 0.6rem;
}

/* ------------------------------------------------------------- notices */
.notice { border: 1px dashed var(--hair); border-radius: 10px; padding: 1rem 1.1rem; background: hsl(var(--card)); }
.notice-error { border-style: solid; border-color: color-mix(in srgb, var(--j-crit) 45%, transparent); background: color-mix(in srgb, var(--j-crit) 8%, transparent); }
.notice-title { display: flex; align-items: center; gap: 0.4rem; font-weight: 700; font-size: 0.9rem; }
.notice-body { font-size: 0.82rem; color: hsl(var(--muted-foreground)); margin-top: 0.35rem; }
.err-pre { font-family: var(--mono); font-size: 12px; white-space: pre-wrap; word-break: break-word; color: var(--j-crit); background: color-mix(in srgb, var(--j-crit) 9%, transparent); border-radius: 6px; padding: 0.6rem 0.75rem; margin: 0; }

@media (prefers-reduced-motion: reduce) { .animate-spin { animation: none; } }
</style>
