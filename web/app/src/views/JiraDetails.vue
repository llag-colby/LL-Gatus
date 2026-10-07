<template>
  <div class="dashboard-container detail-page jira-view bg-background">
    <div class="jira-shell">

      <!-- COMMAND RAIL.
           Always present. On the wall it keeps only what proves the board is
           alive (data age, clock) and drops every control, because nobody
           is going to click a back arrow on a television. -->
      <header class="rail">
        <div class="rail-id">
          <router-link v-if="!isFullscreen" to="/" class="rail-back"
            data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-5 w-5" />
          </router-link>
          <span class="rail-mark" :style="{ backgroundImage: `url(${jiraIcon})` }"></span>
          <div class="rail-name">
            <span class="rail-title">Jira</span>
            <span v-if="deskSubtitle" class="rail-sub">{{ deskSubtitle }}</span>
          </div>
        </div>

        <nav v-if="projects.length > 1" class="projsel" role="tablist" aria-label="Project">
          <button v-for="p in projects" :key="p.key" role="tab" :aria-selected="p.key === selectedKey"
            class="proj" :class="{ on: p.key === selectedKey }" @click="selectedKey = p.key"
            :data-tooltip="p.name" data-tip-pos="bottom">
            <span class="proj-key">{{ p.key }}</span>
            <span class="proj-n">{{ p.totalOpen }}</span>
          </button>
        </nav>

        <div class="rail-state">
          <span class="feed">{{ feedLabel }}</span>
          <span class="clock">{{ clockLabel }}</span>
          <Button v-if="!isFullscreen" variant="ghost" size="icon" class="h-9 w-9 shrink-0"
            @click="fetchMetrics" data-tooltip="Refresh" data-tip-pos="bottom">
            <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
          </Button>
        </div>
      </header>

      <nav v-if="!isFullscreen" class="tabbar" role="tablist" aria-label="View">
        <button v-for="t in TABS" :key="t.id" role="tab" :aria-selected="tab === t.id"
          class="tab" :class="{ on: tab === t.id }" @click="tab = t.id">
          <component :is="t.icon" class="h-4 w-4" />{{ t.label }}
        </button>
      </nav>

      <div v-if="loaded && !snapshot.configured" class="notice">
        <div class="notice-title">Jira is not connected yet</div>
        <p class="notice-body">
          Set <code>JIRA_BASE_URL</code>, <code>JIRA_EMAIL</code> and <code>JIRA_API_TOKEN</code>
          in <code>.env</code>, then <code>docker compose up -d</code>.
        </p>
      </div>

      <div v-else-if="loaded && snapshot.configured && !snapshot.ok" class="notice notice-error">
        <div class="notice-title"><AlertTriangle class="h-4 w-4" /> Cannot reach Jira</div>
        <pre class="err-pre">{{ snapshot.error }}</pre>
      </div>

      <!-- SNAPSHOT: the executive daily view. First tab and the default,
           because it is the one the leadership team reads. -->
      <JiraSnapshot v-else-if="tab === 'snapshot' && snapshot.configured"
        :project-key="selectedKey || proj?.key || ''" :base-url="snapshot.baseUrl || ''" />

      <!-- TEAM: per-assignee counts. Sits in the same v-if chain as Kanban, so
           an unreachable Jira shows the one error above rather than each tab
           discovering it separately. The counts themselves are a separate,
           cached fetch inside the component. -->
      <JiraTeam v-else-if="tab === 'team' && snapshot.configured"
        :project-key="selectedKey || proj?.key || ''" :base-url="snapshot.baseUrl || ''" />

      <JiraKanban v-else-if="tab === 'kanban' && snapshot.configured" @open="openKey = $event" />

      <!-- ============================ OVERVIEW: THE WALL ================= -->
      <section v-else-if="tab === 'overview' && ready" class="wall">

        <!-- KPI rail. Four counts and one instrument. -->
        <div class="kpis">
          <div class="kpi">
            <div class="kpi-n">{{ proj.totalOpen }}</div>
            <div class="kpi-l">Open</div>
          </div>
          <div class="kpi" :class="{ hot: breachCount > 0, 'tone-crit': breachCount > 0 }">
            <div class="kpi-n" :class="{ crit: breachCount > 0 }">{{ slaMeasured ? breachCount : '--' }}</div>
            <div class="kpi-l">{{ slaMeasured ? 'SLA breached' : 'SLA not measured' }}</div>
          </div>
          <div class="kpi" :class="{ hot: proj.unassigned > 0, 'tone-warn': proj.unassigned > 0 }">
            <div class="kpi-n" :class="{ warn: proj.unassigned > 0 }">{{ proj.unassigned }}</div>
            <div class="kpi-l">Unassigned</div>
          </div>
          <div class="kpi">
            <div class="kpi-n">{{ avgResolution }}</div>
            <div class="kpi-l">Avg resolve</div>
          </div>

          <!-- 14-day flow. Created above the axis, resolved below: if the page
               is bottom-heavy the desk is winning, top-heavy and it is not. -->
          <div class="kpi flow">
            <div class="flow-head">
              <span class="kpi-l">14-day flow</span>
              <span class="flow-today">
                <b class="in">+{{ proj.createdToday }}</b>
                <b class="out">-{{ proj.resolvedToday }}</b>
                <em>today</em>
              </span>
            </div>
            <div class="flowchart" role="img" :aria-label="flowLabel">
              <div v-for="d in flow" :key="d.date" class="fday" :class="{ today: d.today }"
                :data-tooltip="`${d.date}  +${d.created} in  -${d.resolved} out`">
                <div class="fup"><span :style="{ height: d.up + '%' }"></span></div>
                <div class="faxis"></div>
                <div class="fdown"><span :style="{ height: d.down + '%' }"></span></div>
              </div>
            </div>
          </div>
        </div>

        <!-- SLA HORIZON.
             One time axis from breached to later. Every open ticket with a
             running clock is a tick, sized by priority. You are meant to read
             the shape of the crowd, not the individual marks. -->
        <div class="horizon" :class="{ quiet: !slaMeasured }">
          <div class="hz-label">
            <span class="eyebrow">SLA horizon</span>
            <!-- Only when there is something the bands cannot tell you. The
                 running-clock total is just the sum of the six band counts, so
                 printing it was restating the instrument in words. -->
            <span v-if="!slaMeasured || noSlaCount" class="hz-note">
              <template v-if="!slaMeasured">not measured for {{ proj.key }}</template>
              <template v-else>{{ noSlaCount }} without a clock</template>
            </span>
          </div>
          <div class="hz-bands">
            <div v-for="b in horizon" :key="b.key" class="band" :class="[`t-${b.tone}`, { live: b.items.length }]">
              <div class="band-top">
                <span class="band-n">{{ b.items.length }}</span>
                <span class="band-l">{{ b.label }}</span>
              </div>
              <div class="band-ticks">
                <span v-for="it in b.items" :key="it.key" class="tick" :class="`p-${prioKey(it.priority)}`"
                  :data-tooltip="`${it.key}  ${it.summary}`"></span>
              </div>
            </div>
          </div>
        </div>

        <!-- THREE LIVE COLUMNS. Header counts are the truth; the body clips. -->
        <div class="cols">
          <section class="col col-risk">
            <div class="col-head">
              <span class="col-dot"></span>At risk
              <b>{{ atRisk.length }}</b>
            </div>
            <div class="col-body">
              <button v-for="it in atRisk" :key="it.key" class="row" :class="{ fresh: flash.has(it.key) }"
                @click="openKey = it.key">
                <span class="r-key">{{ it.key }}</span>
                <span class="r-sum">{{ it.summary || '-' }}</span>
                <span class="r-val clock-val" :class="slaView(it).cls">{{ slaView(it).text }}</span>
                <span class="r-meta">
                  <span class="ttag" :class="typeClass(it.type)">{{ shortType(it.type) }}</span>
                  <span class="r-who" :class="{ none: !it.assignee }">{{ it.assignee || 'unassigned' }}</span>
                  <span v-if="it.slaName" class="r-sla-name">{{ it.slaName }}</span>
                </span>
              </button>
              <p v-if="!atRisk.length" class="col-empty">No clocks running.</p>
            </div>
          </section>

          <section class="col col-unassigned">
            <div class="col-head">
              <span class="col-dot"></span>Unassigned
              <b>{{ proj.unassigned }}</b>
            </div>
            <div class="col-body">
              <button v-for="it in unassigned" :key="it.key" class="row" :class="{ fresh: flash.has(it.key) }"
                @click="openKey = it.key">
                <span class="r-key">{{ it.key }}</span>
                <span class="r-sum">{{ it.summary || '-' }}</span>
                <span class="r-val">{{ ageLabel(it.created) }}</span>
                <span class="r-meta">
                  <span class="ttag" :class="typeClass(it.type)">{{ shortType(it.type) }}</span>
                  <span class="r-status"><i class="sdot" :class="'cat-' + (it.category || 'new')"></i>{{ it.status || '-' }}</span>
                  <span v-if="it.priority" class="r-prio" :class="'prio-' + prioKey(it.priority)">{{ it.priority }}</span>
                </span>
              </button>
              <p v-if="!unassigned.length" class="col-empty">Everything is owned.</p>
            </div>
          </section>

          <section class="col col-in">
            <div class="col-head">
              <span class="col-dot"></span>Just in
              <b>{{ proj.createdToday }}<em>today</em></b>
            </div>
            <div class="col-body">
              <button v-for="it in justIn" :key="it.key" class="row" :class="{ fresh: flash.has(it.key) }"
                @click="openKey = it.key">
                <span class="r-key">{{ it.key }}</span>
                <span class="r-sum">{{ it.summary || '-' }}</span>
                <span class="r-val">{{ ageLabel(it.created) }}</span>
                <span class="r-meta">
                  <span class="ttag" :class="typeClass(it.type)">{{ shortType(it.type) }}</span>
                  <span class="r-who" :class="{ none: !it.assignee }">{{ it.assignee || 'unassigned' }}</span>
                  <span v-if="it.priority" class="r-prio" :class="'prio-' + prioKey(it.priority)">{{ it.priority }}</span>
                </span>
              </button>
              <p v-if="!justIn.length" class="col-empty">Nothing new.</p>
            </div>
          </section>
        </div>
      </section>

      <!-- ============================ QUEUE: THE DESK ==================== -->
      <section v-else-if="tab === 'queue' && ready" class="queue">
        <div class="q-tools">
          <div class="mixes">
            <div class="mix">
              <span class="eyebrow">Priority</span>
              <div class="bar">
                <span v-for="s in prioritySegments" :key="s.name" class="fill"
                  :style="{ width: s.pct + '%', background: s.color }"
                  :data-tooltip="`${s.name}: ${s.count}`"></span>
                <span v-if="!prioritySegments.length" class="fill empty"></span>
              </div>
              <ul class="legend">
                <li v-for="s in prioritySegments" :key="s.name">
                  <i :style="{ background: s.color }"></i>{{ s.name }}<b>{{ s.count }}</b>
                </li>
              </ul>
            </div>
            <div class="mix">
              <span class="eyebrow">Type</span>
              <div class="bar">
                <span v-for="s in typeSegments" :key="s.name" class="fill"
                  :style="{ width: s.pct + '%', background: s.color }"
                  :data-tooltip="`${s.name}: ${s.count}`"></span>
                <span v-if="!typeSegments.length" class="fill empty"></span>
              </div>
              <ul class="legend">
                <li v-for="s in typeSegments" :key="s.name">
                  <i :style="{ background: s.color }"></i>{{ s.name }}<b>{{ s.count }}</b>
                </li>
              </ul>
            </div>
          </div>
          <div class="q-right">
            <input v-model="search" type="text" placeholder="filter tickets" class="q-filter"
              aria-label="Filter tickets" />
            <a v-if="snapshot.baseUrl" class="q-open" :href="`${snapshot.baseUrl}/browse/${proj.key}`"
              target="_blank" rel="noopener">Open in Jira<ExternalLink class="h-3.5 w-3.5" /></a>
          </div>
        </div>

        <div v-if="proj.byStatus && proj.byStatus.length" class="chips">
          <span v-for="s in proj.byStatus" :key="s.name" class="chip">{{ s.name }}<b>{{ s.count }}</b></span>
        </div>

        <div v-if="sortedIssues.length" class="table">
          <div class="thead">
            <button v-for="c in COLUMNS" :key="c.id" class="th" :class="{ right: c.right, on: sortKey === c.id }"
              @click="sortBy(c.id)">{{ c.label }}<span class="ar">{{ arrow(c.id) }}</span></button>
          </div>
          <button v-for="it in sortedIssues" :key="it.key" class="trow" :class="{ fresh: flash.has(it.key) }"
            @click="openKey = it.key">
            <span class="r-key">{{ it.key }}</span>
            <span class="ttag" :class="typeClass(it.type)">{{ shortType(it.type) }}</span>
            <span class="t-sum">{{ it.summary || '-' }}</span>
            <span class="r-status"><i class="sdot" :class="'cat-' + (it.category || 'new')"></i>{{ it.status || '-' }}</span>
            <span class="r-prio" :class="'prio-' + prioKey(it.priority)">{{ it.priority || '-' }}</span>
            <span class="r-who" :class="{ none: !it.assignee }">{{ it.assignee || 'unassigned' }}</span>
            <span class="t-sla clock-val" :class="slaView(it).cls">{{ slaView(it).text || ageLabel(it.created) }}</span>
          </button>
        </div>
        <div v-else class="notice">
          <div class="notice-title">Nothing matches</div>
          <p class="notice-body">No open ticket in {{ proj.key }} matches the current filter.</p>
        </div>
      </section>
    </div>

    <JiraTicketPanel :issue-key="openKey" @close="openKey = ''" />
    <Settings v-if="!isFullscreen" @refreshData="fetchMetrics" />
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { ArrowLeft, RefreshCw, AlertTriangle, Gauge, Rows3, LayoutGrid, Users, LayoutDashboard, ExternalLink } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import Settings from '@/components/Settings.vue'
import { generatePrettyTimeAgo } from '@/utils/time'
import { isFullscreen, now as serverNow } from '@/store'
import jiraIcon from '@/assets/jira.png'
import JiraTicketPanel from '@/components/JiraTicketPanel.vue'
import JiraKanban from '@/components/JiraKanban.vue'
import JiraTeam from '@/components/JiraTeam.vue'
import JiraSnapshot from '@/components/JiraSnapshot.vue'

const TABS = [
  { id: 'snapshot', label: 'Snapshot', icon: LayoutDashboard },
  { id: 'overview', label: 'Overview', icon: Gauge },
  { id: 'queue', label: 'Queue', icon: Rows3 },
  { id: 'team', label: 'Team', icon: Users },
  { id: 'kanban', label: 'Kanban', icon: LayoutGrid },
]
const COLUMNS = [
  { id: 'key', label: 'Key' }, { id: 'type', label: 'Type' }, { id: 'summary', label: 'Summary' },
  { id: 'status', label: 'Status' }, { id: 'priority', label: 'Priority' },
  { id: 'assignee', label: 'Assignee' }, { id: 'sla', label: 'SLA', right: true },
]

const loading = ref(false)
const loaded = ref(false)
const live = ref(false)
const search = ref('')
const selectedKey = ref('')
const openKey = ref('')
const now = ref(Date.now())
const snapshot = ref({ configured: false, ok: false, status: 'unknown', projects: [] })

// Which tab is showing, remembered so a wall display that reboots comes back
// to the same view. 'tickets' is the pre-rewrite value for what is now 'queue'.
const savedTab = localStorage.getItem('gatus.jira.tab')
const tab = ref(TABS.some(t => t.id === savedTab) ? savedTab : 'snapshot')
watch(tab, (v) => localStorage.setItem('gatus.jira.tab', v))

const projects = computed(() => snapshot.value.projects || [])
const proj = computed(() => projects.value.find(p => p.key === selectedKey.value) || projects.value[0] || null)
const ready = computed(() => snapshot.value.configured && snapshot.value.ok && !!proj.value)
const issues = computed(() => proj.value?.issues || [])

// --- rail state ---------------------------------------------------------
const deskSubtitle = computed(() => {
  const parts = []
  if (proj.value) parts.push(proj.value.name || proj.value.key)
  if (snapshot.value.account) parts.push(snapshot.value.account)
  return parts.join('  ·  ')
})
// h23 so a wall display never shows a bare "1:05" that could be either half of
// the day. Anchored to the server clock, like every other time on the site.
const clockLabel = computed(() => new Date(serverNow.value)
  .toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }))
// How old the data on screen is, recomputed every second because it reads the
// server clock. This replaced a pulsing dot and a healthy/degraded pill: the
// dot only ever claimed the stream was open, and "degraded" just meant some
// SLA was breached, which the KPI rail already says in a much larger number.
// A figure that visibly climbs is the honest version of both - if it stops
// resetting, the feed is gone, and you can see that without a legend.
const feedLabel = computed(() => {
  if (!snapshot.value.updatedAt) return 'waiting for first poll'
  const stamp = new Date(snapshot.value.updatedAt).getTime()
  if (!stamp) return 'updated just now'
  const secs = Math.max(0, Math.round((serverNow.value - stamp) / 1000))
  if (secs < 60) return `updated ${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `updated ${mins}m ${secs % 60}s ago`
  return `updated ${Math.floor(mins / 60)}h ${mins % 60}m ago`
})

// --- KPIs ---------------------------------------------------------------
// slaBreached is a -1 sentinel meaning "this project is not SLA-measured",
// which the old page rendered as a dash inside a number slot. It is a
// different statement from zero and gets a different label now.
const slaMeasured = computed(() => (proj.value?.slaBreached ?? -1) >= 0)
const breachCount = computed(() => Math.max(0, proj.value?.slaBreached || 0))
const avgResolution = computed(() => {
  const h = proj.value?.avgResolutionHours || 0
  if (!h) return '-'
  return h >= 48 ? (h / 24).toFixed(1) + 'd' : h.toFixed(1) + 'h'
})

const flow = computed(() => {
  const t = proj.value?.trend || []
  const max = Math.max(1, ...t.map(p => Math.max(p.created, p.resolved)))
  const last = t.length - 1
  return t.map((p, i) => ({
    date: p.date, created: p.created, resolved: p.resolved, today: i === last,
    up: (p.created / max) * 100, down: (p.resolved / max) * 100,
  }))
})
const flowLabel = computed(() => {
  const p = proj.value
  if (!p) return ''
  return `14 day flow: ${p.createdLast7d} created and ${p.resolvedLast7d} resolved in the last 7 days`
})

// --- SLA ----------------------------------------------------------------
const fmtDur = (ms) => {
  let s = Math.floor(Math.abs(ms) / 1000)
  const d = Math.floor(s / 86400); s -= d * 86400
  const h = Math.floor(s / 3600); s -= h * 3600
  const m = Math.floor(s / 60); s -= m * 60
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${String(m).padStart(2, '0')}m`
  return `${m}m ${String(s).padStart(2, '0')}s`
}
const hasSla = (it) => !!(it.slaName || it.slaBreached)
// Only a running clock can be counted down locally. A paused or out-of-hours
// cycle keeps the value the poll last saw, otherwise the wall would show a
// ticket burning down overnight when its calendar says it is not.
const remainingOf = (it) => (it.slaActive && it.slaBreachEpoch) ? (it.slaBreachEpoch - now.value) : (it.slaRemainingMs || 0)
const slaView = (it) => {
  if (!hasSla(it)) return { show: false, cls: '', text: '' }
  const rem = remainingOf(it)
  if (it.slaBreached || rem < 0) return { show: true, cls: 'sla-over', text: 'over ' + fmtDur(rem) }
  if (it.slaPaused) return { show: true, cls: 'sla-hold', text: fmtDur(rem) + ' held' }
  const mins = rem / 60000
  return { show: true, cls: mins < 15 ? 'sla-crit' : mins < 60 ? 'sla-warn' : 'sla-ok', text: fmtDur(rem) }
}
// Sorting reads the SNAPSHOT value, not the ticking clock, so rows do not
// reshuffle under someone's cursor once a second.
const slaSortKey = (it) => {
  if (it.slaBreached) return -1e14 + (it.slaRemainingMs || 0)
  if (hasSla(it)) return it.slaRemainingMs
  return 1e14
}

const BANDS = [
  { key: 'over', label: 'Breached', tone: 'crit', max: 0 },
  { key: 'm15', label: 'Under 15m', tone: 'crit', max: 15 * 60e3 },
  { key: 'h1', label: 'Under 1h', tone: 'warn', max: 60 * 60e3 },
  { key: 'h4', label: 'Under 4h', tone: 'warn', max: 4 * 3600e3 },
  { key: 'd1', label: 'Under 24h', tone: 'ok', max: 24 * 3600e3 },
  { key: 'far', label: 'Later', tone: 'idle', max: Infinity },
]
const onClock = computed(() => issues.value.filter(hasSla))
const noSlaCount = computed(() => issues.value.length - onClock.value.length)
const horizon = computed(() => {
  const bands = BANDS.map(b => ({ ...b, items: [] }))
  for (const it of onClock.value) {
    const rem = remainingOf(it)
    if (it.slaBreached || rem < 0) { bands[0].items.push(it); continue }
    bands[bands.findIndex(b => rem < b.max)].items.push(it)
  }
  return bands
})

// --- the three columns --------------------------------------------------
const byUrgency = (a, b) => slaSortKey(a) - slaSortKey(b)
const newestFirst = (a, b) => String(b.created || '').localeCompare(String(a.created || ''))
const atRisk = computed(() => onClock.value.slice().sort(byUrgency))
const unassigned = computed(() => issues.value.filter(it => !it.assignee).sort(newestFirst))
const justIn = computed(() => issues.value.slice().sort(newestFirst))

// --- queue --------------------------------------------------------------
const prioKey = (p) => (p || '').toLowerCase().replace(/[^a-z]/g, '') || 'none'
const CAT = ['--j-cat-1', '--j-cat-2', '--j-cat-3', '--j-cat-4', '--j-cat-5', '--j-cat-6', '--j-cat-7', '--j-cat-8']
const PRIO_VAR = { highest: '--j-crit', high: '--j-warn', medium: '--j-idle' }
const segmentsOf = (items, varFor) => {
  const list = (items || []).filter(i => i.count > 0)
  const total = list.reduce((a, b) => a + b.count, 0) || 1
  return list.map((i, idx) => ({
    name: i.name, count: i.count, pct: (i.count / total) * 100,
    color: `var(${varFor(i, idx)})`,
  }))
}
const prioritySegments = computed(() => segmentsOf(proj.value?.byPriority, (i) => PRIO_VAR[prioKey(i.name)] || '--j-idle'))
const typeSegments = computed(() => segmentsOf(proj.value?.byType, (_, idx) => CAT[idx % CAT.length]))

const filteredIssues = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return issues.value
  return issues.value.filter(it =>
    [it.key, it.summary, it.assignee, it.status, it.type].some(v => (v || '').toString().toLowerCase().includes(q)))
})
const sortKey = ref('sla')
const sortDir = ref('asc')
const priRank = { highest: 0, high: 1, medium: 2, low: 3, lowest: 4, none: 5 }
const sortValue = (it, key) => {
  switch (key) {
    case 'type': return (it.type || '').toLowerCase()
    case 'summary': return (it.summary || '').toLowerCase()
    case 'status': return (it.status || '').toLowerCase()
    case 'priority': return priRank[prioKey(it.priority)] ?? 9
    case 'assignee': return (it.assignee || '~~~').toLowerCase() // unassigned sorts last
    case 'sla': return slaSortKey(it)
    default: return (it.key || '')
  }
}
const sortBy = (key) => {
  if (sortKey.value === key) sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
  else { sortKey.value = key; sortDir.value = 'asc' }
}
const arrow = (key) => (sortKey.value !== key ? '' : sortDir.value === 'asc' ? '▲' : '▼')
const sortedIssues = computed(() => {
  const dir = sortDir.value === 'asc' ? 1 : -1
  return [...filteredIssues.value].sort((a, b) => {
    const av = sortValue(a, sortKey.value), bv = sortValue(b, sortKey.value)
    if (typeof av === 'number' && typeof bv === 'number') return (av - bv) * dir
    return String(av).localeCompare(String(bv), undefined, { numeric: true }) * dir
  })
})

// --- labels -------------------------------------------------------------
const shortType = (t) => (t || '').replace(/^\[System\]\s*/i, '').replace(/service request/i, 'Request')
const typeClass = (t) => {
  const s = (t || '').toLowerCase()
  if (s.includes('incident') || s.includes('bug')) return 'ttag-incident'
  if (s.includes('problem')) return 'ttag-problem'
  if (s.includes('change')) return 'ttag-change'
  if (s.includes('request') || s.includes('service')) return 'ttag-request'
  if (s.includes('epic')) return 'ttag-epic'
  if (s.includes('story')) return 'ttag-story'
  return 'ttag-task'
}
const ageLabel = (c) => { if (!c) return '-'; try { return generatePrettyTimeAgo(new Date(c)) } catch (e) { return '-' } }

// --- snapshot ingestion + new-ticket flash ------------------------------
const seen = new Set()
let seenInit = false
const flash = ref(new Set())
const applySnapshot = (data) => {
  snapshot.value = data
  loaded.value = true
  const keys = (data.projects || []).flatMap(p => (p.issues || []).map(i => i.key))
  if (seenInit) {
    const fresh = keys.filter(k => !seen.has(k))
    if (fresh.length) {
      const s = new Set(flash.value); fresh.forEach(k => s.add(k)); flash.value = s
      setTimeout(() => {
        const s2 = new Set(flash.value); fresh.forEach(k => s2.delete(k)); flash.value = s2
      }, 8000)
    }
  }
  keys.forEach(k => seen.add(k)); seenInit = true
  const pk = (data.projects || []).map(p => p.key)
  if (!pk.includes(selectedKey.value) && pk.length) selectedKey.value = pk[0]
}

const fetchMetrics = async () => {
  loading.value = true
  try {
    const res = await fetch('/api/v1/jira/metrics', { cache: 'no-store' })
    if (res.ok) applySnapshot(await res.json())
  } catch (e) {
    // keep the last good snapshot on screen; the feed indicator carries the age
  } finally {
    loading.value = false
  }
}

let es = null
const connectLive = () => {
  try {
    es = new EventSource('/api/v1/jira/live')
    es.onmessage = (e) => {
      try { applySnapshot(JSON.parse(e.data)); live.value = true } catch (err) { /* malformed frame */ }
    }
    es.onerror = () => { live.value = false }
  } catch (e) { live.value = false }
}

let tick = null, fallback = null
onMounted(() => { document.title = "Jira" })
onMounted(() => {
  fetchMetrics()
  connectLive()
  tick = setInterval(() => { now.value = Date.now() }, 1000)              // drives the countdowns
  fallback = setInterval(() => { if (!live.value) fetchMetrics() }, 30000) // if SSE drops
})
onUnmounted(() => {
  if (es) es.close()
  if (tick) clearInterval(tick)
  if (fallback) clearInterval(fallback)
})
</script>

<style scoped>
/* ====================================================================
   SERVICE DESK WALL BOARD

   An operations instrument, not a product dashboard. Two rules do most
   of the work:

   1. Colour is signal only. Every hue on this page comes from the
      --j-* tokens in index.css, which derive from the dashboard's own
      user-themeable status colours. Chrome is achromatic. If something
      is coloured, something is happening.
   2. Anything a machine produced is monospace with tabular numerals;
      anything a person wrote is sans. That split, plus the scale jump
      between the huge tight numerals and the tiny wide-tracked labels,
      is where the board gets its character - no webfont, because a wall
      display has to render correctly with no internet.
   ==================================================================== */
.jira-view {
  --hair: hsl(var(--border));
  --hair-soft: hsl(var(--border) / 0.55);
  --surface: hsl(var(--card));
  --sunk: hsl(var(--muted) / 0.35);
}
.jira-shell { width: 100%; padding: 0.9rem 1rem 1.25rem; display: flex; flex-direction: column; gap: 0.85rem; }
/* In fullscreen the shell has to BE the viewport, not merely sit inside it.
   Its height was auto, so a child asking for height:100% resolved against
   content height and the grid rows never stretched - which is exactly the
   empty band at the bottom of the screen. */
.fs-active .jira-shell {
  height: 100vh; min-height: 0; overflow: hidden;
  padding: 0.5rem 0.6rem;
}
/* The active panel takes everything the rail leaves. Direct children only, so
   this cannot accidentally stretch something nested. */
.fs-active .jira-shell > :not(.rail) { flex: 1 1 auto; min-height: 0; }

.eyebrow {
  font-family: var(--j-mono); font-size: 10px; font-weight: 700;
  letter-spacing: 0.18em; text-transform: uppercase; color: hsl(var(--muted-foreground));
}

/* ---- command rail ------------------------------------------------- */
.rail { display: flex; align-items: center; gap: 1rem; flex-wrap: wrap; }
.rail-id { display: flex; align-items: center; gap: 0.7rem; min-width: 0; }
.rail-back { color: hsl(var(--muted-foreground)); transition: color var(--dur-2) ease; }
.rail-back:hover { color: hsl(var(--foreground)); }
.rail-mark { width: 30px; height: 30px; border-radius: 7px; flex-shrink: 0; background-size: contain; background-repeat: no-repeat; background-position: center; }
.rail-name { display: flex; flex-direction: column; min-width: 0; }
.rail-title { font-size: 1.35rem; font-weight: 800; letter-spacing: -0.02em; line-height: 1.05; }
.rail-sub { font-family: var(--j-mono); font-size: 0.7rem; color: hsl(var(--muted-foreground)); letter-spacing: 0.02em; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* Project switcher. A hairline rail, not a filled pill - on a wall the
   loudest thing must be the numbers, never the navigation. */
.projsel { display: inline-flex; border: 1px solid var(--hair); border-radius: 9px; overflow: hidden; }
.proj { display: inline-flex; align-items: baseline; gap: 0.45rem; padding: 0.35rem 0.7rem; background: transparent; cursor: pointer; color: hsl(var(--muted-foreground)); border-right: 1px solid var(--hair-soft); transition: color var(--dur-2) ease, background var(--dur-2) ease; }
.proj:last-child { border-right: 0; }
.proj:hover { color: hsl(var(--foreground)); background: hsl(var(--accent) / 0.4); }
.proj.on { color: hsl(var(--foreground)); background: var(--sunk); }
.proj-key { font-family: var(--j-mono); font-weight: 700; font-size: 0.78rem; letter-spacing: 0.04em; }
.proj-n { font-family: var(--j-mono); font-size: 0.7rem; font-variant-numeric: tabular-nums; opacity: 0.65; }
.proj.on .proj-n { opacity: 1; color: var(--j-info); }

.rail-state { display: flex; align-items: baseline; gap: 0.9rem; margin-left: auto; }
/* Plain grey text. No dot, no hue, no animation.
   It counts up on its own, so it carries its meaning without borrowing a
   status colour that would then compete with the board for attention. */
.feed {
  font-family: var(--j-mono); font-size: 0.72rem; font-variant-numeric: tabular-nums;
  letter-spacing: 0.04em; color: hsl(var(--muted-foreground)); white-space: nowrap;
}
.clock { font-family: var(--j-mono); font-size: 0.95rem; font-weight: 700; font-variant-numeric: tabular-nums; letter-spacing: 0.02em; }

/* ---- tabs ---------------------------------------------------------- */
.tabbar { display: flex; gap: 1.4rem; border-bottom: 1px solid var(--hair); }
.tab { display: inline-flex; align-items: center; gap: 0.45rem; padding: 0 0.1rem 0.55rem; font-size: 0.82rem; font-weight: 600; color: hsl(var(--muted-foreground)); background: transparent; cursor: pointer; border-bottom: 2px solid transparent; margin-bottom: -1px; transition: color var(--dur-2) ease, border-color var(--dur-2) ease; }
.tab:hover { color: hsl(var(--foreground)); }
.tab.on { color: hsl(var(--foreground)); border-bottom-color: var(--j-info); }
.tab:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 3px; border-radius: 3px; }

/* ---- notices ------------------------------------------------------- */
.notice { border: 1px dashed var(--hair); border-radius: 12px; padding: 1.5rem; }
.notice-error { border-style: solid; border-color: color-mix(in srgb, var(--j-crit) 40%, transparent); background: color-mix(in srgb, var(--j-crit) 6%, transparent); }
.notice-title { display: flex; align-items: center; gap: 0.5rem; font-weight: 700; margin-bottom: 0.35rem; }
.notice-body { font-size: 0.875rem; color: hsl(var(--muted-foreground)); }
.notice code { font-family: var(--j-mono); background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em; }
.err-pre { font-size: 12px; white-space: pre-wrap; word-break: break-word; font-family: var(--j-mono); color: var(--j-crit); background: color-mix(in srgb, var(--j-crit) 8%, transparent); border-radius: 6px; padding: 0.6rem 0.75rem; }

/* ==================== OVERVIEW: THE WALL ============================ */
.wall { display: flex; flex-direction: column; gap: 0.85rem; min-height: 0; }

/* ---- KPI rail ------------------------------------------------------ */
.kpis { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)) minmax(0, 1.9fr); border: 1px solid var(--hair); border-radius: 12px; background: var(--surface); overflow: hidden; }
.kpi { padding: 0.9rem 1.1rem; border-left: 1px solid var(--hair-soft); min-width: 0; display: flex; flex-direction: column; justify-content: center; }
.kpi:first-child { border-left: 0; }
/* A count that matters gets a left rule in its own colour rather than a tinted
   panel: the tint made four cells compete, the rule marks one. */
.kpi.hot { box-shadow: inset 3px 0 0 currentColor; }
.kpi.tone-crit { color: var(--j-crit); }
.kpi.tone-warn { color: var(--j-warn); }
.kpi-n { font-family: var(--j-mono); font-size: clamp(1.9rem, 3.4vw, 2.9rem); font-weight: 800; line-height: 0.95; letter-spacing: -0.045em; font-variant-numeric: tabular-nums; color: hsl(var(--foreground)); }
.kpi-n.crit { color: var(--j-crit); }
.kpi-n.warn { color: var(--j-warn); }
.kpi-l { font-family: var(--j-mono); font-size: 10px; font-weight: 700; letter-spacing: 0.16em; text-transform: uppercase; color: hsl(var(--muted-foreground)); margin-top: 0.5rem; }

.flow { background: var(--sunk); gap: 0.4rem; }
.flow-head { display: flex; align-items: baseline; justify-content: space-between; gap: 0.75rem; }
.flow .kpi-l { margin-top: 0; }
.flow-today { font-family: var(--j-mono); font-size: 0.78rem; font-variant-numeric: tabular-nums; display: inline-flex; align-items: baseline; gap: 0.4rem; }
.flow-today .in { color: var(--j-info); } .flow-today .out { color: var(--j-ok); }
.flow-today em { font-style: normal; font-size: 0.62rem; letter-spacing: 0.12em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
/* Diverging bars about a shared axis: inbound up, outbound down. Bottom-heavy
   means the desk is closing more than it opens. */
.flowchart { display: flex; align-items: stretch; gap: 2px; height: 46px; }
.fday { flex: 1 1 0; min-width: 0; display: flex; flex-direction: column; }
.fup, .fdown { flex: 1 1 0; display: flex; }
.fup { align-items: flex-end; }
.fup span, .fdown span { width: 100%; border-radius: 1px; min-height: 0; }
.fup span { background: var(--j-info); opacity: 0.75; align-self: flex-end; }
.fdown span { background: var(--j-ok); opacity: 0.75; }
.faxis { height: 1px; background: var(--hair); flex-shrink: 0; }
.fday.today .fup span, .fday.today .fdown span { opacity: 1; }
.fday.today .faxis { background: hsl(var(--foreground) / 0.45); }

/* ---- SLA horizon --------------------------------------------------- */
/* The signature instrument. One axis, six bands, one tick per running clock.
   The point is the SHAPE of the crowd: a mass piled at the left edge is a
   desk about to breach, and that reads from ten feet away. */
.horizon { display: grid; grid-template-columns: 150px minmax(0, 1fr); gap: 0.9rem; align-items: stretch; border: 1px solid var(--hair); border-radius: 12px; background: var(--surface); padding: 0.75rem 0.9rem; }
.hz-label { display: flex; flex-direction: column; justify-content: center; gap: 0.3rem; }
.hz-note { font-family: var(--j-mono); font-size: 0.72rem; color: hsl(var(--muted-foreground)); font-variant-numeric: tabular-nums; }
.horizon.quiet { opacity: 0.6; }
.hz-bands { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 3px; }
.band { display: flex; flex-direction: column; gap: 0.4rem; padding: 0.4rem 0.5rem 0.35rem; border-radius: 7px; background: var(--sunk); border-top: 2px solid var(--j-idle); min-width: 0; }
.band.t-crit { border-top-color: var(--j-crit); }
.band.t-warn { border-top-color: var(--j-warn); }
.band.t-ok { border-top-color: var(--j-ok); }
.band.t-crit.live { background: color-mix(in srgb, var(--j-crit) 12%, transparent); }
.band.t-warn.live { background: color-mix(in srgb, var(--j-warn) 10%, transparent); }
.band-top { display: flex; align-items: baseline; gap: 0.4rem; min-width: 0; }
.band-n { font-family: var(--j-mono); font-size: 1.25rem; font-weight: 800; line-height: 1; font-variant-numeric: tabular-nums; letter-spacing: -0.03em; color: hsl(var(--muted-foreground)); }
.band.live .band-n { color: hsl(var(--foreground)); }
.band.t-crit.live .band-n { color: var(--j-crit); }
.band.t-warn.live .band-n { color: var(--j-warn); }
.band-l { font-family: var(--j-mono); font-size: 9px; font-weight: 700; letter-spacing: 0.12em; text-transform: uppercase; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.band-ticks { display: flex; flex-wrap: wrap; align-items: flex-end; gap: 2px; min-height: 18px; }
/* Tick height carries priority, so the crowd has texture: a tall mark in the
   breached band is a Highest that is already over. */
.tick { width: 4px; height: 10px; border-radius: 1px; background: currentColor; color: var(--j-idle); opacity: 0.75; }
.band.t-crit .tick { color: var(--j-crit); }
.band.t-warn .tick { color: var(--j-warn); }
.band.t-ok .tick { color: var(--j-ok); }
.tick.p-highest { height: 18px; opacity: 1; }
.tick.p-high { height: 15px; opacity: 0.95; }
.tick.p-medium { height: 12px; }
.tick.p-low, .tick.p-lowest, .tick.p-none { height: 8px; opacity: 0.55; }

/* ---- the three columns --------------------------------------------- */
.cols { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.75rem; min-height: 0; }
.col { display: flex; flex-direction: column; min-height: 0; min-width: 0; border: 1px solid var(--hair); border-radius: 12px; background: var(--surface); overflow: hidden; }
.col-head { display: flex; align-items: center; gap: 0.5rem; padding: 0.55rem 0.8rem; border-bottom: 1px solid var(--hair); background: var(--sunk); font-family: var(--j-mono); font-size: 0.7rem; font-weight: 700; letter-spacing: 0.14em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
/* The header count is the TRUE count. The body clips on a wall, so the number
   must never be a count of what happens to be visible. */
.col-head b { margin-left: auto; display: inline-flex; align-items: baseline; gap: 0.3rem; font-size: 1rem; font-variant-numeric: tabular-nums; color: hsl(var(--foreground)); letter-spacing: -0.02em; }
.col-head b em { font-style: normal; font-size: 9px; letter-spacing: 0.12em; color: hsl(var(--muted-foreground)); }
.col-dot { width: 8px; height: 8px; border-radius: 999px; flex-shrink: 0; }
.col-risk .col-dot { background: var(--j-crit); }
.col-risk .col-head b { color: var(--j-crit); }
.col-unassigned .col-dot { background: var(--j-warn); }
.col-unassigned .col-head b { color: var(--j-warn); }
.col-in .col-dot { background: var(--j-info); }
.col-in .col-head b { color: var(--j-info); }
.col-body { flex: 1 1 auto; min-height: 0; overflow-y: auto; max-height: 46vh; }
.fs-active .col-body { max-height: none; }
.col-empty { padding: 1.1rem 0.85rem; font-size: 0.82rem; color: hsl(var(--muted-foreground)); }

.row {
  display: grid; grid-template-columns: minmax(0, 1fr) auto; grid-template-rows: auto auto auto;
  gap: 0.15rem 0.6rem; align-items: baseline; width: 100%; text-align: left;
  padding: 0.5rem 0.8rem; border-bottom: 1px solid var(--hair-soft);
  background: transparent; cursor: pointer; transition: background var(--dur-2) ease;
}
.row:last-child { border-bottom: 0; }
.row:hover { background: hsl(var(--accent) / 0.45); }
.row:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: -2px; }
.r-key { grid-column: 1; font-family: var(--j-mono); font-weight: 700; font-size: 0.74rem; letter-spacing: 0.02em; color: hsl(var(--muted-foreground)); }
.r-sum { grid-column: 1; grid-row: 2; font-size: 0.86rem; line-height: 1.25; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.r-val { grid-column: 2; grid-row: 1 / span 2; align-self: center; font-family: var(--j-mono); font-size: 0.78rem; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); white-space: nowrap; }
.clock-val { font-weight: 700; }
.r-meta { grid-column: 1 / -1; grid-row: 3; display: flex; align-items: center; gap: 0.45rem; margin-top: 0.25rem; min-width: 0; overflow: hidden; }
.r-meta > * { flex-shrink: 0; }
.r-who, .r-status, .r-prio, .r-sla-name { font-size: 0.7rem; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.r-who { flex-shrink: 1; }
.r-who.none { color: var(--j-warn); }
.r-sla-name { font-family: var(--j-mono); font-size: 0.64rem; opacity: 0.7; flex-shrink: 1; }
.r-status { display: inline-flex; align-items: center; gap: 0.3rem; flex-shrink: 1; }
.sdot { width: 6px; height: 6px; border-radius: 999px; flex-shrink: 0; background: var(--j-idle); }
.sdot.cat-new { background: var(--j-idle); }
.sdot.cat-indeterminate { background: var(--j-info); }
.sdot.cat-done { background: var(--j-ok); }
.r-prio { font-weight: 600; }

/* A ticket that appeared since the last poll. Cyan because it is news, not a
   problem - the board keeps red for things that are actually going wrong. */
.row.fresh, .trow.fresh { animation: jfresh 8s var(--ease-out-quart); }
@keyframes jfresh {
  0% { background: color-mix(in srgb, var(--j-info) 26%, transparent); box-shadow: inset 3px 0 0 var(--j-info); }
  14% { background: color-mix(in srgb, var(--j-info) 18%, transparent); box-shadow: inset 3px 0 0 var(--j-info); }
  100% { background: transparent; box-shadow: inset 3px 0 0 transparent; }
}

.sla-ok { color: hsl(var(--muted-foreground)); }
.sla-warn { color: var(--j-warn); }
.sla-crit { color: var(--j-crit); opacity: 0.85; }
.sla-over { color: var(--j-crit); }
.sla-hold { color: hsl(var(--muted-foreground) / 0.7); }

/* ==================== QUEUE: THE DESK =============================== */
.queue { display: flex; flex-direction: column; gap: 0.8rem; }
.q-tools { display: flex; align-items: flex-end; justify-content: space-between; gap: 1.25rem; flex-wrap: wrap; }
.mixes { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 1.1rem; flex: 1 1 420px; min-width: 0; }
.mix { min-width: 0; }
.bar { display: flex; height: 8px; border-radius: 999px; overflow: hidden; background: var(--sunk); gap: 2px; margin-top: 0.4rem; }
.fill { height: 100%; } .fill.empty { flex: 1; background: var(--sunk); }
.legend { display: flex; flex-wrap: wrap; gap: 0.25rem 0.85rem; margin-top: 0.45rem; }
.legend li { display: inline-flex; align-items: center; gap: 0.35rem; font-size: 0.74rem; color: hsl(var(--muted-foreground)); }
.legend i { width: 8px; height: 8px; border-radius: 2px; }
.legend b { color: hsl(var(--foreground)); font-family: var(--j-mono); font-variant-numeric: tabular-nums; }
.q-right { display: flex; align-items: center; gap: 0.7rem; }
.q-filter { font-family: var(--j-mono); font-size: 0.82rem; background: hsl(var(--background)); border: 1px solid var(--hair); border-radius: 8px; padding: 0.4rem 0.7rem; width: 12rem; }
.q-filter:focus { outline: none; border-color: var(--j-info); box-shadow: 0 0 0 3px color-mix(in srgb, var(--j-info) 18%, transparent); }
.q-open { display: inline-flex; align-items: center; gap: 0.35rem; font-size: 0.78rem; font-weight: 600; color: var(--j-info); white-space: nowrap; }
.q-open:hover { text-decoration: underline; }

.chips { display: flex; flex-wrap: wrap; gap: 0.35rem; }
.chip { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.73rem; color: hsl(var(--muted-foreground)); background: var(--sunk); border: 1px solid var(--hair); border-radius: 999px; padding: 0.15rem 0.3rem 0.15rem 0.65rem; }
.chip b { font-family: var(--j-mono); font-variant-numeric: tabular-nums; color: hsl(var(--foreground)); background: hsl(var(--background)); border-radius: 999px; padding: 0.02rem 0.42rem; font-size: 0.7rem; }

/* Bounded. Without a max-height this grew with every ticket and the page
   scrolled forever; the table now scrolls inside its own frame instead. */
.table {
  border: 1px solid var(--hair); border-radius: 12px; background: var(--surface);
  max-height: calc(100vh - 19rem); overflow: auto;
}
.fs-active .table { max-height: calc(100vh - 12rem); }
.thead, .trow { display: grid; grid-template-columns: 96px 92px minmax(0, 1fr) 150px 88px 150px 120px; gap: 0.75rem; align-items: center; }
.thead { padding: 0.45rem 0.9rem; border-bottom: 1px solid var(--hair); background: var(--sunk); }
.th { display: inline-flex; align-items: center; gap: 0.3rem; font-family: var(--j-mono); font-size: 10px; font-weight: 700; letter-spacing: 0.14em; text-transform: uppercase; color: hsl(var(--muted-foreground)); background: transparent; cursor: pointer; text-align: left; white-space: nowrap; }
.th:hover, .th.on { color: hsl(var(--foreground)); }
.th.right { justify-content: flex-end; }
.th .ar { font-size: 8px; line-height: 1; }
.trow { width: 100%; text-align: left; padding: 0.6rem 0.9rem; border-bottom: 1px solid var(--hair-soft); cursor: pointer; background: transparent; transition: background var(--dur-2) ease; }
.trow:last-child { border-bottom: 0; }
.trow:hover { background: hsl(var(--accent) / 0.45); }
.trow .r-key { color: hsl(var(--foreground)); font-size: 0.8rem; }
.t-sum { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 0.88rem; }
.trow .r-status, .trow .r-who, .trow .r-prio { font-size: 0.78rem; }
.t-sla { font-family: var(--j-mono); font-size: 0.78rem; font-variant-numeric: tabular-nums; text-align: right; color: hsl(var(--muted-foreground)); }
@media (max-width: 1000px) {
  .thead { display: none; }
  .trow { grid-template-columns: 82px minmax(0, 1fr) 104px; }
  .trow .ttag, .trow .r-status, .trow .r-who, .trow .r-prio { display: none; }
}

/* ---- type tags ----------------------------------------------------- */
/* Achromatic by default. A tag only takes a signal colour when the TYPE
   itself is a signal - an incident is louder than a service request, and
   nothing here is allowed to be merely decorative. */
.ttag { font-family: var(--j-mono); font-size: 9px; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase; padding: 0.12rem 0.38rem; border-radius: 4px; white-space: nowrap; text-align: center; background: var(--sunk); color: hsl(var(--muted-foreground)); }
.ttag-incident { background: color-mix(in srgb, var(--j-crit) 16%, transparent); color: var(--j-crit); }
.ttag-problem { background: color-mix(in srgb, var(--j-warn) 16%, transparent); color: var(--j-warn); }
.ttag-change { background: color-mix(in srgb, var(--j-info) 14%, transparent); color: var(--j-info); }
.ttag-story { background: color-mix(in srgb, var(--j-ok) 14%, transparent); color: var(--j-ok); }
.ttag-epic { background: color-mix(in srgb, var(--j-cat-2) 16%, transparent); color: var(--j-cat-2); }
.ttag-request, .ttag-task { background: var(--sunk); color: hsl(var(--muted-foreground)); }

.prio-highest { color: var(--j-crit); }
.prio-high { color: var(--j-warn); }
.prio-medium, .prio-low, .prio-lowest, .prio-none { color: hsl(var(--muted-foreground)); }

@media (max-width: 900px) {
  .kpis { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .kpi { border-top: 1px solid var(--hair-soft); }
  .kpi:nth-child(-n+2) { border-top: 0; }
  .kpi:nth-child(odd) { border-left: 0; }
  .flow { grid-column: 1 / -1; }
  .horizon { grid-template-columns: 1fr; }
  .hz-bands { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .cols { grid-template-columns: 1fr; }
}
</style>
