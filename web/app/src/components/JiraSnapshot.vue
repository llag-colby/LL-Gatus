<template>
  <section class="snap" :class="{ wall: isFullscreen }">

    <div class="ctl">
      <span class="ctl-date">{{ todayLong }}
        <i v-if="daily.monthDays">day {{ daily.monthDay }} of {{ daily.monthDays }}</i></span>
      <ul class="key" data-tooltip="Colour is the figure as a percentage of its target" data-tip-pos="bottom">
        <li class="g-on"><i></i>on target</li>
        <li class="g-near"><i></i>within 10%</li>
        <li class="g-behind"><i></i>behind</li>
      </ul>
      <span class="ctl-tgt">target: {{ targetSource }}</span>
      <span class="ctl-feed" :class="{ stale }">{{ feedText }}</span>
      <button v-if="!isFullscreen" class="ctl-rf" @click="refresh" :disabled="busy"
        data-tooltip="Refresh  R" data-tip-pos="bottom">
        <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': busy }" />
      </button>
    </div>

    <div v-if="warming" class="stalebar warm" role="status">Loading from Jira. Figures below are not final yet.</div>
    <div v-if="stale" class="stalebar" role="status">
      Data is {{ staleMins }} minutes old. The page is retrying and will reload itself if the feed does not return.
    </div>

    <!-- Right-now numbers, then how the month is going. Time metrics are month
         to date: a single morning has too few tickets for an average to mean
         anything, so the wall never shows one. -->
    <div class="kpis">
      <div class="kpi" v-for="k in liveKpis" :key="k.l" :class="k.tone">
        <div class="kpi-n" :class="k.cls">{{ k.v }}</div>
        <div class="kpi-l">{{ k.l }}</div>
      </div>
      <div v-for="k in kpis" :key="k.id" class="kpi graded" :class="'g-' + k.grade">
        <div class="kpi-n">{{ k.value }}<em>{{ k.pace }}</em></div>
        <div class="kpi-l">{{ k.label }}<b>{{ k.pctText }}</b></div>
      </div>
    </div>

    <div class="panels">
      <article v-for="p in boards" :key="p.id" class="panel">
        <div class="p-head"><span class="p-t">{{ p.title }}</span><span class="p-s">{{ p.sub }}</span></div>
        <ol class="p-rows" :style="{ '--n': Math.max(p.rows.length, 1) }">
          <li v-for="(r, i) in p.rows" :key="r.name" class="prow" :class="['g-' + r.grade, { muted: r.muted }]"
            @click="openInJira(r, p.dim)">
            <span class="pr-rank">{{ r.muted ? '' : i + 1 }}</span>
            <span class="pr-name" :title="r.name">{{ r.name }}</span>
            <span class="pr-today" :class="{ on: r.today > 0 }">{{ r.today > 0 ? '+' + r.today : '' }}</span>
            <span class="pr-val">{{ r.value }}</span>
            <span class="pr-bar"><i class="pr-fill" :style="r.barStyle"></i><i class="pr-tick" :style="{ left: r.tickPct + '%' }"></i></span>
          </li>
          <li v-if="!p.rows.length" class="p-empty">Nothing closed yet this month.</li>
        </ol>
        <div class="p-foot"><span>{{ p.footLabel }}</span><b>{{ p.footValue }}</b></div>
      </article>

      <!-- PACE: where the month lands, the last two weeks, and who is slow. -->
      <article class="panel pace">
        <div class="p-head"><span class="p-t">Month pace</span><span class="p-s">{{ fmt(mtd.bucket.closed) }} of {{ fmt(Math.round(targets.closedMonth)) }}</span></div>
        <div class="pace-body">
          <div class="pace-big" :class="'g-' + paceGrade">
            <span class="pace-n">{{ fmt(monthPace) }}</span>
            <span class="pace-l">on pace to close<b>{{ pacePct }}</b></span>
          </div>
          <div class="pace-track" role="img" :aria-label="`Month to date ${mtd.bucket.closed} of ${Math.round(targets.closedMonth)}`">
            <i class="pace-fill" :class="'g-' + paceGrade" :style="{ width: paceFill + '%' }"></i>
            <i class="pace-day" :style="{ left: dayFraction * 100 + '%' }"></i>
          </div>
          <div class="pace-note">line marks today in the month</div>

          <div class="flow-head"><span class="kpi-l">14-day flow</span>
            <span class="flow-today"><b class="in">+{{ live?.createdToday ?? 0 }}</b><b class="out">-{{ live?.resolvedToday ?? 0 }}</b><em>today</em></span></div>
          <div class="flowchart" role="img" :aria-label="flowLabel">
            <div v-for="d in flow" :key="d.date" class="fday" :class="{ today: d.today }"
              :data-tooltip="`${d.date}  +${d.created} in  -${d.resolved} out`">
              <div class="fup"><span :style="{ height: d.up + '%' }"></span></div>
              <div class="faxis"></div>
              <div class="fdown"><span :style="{ height: d.down + '%' }"></span></div>
            </div>
          </div>

          <div class="kpi-l slow-l">Slowest to resolve, month to date</div>
          <ul class="slow">
            <li v-for="r in slowest" :key="r.name" :class="'g-' + r.grade">
              <span class="s-name">{{ r.name }}</span><span class="s-sec">1st {{ r.first }}</span><b>{{ r.value }}</b>
            </li>
            <li v-if="!slowest.length" class="s-none">No resolved tickets with a clock yet.</li>
          </ul>
        </div>
      </article>
    </div>
  </section>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { RefreshCw } from 'lucide-vue-next'
import { isFullscreen, now as serverNow } from '@/store'

const props = defineProps({
  projectKey: { type: String, default: '' },
  baseUrl: { type: String, default: '' },
  // The live project object from /jira/metrics (open, breached, unassigned...).
  live: { type: Object, default: null },
})

const daily = ref({ configured: false, ok: false, projects: [] })
const busy = ref(false)
const lastOk = ref(0)

// --- polling: a chain of timeouts so a slow reply cannot stack a second poll ---
const load = async (force = false) => {
  busy.value = true
  try {
    const d = await fetch(`/api/v1/jira/daily${force ? '?refresh=1' : ''}`, { cache: 'no-store' })
    if (d.ok) { daily.value = await d.json(); lastOk.value = Date.now() }
  } catch (e) {
    // keep the last good payload; the feed label and stale bar carry the age
  } finally {
    busy.value = false
  }
}
const refresh = () => load(true)
let timer = null
const pump = async () => {
  await load()
  timer = setTimeout(pump, warming.value ? 4000 : 30000)
}
const onKey = (e) => {
  if (e.target && /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName)) return
  if (e.key.toLowerCase() === 'r') refresh()
}
onMounted(() => { pump(); window.addEventListener('keydown', onKey) })
onUnmounted(() => { clearTimeout(timer); window.removeEventListener('keydown', onKey) })

// --- feed health ----------------------------------------------------------
const STALE_MS = 5 * 60 * 1000
const ageMs = computed(() => {
  void serverNow.value
  const heard = lastOk.value ? Date.now() - lastOk.value : 0
  const stamp = daily.value.updatedAt ? new Date(daily.value.updatedAt).getTime() : 0
  const built = stamp ? Date.now() - stamp : 0
  return Math.max(heard, built > 3 * STALE_MS ? built : 0)
})
const stale = computed(() => lastOk.value > 0 && ageMs.value > STALE_MS)
const staleMins = computed(() => Math.floor(ageMs.value / 60000))
const feedText = computed(() => {
  if (!lastOk.value) return 'waiting for first poll'
  const s = Math.round((Date.now() - lastOk.value) / 1000)
  void serverNow.value
  return s < 60 ? `live  ${s}s` : `live  ${Math.floor(s / 60)}m ${s % 60}s`
})

// --- data -----------------------------------------------------------------
const project = computed(() => {
  const list = daily.value.projects || []
  return list.find(p => p.key === props.projectKey) || list[0] || null
})
// Right after a restart the payload exists but is empty; zeros drawn then
// would read as a real, terrible day.
const warming = computed(() => !!daily.value.computing || (daily.value.ok && !(project.value?.periods || []).length))
const EMPTY = {
  closed: 0, opened: 0, frtMedianMs: 0, frtAvgMs: 0,
  resMedianMs: 0, resAvgMs: 0, slaMet: 0, slaBreach: 0, measured: 0,
}
const periodFor = (id) => (project.value?.periods || []).find(x => x.id === id)
  || { id, label: id, bucket: EMPTY, slaPct: 0, byLocation: [], byTech: [], from: '' }
const today = computed(() => periodFor('today'))
const mtd = computed(() => periodFor('mtd'))
const targets = computed(() => project.value?.targets
  || { closedPerDay: 0, closedMonth: 0, frtMs: 0, resMs: 0, slaPct: 95, source: 'none' })
const targetSource = computed(() => targets.value.source === 'configured'
  ? 'configured' : `last month (${fmt(project.value?.baseline?.closed || 0)} closed)`)
const dayFraction = computed(() => {
  const d = daily.value
  return d.monthDays && d.monthDay ? d.monthDay / d.monthDays : 1
})
const monthPace = computed(() => dayFraction.value > 0 ? Math.round(mtd.value.bucket.closed / dayFraction.value) : 0)
const paceRatio = computed(() => targets.value.closedMonth > 0 ? (monthPace.value / targets.value.closedMonth) * 100 : 0)
const pacePct = computed(() => paceRatio.value ? `${Math.round(paceRatio.value)}% of target` : '')
const paceGrade = computed(() => gradeOf(paceRatio.value))
const paceFill = computed(() => targets.value.closedMonth > 0 ? Math.min(100, (mtd.value.bucket.closed / targets.value.closedMonth) * 100) : 0)

const unsetName = (n) => n === 'No location set' || n === 'Unassigned'
const gradeOf = (p) => {
  if (!isFinite(p) || p <= 0) return 'none'
  if (p >= 100) return 'on'
  if (p >= 90) return 'near'
  return 'behind'
}

// --- rail -----------------------------------------------------------------
const slaMeasured = computed(() => (props.live?.slaBreached ?? -1) >= 0)
const liveKpis = computed(() => {
  const p = props.live
  if (!p) return []
  const breach = Math.max(0, p.slaBreached || 0)
  return [
    { l: 'Open now', v: p.totalOpen },
    {
      l: slaMeasured.value ? 'SLA breached' : 'SLA not measured', v: slaMeasured.value ? breach : '--',
      tone: breach > 0 ? 'hot tone-crit' : '', cls: breach > 0 ? 'crit' : '',
    },
    { l: 'Unassigned', v: p.unassigned, tone: p.unassigned > 0 ? 'hot tone-warn' : '', cls: p.unassigned > 0 ? 'warn' : '' },
  ]
})
const kpis = computed(() => {
  const T = targets.value
  const t = today.value.bucket, b = mtd.value.bucket
  const todayPct = T.closedPerDay > 0 ? (t.closed / T.closedPerDay) * 100 : 0
  const frtPct = T.frtMs > 0 && b.frtAvgMs > 0 ? (T.frtMs / b.frtAvgMs) * 100 : 0
  const resPct = T.resMs > 0 && b.resAvgMs > 0 ? (T.resMs / b.resAvgMs) * 100 : 0
  const slaNow = b.slaMet + b.slaBreach > 0 ? (b.slaMet / (b.slaMet + b.slaBreach)) * 100 : 0
  const slaPct = T.slaPct > 0 ? (slaNow / T.slaPct) * 100 : 0
  const pc = (v) => (v ? `${Math.round(v)}%` : '')
  return [
    { id: 'vol', label: 'Closed today', value: fmt(t.closed), grade: gradeOf(todayPct), pctText: pc(todayPct), pace: `of ${fmt(Math.round(T.closedPerDay))}` },
    { id: 'frt', label: 'First response', value: dur(b.frtAvgMs), grade: gradeOf(frtPct), pctText: pc(frtPct), pace: `goal ${dur(T.frtMs)}` },
    { id: 'res', label: 'Time to resolve', value: dur(b.resAvgMs), grade: gradeOf(resPct), pctText: pc(resPct), pace: `goal ${dur(T.resMs)}` },
    { id: 'sla', label: 'SLA met', value: slaNow ? `${slaNow.toFixed(0)}%` : '--', grade: gradeOf(slaPct), pctText: pc(slaPct), pace: `${fmt(b.slaBreach)} missed` },
  ]
})

// --- leaderboards: month to date, today beside it -------------------------
const todayCount = (dim, name) => {
  const rows = (dim === 'location' ? today.value.byLocation : today.value.byTech) || []
  return rows.find(r => r.name === name)?.bucket.closed || 0
}
const board = (dim, title) => {
  const src = (dim === 'location' ? mtd.value.byLocation : mtd.value.byTech) || []
  const real = src.filter(r => !unsetName(r.name))
  // Expected to date, split across real rows: an unset bucket is not a site.
  const target = (targets.value.closedPerDay * (daily.value.monthDay || 1)) / Math.max(1, real.length)
  const ordered = [...real.sort((a, b) => b.bucket.closed - a.bucket.closed || a.name.localeCompare(b.name)),
    ...src.filter(r => unsetName(r.name))]
  const m = Math.max(1, ...ordered.map(r => r.bucket.closed), target)
  return {
    id: dim, title, dim,
    sub: `expected ${fmt(Math.round(target))} each so far`,
    rows: ordered.map(r => {
      const muted = unsetName(r.name)
      const v = r.bucket.closed
      return {
        name: r.name, accountId: r.accountId, muted, value: fmt(v), today: todayCount(dim, r.name),
        grade: muted ? 'none' : gradeOf(target > 0 && v > 0 ? (v / target) * 100 : 0),
        barStyle: { width: `${Math.min(100, (v / m) * 100)}%` },
        tickPct: Math.min(100, (target / m) * 100),
      }
    }),
    footLabel: 'Total this month', footValue: fmt(src.reduce((s, r) => s + r.bucket.closed, 0)),
  }
}
const boards = computed(() => [board('tech', 'Closed by technician'), board('location', 'Closed by location')])

const slowest = computed(() => {
  const T = targets.value
  return (mtd.value.byLocation || [])
    .filter(r => !unsetName(r.name) && r.bucket.resAvgMs > 0 && r.bucket.closed >= 5)
    .sort((a, b) => b.bucket.resAvgMs - a.bucket.resAvgMs).slice(0, 4)
    .map(r => ({
      name: r.name, value: dur(r.bucket.resAvgMs), first: dur(r.bucket.frtAvgMs),
      grade: gradeOf(T.resMs > 0 ? (T.resMs / r.bucket.resAvgMs) * 100 : 0),
    }))
})

const flow = computed(() => {
  const t = props.live?.trend || []
  const max = Math.max(1, ...t.map(p => Math.max(p.created, p.resolved)))
  const last = t.length - 1
  return t.map((p, i) => ({
    date: p.date, created: p.created, resolved: p.resolved, today: i === last,
    up: (p.created / max) * 100, down: (p.resolved / max) * 100,
  }))
})
const flowLabel = computed(() => `14 day flow: ${props.live?.createdLast7d ?? 0} created and ${props.live?.resolvedLast7d ?? 0} resolved in the last 7 days`)

// Click a row to open exactly that slice in Jira (month to date).
const openInJira = (row, dim) => {
  if (!props.baseUrl || !project.value || isFullscreen.value) return
  const p = mtd.value
  const bits = [`project = "${project.value.key}"`, `resolutiondate >= "${p.from}"`, `resolutiondate < "${p.to}"`]
  if (dim === 'location') bits.push(row.name === 'No location set' ? 'cf[10090] IS EMPTY' : `cf[10090] = "${row.name}"`)
  else if (row.accountId) bits.push(`assignee = "${row.accountId}"`)
  else bits.push('assignee IS EMPTY')
  window.open(`${props.baseUrl}/issues/?jql=${encodeURIComponent(bits.join(' AND '))}`, '_blank', 'noopener')
}

const fmt = (n) => (Number(n) || 0).toLocaleString()
const dur = (ms) => {
  if (!ms || ms <= 0) return '--'
  const s = ms / 1000
  if (s < 60) return `${Math.round(s)}s`
  const m = s / 60
  if (m < 60) return `${Math.round(m)}m`
  const h = m / 60
  if (h < 24) return `${h.toFixed(h < 10 ? 1 : 0)}h`
  return `${(h / 24).toFixed(1)}d`
}
const todayLong = computed(() => {
  const d = new Date(`${today.value.from || daily.value.today}T12:00:00`)
  return isNaN(d) ? '' : d.toLocaleDateString([], { weekday: 'long', day: 'numeric', month: 'long' })
})
</script>

<style scoped>
/* ====================================================================
   IT DAILY SNAPSHOT, WALL EDITION

   Built from the Overview's own vocabulary so the two read as one board:
   hairline panels, achromatic chrome, huge tight monospace numerals over
   tiny wide-tracked labels, colour only where something is happening.

   Fullscreen is a three-row grid pinned to the viewport (rail, KPIs,
   panels) and never scrolls; each panel's rows stretch to fill it.
   ==================================================================== */
.snap {
  display: flex; flex-direction: column; gap: 0.7rem; min-width: 0; width: 100%;
  --hair: hsl(var(--border));
  --hair-soft: hsl(var(--border) / 0.55);
  --surface: hsl(var(--card));
  --sunk: hsl(var(--muted) / 0.35);
  --mono: var(--j-mono, ui-monospace, Menlo, monospace);
  --on: var(--j-ok); --near: var(--j-warn); --behind: var(--j-crit);
}
.snap.wall {
  display: grid; grid-template-rows: auto auto minmax(0, 1fr); gap: 0.6rem;
  height: 100%; min-height: 0; overflow: hidden;
}
.snap.wall:has(.stalebar) { grid-template-rows: auto auto auto minmax(0, 1fr); }
.snap.wall:has(.stalebar ~ .stalebar) { grid-template-rows: auto auto auto auto minmax(0, 1fr); }
.stalebar.warm { border-color: var(--hair); background: var(--sunk); color: hsl(var(--muted-foreground)); }

/* ---- control rail ---- */
.ctl { display: flex; align-items: center; gap: 0.9rem; flex-wrap: wrap; }
.seg { display: inline-flex; border: 1px solid var(--hair); border-radius: 9px; overflow: hidden; }
.sgb { display: inline-flex; align-items: center; gap: 0.4rem; padding: 0.35rem 0.8rem; font-size: 0.82rem; font-weight: 700; color: hsl(var(--muted-foreground)); background: transparent; cursor: pointer; transition: color var(--dur-2) ease, background var(--dur-2) ease; }
.sgb + .sgb { border-left: 1px solid var(--hair-soft); }
.sgb:hover { color: hsl(var(--foreground)); background: hsl(var(--accent) / 0.4); }
.sgb.on { color: hsl(var(--foreground)); background: var(--sunk); box-shadow: inset 0 -2px 0 var(--j-info); }
.sgb kbd { font-family: var(--mono); font-size: 0.6rem; border: 1px solid var(--hair); border-radius: 3px; padding: 0.05rem 0.25rem; color: hsl(var(--muted-foreground)); }
.wall .sgb { font-size: 1rem; cursor: default; }
.ctl-date { font-size: 1.05rem; font-weight: 800; letter-spacing: -0.01em; white-space: nowrap; }
.ctl-date i { font-style: normal; font-family: var(--mono); font-size: 0.72rem; font-weight: 600; color: hsl(var(--muted-foreground)); margin-left: 0.5rem; }
.wall .ctl-date { font-size: 1.3rem; }
.ctl-rot { display: inline-flex; gap: 4px; }
.ctl-rot i { width: 18px; height: 3px; border-radius: 2px; background: var(--hair); }
.ctl-rot i.on { background: var(--j-info); }
.key { display: flex; gap: 0.7rem; margin: 0 0 0 auto; padding: 0; list-style: none; cursor: help; }
.key li { display: inline-flex; align-items: center; gap: 0.3rem; font-family: var(--mono); font-size: 0.66rem; font-weight: 700; color: hsl(var(--muted-foreground)); white-space: nowrap; }
.key i { width: 0.55rem; height: 0.55rem; border-radius: 2px; }
.key .g-on i { background: var(--on); } .key .g-near i { background: var(--near); } .key .g-behind i { background: var(--behind); }
.ctl-tgt, .ctl-feed { font-family: var(--mono); font-size: 0.68rem; color: hsl(var(--muted-foreground)); white-space: nowrap; font-variant-numeric: tabular-nums; letter-spacing: 0.03em; }
.ctl-feed.stale { color: var(--j-crit); font-weight: 700; }
.ctl-rf { color: hsl(var(--muted-foreground)); } .ctl-rf:hover:not(:disabled) { color: hsl(var(--foreground)); }

.stalebar { border: 1px solid color-mix(in srgb, var(--j-crit) 45%, transparent); background: color-mix(in srgb, var(--j-crit) 9%, transparent); color: var(--j-crit); border-radius: 9px; padding: 0.45rem 0.8rem; font-size: 0.85rem; font-weight: 600; }

/* ---- KPI rail ---- */
.kpis { display: grid; grid-template-columns: repeat(3, minmax(0, 0.8fr)) repeat(4, minmax(0, 1.15fr)) minmax(0, 1.8fr); border: 1px solid var(--hair); border-radius: 12px; background: var(--surface); overflow: hidden; }
.kpi { padding: 0.8rem 1rem; border-left: 1px solid var(--hair-soft); min-width: 0; display: flex; flex-direction: column; justify-content: center; }
.kpi:first-child { border-left: 0; }
.kpi.hot { box-shadow: inset 3px 0 0 currentColor; }
.kpi.tone-crit { color: var(--j-crit); } .kpi.tone-warn { color: var(--j-warn); }
.kpi-n { font-family: var(--mono); font-size: clamp(1.9rem, 3.2vw, 3.1rem); font-weight: 800; line-height: 0.95; letter-spacing: -0.045em; font-variant-numeric: tabular-nums; color: hsl(var(--foreground)); white-space: nowrap; }
.kpi-n.crit { color: var(--j-crit); } .kpi-n.warn { color: var(--j-warn); }
.kpi-n em { font-style: normal; font-size: clamp(0.62rem, 0.8vw, 0.8rem); font-weight: 700; letter-spacing: 0; color: hsl(var(--muted-foreground)); margin-left: 0.45rem; }
.kpi-l { font-family: var(--mono); font-size: 10px; font-weight: 700; letter-spacing: 0.16em; text-transform: uppercase; color: hsl(var(--muted-foreground)); margin-top: 0.5rem; display: flex; justify-content: space-between; gap: 0.5rem; }
.kpi-l b { letter-spacing: 0; }
/* A graded cell is marked by a left rule and a coloured percentage, not a
   tinted panel: four tints would compete, one rule marks one result. */
.kpi.graded { --gc: hsl(var(--border)); box-shadow: inset 3px 0 0 var(--gc); }
.kpi.graded.g-on { --gc: var(--on); } .kpi.graded.g-near { --gc: var(--near); } .kpi.graded.g-behind { --gc: var(--behind); }
.kpi.graded .kpi-l b { color: var(--gc); font-size: 0.8rem; }
.kpi.graded.g-behind .kpi-n { color: var(--behind); }

.flow { background: var(--sunk); gap: 0.4rem; }
.flow-head { display: flex; align-items: baseline; justify-content: space-between; gap: 0.75rem; }
.flow .kpi-l { margin-top: 0; }
.flow-today { font-family: var(--mono); font-size: 0.78rem; font-variant-numeric: tabular-nums; display: inline-flex; align-items: baseline; gap: 0.4rem; }
.flow-today .in { color: var(--j-info); } .flow-today .out { color: var(--j-ok); }
.flow-today em { font-style: normal; font-size: 0.62rem; letter-spacing: 0.12em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
.flowchart { display: flex; align-items: stretch; gap: 2px; height: 46px; }
.fday { flex: 1 1 0; min-width: 0; display: flex; flex-direction: column; }
.fup, .fdown { flex: 1 1 0; display: flex; } .fup { align-items: flex-end; }
.fup span, .fdown span { width: 100%; border-radius: 1px; min-height: 0; }
.fup span { background: var(--j-info); opacity: 0.75; align-self: flex-end; } .fdown span { background: var(--j-ok); opacity: 0.75; }
.faxis { height: 1px; background: var(--hair); flex-shrink: 0; }
.fday.today .fup span, .fday.today .fdown span { opacity: 1; }
.fday.today .faxis { background: hsl(var(--foreground) / 0.45); }

/* ---- panels ---- */
.panels { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.75rem; min-height: 0; }
.panel { display: flex; flex-direction: column; min-width: 0; min-height: 0; border: 1px solid var(--hair); border-radius: 12px; background: var(--surface); overflow: hidden; }
.p-head { display: flex; align-items: baseline; justify-content: space-between; gap: 0.6rem; padding: 0.55rem 0.9rem; border-bottom: 1px solid var(--hair); background: var(--sunk); flex: none; }
.p-t { font-family: var(--mono); font-size: 0.72rem; font-weight: 800; letter-spacing: 0.14em; text-transform: uppercase; }
.p-s { font-family: var(--mono); font-size: 0.66rem; color: hsl(var(--muted-foreground)); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.p-rows { list-style: none; margin: 0; padding: 0.2rem 0; flex: 1 1 auto; min-height: 0; overflow-y: auto; display: grid; align-content: start; }
.wall .p-rows { align-content: start; grid-auto-rows: minmax(3.4rem, 5.5rem); overflow: hidden; }
.wall .prow { align-content: center; }
.prow {
  display: grid; align-items: center; gap: 0.2rem 0.6rem; padding: 0.35rem 0.9rem;
  grid-template-columns: 1.1rem minmax(0, 1fr) auto auto; grid-template-areas: "rank name sec val" "bar bar bar bar";
  border-bottom: 1px solid var(--hair-soft); cursor: pointer; transition: background var(--dur-2) ease;
  --gc: hsl(var(--border));
}
.prow:last-child { border-bottom: 0; }
.prow:hover { background: hsl(var(--accent) / 0.45); }
.wall .prow { cursor: default; }
.prow.g-on { --gc: var(--on); } .prow.g-near { --gc: var(--near); } .prow.g-behind { --gc: var(--behind); }
.pr-rank { grid-area: rank; font-family: var(--mono); font-size: 0.7rem; font-weight: 700; color: hsl(var(--muted-foreground)); }
.pr-name { grid-area: name; min-width: 0; font-size: clamp(0.88rem, 1.05vw, 1.1rem); font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pr-sec { grid-area: sec; font-family: var(--mono); font-size: 0.72rem; color: hsl(var(--muted-foreground)); white-space: nowrap; font-variant-numeric: tabular-nums; }
.pr-val { grid-area: val; min-width: 3.2ch; text-align: right; font-family: var(--mono); font-size: clamp(1.1rem, 1.5vw, 1.55rem); font-weight: 800; letter-spacing: -0.03em; font-variant-numeric: tabular-nums; color: var(--gc); }
.prow.g-none .pr-val { color: hsl(var(--foreground)); }
.pr-bar { grid-area: bar; position: relative; height: 0.32rem; border-radius: 2px; background: hsl(var(--muted)); }
.pr-fill { position: absolute; inset: 0 auto 0 0; background: var(--gc); border-radius: 2px; transition: width 600ms var(--ease-out-quart); }
.pr-tick { position: absolute; top: -2px; bottom: -2px; width: 2px; background: hsl(var(--foreground) / 0.7); }
.p-empty { padding: 1.1rem 0.9rem; font-size: 0.85rem; color: hsl(var(--muted-foreground)); }
.p-foot { display: flex; justify-content: space-between; padding: 0.45rem 0.9rem; border-top: 1px solid var(--hair); font-family: var(--mono); font-size: 0.78rem; font-weight: 700; color: hsl(var(--muted-foreground)); flex: none; }
.p-foot b { color: hsl(var(--foreground)); font-size: 0.95rem; }

@media (max-width: 1100px) {
  .kpis { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .kpi { border-top: 1px solid var(--hair-soft); }
  .flow { grid-column: 1 / -1; }
  .panels { grid-template-columns: 1fr; }
}
@media (prefers-reduced-motion: reduce) { .animate-spin { animation: none; } .pr-fill { transition: none; } }

/* panels: a 12-person board has to fit, so rows share the height equally */
.panels { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) minmax(0, 0.9fr); }
.p-rows { display: grid; grid-template-rows: repeat(var(--n), minmax(0, 1fr)); padding: 0.3rem 0; }
.wall .p-rows { align-content: stretch; grid-auto-rows: unset; }
.snap:not(.wall) .p-rows { grid-template-rows: none; grid-auto-rows: 3rem; }
.prow { grid-template-columns: 1.1rem minmax(0, 1fr) auto minmax(3ch, auto); grid-template-areas: "rank name today val" "bar bar bar bar"; align-content: center; padding: 0 0.9rem; }
.wall .prow { align-content: center; }
.prow.muted { opacity: 0.6; }
.pr-today { grid-area: today; font-family: var(--mono); font-size: 0.8rem; font-weight: 700; color: transparent; font-variant-numeric: tabular-nums; }
.pr-today.on { color: var(--j-info); }
.pr-name { font-size: clamp(0.85rem, 1.1vw, 1.15rem); }
.pr-val { font-size: clamp(1.1rem, 1.6vw, 1.7rem); }
.kpis { grid-template-columns: repeat(3, minmax(0, 0.8fr)) repeat(4, minmax(0, 1.2fr)); }

.pace-body { flex: 1 1 auto; min-height: 0; padding: 0.8rem 0.9rem; display: flex; flex-direction: column; gap: 0.5rem; }
.pace-big { --gc: hsl(var(--foreground)); }
.pace-big.g-on { --gc: var(--on); } .pace-big.g-near { --gc: var(--near); } .pace-big.g-behind { --gc: var(--behind); }
.pace-n { font-family: var(--mono); font-size: clamp(2.6rem, 5vw, 4.6rem); font-weight: 800; line-height: 0.95; letter-spacing: -0.05em; color: var(--gc); font-variant-numeric: tabular-nums; }
.pace-l { display: flex; justify-content: space-between; font-family: var(--mono); font-size: 10px; font-weight: 700; letter-spacing: 0.16em; text-transform: uppercase; color: hsl(var(--muted-foreground)); margin-top: 0.4rem; }
.pace-l b { letter-spacing: 0; color: var(--gc); font-size: 0.8rem; }
.pace-track { position: relative; height: 0.8rem; border-radius: 3px; background: hsl(var(--muted)); }
.pace-fill { position: absolute; inset: 0 auto 0 0; border-radius: 3px; background: hsl(var(--muted-foreground)); }
.pace-fill.g-on { background: var(--on); } .pace-fill.g-near { background: var(--near); } .pace-fill.g-behind { background: var(--behind); }
.pace-day { position: absolute; top: -4px; bottom: -4px; width: 2px; background: hsl(var(--foreground)); }
.pace-note { font-family: var(--mono); font-size: 0.64rem; color: hsl(var(--muted-foreground)); margin-top: -0.2rem; }
.pace-body .flow-head { margin-top: 0.7rem; display: flex; justify-content: space-between; align-items: baseline; }
.pace-body .kpi-l { margin-top: 0; }
.pace-body .flowchart { height: clamp(46px, 9vh, 110px); }
.slow-l { margin-top: 0.7rem !important; }
.slow { list-style: none; margin: 0; padding: 0; display: grid; gap: 0.35rem; }
.slow li { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: 0.7rem; align-items: baseline; --gc: hsl(var(--foreground)); }
.slow li.g-behind { --gc: var(--behind); } .slow li.g-near { --gc: var(--near); } .slow li.g-on { --gc: var(--on); }
.s-name { font-size: clamp(0.85rem, 1.1vw, 1.1rem); font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.s-sec { font-family: var(--mono); font-size: 0.7rem; color: hsl(var(--muted-foreground)); }
.slow b { font-family: var(--mono); font-size: clamp(1rem, 1.4vw, 1.4rem); color: var(--gc); font-variant-numeric: tabular-nums; }
.s-none { font-size: 0.85rem; color: hsl(var(--muted-foreground)); }
.snap.wall { grid-template-rows: auto auto minmax(0, 1fr); }
.snap.wall:has(.stalebar) { grid-template-rows: auto auto auto minmax(0, 1fr); }
@media (max-width: 1100px) { .panels { grid-template-columns: 1fr; } }
</style>
