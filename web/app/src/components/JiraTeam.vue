<template>
  <section class="team" :class="{ wall: isFullscreen }">

    <!-- NOT CONFIGURED / BROKEN ------------------------------------------ -->
    <div v-if="loaded && !data.configured" class="notice">
      <div class="notice-title">Jira is not connected yet</div>
      <p class="notice-body">
        Set <code>JIRA_BASE_URL</code>, <code>JIRA_EMAIL</code> and <code>JIRA_API_TOKEN</code>
        in <code>.env</code>, then <code>docker compose up -d</code>.
      </p>
    </div>

    <!-- Counting. The route answers at once and the pass runs behind it, so an
         empty payload with computing set is normal on a cold start, not an
         error, and must be checked before the failure branch. -->
    <div v-else-if="!loaded || (!data.ok && data.computing)" class="notice">
      <div class="notice-title">
        <RefreshCw class="h-4 w-4 animate-spin" /> Counting tickets…
      </div>
      <p class="notice-body">
        Five windows per project, each paged out of Jira. The month windows are
        the slow ones; the result is then cached for three minutes, so this wait
        happens once.
      </p>
    </div>

    <div v-else-if="!data.ok" class="notice notice-error">
      <div class="notice-title"><AlertTriangle class="h-4 w-4" /> Cannot read the team breakdown</div>
      <pre class="err-pre">{{ data.error || 'Jira returned no usable windows.' }}</pre>
    </div>

    <div v-else-if="!board" class="notice">
      <div class="notice-title">No data for {{ projectKey }}</div>
      <p class="notice-body">The breakdown carries no window for this project.</p>
    </div>

    <template v-else>
      <!-- ============================ FLOW LINE ========================= -->
      <!-- The day and the month in one line of numbers, because the first
           question a team board gets asked is whether the queue is growing. -->
      <div class="flow">
        <div class="f-id">
          <span class="f-key">{{ board.key }}</span>
          <span class="f-name">{{ board.name }}</span>
        </div>

        <div class="f-nums">
          <div class="f-cell">
            <span class="f-n">{{ fmt(total('open')) }}</span>
            <span class="f-l">Open</span>
          </div>
          <div class="f-cell">
            <span class="f-n tone-in">{{ fmt(total('createdToday')) }}</span>
            <span class="f-l">In today</span>
          </div>
          <div class="f-cell">
            <span class="f-n tone-out">{{ fmt(total('resolvedToday')) }}</span>
            <span class="f-l">Done today</span>
          </div>
          <div class="f-cell f-net" :class="netTone"
            data-tooltip="Submitted today minus completed today. Positive means the queue grew."
            data-tip-pos="bottom">
            <span class="f-n">{{ netLabel }}</span>
            <span class="f-l">Net today</span>
          </div>
          <div class="f-sep"></div>
          <div class="f-cell">
            <span class="f-n">{{ fmt(total('resolvedMonth')) }}</span>
            <span class="f-l">Done MTD</span>
          </div>
          <div class="f-cell">
            <span class="f-n muted">{{ fmt(total('resolvedLastMonth')) }}</span>
            <span class="f-l">Last month</span>
          </div>
          <div class="f-cell" :data-tooltip="paceTip" data-tip-pos="bottom">
            <span class="f-n" :class="paceTone">{{ fmt(pace) }}</span>
            <span class="f-l">On pace</span>
          </div>
        </div>

        <div class="f-meta">
          <span class="f-age">{{ ageLabel }}</span>
          <button class="f-refresh" :disabled="loading" @click="forceRefresh"
            data-tooltip="Recompute now, bypassing the cache" data-tip-pos="bottom">
            <RefreshCw class="h-4 w-4" :class="{ 'animate-spin': loading }" />
          </button>
        </div>
      </div>

      <!-- ============================ ROSTER MATRIX ===================== -->
      <!-- Everyone against every window at once. The five boxes below each
           answer one question; this answers the comparison between them,
           which is the thing you cannot get by reading them one at a time. -->
      <article class="box b-matrix">
        <header class="bh">
          <h3 class="bt">Roster</h3>
          <span class="bsub">{{ roster.length }} {{ roster.length === 1 ? 'person' : 'people' }} · every window</span>
        </header>

        <div class="tscroll">
          <table class="t t-matrix">
            <thead>
              <tr>
                <th class="c-who">
                  <button class="sortable" :class="{ on: sortBy === 'name' }" @click="sort('name')">Assignee</button>
                </th>
                <th v-for="c in MATRIX_COLS" :key="c.id" class="c-n">
                  <button class="sortable" :class="{ on: sortBy === c.id }" @click="sort(c.id)"
                    :data-tooltip="c.tip" data-tip-pos="bottom">{{ c.label }}</button>
                </th>
                <th class="c-bar">Share of open</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in roster" :key="r.id" :class="{ idle: r.open === 0, nobody: !r.accountId }">
                <td class="c-who">
                  <span class="dot" :style="{ background: colourFor(r.id) }"></span>
                  <span class="who" :title="r.name">{{ r.name }}</span>
                </td>
                <td v-for="c in MATRIX_COLS" :key="c.id" class="c-n">
                  <a v-if="r[c.id] > 0 && baseUrl" class="n link" :href="rowLink(c.id, r)"
                    target="_blank" rel="noopener">{{ fmt(r[c.id]) }}</a>
                  <span v-else class="n" :class="{ zero: !r[c.id] }">{{ r[c.id] ? fmt(r[c.id]) : '·' }}</span>
                </td>
                <td class="c-bar">
                  <div class="barwrap">
                    <div class="mbar">
                      <span class="mfill" :style="{ width: pct(r.open, total('open')) + '%', background: colourFor(r.id) }"></span>
                    </div>
                    <span class="mpct">{{ pctLabel(r.open, total('open')) }}</span>
                  </div>
                </td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <td class="c-who">Total</td>
                <td v-for="c in MATRIX_COLS" :key="c.id" class="c-n">
                  <span class="n">{{ fmt(total(c.id)) }}</span>
                </td>
                <td class="c-bar"><span class="mpct">100%</span></td>
              </tr>
            </tfoot>
          </table>
        </div>
      </article>

      <!-- ============================ WINDOW BOXES ====================== -->
      <article v-for="w in windows" :key="w.id" class="box b-win" :class="'w-' + w.id">
        <header class="bh">
          <h3 class="bt">{{ w.label }}</h3>
          <div class="bnum">
            <a v-if="baseUrl && !w.error" class="btotal link" :href="windowLink(w)"
              target="_blank" rel="noopener"
              data-tooltip="Open this exact query in Jira" data-tip-pos="left">{{ fmt(w.total) }}</a>
            <span v-else class="btotal">{{ fmt(w.total) }}</span>
            <span v-if="w.truncated" class="bflag"
              data-tooltip="Hit the fetch cap: the real number is higher" data-tip-pos="left">capped</span>
          </div>
        </header>

        <pre v-if="w.error" class="err-pre small">{{ w.error }}</pre>

        <p v-else-if="!w.rows || !w.rows.length" class="bempty">Nothing in this window.</p>

        <div v-else class="tscroll tscroll-win">
          <table class="t t-win">
            <thead>
              <tr>
                <th class="c-who">Assignee</th>
                <th class="c-n">Count</th>
                <th class="c-pc">%</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(r, i) in w.rows" :key="r.accountId || r.name" :class="{ lead: i === 0 }">
                <td class="c-who">
                  <span class="wfill" :style="{ width: pct(r.count, w.total) + '%', background: colourFor(idOf(r)) }"></span>
                  <span class="dot" :style="{ background: colourFor(idOf(r)) }"></span>
                  <span class="who" :title="r.name">{{ r.name }}</span>
                </td>
                <td class="c-n">
                  <a v-if="baseUrl" class="n link" :href="cellLink(w, r)" target="_blank" rel="noopener">{{ fmt(r.count) }}</a>
                  <span v-else class="n">{{ fmt(r.count) }}</span>
                </td>
                <td class="c-pc">{{ pctLabel(r.count, w.total) }}</td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <td class="c-who">Total</td>
                <td class="c-n"><span class="n">{{ fmt(w.total) }}</span></td>
                <td class="c-pc">100%</td>
              </tr>
            </tfoot>
          </table>
        </div>
      </article>

      <!-- ============================ EFFORT & TIMING ===================
           What the work actually cost. The source matters and is stated on
           the page: this instance has no worklogs at all, so these come from
           the JSM SLA clocks, which are business-hours aware and pause while
           a ticket waits on the customer. That makes the resolution clock a
           closer proxy for agent time than anything a human would have
           remembered to type in. -->
      <template v-if="effort">
        <article class="box b-matrix prov">
          <header class="bh">
            <h3 class="bt">Effort &amp; timing</h3>
            <span class="bsub">
              {{ effort.sampled.toLocaleString() }} tickets resolved in the last
              {{ effort.windowDays }} days<span v-if="effort.truncated"> (capped)</span>
            </span>
          </header>

          <p v-if="effort.error" class="provnote err">{{ effort.error }}</p>
          <p v-else-if="effort.noSlaData" class="provnote err">
            No SLA data &mdash; wall clock only
          </p>
          <p v-else class="provnote"
            data-tooltip="Nobody logs labour in this Jira: timespent is zero on every ticket in the instance. On-desk time is the Time-to-resolution SLA clock — business hours only, paused while waiting on the customer. It measures how long a ticket was the desk's problem, not how long anyone worked on it. Wall clock is created to resolved with nothing excluded."
            data-tip-pos="bottom">
            <Info class="h-3 w-3" />
            <span>SLA clock, business hours, paused on customer wait &mdash; not logged labour</span>
          </p>

          <div class="estrip">
            <div class="ecell"
              data-tooltip="Business-hours time these tickets were open and not waiting on the customer. Elapsed time on the desk, not hours worked."
              data-tip-pos="bottom">
              <span class="e-n">{{ hours(effort.workTotalMs) }}</span>
              <span class="e-l">Desk hours</span>
            </div>
            <div class="ecell">
              <span class="e-n">{{ dur(effort.workMedianMs) }}</span>
              <span class="e-l">Median on desk</span>
            </div>
            <div class="ecell">
              <span class="e-n">{{ dur(effort.workP90Ms) }}</span>
              <span class="e-l">p90 on desk</span>
            </div>
            <div class="e-sep"></div>
            <div class="ecell">
              <span class="e-n muted">{{ dur(effort.cycleMedianMs) }}</span>
              <span class="e-l">Median wall clock</span>
            </div>
            <div class="ecell" :data-tooltip="waitTip" data-tip-pos="bottom">
              <span class="e-n" :class="waitTone">{{ waitPct }}</span>
              <span class="e-l">Share on desk</span>
            </div>
            <div class="e-sep"></div>
            <div class="ecell">
              <span class="e-n">{{ dur(effort.frtMedianMs) }}</span>
              <span class="e-l">Median first reply</span>
            </div>
            <div class="ecell"
              data-tooltip="Both SLA clocks stopped at the same elapsed time, so the ticket was done at the first reply"
              data-tip-pos="bottom">
              <span class="e-n tone-ok">{{ firstTouchPct }}</span>
              <span class="e-l">First-touch fix</span>
            </div>
            <div class="ecell" :class="{ bad: effort.breaches > 0 }">
              <span class="e-n">{{ effort.breaches.toLocaleString() }}</span>
              <span class="e-l">SLA breaches</span>
            </div>
          </div>
        </article>

        <!-- Per-person. Medians, never means: the real distribution runs from
             seconds to weeks, so a mean would describe nobody's actual day. -->
        <article class="box b-matrix">
          <header class="bh">
            <h3 class="bt">Time per person</h3>
            <span class="bsub">medians, not averages</span>
          </header>
          <table class="t t-matrix">
            <thead>
              <tr>
                <th class="c-who">Assignee</th>
                <th class="c-n" data-tooltip="Tickets they resolved in the window" data-tip-pos="bottom">Closed</th>
                <th class="c-n" data-tooltip="Median business-hours time the ticket sat with the desk" data-tip-pos="bottom">Median</th>
                <th class="c-n" data-tooltip="Their slowest tenth" data-tip-pos="bottom">p90</th>
                <th class="c-n" data-tooltip="Total business-hours time on the desk across the window. Elapsed, not hours worked." data-tip-pos="bottom">Desk h</th>
                <th class="c-n" data-tooltip="Median time to first reply" data-tip-pos="bottom">1st reply</th>
                <th class="c-n" data-tooltip="Resolved at the first reply" data-tip-pos="bottom">1-touch</th>
                <th class="c-n" data-tooltip="Resolution SLAs met" data-tip-pos="bottom">SLA</th>
                <th class="c-bar">Share of desk time</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in effort.rows" :key="r.accountId || r.name">
                <td class="c-who">
                  <span class="dot" :style="{ background: colourFor(r.accountId || ('name:' + r.name)) }"></span>
                  <span class="who" :title="r.name">{{ r.name }}</span>
                </td>
                <td class="c-n"><span class="n">{{ r.resolved.toLocaleString() }}</span></td>
                <td class="c-n">{{ dur(r.workMedianMs) }}</td>
                <td class="c-n">{{ dur(r.workP90Ms) }}</td>
                <td class="c-n"><span class="n">{{ hours(r.workTotalMs) }}</span></td>
                <td class="c-n">{{ dur(r.frtMedianMs) }}</td>
                <td class="c-n">{{ ratio(r.firstTouch, r.measured) }}</td>
                <td class="c-n" :class="{ slabad: r.slaBreachRes > 0 }">
                  {{ ratio(r.slaMetRes, r.slaMetRes + r.slaBreachRes) }}
                </td>
                <td class="c-bar">
                  <div class="barwrap">
                    <div class="mbar">
                      <span class="mfill"
                        :style="{ width: pct(r.workTotalMs, effort.workTotalMs) + '%', background: colourFor(r.accountId || ('name:' + r.name)) }"></span>
                    </div>
                    <span class="mpct">{{ pctLabel(r.workTotalMs, effort.workTotalMs) }}</span>
                  </div>
                </td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <td class="c-who">Total</td>
                <td class="c-n"><span class="n">{{ effort.sampled.toLocaleString() }}</span></td>
                <td class="c-n">{{ dur(effort.workMedianMs) }}</td>
                <td class="c-n">{{ dur(effort.workP90Ms) }}</td>
                <td class="c-n"><span class="n">{{ hours(effort.workTotalMs) }}</span></td>
                <td class="c-n">{{ dur(effort.frtMedianMs) }}</td>
                <td class="c-n">{{ firstTouchPct }}</td>
                <td class="c-n">&mdash;</td>
                <td class="c-bar"><span class="mpct">100%</span></td>
              </tr>
            </tfoot>
          </table>
        </article>

        <!-- How long tickets take, in time order. A histogram sorted by
             frequency is not a histogram. -->
        <article class="box b-win">
          <header class="bh">
            <h3 class="bt">How long tickets take</h3>
            <span class="bsub">business hours on the desk</span>
          </header>
          <table class="t t-win">
            <tbody>
              <tr v-for="b in effort.buckets" :key="b.name">
                <td class="c-who">
                  <span class="wfill"
                    :style="{ width: pct(b.count, bucketMax) + '%', background: 'var(--j-info)' }"></span>
                  <span class="who">{{ b.name }}</span>
                </td>
                <td class="c-n"><span class="n">{{ b.count.toLocaleString() }}</span></td>
                <td class="c-pc">{{ pctLabel(b.count, effort.measured) }}</td>
              </tr>
            </tbody>
          </table>
        </article>

        <!-- WHEN the desk closes tickets. Hour-of-day comes from each
             ticket's own Jira offset, so these are the desk's local hours
             (US/Central here), not UTC - which would smear the working day
             across midnight. -->
        <article class="box b-heat">
          <header class="bh">
            <h3 class="bt">When tickets get closed</h3>
            <span class="bsub">local time &middot; peak {{ heatPeak }} in an hour</span>
          </header>
          <div class="heat">
            <template v-for="(row, d) in heatRows" :key="d">
              <span class="heat-day">{{ DAYS[d] }}</span>
              <span v-for="(n, h) in row" :key="h" class="cellx"
                :class="{ none: !n, top: n === heatPeak && n > 0 }"
                :style="{ opacity: n ? (0.2 + 0.8 * (n / heatPeak)) : 1 }"
                :data-tooltip="DAYS[d] + ' ' + String(h).padStart(2, '0') + ':00 — ' + n + ' closed'"></span>
            </template>
            <span class="heat-day"></span>
            <span v-for="h in 24" :key="'h' + h" class="hh">{{ (h - 1) % 3 === 0 ? (h - 1) : '' }}</span>
          </div>
        </article>
      </template>
    </template>
  </section>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { AlertTriangle, RefreshCw, Info } from 'lucide-vue-next'
import { isFullscreen, now as serverNow } from '@/store'

const props = defineProps({
  projectKey: { type: String, default: '' },
  baseUrl: { type: String, default: '' },
})

// The matrix columns, in the order the windows are asked about. Open first
// because it is the only one that is a live load rather than a past event.
const MATRIX_COLS = [
  { id: 'open', label: 'Open', tip: 'Not in a Done status category' },
  { id: 'createdToday', label: 'In', tip: 'Submitted today' },
  { id: 'resolvedToday', label: 'Done', tip: 'Completed today' },
  { id: 'resolvedMonth', label: 'MTD', tip: 'Completed this month to date' },
  { id: 'resolvedLastMonth', label: 'Prev', tip: 'Completed last month, in full' },
]

// Eight hues, enough that a desk of this size never repeats, and the same
// tokens the rest of the Jira view draws its categories from.
const CAT = ['--j-cat-1', '--j-cat-2', '--j-cat-3', '--j-cat-4', '--j-cat-5', '--j-cat-6', '--j-cat-7', '--j-cat-8']

const data = ref({ configured: false, ok: false, projects: [] })
const loaded = ref(false)
const loading = ref(false)

const load = async (force = false) => {
  loading.value = true
  try {
    const r = await fetch(`/api/v1/jira/breakdown${force ? '?refresh=1' : ''}`, { cache: 'no-store' })
    if (r.ok) data.value = await r.json()
  } catch (e) {
    // The route always answers 200 with its own error field, so reaching here
    // means the dashboard itself is unreachable; the age counter will show it.
  } finally {
    loading.value = false
    loaded.value = true
  }
}

// Two cadences. While a pass is running the numbers are not on screen yet, so
// poll often enough that they appear promptly; once they are, back off to a
// minute. The server caches for three minutes either way, so most of these
// polls never reach Jira at all.
//
// A chain of timeouts rather than setInterval: the next delay is chosen after
// each poll from what came back, and a slow response can never stack a second
// poll on top of the one still in flight.
const FAST_MS = 4_000
const IDLE_MS = 60_000
let timer = null
const pump = async () => {
  await load()
  timer = setTimeout(pump, data.value.computing ? FAST_MS : IDLE_MS)
}
onMounted(pump)
onUnmounted(() => clearTimeout(timer))

// A forced recompute starts a pass, so restart the chain at the fast cadence
// to collect its result rather than waiting out the idle minute.
const forceRefresh = async () => {
  clearTimeout(timer)
  await load(true)
  timer = setTimeout(pump, FAST_MS)
}

const board = computed(() => {
  const list = data.value.projects || []
  return list.find(p => p.key === props.projectKey) || list[0] || null
})
const windows = computed(() => board.value?.windows || [])
const windowById = (id) => windows.value.find(w => w.id === id) || null
const total = (id) => windowById(id)?.total || 0

// --- the roster ----------------------------------------------------------
// One row per person who appears in ANY window, so somebody who closed four
// tickets and has none open still shows up with their work visible.
const sortBy = ref('open')
const sortDir = ref('desc')
const sort = (id) => {
  if (sortBy.value === id) {
    sortDir.value = sortDir.value === 'desc' ? 'asc' : 'desc'
  } else {
    sortBy.value = id
    sortDir.value = id === 'name' ? 'asc' : 'desc'
  }
}

const idOf = (row) => row.accountId || `name:${row.name}`

// --- effort & timing -----------------------------------------------------
const effort = computed(() => board.value?.effort || null)

// Durations are formatted at the precision that is meaningful at that scale:
// seconds below a minute, whole minutes below an hour, one decimal of an hour
// below a day. A median of "0.31h" tells a human nothing; "18m" does.
const dur = (ms) => {
  if (!ms || ms <= 0) return '\u2014'
  const secs = ms / 1000
  if (secs < 60) return `${Math.round(secs)}s`
  const mins = secs / 60
  if (mins < 60) return `${Math.round(mins)}m`
  const hrs = mins / 60
  if (hrs < 24) return `${hrs.toFixed(hrs < 10 ? 1 : 0)}h`
  return `${(hrs / 24).toFixed(1)}d`
}
const hours = (ms) => (!ms || ms <= 0 ? '0' : (ms / 3600000).toFixed(ms < 36000000 ? 1 : 0))
const ratio = (n, of) => (of > 0 ? `${Math.round((n / of) * 100)}%` : '\u2014')

const waitPct = computed(() => {
  const r = effort.value?.waitRatio
  return r > 0 ? `${Math.round(r * 100)}%` : '\u2014'
})
// A low figure is not "bad" - it means tickets sit waiting on the customer
// rather than on the desk - so it is informational, not a failure colour.
const waitTone = computed(() => {
  const r = effort.value?.waitRatio || 0
  return r > 0 && r < 0.4 ? 'tone-in' : ''
})
const waitTip = computed(() => {
  const e = effort.value
  if (!e || !e.waitRatio) return 'On-desk time as a share of wall-clock time'
  const rest = Math.round((1 - e.waitRatio) * 100)
  return `On-desk business hours as a share of total wall-clock time. The other ${rest}% was waiting - on the customer, on a vendor, or outside working hours.`
})
const firstTouchPct = computed(() => {
  const e = effort.value
  return e && e.measured > 0 ? `${Math.round((e.firstTouch / e.measured) * 100)}%` : '\u2014'
})
const bucketMax = computed(() =>
  (effort.value?.buckets || []).reduce((m, b) => Math.max(m, b.count), 0))

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
// A dense 7x24 grid built from the sparse cells the API returns, so an hour
// with nothing in it is a real zero rather than a gap in the layout.
const heatRows = computed(() => {
  const grid = Array.from({ length: 7 }, () => Array(24).fill(0))
  for (const c of effort.value?.heatmap || []) {
    if (c.day >= 0 && c.day < 7 && c.hour >= 0 && c.hour < 24) grid[c.day][c.hour] = c.count
  }
  return grid
})
const heatPeak = computed(() =>
  (effort.value?.heatmap || []).reduce((m, c) => Math.max(m, c.count), 0))

const roster = computed(() => {
  const people = new Map()
  for (const w of windows.value) {
    for (const r of w.rows || []) {
      const id = idOf(r)
      let person = people.get(id)
      if (!person) {
        person = { id, name: r.name, accountId: r.accountId || '' }
        for (const c of MATRIX_COLS) person[c.id] = 0
        people.set(id, person)
      }
      person[w.id] = r.count
    }
  }
  const rows = [...people.values()]
  const dir = sortDir.value === 'desc' ? -1 : 1
  rows.sort((a, b) => {
    if (sortBy.value === 'name') return a.name.localeCompare(b.name) * dir
    const delta = (a[sortBy.value] || 0) - (b[sortBy.value] || 0)
    // Name as the tie-break so the order does not shuffle between refreshes.
    return delta !== 0 ? delta * dir : a.name.localeCompare(b.name)
  })
  return rows
})

// Stable colour per person: hashed from their identity rather than taken from
// their position, so sorting the table does not recolour everyone.
const colourFor = (id) => {
  let hash = 0
  for (let i = 0; i < id.length; i++) hash = (hash * 31 + id.charCodeAt(i)) | 0
  return `var(${CAT[Math.abs(hash) % CAT.length]})`
}

// --- flow line -----------------------------------------------------------
const net = computed(() => total('createdToday') - total('resolvedToday'))
const netLabel = computed(() => (net.value > 0 ? `+${net.value}` : String(net.value)))
const netTone = computed(() => (net.value > 0 ? 'tone-grow' : net.value < 0 ? 'tone-shrink' : 'tone-flat'))

// Linear extrapolation from the days gone in the month. Labelled "on pace" and
// explained in the tooltip rather than presented as a forecast, because it
// assumes the rest of the month looks like the part that has happened.
const monthProgress = computed(() => {
  const d = new Date(serverNow.value)
  const days = new Date(d.getFullYear(), d.getMonth() + 1, 0).getDate()
  return { day: d.getDate(), days }
})
const pace = computed(() => {
  const { day, days } = monthProgress.value
  if (!day) return 0
  return Math.round((total('resolvedMonth') / day) * days)
})
const paceTone = computed(() => {
  const prev = total('resolvedLastMonth')
  if (!prev) return ''
  return pace.value >= prev ? 'tone-out' : 'tone-warn'
})
const paceTip = computed(() => {
  const { day, days } = monthProgress.value
  const prev = total('resolvedLastMonth')
  const base = `${total('resolvedMonth')} completed over ${day} of ${days} days, carried forward at the same rate`
  return prev ? `${base}. Last month finished on ${prev}.` : base
})

const ageLabel = computed(() => {
  if (!data.value.updatedAt) return '—'
  const stamp = new Date(data.value.updatedAt).getTime()
  if (!stamp) return '—'
  const secs = Math.max(0, Math.round((serverNow.value - stamp) / 1000))
  const age = secs < 60 ? `${secs}s` : `${Math.floor(secs / 60)}m`
  if (data.value.computing) return `${age} old · recounting`
  return data.value.stale ? `${age} old · stale` : `${age} old`
})

// --- formatting ----------------------------------------------------------
const fmt = (n) => (n || 0).toLocaleString()
const pct = (n, of) => (of > 0 ? (n / of) * 100 : 0)
const pctLabel = (n, of) => {
  if (!of) return '—'
  const p = (n / of) * 100
  // Whole numbers above 10 percent, one decimal below it: on a desk of a dozen
  // people a 0 percent row would otherwise appear for real work.
  return p >= 10 || p === 0 ? `${Math.round(p)}%` : `${p.toFixed(1)}%`
}

// --- deep links ----------------------------------------------------------
// Each number links to the same question in Jira, which makes the figure
// checkable instead of something this page merely asserts.
const navLink = (jql) => `${props.baseUrl}/issues/?jql=${encodeURIComponent(jql)}`
const windowLink = (w) => navLink(w.jql || '')
const withAssignee = (jql, row) =>
  row.accountId ? `${jql} AND assignee = "${row.accountId}"` : `${jql} AND assignee IS EMPTY`
const cellLink = (w, row) => navLink(withAssignee(w.jql || '', row))
const rowLink = (windowId, row) => {
  const w = windowById(windowId)
  return w?.jql ? navLink(withAssignee(w.jql, row)) : '#'
}
</script>

<style scoped>
/* An instrument panel, not a report: every box is the same plate as the cards
   and drill-ins, and the type is mono and tabular throughout so a column of
   numbers lines up and can be read down rather than across. */
.team {
  display: grid;
  grid-template-columns: repeat(12, minmax(0, 1fr));
  gap: 0.6rem;
  align-content: start;
  --t-row: 1.45rem;
  --t-size: 0.76rem;
}
.team.wall { --t-row: 1.85rem; --t-size: 0.95rem; gap: 0.8rem; }

/* ---------------------------------------------------------------- notices */
.notice, .notice-error { grid-column: span 12; }
.notice {
  border: 1px dashed hsl(var(--border)); border-radius: 10px;
  padding: 1rem 1.1rem; background: hsl(var(--card) / 0.4);
}
.notice-error {
  border-style: solid; border-color: color-mix(in srgb, var(--j-crit) 40%, transparent);
  background: color-mix(in srgb, var(--j-crit) 6%, transparent);
}
.notice-title { display: flex; align-items: center; gap: 0.4rem; font-weight: 700; font-size: 0.9rem; }
.notice-body { font-size: 0.82rem; color: hsl(var(--muted-foreground)); margin-top: 0.35rem; }
.notice code {
  font-family: var(--j-mono); background: hsl(var(--muted) / 0.6);
  padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em;
}
.err-pre {
  font-family: var(--j-mono); font-size: 12px; white-space: pre-wrap; word-break: break-word;
  color: var(--j-crit); background: color-mix(in srgb, var(--j-crit) 8%, transparent);
  border-radius: 6px; padding: 0.6rem 0.75rem; margin: 0;
}
.err-pre.small { font-size: 10px; padding: 0.4rem 0.5rem; }

/* -------------------------------------------------------------- flow line */
.flow {
  grid-column: span 12;
  display: flex; align-items: center; gap: 1rem; flex-wrap: wrap;
  padding: 0.55rem 0.8rem;
  border: 1px solid hsl(var(--border)); border-radius: 10px;
  background:
    radial-gradient(120% 180% at 0% 0%, hsl(var(--card)) 0%, hsl(var(--card) / 0.55) 70%),
    hsl(var(--card) / 0.5);
}
.f-id { display: flex; align-items: baseline; gap: 0.5rem; min-width: 0; }
.f-key {
  font-family: var(--j-mono); font-size: 0.82rem; font-weight: 800; letter-spacing: 0.06em;
}
.f-name {
  font-size: 0.74rem; color: hsl(var(--muted-foreground));
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.f-nums { display: flex; align-items: flex-end; gap: 0.1rem; flex-wrap: wrap; margin-left: auto; }
.f-cell {
  display: flex; flex-direction: column; align-items: flex-end;
  padding: 0 0.6rem; border-radius: 6px;
}
.f-sep { width: 1px; align-self: stretch; margin: 0.15rem 0.45rem; background: hsl(var(--border)); }
.f-n {
  font-family: var(--j-mono); font-variant-numeric: tabular-nums;
  font-size: clamp(1.05rem, 1.5vw, 1.45rem); font-weight: 800; line-height: 1;
  letter-spacing: -0.03em;
}
.team.wall .f-n { font-size: clamp(1.5rem, 2.4vw, 2.3rem); }
.f-n.muted { color: hsl(var(--muted-foreground)); }
.f-l {
  font-family: var(--j-mono); font-size: 9px; font-weight: 700; letter-spacing: 0.14em;
  text-transform: uppercase; color: hsl(var(--muted-foreground)); margin-top: 0.3rem;
  white-space: nowrap;
}
.team.wall .f-l { font-size: 11px; }
.tone-in { color: var(--j-info); }
.tone-out { color: var(--j-ok); }
.tone-warn { color: var(--j-warn); }
/* A growing queue is the one state worth colouring: flat and shrinking are
   both fine, so neither shouts. */
.f-net.tone-grow .f-n { color: var(--j-crit); }
.f-net.tone-shrink .f-n { color: var(--j-ok); }
.f-net.tone-flat .f-n { color: hsl(var(--muted-foreground)); }
.f-meta { display: flex; align-items: center; gap: 0.5rem; }
.f-age {
  font-family: var(--j-mono); font-size: 0.68rem; font-variant-numeric: tabular-nums;
  color: hsl(var(--muted-foreground)); letter-spacing: 0.03em; white-space: nowrap;
}
.f-refresh {
  display: grid; place-items: center; width: 1.9rem; height: 1.9rem; border-radius: 6px;
  color: hsl(var(--muted-foreground)); border: 1px solid transparent;
}
.f-refresh:hover:not(:disabled) { background: hsl(var(--muted) / 0.6); color: hsl(var(--foreground)); }
.f-refresh:disabled { opacity: 0.5; }

/* ------------------------------------------------------------------ boxes */
.box {
  display: flex; flex-direction: column; min-width: 0;
  border: 1px solid hsl(var(--border)); border-radius: 10px;
  background: hsl(var(--card) / 0.55);
  padding: 0.5rem 0.6rem 0.4rem;
  overflow: hidden;
}
.bh {
  display: flex; align-items: baseline; justify-content: space-between; gap: 0.5rem;
  padding-bottom: 0.35rem; margin-bottom: 0.3rem;
  border-bottom: 1px solid hsl(var(--border) / 0.7);
}
.bt {
  font-family: var(--j-mono); font-size: 10px; font-weight: 800; letter-spacing: 0.14em;
  text-transform: uppercase; color: hsl(var(--muted-foreground)); margin: 0; white-space: nowrap;
}
.team.wall .bt { font-size: 12px; }
.bsub { font-size: 0.68rem; color: hsl(var(--muted-foreground)); opacity: 0.8; white-space: nowrap; }
.bnum { display: flex; align-items: baseline; gap: 0.35rem; }
.btotal {
  font-family: var(--j-mono); font-variant-numeric: tabular-nums;
  font-size: 1.25rem; font-weight: 800; line-height: 1; letter-spacing: -0.035em;
}
.team.wall .btotal { font-size: 1.7rem; }
.bflag {
  font-family: var(--j-mono); font-size: 8px; font-weight: 700; letter-spacing: 0.1em;
  text-transform: uppercase; color: var(--j-warn);
  border: 1px solid color-mix(in srgb, var(--j-warn) 40%, transparent);
  border-radius: 4px; padding: 0.05rem 0.25rem;
}
.bempty { font-size: 0.72rem; color: hsl(var(--muted-foreground)); padding: 0.5rem 0; margin: 0; }

/* The bento: the roster is the big plate and takes the width it needs, the
   five windows pack around it. Twelve columns so the two rows both land flush
   rather than leaving a ragged gap at the end. */
.b-matrix { grid-column: span 12; }
.b-win { grid-column: span 6; }
@media (min-width: 900px) { .b-win { grid-column: span 4; } }
@media (min-width: 1280px) {
  .b-matrix { grid-column: span 7; }
  .w-open { grid-column: span 5; }
  .b-win:not(.w-open) { grid-column: span 3; }
}

/* ----------------------------------------------------------------- tables */
/* No inner scrollbars. Boxes size to their content and the PAGE scrolls: a
   dashboard you have to scroll inside eleven separate little panes to read is
   worse than one long page, and a scrollbar hides rows without saying so. */
.tscroll, .tscroll-win { overflow: visible; max-height: none; }
.t {
  width: 100%; border-collapse: collapse;
  font-family: var(--j-mono); font-size: var(--t-size); font-variant-numeric: tabular-nums;
}
.t th {
  font-size: 9px; font-weight: 700; letter-spacing: 0.1em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); text-align: right; padding: 0 0.3rem 0.25rem;
  white-space: nowrap;
}
.team.wall .t th { font-size: 11px; }
.t th.c-who { text-align: left; }
.t td { padding: 0 0.3rem; height: var(--t-row); white-space: nowrap; }
.t tbody tr { border-top: 1px solid hsl(var(--border) / 0.35); }
.t tbody tr:hover { background: hsl(var(--muted) / 0.45); }
.t tfoot td {
  border-top: 1px solid hsl(var(--border));
  font-weight: 800; padding-top: 0.1rem;
}

/* The assignee cell is the bar's track in the window boxes: the proportion
   sits behind the name instead of costing a column of its own, which is how
   five boxes fit beside the roster at all. */
.t-win .c-who { position: relative; overflow: hidden; max-width: 0; width: 60%; }
.wfill {
  position: absolute; inset: 0 auto 0 0; z-index: 0;
  opacity: 0.17; border-radius: 0 3px 3px 0; pointer-events: none;
}
.dot {
  position: relative; z-index: 1;
  display: inline-block; width: 0.42rem; height: 0.42rem; border-radius: 50%;
  margin-right: 0.35rem; vertical-align: 0.04rem; flex: none;
}
.who {
  position: relative; z-index: 1;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  display: inline-block; max-width: calc(100% - 0.8rem); vertical-align: bottom;
}
.t .c-n, .t .c-pc { text-align: right; }
.c-pc { color: hsl(var(--muted-foreground)); width: 3.4rem; }
.c-n { width: 3rem; }
.n { font-weight: 700; }
.n.zero { color: hsl(var(--muted-foreground)); opacity: 0.45; font-weight: 400; }
a.link { color: inherit; text-decoration: none; }
a.link:hover { text-decoration: underline; text-underline-offset: 0.15em; }
.t-win tbody tr.lead .n { color: hsl(var(--foreground)); }

/* Nobody's row and idle rows stay legible but recede, so the people carrying
   work are the ones the eye lands on. */
.t-matrix tbody tr.idle .who { color: hsl(var(--muted-foreground)); }
.t-matrix tbody tr.nobody .who { font-style: italic; }
.t-matrix .c-who { width: 40%; max-width: 0; overflow: hidden; }
/* Not sticky: with no scroll container a sticky header would latch onto the
   viewport and float over the rest of the page as you scroll past the box. */
.sortable {
  font: inherit; letter-spacing: inherit; text-transform: inherit; color: inherit;
  padding: 0; border: 0; background: none;
}
.sortable:hover { color: hsl(var(--foreground)); }
.sortable.on { color: hsl(var(--foreground)); text-decoration: underline; text-underline-offset: 0.2em; }
.c-bar { width: 22%; min-width: 5rem; }
/* The flex lives on a wrapper, not the td: a table cell set to display:flex
   leaves the row's column model and stops aligning with its own header. */
.barwrap { display: flex; align-items: center; gap: 0.4rem; }
.mbar {
  flex: 1; height: 0.4rem; min-width: 2rem; border-radius: 3px;
  background: hsl(var(--muted) / 0.7); overflow: hidden;
}
.mfill { display: block; height: 100%; border-radius: 3px; }
.mpct { color: hsl(var(--muted-foreground)); font-size: 0.7em; min-width: 2.6rem; text-align: right; }


/* ---------------------------------------------------------- effort block */
/* The provenance note is part of the data, not decoration: a reader has to
   know these numbers are an SLA clock rather than logged work, or they will
   read them as something a person actually recorded. */
.prov { border-color: color-mix(in srgb, var(--j-info) 30%, hsl(var(--border))); }
.provnote {
  display: inline-flex; align-items: center; gap: 0.35rem; margin: 0 0 0.5rem;
  font-size: 0.68rem; color: hsl(var(--muted-foreground));
  border: 1px solid hsl(var(--border) / 0.8); border-radius: 999px;
  padding: 0.12rem 0.5rem; align-self: flex-start; cursor: help;
}
.provnote code {
  font-family: var(--j-mono); background: hsl(var(--muted) / 0.6);
  padding: 0.02rem 0.25rem; border-radius: 3px; font-size: 0.95em;
}
.provnote b { color: hsl(var(--foreground)); font-weight: 700; }
.provnote.err { color: var(--j-crit); }

.estrip { display: flex; align-items: flex-end; flex-wrap: wrap; gap: 0.1rem; }
.ecell { display: flex; flex-direction: column; padding: 0.15rem 0.7rem 0 0; min-width: 5.5rem; }
.e-sep { width: 1px; align-self: stretch; margin: 0.2rem 0.6rem; background: hsl(var(--border)); }
.e-n {
  font-family: var(--j-mono); font-variant-numeric: tabular-nums;
  font-size: clamp(1.05rem, 1.6vw, 1.45rem); font-weight: 800; line-height: 1;
  letter-spacing: -0.03em;
}
.team.wall .e-n { font-size: clamp(1.4rem, 2.2vw, 2rem); }
.e-n.muted { color: hsl(var(--muted-foreground)); }
.ecell.bad .e-n { color: var(--j-crit); }
.e-l {
  font-family: var(--j-mono); font-size: 9px; font-weight: 700; letter-spacing: 0.13em;
  text-transform: uppercase; color: hsl(var(--muted-foreground)); margin-top: 0.3rem;
  white-space: nowrap;
}
.team.wall .e-l { font-size: 11px; }
.c-n.slabad { color: var(--j-crit); }

/* ------------------------------------------------------------- heatmap */
.b-heat { grid-column: span 12; }
@media (min-width: 1280px) { .b-heat { grid-column: span 8; } }
/* A day-label column plus a fixed 24, rather than auto-fit, so every row
   lines up under the hour scale beneath it. */
.heat {
  display: grid; grid-template-columns: 2.2rem repeat(24, minmax(0, 1fr));
  gap: 2px; align-items: center;
}
.hh {
  font-family: var(--j-mono); font-size: 8px; color: hsl(var(--muted-foreground));
  font-variant-numeric: tabular-nums; text-align: center; padding-top: 0.15rem;
}
.heat-day {
  font-family: var(--j-mono); font-size: 9px; font-weight: 700; letter-spacing: 0.06em;
  color: hsl(var(--muted-foreground)); text-transform: uppercase;
}
.cellx {
  aspect-ratio: 1; min-height: 0.65rem; border-radius: 2px;
  background: var(--j-info);
}
.cellx.none { background: hsl(var(--muted) / 0.45); }
/* The busiest hour gets an outline, so the peak is findable rather than
   being merely the darkest of several dark squares. */
.cellx.top { outline: 1.5px solid var(--j-warn); }

@media (prefers-reduced-motion: reduce) {
  .animate-spin { animation: none; }
}
</style>
