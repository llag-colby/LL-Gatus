<template>
  <section class="snap" :class="{ wall: isFullscreen }">

    <div class="ctl">
      <span class="ctl-date">{{ todayLong }}
        <i v-if="daily.monthDays">day {{ daily.monthDay }} of {{ daily.monthDays }}</i></span>
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

    <!-- One plain sentence first: what the scorecard below adds up to. -->
    <div v-if="sheet.length" class="brief">
      <b>{{ fmt(tot.open) }}</b> tickets open across {{ sheet.length }} spaces, <b>{{ fmt(tot.paused) }}</b> paused.
      Today <b>{{ fmt(tot.opened) }}</b> opened and <b>{{ fmt(tot.closed) }}</b> closed, so the backlog
      <b :class="tot.net > 0 ? 'bad' : tot.net < 0 ? 'good' : ''">{{ netWord(tot.net) }}</b>.
      Of the active tickets, <b :class="{ bad: tot.over3 }">{{ fmt(tot.over3) }}</b> are 3+ days old,
      <b :class="{ bad: tot.stale }">{{ fmt(tot.stale) }}</b> are stale and
      <b :class="{ bad: tot.sla }">{{ fmt(tot.sla) }}</b> missed an SLA.
    </div>

    <!-- THE SCORECARD: every space, every number, one table. Paused tickets
         (waiting on a customer or vendor) are left out of the age, stale and
         SLA figures and out of the grade, because the team cannot move them. -->
    <article v-if="sheet.length" class="panel sheet-panel">
      <div class="p-head"><span class="p-t">Spaces</span><span class="p-s">best grade first</span></div>
      <table class="sheet">
        <thead>
          <tr>
            <th class="l">Space</th>
            <th data-tooltip="Every ticket not finished, paused included" data-tip-pos="bottom">Open</th>
            <th data-tooltip="Waiting on a customer or vendor; the team cannot move these" data-tip-pos="bottom">Paused</th>
            <th data-tooltip="Open and not paused: the work the team can act on" data-tip-pos="bottom">Active</th>
            <th class="g">New</th><th>Closed</th><th data-tooltip="New minus closed today" data-tip-pos="bottom">Net</th>
            <th class="g">New</th><th>Closed</th>
            <th class="g" data-tooltip="Tickets finished so far this month" data-tip-pos="bottom">Month closed</th>
            <th class="g" data-tooltip="Active tickets created more than 3 days ago" data-tip-pos="bottom">3d+</th>
            <th data-tooltip="Active tickets with no update in 3 days or more" data-tip-pos="bottom">Stale</th>
            <th data-tooltip="Active tickets past their SLA" data-tip-pos="bottom">SLA missed</th>
            <th class="g" data-tooltip="Average opened per business day, last 2 weeks" data-tip-pos="bottom">In / day</th>
            <th data-tooltip="Average closed per business day, last 2 weeks" data-tip-pos="bottom">Out / day</th>
            <th>Pace</th>
            <th class="g">Grade</th>
          </tr>
          <tr class="grp">
            <th></th><th colspan="3">Queue</th><th colspan="3">Today</th><th colspan="2">Yesterday</th>
            <th>Month</th><th colspan="3">Needs attention</th><th colspan="3">Last 2 weeks</th><th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="x in sheet" :key="x.key" :class="{ sel: x.key === projectKey, idle: x.idle }">
            <td class="l"><b>{{ x.key }}</b><span>{{ x.name }}</span></td>
            <td>{{ fmt(x.open) }}</td>
            <td class="dim">{{ fmt(x.paused) }}</td>
            <td>{{ fmt(x.active) }}</td>
            <td class="g">{{ fmt(x.opened) }}</td><td>{{ fmt(x.closed) }}</td>
            <td :class="x.net > 0 ? 'bad' : x.net < 0 ? 'good' : 'dim'">{{ signed(x.net) }}</td>
            <td class="g">{{ fmt(x.yOpened) }}</td><td>{{ fmt(x.yClosed) }}</td>
            <td class="g">{{ fmt(x.mClosed) }}</td>
            <td class="g" :class="{ bad: x.over3 > 0 }">{{ fmt(x.over3) }}</td>
            <td :class="{ bad: x.stale > 0 }">{{ fmt(x.stale) }}</td>
            <td v-if="x.slaMeasured" :class="{ bad: x.sla > 0 }">{{ fmt(x.sla) }}</td>
            <td v-else class="dim">n/a</td>
            <td class="g">{{ x.inDay.toFixed(1) }}</td><td>{{ x.outDay.toFixed(1) }}</td>
            <td class="pace" :class="x.paceTone">{{ x.paceText }}</td>
            <td class="g grade-cell">
              <span v-if="x.grade" class="grade" :class="'gr-' + x.grade">{{ x.grade }}<em>{{ x.score }}</em></span>
              <span v-else class="dim">idle</span>
            </td>
          </tr>
        </tbody>
        <tfoot>
          <tr>
            <td class="l"><b>All spaces</b></td>
            <td>{{ fmt(tot.open) }}</td><td class="dim">{{ fmt(tot.paused) }}</td><td>{{ fmt(tot.active) }}</td>
            <td class="g">{{ fmt(tot.opened) }}</td><td>{{ fmt(tot.closed) }}</td>
            <td :class="tot.net > 0 ? 'bad' : tot.net < 0 ? 'good' : 'dim'">{{ signed(tot.net) }}</td>
            <td class="g">{{ fmt(tot.yOpened) }}</td><td>{{ fmt(tot.yClosed) }}</td>
            <td class="g">{{ fmt(tot.mClosed) }}</td>
            <td class="g" :class="{ bad: tot.over3 > 0 }">{{ fmt(tot.over3) }}</td>
            <td :class="{ bad: tot.stale > 0 }">{{ fmt(tot.stale) }}</td>
            <td :class="{ bad: tot.sla > 0 }">{{ fmt(tot.sla) }}</td>
            <td class="g">{{ tot.inDay.toFixed(1) }}</td><td>{{ tot.outDay.toFixed(1) }}</td>
            <td class="pace" :class="tot.paceTone">{{ tot.paceText }}</td>
            <td class="g"></td>
          </tr>
        </tfoot>
      </table>
    </article>

    <!-- Month-to-date leaderboards for the space picked in the header. -->
    <div class="panels">
      <article v-for="p in boards" :key="p.id" class="panel board">
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
  // The live project object from /jira/metrics, and every project beside it.
  live: { type: Object, default: null },
  projects: { type: Array, default: () => [] },
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
const periodOf = (proj, id) => (proj?.periods || []).find(x => x.id === id)
  || { id, label: id, bucket: EMPTY, slaPct: 0, byLocation: [], byTech: [], from: '' }
const periodFor = (id) => periodOf(project.value, id)
const today = computed(() => periodFor('today'))
const mtd = computed(() => periodFor('mtd'))
const targets = computed(() => project.value?.targets
  || { closedPerDay: 0, closedMonth: 0, frtMs: 0, resMs: 0, slaPct: 95, source: 'none' })

const unsetName = (n) => n === 'No location set' || n === 'Unassigned'
const gradeOf = (p) => {
  if (!isFinite(p) || p <= 0) return 'none'
  if (p >= 100) return 'on'
  if (p >= 90) return 'near'
  return 'behind'
}

// --- the scorecard ---------------------------------------------------------
// Statuses that mean "waiting on someone outside the team". Matched by name,
// case-insensitive, so a rename in Jira only needs this list to follow.
const PAUSED = new Set(['waiting for customer', 'pending', 'waiting on vendor', 'scheduled', 'paused'])
const DAY = 86400000
// Stale = no update for 3 days or more, the same rule as the daily email.
const staleCut = () => Date.now() - 3 * DAY
const gradeLetter = (s) => (s >= 90 ? 'A' : s >= 80 ? 'B' : s >= 70 ? 'C' : s >= 60 ? 'D' : 'F')

const sheet = computed(() => {
  void serverNow.value
  const cut = staleCut()
  const old = Date.now() - 3 * DAY
  const rows = (props.projects || []).map(p => {
    const issues = (p.issues || []).filter(i => i.category !== 'done')
    const paused = issues.filter(i => PAUSED.has((i.status || '').toLowerCase()))
    const act = issues.filter(i => !PAUSED.has((i.status || '').toLowerCase()))
    const dp = (daily.value.projects || []).find(d => d.key === p.key)
    const t = periodOf(dp, 'today').bucket, y = periodOf(dp, 'yesterday').bucket, m = periodOf(dp, 'mtd').bucket
    const opened = dp ? t.opened : (p.createdToday || 0)
    const closed = dp ? t.closed : (p.resolvedToday || 0)
    const over3 = act.filter(i => i.created && new Date(i.created).getTime() <= old).length
    const stl = act.filter(i => i.updated && new Date(i.updated).getTime() <= cut).length
    const slaMeasured = (p.slaBreached ?? -1) >= 0
    const sla = slaMeasured ? act.filter(i => i.slaBreached).length : 0
    const n14 = (p.trend || []).reduce((s, d) => s + (d.created || 0), 0)
    const c14 = (p.trend || []).reduce((s, d) => s + (d.resolved || 0), 0)
    const inDay = n14 / 10, outDay = c14 / 10
    const g = Math.round((inDay - outDay) * 10) / 10
    const idle = issues.length === 0 && opened === 0 && closed === 0
    const left = (n) => (act.length ? 100 - (n / act.length) * 100 : 100)
    const parts = [
      opened ? Math.min(closed / opened, 1) * 100 : 100,
      left(over3), left(stl),
      n14 ? Math.min(c14 / n14, 1) * 100 : 100,
    ]
    if (slaMeasured) parts.push(left(sla))
    const score = Math.round(parts.reduce((a, v) => a + v, 0) / parts.length)
    return {
      key: p.key, name: p.name, open: issues.length, paused: paused.length, active: act.length,
      opened, closed, net: opened - closed, yOpened: y.opened, yClosed: y.closed, mClosed: m.closed,
      over3, stale: stl, sla, slaMeasured, inDay, outDay,
      paceText: n14 + c14 === 0 ? 'no activity' : g > 0 ? `+${g.toFixed(1)} / day` : g < 0 ? `${g.toFixed(1)} / day` : 'flat',
      paceTone: n14 + c14 === 0 ? 'dim' : g > 0 ? 'bad' : 'good',
      idle, score, grade: idle ? null : gradeLetter(score),
    }
  })
  return rows.sort((a, b) => Number(a.idle) - Number(b.idle) || b.score - a.score || a.net - b.net)
})
const tot = computed(() => {
  const sum = (k) => sheet.value.reduce((s, x) => s + (x[k] || 0), 0)
  const inDay = sum('inDay'), outDay = sum('outDay')
  const g = Math.round((inDay - outDay) * 10) / 10
  return {
    open: sum('open'), paused: sum('paused'), active: sum('active'), opened: sum('opened'), closed: sum('closed'),
    net: sum('opened') - sum('closed'), yOpened: sum('yOpened'), yClosed: sum('yClosed'), mClosed: sum('mClosed'),
    over3: sum('over3'), stale: sum('stale'), sla: sum('sla'), inDay, outDay,
    paceText: inDay + outDay === 0 ? 'no activity' : g > 0 ? `+${g.toFixed(1)} / day` : g < 0 ? `${g.toFixed(1)} / day` : 'flat',
    paceTone: inDay + outDay === 0 ? 'dim' : g > 0 ? 'bad' : 'good',
  }
})
const signed = (n) => (n > 0 ? `+${fmt(n)}` : n < 0 ? `-${fmt(-n)}` : '0')
const netWord = (n) => (n > 0 ? `grew by ${n}` : n < 0 ? `shrank by ${-n}` : 'held flat')

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
    id: dim, title: `${title}${project.value ? ' · ' + project.value.key : ''}`, dim,
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
const todayLong = computed(() => {
  const d = new Date(`${today.value.from || daily.value.today}T12:00:00`)
  return isNaN(d) ? '' : d.toLocaleDateString([], { weekday: 'long', day: 'numeric', month: 'long' })
})
</script>

<style scoped>
/* ====================================================================
   IT DAILY SNAPSHOT, WALL EDITION

   Dense by design: a one-line summary, one scorecard table with every
   number for every space, then two compact month-to-date leaderboards.
   Hairline panels, achromatic chrome, mono numerals; colour only where a
   figure needs a person (red) or is going the right way (green).
   ==================================================================== */
.snap {
  display: flex; flex-direction: column; gap: 0.6rem; min-width: 0; width: 100%;
  --hair: hsl(var(--border));
  --hair-soft: hsl(var(--border) / 0.55);
  --surface: hsl(var(--card));
  --sunk: hsl(var(--muted) / 0.35);
  --mono: var(--j-mono, ui-monospace, Menlo, monospace);
  --on: var(--j-ok); --near: var(--j-warn); --behind: var(--j-crit);
}
.snap.wall { height: 100%; min-height: 0; overflow: hidden; }
.stalebar.warm { border-color: var(--hair); background: var(--sunk); color: hsl(var(--muted-foreground)); }

/* ---- control rail ---- */
.ctl { display: flex; align-items: center; gap: 0.9rem; flex-wrap: wrap; flex: none; }
.ctl-date { font-size: 1.05rem; font-weight: 800; letter-spacing: -0.01em; white-space: nowrap; }
.ctl-date i { font-style: normal; font-family: var(--mono); font-size: 0.72rem; font-weight: 600; color: hsl(var(--muted-foreground)); margin-left: 0.5rem; }
.wall .ctl-date { font-size: 1.3rem; }
.ctl-feed { margin-left: auto; font-family: var(--mono); font-size: 0.68rem; color: hsl(var(--muted-foreground)); white-space: nowrap; font-variant-numeric: tabular-nums; letter-spacing: 0.03em; }
.ctl-feed.stale { color: var(--j-crit); font-weight: 700; }
.ctl-rf { color: hsl(var(--muted-foreground)); } .ctl-rf:hover:not(:disabled) { color: hsl(var(--foreground)); }
.stalebar { flex: none; border: 1px solid color-mix(in srgb, var(--j-crit) 45%, transparent); background: color-mix(in srgb, var(--j-crit) 9%, transparent); color: var(--j-crit); border-radius: 9px; padding: 0.45rem 0.8rem; font-size: 0.85rem; font-weight: 600; }

.brief { flex: none; font-size: clamp(0.9rem, 1.15vw, 1.2rem); line-height: 1.5; color: hsl(var(--muted-foreground)); }
.brief b { font-family: var(--mono); color: hsl(var(--foreground)); font-variant-numeric: tabular-nums; }
.brief b.bad { color: var(--j-crit); } .brief b.good { color: var(--j-ok); }

/* ---- panels ---- */
.panel { display: flex; flex-direction: column; min-width: 0; min-height: 0; border: 1px solid var(--hair); border-radius: 12px; background: var(--surface); overflow: hidden; }
.p-head { display: flex; align-items: baseline; justify-content: space-between; gap: 0.6rem; padding: 0.45rem 0.9rem; border-bottom: 1px solid var(--hair); background: var(--sunk); flex: none; }
.p-t { font-family: var(--mono); font-size: 0.72rem; font-weight: 800; letter-spacing: 0.14em; text-transform: uppercase; }
.p-s { font-family: var(--mono); font-size: 0.66rem; color: hsl(var(--muted-foreground)); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

/* ---- scorecard table ---- */
.sheet-panel { flex: none; }
.sheet { width: 100%; border-collapse: collapse; font-variant-numeric: tabular-nums; }
.sheet th, .sheet td { text-align: right; padding: 0 0.7rem; white-space: nowrap; }
.sheet .l { text-align: left; }
.sheet thead th { font-family: var(--mono); font-size: 10px; font-weight: 700; letter-spacing: 0.1em; text-transform: uppercase; color: hsl(var(--muted-foreground)); padding-top: 0.5rem; padding-bottom: 0.3rem; }
.sheet thead tr.grp th { padding: 0 0.7rem 0.4rem; font-size: 9px; letter-spacing: 0.18em; text-align: center; border-bottom: 1px solid var(--hair); color: hsl(var(--muted-foreground) / 0.8); }
.sheet thead tr.grp th[colspan] { border-left: 1px solid var(--hair-soft); }
.sheet .g { border-left: 1px solid var(--hair-soft); }
.sheet tbody td { font-family: var(--mono); font-size: clamp(1.05rem, 1.6vw, 1.7rem); font-weight: 800; letter-spacing: -0.03em; height: clamp(2.6rem, 5.4vh, 4rem); border-bottom: 1px solid var(--hair-soft); }
.sheet tbody tr:last-child td { border-bottom: 0; }
.sheet tbody tr.sel td.l { box-shadow: inset 3px 0 0 var(--j-info); }
.sheet tbody tr.idle td { opacity: 0.55; }
.sheet td.l { font-family: inherit; letter-spacing: 0; }
.sheet td.l b { font-family: var(--mono); font-size: clamp(1.05rem, 1.5vw, 1.55rem); letter-spacing: -0.02em; margin-right: 0.6rem; }
.sheet td.l span { font-size: clamp(0.72rem, 0.85vw, 0.9rem); font-weight: 500; color: hsl(var(--muted-foreground)); }
.sheet .dim { color: hsl(var(--muted-foreground)); font-weight: 600; }
.sheet .bad { color: var(--j-crit); } .sheet .good { color: var(--j-ok); }
.sheet td.pace { font-size: clamp(0.78rem, 0.95vw, 1rem); font-weight: 700; letter-spacing: 0; }
.sheet tfoot td { font-family: var(--mono); font-size: clamp(1.05rem, 1.6vw, 1.7rem); font-weight: 800; letter-spacing: -0.03em; height: clamp(2.6rem, 5.2vh, 3.8rem); background: var(--sunk); border-top: 1px solid var(--hair); }
.sheet tfoot td.l b { font-size: clamp(0.8rem, 1vw, 1rem); letter-spacing: 0.1em; text-transform: uppercase; }
.sheet tfoot td.pace { font-size: clamp(0.78rem, 0.95vw, 1rem); font-weight: 700; letter-spacing: 0; }
.sheet .grade-cell { text-align: center; }
.grade { display: inline-flex; align-items: baseline; gap: 0.35rem; padding: 0.05rem 0.7rem; border-radius: 6px; color: #fff; font-family: var(--mono); font-weight: 800; }
.grade em { font-style: normal; font-size: 0.55em; font-weight: 700; opacity: 0.9; }
.gr-A { background: var(--j-ok); } .gr-B { background: var(--j-info); }
.gr-C { background: var(--j-warn); } .gr-D { background: var(--j-warn); }
.gr-F { background: var(--j-crit); }

/* ---- compact leaderboards ---- */
.panels { flex: 1 1 auto; min-height: 0; display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.6rem; }
.p-rows { list-style: none; margin: 0; padding: 0.2rem 0; flex: 1 1 auto; min-height: 0; overflow: hidden; display: grid; grid-template-rows: repeat(var(--n), minmax(0, 1fr)); }
.snap:not(.wall) .p-rows { grid-template-rows: none; grid-auto-rows: 2.4rem; }
.prow {
  display: grid; align-items: center; align-content: center; gap: 0.15rem 0.6rem; padding: 0 0.9rem;
  grid-template-columns: 1.1rem minmax(0, 1fr) auto minmax(3ch, auto); grid-template-areas: "rank name today val" "bar bar bar bar";
  border-bottom: 1px solid var(--hair-soft); cursor: pointer; transition: background var(--dur-2) ease;
  --gc: hsl(var(--border));
}
.prow:last-child { border-bottom: 0; }
.prow:hover { background: hsl(var(--accent) / 0.45); }
.wall .prow { cursor: default; }
.prow.g-on { --gc: var(--on); } .prow.g-near { --gc: var(--near); } .prow.g-behind { --gc: var(--behind); }
.prow.muted { opacity: 0.6; }
.pr-rank { grid-area: rank; font-family: var(--mono); font-size: 0.66rem; font-weight: 700; color: hsl(var(--muted-foreground)); }
.pr-name { grid-area: name; min-width: 0; font-size: clamp(0.8rem, 0.95vw, 1rem); font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pr-today { grid-area: today; font-family: var(--mono); font-size: 0.72rem; font-weight: 700; color: transparent; font-variant-numeric: tabular-nums; }
.pr-today.on { color: var(--j-info); }
.pr-val { grid-area: val; min-width: 3ch; text-align: right; font-family: var(--mono); font-size: clamp(0.95rem, 1.2vw, 1.3rem); font-weight: 800; letter-spacing: -0.03em; font-variant-numeric: tabular-nums; color: var(--gc); }
.prow.g-none .pr-val { color: hsl(var(--foreground)); }
.pr-bar { grid-area: bar; position: relative; height: 0.26rem; border-radius: 2px; background: hsl(var(--muted)); }
.pr-fill { position: absolute; inset: 0 auto 0 0; background: var(--gc); border-radius: 2px; transition: width 600ms var(--ease-out-quart); }
.pr-tick { position: absolute; top: -2px; bottom: -2px; width: 2px; background: hsl(var(--foreground) / 0.7); }
.p-empty { padding: 1.1rem 0.9rem; font-size: 0.85rem; color: hsl(var(--muted-foreground)); }
.p-foot { display: flex; justify-content: space-between; padding: 0.35rem 0.9rem; border-top: 1px solid var(--hair); font-family: var(--mono); font-size: 0.72rem; font-weight: 700; color: hsl(var(--muted-foreground)); flex: none; }
.p-foot b { color: hsl(var(--foreground)); font-size: 0.85rem; }

@media (max-width: 1100px) {
  .snap.wall { height: auto; overflow: visible; }
  .panels { grid-template-columns: 1fr; }
  .sheet-panel { overflow-x: auto; }
}
@media (prefers-reduced-motion: reduce) { .animate-spin { animation: none; } .pr-fill { transition: none; } }
</style>
