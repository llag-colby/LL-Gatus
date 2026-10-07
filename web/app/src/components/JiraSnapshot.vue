<template>
  <section class="snap" :class="{ wall: isFullscreen }">

    <!-- ============================ CONTROL BAR ============================
         Everything the sidebar used to hold, inline and horizontal. The
         sidebar cost a 14rem column of width and stacked its labels into a
         jumble; a single bar is both wider for the data and easier to read. -->
    <header class="bar">
      <div class="seg" role="tablist" aria-label="Period">
        <button v-for="(p, i) in PERIODS" :key="p.id" role="tab"
          :aria-selected="periodId === p.id" class="sgb"
          :class="{ on: periodId === p.id }" @click="setPeriod(p.id)">
          {{ p.label }}<kbd>{{ i + 1 }}</kbd>
        </button>
      </div>

      <span class="bar-date">
        {{ longDate }}
        <i v-if="periodId === 'mtd' && daily.monthDays" class="bar-day">
          day {{ daily.monthDay }} of {{ daily.monthDays }}
        </i>
      </span>

      <div class="seg sm">
        <button v-for="b in BASIS" :key="b.id" class="sgb"
          :class="{ on: basis === b.id }" @click="basis = b.id">{{ b.label }}</button>
      </div>
      <div class="seg sm">
        <button v-for="r in RANKBY" :key="r.id" class="sgb"
          :class="{ on: rankBy === r.id }" @click="setRankBy(r.id)">{{ r.label }}</button>
      </div>

      <select v-model="locationFilter" class="pick" aria-label="Location">
        <option value="">All locations</option>
        <option v-for="l in locationList" :key="l.name" :value="l.name">
          {{ l.name }} ({{ l.bucket.closed }})
        </option>
      </select>

      <ul class="legend"
        data-tooltip="Status colour is the figure as a percentage of its target" data-tip-pos="bottom">
        <li v-for="g in GRADES" :key="g.k" :class="'g-' + g.k"><i></i>{{ g.l }}</li>
      </ul>

      <span class="bar-tgt">Target: {{ targetSource }}</span>
      <span class="bar-upd">{{ projectName }} · updated {{ updatedLabel }}</span>
      <button class="bar-rf" @click="refresh" :disabled="busy"
        data-tooltip="Refresh  R" data-tip-pos="bottom">
        <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': busy }" />
      </button>
    </header>

    <!-- ============================ FOUR KPI CARDS ======================== -->
    <div class="kpis">
      <article v-for="k in kpis" :key="k.id" class="kpi" :class="'g-' + k.grade">
        <header class="k-top">
          <span class="k-l">{{ k.label }}</span>
          <span class="k-pct"><i class="dot"></i>{{ k.pctText }}</span>
        </header>
        <div class="k-main">
          <span class="k-n">{{ k.value }}</span>
          <span class="k-pace" :data-tooltip="k.paceTip" data-tip-pos="bottom">{{ k.pace }}</span>
        </div>
        <ul class="k-ctx">
          <li v-for="line in k.context" :key="line">{{ line }}</li>
        </ul>
      </article>
    </div>

    <!-- ============================ THREE RANKED PANELS ===================
         Same row order in all three, so one name can be followed across. Each
         panel states its own target in its subtitle - no unlabelled bars. -->
    <div class="ranks">
      <article v-for="panel in panels" :key="panel.id" class="rank">
        <header class="r-head">
          <h3 class="r-t">{{ panel.title }}</h3>
          <span class="r-s">{{ panel.sub }}</span>
        </header>
        <ol class="r-rows">
          <li v-for="(row, i) in panel.rows" :key="row.name" class="r-row" :class="'g-' + row.grade">
            <span class="r-rank">{{ i + 1 }}</span>
            <span class="r-name" :title="row.name">{{ row.name }}</span>
            <span class="r-ratio">{{ row.ratio }}</span>
            <span class="r-val">{{ row.value }}</span>
            <span class="r-sec">{{ row.secondary }}</span>
            <span class="r-bar">
              <i class="r-fill" :style="row.barStyle"></i>
              <i class="r-tick" :style="{ left: row.tickPct + '%' }"
                :data-tooltip="`Target ${panel.targetText}`" data-tip-pos="top"></i>
            </span>
          </li>
          <li v-if="!panel.rows.length" class="r-empty">Nothing closed in this period.</li>
        </ol>
        <footer class="r-foot">
          <span>{{ panel.footLabel }}</span>
          <span class="r-val">{{ panel.footValue }}</span>
        </footer>
      </article>
    </div>

    <!-- ============================ SUMMARY TABLE ========================= -->
    <article class="sumwrap">
      <header class="r-head">
        <h3 class="r-t">Summary by {{ rankBy === 'location' ? 'location' : 'technician' }}</h3>
        <span class="r-s">
          {{ summary.length }} rows · click a row for its tickets in Jira
          <template v-if="noLocation > 0"> · {{ fmt(noLocation) }} with no location set</template>
        </span>
      </header>
      <div class="sumscroll">
        <table class="sum">
          <thead>
            <tr>
              <th class="s-name">{{ rankBy === 'location' ? 'Location' : 'Technician' }}</th>
              <th class="hl">{{ period.label }}</th>
              <th>Month to date</th>
              <th>Pace</th>
              <th>Target</th>
              <th>Pace vs target</th>
              <th>Avg 1st response</th>
              <th>Avg resolve</th>
              <th>SLA met</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in summary" :key="r.name" @click="openInJira(r)">
              <td class="s-name"><i class="dot" :class="'g-' + r.grade"></i>{{ r.name }}</td>
              <td class="hl"><b>{{ fmt(r.periodClosed) }}</b></td>
              <td>{{ fmt(r.mtdClosed) }}</td>
              <td>{{ fmt(r.pace) }}</td>
              <td>{{ fmt(r.target) }}</td>
              <td :class="r.variance >= 0 ? 'pos' : 'neg'">
                {{ r.variance >= 0 ? '+' : '' }}{{ fmt(r.variance) }}
                <span class="s-pct">{{ r.variancePct }}</span>
              </td>
              <td>{{ dur(r.frtMs) }}</td>
              <td>{{ dur(r.resMs) }}</td>
              <td :class="slaClass(r.slaPct)">{{ r.slaPct ? r.slaPct.toFixed(0) + '%' : '—' }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr>
              <td class="s-name">Total</td>
              <td class="hl"><b>{{ fmt(period.bucket.closed) }}</b></td>
              <td>{{ fmt(mtd.bucket.closed) }}</td>
              <td>{{ fmt(monthPace) }}</td>
              <td>{{ fmt(round(targets.closedMonth)) }}</td>
              <td :class="monthPace - targets.closedMonth >= 0 ? 'pos' : 'neg'">
                {{ monthPace - targets.closedMonth >= 0 ? '+' : '' }}{{ fmt(round(monthPace - targets.closedMonth)) }}
              </td>
              <td>{{ dur(period.bucket.frtAvgMs) }}</td>
              <td>{{ dur(period.bucket.resAvgMs) }}</td>
              <td>{{ period.slaPct ? period.slaPct.toFixed(0) + '%' : '—' }}</td>
            </tr>
          </tfoot>
        </table>
      </div>
    </article>

    <!-- Reading aids. Dropped in fullscreen: on a wall they are space that a
         number could have had, and nobody reads a paragraph twice. -->
    <p v-if="!isFullscreen" class="howto">
      <Info class="h-3.5 w-3.5" />
      <span>
        Cards show <b>{{ period.label.toLowerCase() }}</b> against a target of
        <b>{{ targetSource }}</b>. The big number is actual; beside it is <b>pace</b> —
        where the month lands if the rest looks like the part that has happened. Colour is
        percent of target: green at or above, amber within 10%, red behind. Bars carry a tick
        at the target, and all three panels keep one row order so a name can be followed
        across them.
      </span>
    </p>
  </section>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { RefreshCw, Info } from 'lucide-vue-next'
import { isFullscreen } from '@/store'

const props = defineProps({
  projectKey: { type: String, default: '' },
  baseUrl: { type: String, default: '' },
})

const PERIODS = [
  { id: 'today', label: 'Today' },
  { id: 'yesterday', label: 'Yesterday' },
  { id: 'mtd', label: 'Month to date' },
]
// The sales page's "Store basis" toggle: here it switches the volume KPI
// between tickets closed and tickets opened.
const BASIS = [
  { id: 'resolved', label: 'Closed' },
  { id: 'created', label: 'Opened' },
]
// Location for the leadership view, technician as a toggle - ranking people by
// name in front of the C-suite is a different conversation from ranking sites.
const RANKBY = [
  { id: 'location', label: 'By location' },
  { id: 'tech', label: 'By tech' },
]
// The grade bands from the sales page, unchanged, so the colours mean the same
// thing to anyone who already reads that one.
const GRADES = [
  { k: 'on', l: '≥100% on target' },
  { k: 'near', l: '90–99%' },
  { k: 'behind', l: 'under 90%' },
]

const daily = ref({ configured: false, ok: false, projects: [] })
const busy = ref(false)
const periodId = ref(localStorage.getItem('gatus.jira.snap.period') || 'today')
const basis = ref('resolved')
const rankBy = ref(localStorage.getItem('gatus.jira.snap.rankby') || 'location')
const locationFilter = ref('')

const setPeriod = (id) => {
  periodId.value = id
  localStorage.setItem('gatus.jira.snap.period', id)
}
const setRankBy = (id) => {
  rankBy.value = id
  localStorage.setItem('gatus.jira.snap.rankby', id)
}

const load = async (force = false) => {
  busy.value = true
  try {
    const r = await fetch(`/api/v1/jira/daily${force ? '?refresh=1' : ''}`, { cache: 'no-store' })
    if (r.ok) daily.value = await r.json()
  } catch (e) {
    // keep the last good payload; the updated label carries the staleness
  } finally {
    busy.value = false
  }
}
const refresh = () => load(true)

// A chain of timeouts, not setInterval: the next delay comes from what came
// back, and a slow response cannot stack a second poll on the first.
let timer = null
const pump = async () => {
  await load()
  timer = setTimeout(pump, daily.value.computing ? 4000 : 60000)
}
const onKey = (e) => {
  if (e.target && /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName)) return
  const k = e.key.toLowerCase()
  if (k === '1') setPeriod('today')
  else if (k === '2') setPeriod('yesterday')
  else if (k === '3') setPeriod('mtd')
  else if (k === 'r') refresh()
}
onMounted(() => { pump(); window.addEventListener('keydown', onKey) })
onUnmounted(() => { clearTimeout(timer); window.removeEventListener('keydown', onKey) })

// --- data selection ------------------------------------------------------
const project = computed(() => {
  const list = daily.value.projects || []
  return list.find(p => p.key === props.projectKey) || list[0] || null
})
const projectName = computed(() => project.value?.name || props.projectKey || '—')
const EMPTY = {
  closed: 0, opened: 0, frtMedianMs: 0, frtAvgMs: 0,
  resMedianMs: 0, resAvgMs: 0, slaMet: 0, slaBreach: 0, measured: 0,
}
const periodFor = (id) => (project.value?.periods || []).find(x => x.id === id)
  || { id, label: PERIODS.find(x => x.id === id)?.label || id, bucket: EMPTY, slaPct: 0, byLocation: [], byTech: [] }
const period = computed(() => periodFor(periodId.value))
const mtd = computed(() => periodFor('mtd'))
const targets = computed(() => project.value?.targets
  || { closedPerDay: 0, closedMonth: 0, frtMs: 0, resMs: 0, slaPct: 95, source: 'none' })
const targetSource = computed(() => targets.value.source === 'configured'
  ? 'configured' : `last month (${fmt(project.value?.baseline?.closed || 0)} closed)`)
const noLocation = computed(() => project.value?.noLocation || 0)

const rowsOf = (p, dim) => (dim === 'location' ? p.byLocation : p.byTech) || []
const locationList = computed(() => rowsOf(period.value, 'location'))
// A location filter narrows the cards to that one site.
const filtered = computed(() => {
  if (!locationFilter.value) return period.value.bucket
  const row = (period.value.byLocation || []).find(r => r.name === locationFilter.value)
  return row ? row.bucket : EMPTY
})

// --- grading -------------------------------------------------------------
const unsetName = (n) => n === 'No location set' || n === 'Unassigned'

const gradeOf = (pctOfTarget) => {
  if (!isFinite(pctOfTarget) || pctOfTarget <= 0) return 'none'
  // Green at or above target, amber close, red behind. No blue band: blue is
  // neutral on this page and using it for "best" made the strongest result
  // look like an informational note.
  if (pctOfTarget >= 100) return 'on'
  if (pctOfTarget >= 90) return 'near'
  return 'behind'
}
const dayFraction = computed(() => {
  const d = daily.value
  return d.monthDays && d.monthDay ? d.monthDay / d.monthDays : 1
})
const monthPace = computed(() => {
  const f = dayFraction.value
  return f > 0 ? Math.round(mtd.value.bucket.closed / f) : 0
})
const remainingPerDay = computed(() => {
  const d = daily.value
  const left = (d.monthDays || 0) - (d.monthDay || 0)
  if (left <= 0) return 0
  const gap = targets.value.closedMonth - mtd.value.bucket.closed
  return gap > 0 ? gap / left : 0
})

const kpis = computed(() => {
  const b = filtered.value
  const T = targets.value
  const isMonth = periodId.value === 'mtd'
  const volume = basis.value === 'created' ? b.opened : b.closed
  const volTarget = isMonth ? T.closedPerDay * (daily.value.monthDay || 1) : T.closedPerDay
  const volPct = volTarget > 0 ? (volume / volTarget) * 100 : 0
  // Lower is better for the time metrics, so the ratio inverts.
  const frtPct = T.frtMs > 0 && b.frtAvgMs > 0 ? (T.frtMs / b.frtAvgMs) * 100 : 0
  const resPct = T.resMs > 0 && b.resAvgMs > 0 ? (T.resMs / b.resAvgMs) * 100 : 0
  const slaNow = b.slaMet + b.slaBreach > 0 ? (b.slaMet / (b.slaMet + b.slaBreach)) * 100 : 0
  const slaPct = T.slaPct > 0 ? (slaNow / T.slaPct) * 100 : 0

  return [
    {
      id: 'vol',
      label: basis.value === 'created' ? 'Tickets opened' : 'Tickets closed',
      value: fmt(volume),
      pace: isMonth ? `${fmt(monthPace.value)} pace` : `${fmt(round(T.closedPerDay))} target`,
      paceTip: isMonth
        ? `${fmt(mtd.value.bucket.closed)} over ${daily.value.monthDay} of ${daily.value.monthDays} days, carried forward at the same rate`
        : 'A single day’s target, from last month’s daily average',
      pctText: volPct ? `${Math.round(volPct)}%` : '—',
      grade: gradeOf(volPct),
      context: [
        `Target ${fmt(round(volTarget))}`,
        `Opened ${fmt(b.opened)} · closed ${fmt(b.closed)} · net ${b.opened - b.closed >= 0 ? '+' : ''}${b.opened - b.closed}`,
        isMonth
          ? `Daily need ${fmt(round(remainingPerDay.value))} · variance at pace ${monthPace.value - T.closedMonth >= 0 ? '+' : ''}${fmt(round(monthPace.value - T.closedMonth))}`
          : `Month to date ${fmt(mtd.value.bucket.closed)} of ${fmt(round(T.closedMonth))}`,
      ],
    },
    {
      id: 'frt',
      label: 'Avg first response',
      value: dur(b.frtAvgMs),
      pace: `med ${dur(b.frtMedianMs)}`,
      paceTip: 'Median beside the average: the distribution is skewed, so the two differ a lot',
      pctText: frtPct ? `${Math.round(frtPct)}%` : '—',
      grade: gradeOf(frtPct),
      context: [
        `Target ${dur(T.frtMs)} · last month ${dur(project.value?.baseline?.frtAvgMs || 0)}`,
        `${fmt(b.measured)} tickets on the clock`,
        'Lower is better, so percent is target ÷ actual',
      ],
    },
    {
      id: 'res',
      label: 'Avg time to resolve',
      value: dur(b.resAvgMs),
      pace: `med ${dur(b.resMedianMs)}`,
      paceTip: 'Business-hours SLA clock, paused while waiting on the customer — not labour',
      pctText: resPct ? `${Math.round(resPct)}%` : '—',
      grade: gradeOf(resPct),
      context: [
        `Target ${dur(T.resMs)} · last month ${dur(project.value?.baseline?.resAvgMs || 0)}`,
        `${fmt(b.measured)} tickets on the clock`,
        'Business hours, paused on customer wait',
      ],
    },
    {
      id: 'sla',
      label: 'SLA met',
      value: slaNow ? `${slaNow.toFixed(0)}%` : '—',
      pace: `${fmt(b.slaBreach)} missed`,
      paceTip: 'Resolution SLAs that breached their goal',
      pctText: slaPct ? `${Math.round(slaPct)}%` : '—',
      grade: gradeOf(slaPct),
      context: [
        `Target ${T.slaPct.toFixed(0)}%`,
        `Met ${fmt(b.slaMet)} of ${fmt(b.slaMet + b.slaBreach)}`,
        `Last month ${project.value?.baseline ? slaTextOf(project.value.baseline) : '—'}`,
      ],
    },
  ]
})

// --- ranked panels -------------------------------------------------------
const rankRows = computed(() => rowsOf(period.value, rankBy.value === 'location' ? 'location' : 'tech'))
const perRowTarget = computed(() => {
  const n = Math.max(1, rankRows.value.filter(r => !unsetName(r.name)).length)
  const isMonth = periodId.value === 'mtd'
  const t = isMonth ? targets.value.closedPerDay * (daily.value.monthDay || 1) : targets.value.closedPerDay
  return t / n
})
const panels = computed(() => {
  const rows = rankRows.value
  const T = targets.value
  const volTarget = perRowTarget.value
  const max = (pick) => Math.max(1, ...rows.map(pick))

  const build = (title, targetText, pick, fmtVal, secVal, target, lowerBetter) => {
    const m = max(pick)
    return {
      id: title, title, sub: `target ${targetText}`, targetText,
      rows: rows.map(r => {
        const v = pick(r)
        const p = target > 0 && v > 0 ? (lowerBetter ? (target / v) * 100 : (v / target) * 100) : 0
        return {
          name: r.name,
          ratio: p ? `${Math.round(p)}%` : '—',
          value: fmtVal(r),
          secondary: secVal(r),
          grade: gradeOf(p),
          barStyle: { width: `${Math.min(100, (v / m) * 100)}%` },
          tickPct: Math.min(100, (target / m) * 100),
        }
      }),
      footLabel: lowerBetter ? 'Average' : 'Total',
      footValue: lowerBetter ? fmtVal({ bucket: period.value.bucket })
        : fmt(rows.reduce((s, r) => s + pick(r), 0)),
    }
  }

  return [
    build(rankBy.value === 'location' ? 'Closed by location' : 'Closed by technician',
      `${fmt(round(volTarget))} each`,
      r => r.bucket.closed, r => fmt(r.bucket.closed),
      r => `${fmt(r.bucket.opened)} opened`, volTarget, false),
    build('First response', dur(T.frtMs),
      r => r.bucket.frtAvgMs, r => dur(r.bucket.frtAvgMs),
      r => `med ${dur(r.bucket.frtMedianMs)}`, T.frtMs, true),
    build('Time to resolve', dur(T.resMs),
      r => r.bucket.resAvgMs, r => dur(r.bucket.resAvgMs),
      r => (r.slaPct ? `${r.slaPct.toFixed(0)}% SLA` : '—'), T.resMs, true),
  ]
})

// --- summary table -------------------------------------------------------
const summary = computed(() => {
  const dim = rankBy.value === 'location' ? 'location' : 'tech'
  const byName = new Map()
  for (const r of rowsOf(mtd.value, dim)) byName.set(r.name, { mtd: r, per: null })
  for (const r of rowsOf(period.value, dim)) {
    const e = byName.get(r.name) || { mtd: null, per: null }
    e.per = r
    byName.set(r.name, e)
  }
  const f = dayFraction.value
  // Split across real locations only: counting the unset bucket as a site
  // made every target smaller than it should be.
  const realCount = [...byName.keys()].filter(n => !unsetName(n)).length
  const target = targets.value.closedMonth / Math.max(1, realCount)
  const out = []
  for (const [name, e] of byName) {
    const mtdClosed = e.mtd?.bucket.closed || 0
    const pace = f > 0 ? Math.round(mtdClosed / f) : 0
    const variance = pace - target
    out.push({
      name,
      periodClosed: e.per?.bucket.closed || 0,
      mtdClosed, pace,
      target: Math.round(target),
      variance: Math.round(variance),
      variancePct: target > 0 ? `${variance >= 0 ? '+' : ''}${Math.round((variance / target) * 100)}%` : '—',
      frtMs: e.per?.bucket.frtAvgMs || e.mtd?.bucket.frtAvgMs || 0,
      resMs: e.per?.bucket.resAvgMs || e.mtd?.bucket.resAvgMs || 0,
      slaPct: e.per?.slaPct || e.mtd?.slaPct || 0,
      grade: gradeOf(target > 0 ? (pace / target) * 100 : 0),
      accountId: e.per?.accountId || e.mtd?.accountId || '',
    })
  }
  // "No location set" is the absence of a location, not a location, so it
  // sorts last however large it is - leading a location ranking with it tells
  // the reader nothing about any site.
  const unset = (n) => (n === 'No location set' || n === 'Unassigned' ? 1 : 0)
  return out.sort((x, y) =>
    unset(x.name) - unset(y.name) || y.mtdClosed - x.mtdClosed || x.name.localeCompare(y.name))
})

// Click a row to open exactly that slice in Jira, so any figure here can be
// checked against the source.
const openInJira = (row) => {
  if (!props.baseUrl || !project.value) return
  const p = period.value
  const bits = [`project = "${project.value.key}"`,
    `resolutiondate >= "${p.from}"`, `resolutiondate < "${p.to}"`]
  if (rankBy.value === 'location') {
    bits.push(row.name === 'No location set' ? 'cf[10090] IS EMPTY' : `cf[10090] = "${row.name}"`)
  } else if (row.accountId) {
    bits.push(`assignee = "${row.accountId}"`)
  } else {
    bits.push('assignee IS EMPTY')
  }
  window.open(`${props.baseUrl}/issues/?jql=${encodeURIComponent(bits.join(' AND '))}`, '_blank', 'noopener')
}

// --- formatting ----------------------------------------------------------
const fmt = (n) => (Number(n) || 0).toLocaleString()
const round = (n) => Math.round(Number(n) || 0)
const dur = (ms) => {
  if (!ms || ms <= 0) return '—'
  const s = ms / 1000
  if (s < 60) return `${Math.round(s)}s`
  const m = s / 60
  if (m < 60) return `${Math.round(m)}m`
  const h = m / 60
  if (h < 24) return `${h.toFixed(h < 10 ? 1 : 0)}h`
  return `${(h / 24).toFixed(1)}d`
}
const slaTextOf = (b) => {
  const t = (b.slaMet || 0) + (b.slaBreach || 0)
  return t > 0 ? `${Math.round(((b.slaMet || 0) / t) * 100)}%` : '—'
}
const slaClass = (p) => (!p ? ''
  : p >= targets.value.slaPct ? 'pos' : p >= targets.value.slaPct - 10 ? 'warnt' : 'neg')
const longDate = computed(() => {
  const p = period.value
  const d = new Date(`${p.from}T12:00:00`)
  if (isNaN(d)) return ''
  if (p.id === 'mtd') return d.toLocaleDateString([], { month: 'long', year: 'numeric' })
  return d.toLocaleDateString([], { weekday: 'long', day: 'numeric', month: 'long' })
})
const updatedLabel = computed(() => {
  if (!daily.value.updatedAt) return '—'
  const t = new Date(daily.value.updatedAt)
  return isNaN(t) ? '—' : t.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
})
</script>

<style scoped>
/* ====================================================================
   IT DAILY SNAPSHOT

   The sales daily-snapshot format, mapped onto the service desk.

   NO SIDEBAR. The controls are a single horizontal bar. The sidebar cost
   a 14rem column of width and stacked its labels into an unreadable
   jumble; inline segmented controls are both narrower and clearer, and
   the page gets the full width for data.

   FULLSCREEN NEVER SCROLLS. The wall is a four-row grid pinned to the
   viewport: bar, cards, panels, table. The panels and the table share
   the remainder, and the table scrolls inside its own frame. The reading
   paragraph is dropped - on a wall it is space a number could have had.
   ==================================================================== */
.snap {
  display: flex; flex-direction: column; gap: 0.5rem; min-width: 0; width: 100%;
  --hair: hsl(var(--border));
  --mono: var(--j-mono, ui-monospace, Menlo, monospace);
  /* Green good, amber close, red bad. --neutral is blue and is used only for
     chrome: the selected period and the highlighted period column. */
  --on: #15803d; --near: #d97706; --behind: #dc2626; --neutral: #2563eb;
  --tint: 10%;
}
:global(.dark) .snap {
  --on: #4ade80; --near: #fbbf24; --behind: #f87171; --neutral: #60a5fa;
  --tint: 16%;
}
/* One screen, no scrollbar. The two flexible regions carry min-height:0,
   without which a flex child refuses to shrink below its content and pushes
   the page past the viewport. */
.snap.wall {
  /* height:100% would resolve against an auto-height parent and collapse to
     content height; the parent is now a flex container that hands this a real
     height, so stretching to it is what fills the screen. */
  height: auto; min-height: 0; overflow: hidden;
  /* bar / cards / panels / summary. The summary is `auto` so it shows all of
     its rows, and the ranked panels absorb whatever is left - they can shrink
     to nothing if a long summary needs the room. */
  display: grid; grid-template-rows: auto auto minmax(0, 1fr) auto;
  gap: 0.45rem;
}
/* Not fullscreen: the summary table takes the remaining height instead of
   stopping at its content and leaving a band of nothing underneath. */
.snap:not(.wall) { min-height: 0; }
.snap:not(.wall) .sumwrap { flex: none; }

/* ------------------------------------------------------------------- bar */
.bar {
  display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; flex: none;
  border: 1px solid var(--hair); border-radius: 10px; background: hsl(var(--card));
  padding: 0.4rem 0.6rem;
}
.seg { display: flex; border: 1px solid var(--hair); border-radius: 8px; overflow: hidden; flex: none; }
.sgb {
  display: inline-flex; align-items: center; gap: 0.3rem;
  font-size: 0.8rem; font-weight: 700; padding: 0.3rem 0.65rem;
  color: hsl(var(--muted-foreground)); white-space: nowrap;
}
.snap.wall .sgb { font-size: 0.95rem; padding: 0.35rem 0.8rem; }
.seg.sm .sgb { font-size: 0.74rem; padding: 0.25rem 0.5rem; }
.snap.wall .seg.sm .sgb { font-size: 0.85rem; }
.sgb + .sgb { border-left: 1px solid var(--hair); }
.sgb:hover { background: hsl(var(--muted)); color: hsl(var(--foreground)); }
.sgb.on { background: color-mix(in srgb, var(--neutral) var(--tint), hsl(var(--card))); color: hsl(var(--foreground)); }
.sgb kbd {
  font-family: var(--mono); font-size: 0.6rem; font-weight: 700; line-height: 1;
  border: 1px solid var(--hair); border-radius: 3px; padding: 0.1rem 0.2rem;
  color: hsl(var(--muted-foreground));
}
.bar-date { font-weight: 800; font-size: 0.88rem; white-space: nowrap; }
.snap.wall .bar-date { font-size: 1.1rem; }
.bar-day { font-style: normal; font-family: var(--mono); font-size: 0.68rem; font-weight: 600; color: hsl(var(--muted-foreground)); margin-left: 0.3rem; }
.pick {
  font-size: 0.76rem; font-weight: 600; background: hsl(var(--background));
  border: 1px solid var(--hair); border-radius: 8px; padding: 0.25rem 0.4rem;
  color: hsl(var(--foreground)); max-width: 11rem;
}
.legend { list-style: none; display: flex; gap: 0.35rem; margin: 0 0 0 auto; padding: 0; cursor: help; flex-wrap: wrap; }
.legend li { display: inline-flex; align-items: center; gap: 0.22rem; font-family: var(--mono); font-size: 0.62rem; font-weight: 700; color: hsl(var(--muted-foreground)); white-space: nowrap; }
.snap.wall .legend li { font-size: 0.74rem; }
.legend i { width: 0.5rem; height: 0.5rem; border-radius: 2px; display: inline-block; }
.legend .g-on i { background: var(--on); }
.legend .g-near i { background: var(--near); }
.legend .g-behind i { background: var(--behind); }
.bar-tgt, .bar-upd { font-family: var(--mono); font-size: 0.64rem; color: hsl(var(--muted-foreground)); white-space: nowrap; }
.snap.wall .bar-tgt, .snap.wall .bar-upd { font-size: 0.76rem; }
.bar-rf { color: hsl(var(--muted-foreground)); flex: none; }
.bar-rf:hover:not(:disabled) { color: hsl(var(--foreground)); }
.bar-rf:disabled { opacity: 0.5; }

/* ------------------------------------------------------------------ kpis */
.kpis { display: grid; gap: 0.45rem; grid-template-columns: repeat(auto-fit, minmax(13rem, 1fr)); flex: none; min-height: 0; }
/* Cards share one height so the row has a straight bottom edge. */
.kpis > .kpi { height: 100%; }
.snap.wall .k-ctx { margin-top: auto; }
.kpi {
  display: flex; flex-direction: column; gap: 0.15rem; min-width: 0; min-height: 0;
  border: 1px solid color-mix(in srgb, var(--gc) 40%, var(--hair)); border-radius: 10px;
  padding: 0.4rem 0.6rem 0.45rem;
  background: color-mix(in srgb, var(--gc) var(--tint), hsl(var(--card)));
}
.g-on { --gc: var(--on); }
.g-near { --gc: var(--near); } .g-behind { --gc: var(--behind); }
.g-none { --gc: hsl(var(--border)); }
.k-top { display: flex; align-items: baseline; justify-content: space-between; gap: 0.4rem; }
.k-l { font-family: var(--mono); font-size: 9px; font-weight: 800; letter-spacing: 0.13em; text-transform: uppercase; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.snap.wall .k-l { font-size: 11px; }
.k-pct { display: inline-flex; align-items: center; gap: 0.25rem; font-family: var(--mono); font-size: 0.72rem; font-weight: 800; color: var(--gc); white-space: nowrap; }
.snap.wall .k-pct { font-size: 0.88rem; }
.dot { width: 0.45rem; height: 0.45rem; border-radius: 50%; background: var(--gc); display: inline-block; flex: none; }
.k-main { display: flex; align-items: baseline; gap: 0.4rem; }
.k-n { font-family: var(--mono); font-variant-numeric: tabular-nums; font-size: clamp(1.5rem, 2.4vw, 2.1rem); font-weight: 800; line-height: 1; letter-spacing: -0.05em; }
.snap.wall .k-n { font-size: clamp(1.7rem, 2.6vw, 2.3rem); }
.k-pace { font-family: var(--mono); font-size: 0.72rem; font-weight: 700; color: hsl(var(--muted-foreground)); white-space: nowrap; cursor: help; }
.snap.wall .k-pace { font-size: 0.88rem; }
.k-ctx { list-style: none; margin: 0; padding: 0; display: grid; gap: 0.02rem; }
.k-ctx li { font-family: var(--mono); font-size: 0.64rem; color: hsl(var(--muted-foreground)); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.snap.wall .k-ctx li { font-size: 0.7rem; }

/* ----------------------------------------------------------- rank panels */
.ranks { display: grid; gap: 0.45rem; grid-template-columns: repeat(auto-fit, minmax(17rem, 1fr)); min-height: 0; }
.rank {
  display: flex; flex-direction: column; min-width: 0; min-height: 0; overflow: hidden;
  border: 1px solid var(--hair); border-radius: 10px; background: hsl(var(--card));
  padding: 0.4rem 0.55rem 0.45rem;
}
.r-head { display: flex; align-items: baseline; justify-content: space-between; gap: 0.4rem; padding-bottom: 0.25rem; margin-bottom: 0.2rem; border-bottom: 1px solid var(--hair); flex: none; }
.r-t { margin: 0; font-family: var(--mono); font-size: 10px; font-weight: 800; letter-spacing: 0.13em; text-transform: uppercase; white-space: nowrap; }
.snap.wall .r-t { font-size: 12px; }
/* Every panel states its own target, so no bar on this page is unlabelled. */
.r-s { font-family: var(--mono); font-size: 0.62rem; color: hsl(var(--muted-foreground)); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.snap.wall .r-s { font-size: 0.76rem; }
.r-rows { list-style: none; margin: 0; padding: 0; display: grid; gap: 0; flex: 1 1 auto; min-height: 0; overflow-y: auto; align-content: start; }
/* On the wall the rows spread to fill the panel rather than bunching at the
   top; packed-to-start is right when the page scrolls, wrong when it cannot. */
.snap.wall .r-rows { align-content: stretch; }
.r-row {
  display: grid; align-items: center; gap: 0.25rem 0.3rem;
  grid-template-columns: 1rem minmax(0, 1fr) 2.3rem 3rem 3.4rem;
  grid-template-areas: "rank name ratio val sec" "bar bar bar bar bar";
  padding: 0.1rem 0; font-family: var(--mono); font-size: 0.71rem; font-variant-numeric: tabular-nums;
}
.snap.wall .r-row { font-size: 0.86rem; }
.r-rank { grid-area: rank; color: hsl(var(--muted-foreground)); font-weight: 700; }
.r-name { grid-area: name; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.r-ratio { grid-area: ratio; text-align: right; color: var(--gc); font-weight: 800; }
.r-val { grid-area: val; text-align: right; font-weight: 800; }
.r-sec { grid-area: sec; text-align: right; color: hsl(var(--muted-foreground)); font-size: 0.92em; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.r-bar { grid-area: bar; position: relative; height: 0.28rem; border-radius: 2px; background: hsl(var(--muted)); overflow: visible; }
.r-fill { position: absolute; inset: 0 auto 0 0; background: var(--gc); border-radius: 2px; }
/* The tick marks the target, so a bar reads against it without a scale. */
.r-tick { position: absolute; top: -2px; bottom: -2px; width: 2px; background: hsl(var(--foreground) / 0.7); cursor: help; }
.r-empty { font-size: 0.72rem; color: hsl(var(--muted-foreground)); padding: 0.5rem 0; }
.r-foot { display: flex; justify-content: space-between; gap: 0.5rem; border-top: 1px solid var(--hair); margin-top: 0.25rem; padding-top: 0.2rem; font-family: var(--mono); font-size: 0.7rem; font-weight: 800; flex: none; }
.snap.wall .r-foot { font-size: 0.85rem; }

/* -------------------------------------------------------------- summary */
.sumwrap {
  display: flex; flex-direction: column; min-width: 0; min-height: 0; overflow: hidden;
  border: 1px solid var(--hair); border-radius: 10px; background: hsl(var(--card));
  padding: 0.4rem 0.55rem 0.45rem;
}
/* No inner scroll: the table shows every row, and the panel is whatever
   height that needs. Scrolling inside a box on a wall board hides rows
   without saying so, which is worse than the box being tall. */
.sumscroll { overflow: visible; max-height: none; }
.sum { width: 100%; border-collapse: collapse; font-family: var(--mono); font-size: 0.72rem; font-variant-numeric: tabular-nums; }
.snap.wall .sum { font-size: 0.82rem; }
.sum th {
  font-size: 8px; font-weight: 800; letter-spacing: 0.1em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); text-align: right; padding: 0 0.35rem 0.22rem; white-space: nowrap;
}
.snap.wall .sum th { font-size: 9px; }
.sum th.s-name { text-align: left; }
.sum th.hl, .sum td.hl { background: color-mix(in srgb, var(--neutral) 8%, transparent); }
.sum td { padding: 0 0.35rem; height: 1.55rem; text-align: right; white-space: nowrap; }
.snap.wall .sum td { height: 1.5rem; }
.sum td.s-name { text-align: left; }
.sum td.s-name .dot { margin-right: 0.35rem; vertical-align: 0.04rem; }
.sum tbody tr { cursor: pointer; border-top: 1px solid hsl(var(--border) / 0.5); }
.sum tbody tr:hover { background: hsl(var(--muted)); }
.sum tfoot td { border-top: 1px solid var(--hair); font-weight: 800; }
.s-pct { color: hsl(var(--muted-foreground)); font-size: 0.9em; margin-left: 0.2rem; }
.pos { color: var(--on); }
.neg { color: var(--behind); }
.warnt { color: var(--near); }

.howto {
  display: flex; align-items: flex-start; gap: 0.4rem; margin: 0; flex: none;
  font-size: 0.7rem; line-height: 1.5; color: hsl(var(--muted-foreground));
  border: 1px dashed var(--hair); border-radius: 8px; padding: 0.4rem 0.6rem;
}
.howto b { color: hsl(var(--foreground)); font-weight: 700; }

@media (prefers-reduced-motion: reduce) { .animate-spin { animation: none; } }
</style>
