<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 space-y-5 jira-panel">

      <!-- Toolbar -->
      <div class="flex items-end justify-between gap-4 flex-wrap">
        <div class="flex items-center gap-3">
          <router-link to="/" class="text-muted-foreground hover:text-foreground transition-colors mb-1"
            data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-5 w-5" />
          </router-link>
          <span class="jira-mark" :style="{ backgroundImage: `url(${jiraIcon})` }"></span>
          <div>
            <h1 class="text-2xl font-bold tracking-tight leading-none">
              Service desk <span class="text-muted-foreground font-normal">monitor</span>
            </h1>
          </div>
        </div>
        <div class="flex items-center gap-3 mb-0.5">
          <span v-if="snapshot.demo" class="demo-badge">demo data</span>
          <span class="live-ind" :class="{ on: live }"><span class="ldot"></span>{{ live ? 'live' : updatedLabel }}</span>
          <Button variant="ghost" size="icon" class="h-9 w-9" @click="fetchMetrics" data-tooltip="Refresh" data-tip-pos="bottom">
            <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
          </Button>
        </div>
      </div>

      <div v-if="loaded && !snapshot.configured" class="notice">
        <div class="notice-title">Jira isn’t connected yet</div>
        <p class="notice-body">Set <code>JIRA_BASE_URL</code>, <code>JIRA_EMAIL</code> and <code>JIRA_API_TOKEN</code> in <code>.env</code>, then <code>docker compose up -d</code>.</p>
      </div>
      <div v-else-if="loaded && snapshot.configured && !snapshot.ok" class="notice notice-error">
        <div class="notice-title flex items-center gap-2"><AlertTriangle class="h-4 w-4" /> Can’t reach Jira</div>
        <pre class="err-pre">{{ snapshot.error }}</pre>
      </div>

      <template v-else-if="snapshot.configured && snapshot.ok && proj">
        <!-- Project switcher -->
        <div class="segmented" role="tablist">
          <button v-for="p in projects" :key="p.key" role="tab" :aria-selected="p.key === selectedKey"
            class="seg" :class="{ active: p.key === selectedKey }" @click="selectedKey = p.key">
            <span class="seg-key">{{ p.key }}</span>
            <span class="seg-name">{{ p.name }}</span>
            <span class="seg-count">{{ p.totalOpen }}</span>
          </button>
        </div>

        <!-- KPI rail -->
        <div class="kpi-rail">
          <div class="kpi kpi-lead"><div class="kpi-num">{{ proj.totalOpen }}</div><div class="kpi-label">Open tickets</div></div>
          <div class="kpi"><div class="kpi-num" :class="{ warn: proj.unassigned > 0 }">{{ proj.unassigned }}</div><div class="kpi-label">Unassigned</div></div>
          <div class="kpi"><div class="kpi-num" :class="{ bad: proj.slaBreached > 0 }">{{ slaValue }}</div><div class="kpi-label">SLA breached</div></div>
          <div class="kpi"><div class="kpi-num">{{ avgResolution }}</div><div class="kpi-label">Avg resolution</div></div>
          <div class="kpi"><div class="kpi-num">{{ proj.createdToday }}</div><div class="kpi-label">Created today</div></div>
          <div class="kpi"><div class="kpi-num" :class="{ good: proj.resolvedToday > 0 }">{{ proj.resolvedToday }}</div><div class="kpi-label">Resolved today</div></div>
        </div>

        <!-- Breakdown + activity -->
        <div class="insight-grid">
          <section class="panel">
            <div class="panel-row">
              <div class="panel-title">Priority</div>
              <div class="segbar">
                <span v-for="s in prioritySegments" :key="s.name" class="segf" :style="{ width: s.pct + '%', background: s.color }" :title="`${s.name}: ${s.count}`"></span>
                <span v-if="!prioritySegments.length" class="segf empty"></span>
              </div>
              <ul class="seg-legend"><li v-for="s in prioritySegments" :key="s.name"><span class="sw" :style="{ background: s.color }"></span>{{ s.name }}<b>{{ s.count }}</b></li></ul>
            </div>
            <div class="panel-row">
              <div class="panel-title">Type</div>
              <div class="segbar">
                <span v-for="s in typeSegments" :key="s.name" class="segf" :style="{ width: s.pct + '%', background: s.color }" :title="`${s.name}: ${s.count}`"></span>
                <span v-if="!typeSegments.length" class="segf empty"></span>
              </div>
              <ul class="seg-legend"><li v-for="s in typeSegments" :key="s.name"><span class="sw" :style="{ background: s.color }"></span>{{ s.name }}<b>{{ s.count }}</b></li></ul>
            </div>
            <div v-if="proj.byStatus && proj.byStatus.length" class="status-chips">
              <span v-for="s in proj.byStatus" :key="s.name" class="chip">{{ s.name }}<b>{{ s.count }}</b></span>
            </div>
          </section>

          <section class="panel activity">
            <div class="panel-title">14-day activity</div>
            <svg class="spark" viewBox="0 0 300 70" preserveAspectRatio="none">
              <polygon :points="spark.area" class="spark-area" />
              <polyline :points="spark.created" class="spark-c" vector-effect="non-scaling-stroke" />
              <polyline :points="spark.resolved" class="spark-r" vector-effect="non-scaling-stroke" />
            </svg>
            <div class="activity-legend">
              <span><i class="mk mk-c"></i>Created <b>{{ proj.createdLast7d }}</b><em>7d</em></span>
              <span><i class="mk mk-r"></i>Resolved <b>{{ proj.resolvedLast7d }}</b><em>7d</em></span>
            </div>
          </section>
        </div>

        <!-- Tickets header + view toggle -->
        <div class="flex items-center justify-between gap-3 flex-wrap">
          <h2 class="section-title">Open tickets <span class="opacity-60">· {{ proj.key }}</span></h2>
          <div class="flex items-center gap-2">
            <input v-model="search" type="text" placeholder="filter tickets"
              class="text-sm font-mono bg-background border rounded-md px-3 py-1.5 w-44 focus:outline-none focus:ring-1 focus:ring-ring" />
            <div class="viewtoggle">
              <button :class="{ on: view === 'list' }" @click="view = 'list'" data-tooltip="List" data-tip-pos="bottom"><Rows3 class="h-4 w-4" /></button>
              <button :class="{ on: view === 'board' }" @click="view = 'board'" data-tooltip="Board" data-tip-pos="bottom"><Columns3 class="h-4 w-4" /></button>
            </div>
          </div>
        </div>

        <!-- LIST view -->
        <div v-if="view === 'list' && sortedIssues.length" class="ticket-list">
          <div class="thead">
            <button class="th" @click="sortBy('key')">Key<span class="ar">{{ arrow('key') }}</span></button>
            <button class="th" @click="sortBy('type')">Type<span class="ar">{{ arrow('type') }}</span></button>
            <button class="th" @click="sortBy('summary')">Summary<span class="ar">{{ arrow('summary') }}</span></button>
            <button class="th" @click="sortBy('status')">Status<span class="ar">{{ arrow('status') }}</span></button>
            <button class="th" @click="sortBy('priority')">Priority<span class="ar">{{ arrow('priority') }}</span></button>
            <button class="th" @click="sortBy('assignee')">Assignee<span class="ar">{{ arrow('assignee') }}</span></button>
            <button class="th th-right" @click="sortBy('sla')">SLA<span class="ar">{{ arrow('sla') }}</span></button>
          </div>
          <button v-for="it in sortedIssues" :key="it.key" class="trow" :class="{ flash: flash.has(it.key) }" @click="openKey = it.key">
            <span class="t-key">{{ it.key }}</span>
            <span class="ttag" :class="typeClass(it.type)">{{ shortType(it.type) }}</span>
            <span class="t-sum">{{ it.summary || '—' }}</span>
            <span class="t-status"><span class="sdot" :class="'cat-' + (it.category || 'new')"></span>{{ it.status || '—' }}</span>
            <span class="t-prio" :class="'prio-' + prioKey(it.priority)">{{ it.priority || '—' }}</span>
            <span class="t-assignee" :class="{ dim: !it.assignee }">{{ it.assignee || 'Unassigned' }}</span>
            <span class="t-sla" :class="slaView(it).cls">{{ slaView(it).text || ageLabel(it.created) }}</span>
          </button>
        </div>

        <!-- BOARD view -->
        <div v-else-if="view === 'board' && sortedIssues.length" class="board">
          <div v-for="col in boardColumns" :key="col.status" class="bcol">
            <div class="bcol-head"><span class="sdot" :class="'cat-' + (col.category || 'new')"></span>{{ col.status }}<b>{{ col.items.length }}</b></div>
            <div class="bcol-body">
              <button v-for="it in col.items" :key="it.key" class="bcard" :class="{ flash: flash.has(it.key) }" @click="openKey = it.key">
                <div class="bc-top"><span class="t-key">{{ it.key }}</span><span class="ttag" :class="typeClass(it.type)">{{ shortType(it.type) }}</span></div>
                <div class="bc-sum">{{ it.summary || '—' }}</div>
                <div class="bc-foot">
                  <span class="t-prio" :class="'prio-' + prioKey(it.priority)">{{ it.priority || '—' }}</span>
                  <span v-if="slaView(it).show" class="t-sla" :class="slaView(it).cls">{{ slaView(it).text }}</span>
                  <span class="bc-assignee" :class="{ dim: !it.assignee }">{{ initials(it.assignee) }}</span>
                </div>
              </button>
            </div>
          </div>
        </div>

        <div v-else class="notice">
          <div class="notice-title">No open tickets in {{ proj.key }}</div>
          <p class="notice-body">Nothing is currently in an un-done status for this project.</p>
        </div>
      </template>
    </div>

    <JiraTicketPanel :issue-key="openKey" @close="openKey = ''" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { ArrowLeft, RefreshCw, AlertTriangle, Rows3, Columns3 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { generatePrettyTimeAgo } from '@/utils/time'
import jiraIcon from '@/assets/jira.png'
import JiraTicketPanel from '@/components/JiraTicketPanel.vue'

const RED = '#ef6b53', GOLD = '#e0a458', GRAY = '#8a8f98'
const prioColor = { highest: RED, high: GOLD, medium: GRAY, low: '#6b7280', lowest: '#6b7280', none: '#6b7280' }
const typePalette = [GOLD, '#b08968', GRAY, '#7d8471', '#a3907a', '#9c6f5e', '#7a7d86', '#c2a878']

const loading = ref(false)
const loaded = ref(false)
const live = ref(false)
const search = ref('')
const view = ref('list')
const selectedKey = ref('')
const openKey = ref('')
const now = ref(Date.now())
const snapshot = ref({ configured: false, ok: false, status: 'unknown', projects: [] })

const projects = computed(() => snapshot.value.projects || [])
const proj = computed(() => projects.value.find(p => p.key === selectedKey.value) || projects.value[0] || null)

const avgResolution = computed(() => {
  const h = proj.value?.avgResolutionHours || 0
  if (!h) return '—'
  return h >= 48 ? (h / 24).toFixed(1) + 'd' : h.toFixed(1) + 'h'
})
const slaValue = computed(() => {
  const s = proj.value?.slaBreached
  return (s === undefined || s === null || s < 0) ? '—' : String(s)
})

const prioKey = (p) => (p || '').toLowerCase().replace(/[^a-z]/g, '') || 'none'
const segmentsOf = (items, colorFn) => {
  const list = (items || []).filter(i => i.count > 0)
  const total = list.reduce((a, b) => a + b.count, 0) || 1
  return list.map((i, idx) => ({ name: i.name, count: i.count, pct: (i.count / total) * 100, color: colorFn(i, idx) }))
}
const prioritySegments = computed(() => segmentsOf(proj.value?.byPriority, (i) => prioColor[prioKey(i.name)] || GRAY))
const typeSegments = computed(() => segmentsOf(proj.value?.byType, (_, idx) => typePalette[idx % typePalette.length]))

const spark = computed(() => {
  const t = proj.value?.trend || []
  const n = t.length
  if (!n) return { created: '', resolved: '', area: '' }
  const W = 300, H = 70, pad = 6
  const max = Math.max(1, ...t.map(p => Math.max(p.created, p.resolved)))
  const X = (i) => pad + (i / Math.max(1, n - 1)) * (W - 2 * pad)
  const Y = (v) => H - pad - (v / max) * (H - 2 * pad)
  const line = (key) => t.map((p, i) => `${X(i).toFixed(1)},${Y(p[key]).toFixed(1)}`).join(' ')
  const created = line('created')
  return { created, resolved: line('resolved'), area: `${pad},${H - pad} ${created} ${W - pad},${H - pad}` }
})

// --- live SLA countdown -------------------------------------------------
const fmtDur = (ms) => {
  let s = Math.floor(Math.abs(ms) / 1000)
  const d = Math.floor(s / 86400); s -= d * 86400
  const h = Math.floor(s / 3600); s -= h * 3600
  const m = Math.floor(s / 60); s -= m * 60
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m ${String(s).padStart(2, '0')}s`
}
const hasSla = (it) => !!(it.slaName || it.slaBreached)
const remainingOf = (it) => (it.slaActive && it.slaBreachEpoch) ? (it.slaBreachEpoch - now.value) : it.slaRemainingMs
const slaView = (it) => {
  if (!hasSla(it)) return { show: false, cls: '', text: '' }
  const rem = remainingOf(it)
  if (it.slaBreached || rem < 0) return { show: true, cls: 'sla-over', text: 'overdue ' + fmtDur(rem) }
  if (it.slaPaused) return { show: true, cls: 'sla-paused', text: fmtDur(rem) + ' paused' }
  const mins = rem / 60000
  return { show: true, cls: mins < 15 ? 'sla-crit' : mins < 60 ? 'sla-warn' : 'sla-ok', text: fmtDur(rem) }
}
// urgency sort uses the snapshot value (not the ticking `now`) so rows don't reshuffle every second
const slaSortKey = (it) => {
  if (it.slaBreached) return -1e14 + (it.slaRemainingMs || 0)
  if (hasSla(it)) return it.slaRemainingMs
  return 1e14
}

const filteredIssues = computed(() => {
  const list = proj.value?.issues || []
  const q = search.value.trim().toLowerCase()
  if (!q) return list
  return list.filter(it => [it.key, it.summary, it.assignee, it.status, it.type].some(v => (v || '').toString().toLowerCase().includes(q)))
})

// --- sortable columns (default: SLA urgency, most overdue first) ---
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

const boardColumns = computed(() => {
  const catOrder = { new: 0, indeterminate: 1, done: 2 }
  const map = new Map()
  for (const it of sortedIssues.value) {
    const k = it.status || '—'
    if (!map.has(k)) map.set(k, { status: k, category: it.category, items: [] })
    map.get(k).items.push(it)
  }
  return [...map.values()].sort((a, b) => (catOrder[a.category] ?? 1) - (catOrder[b.category] ?? 1) || a.status.localeCompare(b.status))
})

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
const initials = (name) => !name ? '—' : name.split(/\s+/).map(w => w[0]).slice(0, 2).join('').toUpperCase()
const ageLabel = (c) => { if (!c) return '—'; try { return generatePrettyTimeAgo(new Date(c)) } catch (e) { return '—' } }
const updatedLabel = computed(() => {
  if (!snapshot.value.updatedAt) return 'never'
  try { return 'updated ' + generatePrettyTimeAgo(new Date(snapshot.value.updatedAt)) } catch (e) { return '—' }
})

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
      setTimeout(() => { const s2 = new Set(flash.value); fresh.forEach(k => s2.delete(k)); flash.value = s2 }, 6000)
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
  } catch (e) { /* keep last */ } finally { loading.value = false }
}

let es = null
const connectLive = () => {
  try {
    es = new EventSource('/api/v1/jira/live')
    es.onmessage = (e) => { try { applySnapshot(JSON.parse(e.data)); live.value = true } catch (err) { /* ignore */ } }
    es.onerror = () => { live.value = false }
  } catch (e) { live.value = false }
}

let tick = null, fallback = null
onMounted(() => {
  fetchMetrics()
  connectLive()
  tick = setInterval(() => { now.value = Date.now() }, 1000)          // drives SLA countdowns
  fallback = setInterval(() => { if (!live.value) fetchMetrics() }, 30000) // if SSE drops
})
onUnmounted(() => {
  if (es) es.close()
  if (tick) clearInterval(tick)
  if (fallback) clearInterval(fallback)
})
</script>

<style scoped>
.jira-panel { width: 100%; }
.eyebrow { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 10px; letter-spacing: 0.18em; text-transform: uppercase; color: hsl(var(--muted-foreground)); font-weight: 600; }
.jira-mark { display: inline-block; width: 32px; height: 32px; border-radius: 8px; background-size: contain; background-repeat: no-repeat; background-position: center; }
.demo-badge { font-size: 10px; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase; color: #1a1206; background: #e0a458; padding: 0.15rem 0.5rem; border-radius: 5px; }

.live-ind { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.72rem; font-family: ui-monospace, monospace; color: hsl(var(--muted-foreground)); text-transform: uppercase; letter-spacing: 0.08em; }
.live-ind .ldot { width: 7px; height: 7px; border-radius: 999px; background: hsl(var(--muted-foreground) / 0.5); }
.live-ind.on { color: #7bbd8a; }
.live-ind.on .ldot { background: #5aa06b; }

.notice { border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 1.5rem; }
.notice-error { border-style: solid; border-color: hsl(var(--destructive) / 0.4); background: hsl(var(--destructive) / 0.05); }
.notice-title { font-weight: 700; margin-bottom: 0.35rem; }
.notice-body { font-size: 0.875rem; color: hsl(var(--muted-foreground)); }
.notice code { font-family: ui-monospace, monospace; background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em; }
.err-pre { font-size: 12px; white-space: pre-wrap; word-break: break-word; color: hsl(var(--destructive)); background: hsl(var(--destructive) / 0.08); border-radius: 6px; padding: 0.6rem 0.75rem; font-family: ui-monospace, monospace; }

.segmented { display: inline-flex; gap: 4px; padding: 4px; background: hsl(var(--muted) / 0.45); border: 1px solid hsl(var(--border)); border-radius: 11px; }
.seg { display: flex; align-items: center; gap: 0.5rem; padding: 0.4rem 0.8rem; border-radius: 8px; cursor: pointer; color: hsl(var(--muted-foreground)); transition: background 0.18s cubic-bezier(0.22,1,0.36,1), color 0.18s; }
.seg:hover { color: hsl(var(--foreground)); }
.seg.active { background: hsl(var(--background)); color: hsl(var(--foreground)); box-shadow: 0 1px 3px rgb(0 0 0 / 0.25); }
.seg-key { font-family: ui-monospace, monospace; font-weight: 700; font-size: 0.8rem; }
.seg-name { font-size: 0.8rem; }
.seg-count { font-size: 0.7rem; font-weight: 700; background: hsl(var(--muted)); color: hsl(var(--foreground)); padding: 0.05rem 0.4rem; border-radius: 999px; min-width: 1.4rem; text-align: center; }
.seg.active .seg-count { background: #e0a458; color: #1a1206; }
@media (max-width: 640px) { .seg-name { display: none; } }

.kpi-rail { display: grid; grid-template-columns: 1.3fr repeat(5, 1fr); border: 1px solid hsl(var(--border)); border-radius: 12px; overflow: hidden; background: hsl(var(--card)); }
.kpi { padding: 0.95rem 1.05rem; border-left: 1px solid hsl(var(--border) / 0.7); }
.kpi:first-child { border-left: 0; }
.kpi-lead { background: hsl(var(--muted) / 0.28); }
.kpi-num { font-size: 1.8rem; font-weight: 800; line-height: 1; font-variant-numeric: tabular-nums; letter-spacing: -0.02em; color: hsl(var(--foreground)); }
.kpi-lead .kpi-num { font-size: 2.4rem; }
.kpi-num.warn { color: #e0a458; } .kpi-num.bad { color: #ef6b53; } .kpi-num.good { color: #5aa06b; }
.kpi-label { font-size: 10px; letter-spacing: 0.1em; text-transform: uppercase; color: hsl(var(--muted-foreground)); font-weight: 600; margin-top: 0.4rem; }
@media (max-width: 900px) { .kpi-rail { grid-template-columns: repeat(3, 1fr); } .kpi { border-top: 1px solid hsl(var(--border) / 0.7); } .kpi:nth-child(-n+3) { border-top: 0; } .kpi:nth-child(3n+1) { border-left: 0; } .kpi-lead .kpi-num { font-size: 1.8rem; } }

.insight-grid { display: grid; grid-template-columns: 2fr 1fr; gap: 0.9rem; }
@media (max-width: 800px) { .insight-grid { grid-template-columns: 1fr; } }
.panel { border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--card)); padding: 1rem 1.1rem; }
.panel-title { font-size: 0.64rem; letter-spacing: 0.12em; text-transform: uppercase; color: hsl(var(--muted-foreground)); font-weight: 700; margin-bottom: 0.5rem; }
.panel-row + .panel-row { margin-top: 1rem; }
.segbar { display: flex; height: 9px; border-radius: 999px; overflow: hidden; background: hsl(var(--muted) / 0.5); gap: 2px; }
.segf { height: 100%; } .segf.empty { flex: 1; background: hsl(var(--muted) / 0.5); }
.seg-legend { display: flex; flex-wrap: wrap; gap: 0.35rem 1rem; margin-top: 0.55rem; }
.seg-legend li { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.78rem; color: hsl(var(--muted-foreground)); }
.seg-legend .sw { width: 9px; height: 9px; border-radius: 3px; }
.seg-legend b { color: hsl(var(--foreground)); font-variant-numeric: tabular-nums; }
.status-chips { display: flex; flex-wrap: wrap; gap: 0.4rem; margin-top: 1rem; }
.chip { font-size: 0.74rem; color: hsl(var(--muted-foreground)); background: hsl(var(--muted) / 0.45); border: 1px solid hsl(var(--border)); border-radius: 999px; padding: 0.18rem 0.3rem 0.18rem 0.65rem; display: inline-flex; align-items: center; gap: 0.4rem; }
.chip b { font-variant-numeric: tabular-nums; color: hsl(var(--foreground)); background: hsl(var(--background)); border-radius: 999px; padding: 0.03rem 0.42rem; font-size: 0.72rem; }

.activity { display: flex; flex-direction: column; }
.spark { width: 100%; height: 78px; margin: 0.3rem 0 0.2rem; display: block; }
.spark-area { fill: rgba(224,160,88,0.10); stroke: none; }
.spark-c { fill: none; stroke: #e0a458; stroke-width: 1.6; stroke-linejoin: round; stroke-linecap: round; }
.spark-r { fill: none; stroke: #5aa06b; stroke-width: 1.6; stroke-linejoin: round; stroke-linecap: round; }
.activity-legend { display: flex; gap: 1.25rem; margin-top: auto; padding-top: 0.6rem; font-size: 0.8rem; color: hsl(var(--muted-foreground)); }
.activity-legend span { display: inline-flex; align-items: center; gap: 0.35rem; }
.activity-legend b { color: hsl(var(--foreground)); font-variant-numeric: tabular-nums; }
.activity-legend em { font-style: normal; font-size: 0.66rem; opacity: 0.6; }
.mk { width: 12px; height: 2.5px; border-radius: 2px; display: inline-block; } .mk-c { background: #e0a458; } .mk-r { background: #5aa06b; }

.section-title { font-size: 0.8rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em; color: hsl(var(--muted-foreground)); }
.viewtoggle { display: inline-flex; border: 1px solid hsl(var(--border)); border-radius: 8px; overflow: hidden; }
.viewtoggle button { display: inline-flex; align-items: center; justify-content: center; height: 34px; width: 36px; color: hsl(var(--muted-foreground)); }
.viewtoggle button:hover { background: hsl(var(--accent) / 0.5); color: hsl(var(--foreground)); }
.viewtoggle button.on { background: hsl(var(--muted) / 0.7); color: hsl(var(--foreground)); }

/* list */
.ticket-list { border: 1px solid hsl(var(--border)); border-radius: 12px; overflow: hidden; }
.thead { display: grid; grid-template-columns: 84px 92px minmax(0, 1fr) 160px 88px 150px 120px; gap: 0.75rem; padding: 0.5rem 0.9rem; border-bottom: 1px solid hsl(var(--border)); background: hsl(var(--muted) / 0.4); }
.th { display: inline-flex; align-items: center; gap: 0.3rem; font-size: 10px; letter-spacing: 0.1em; text-transform: uppercase; font-weight: 600; color: hsl(var(--muted-foreground)); cursor: pointer; background: transparent; text-align: left; white-space: nowrap; }
.th:hover { color: hsl(var(--foreground)); }
.th.th-right { justify-content: flex-end; text-align: right; }
.th .ar { font-size: 8px; line-height: 1; }
@media (max-width: 1000px) { .thead { display: none; } }
.trow { display: grid; grid-template-columns: 84px 92px minmax(0, 1fr) 160px 88px 150px 120px; align-items: center; gap: 0.75rem; width: 100%; text-align: left; padding: 0.7rem 0.9rem; border-bottom: 1px solid hsl(var(--border) / 0.6); cursor: pointer; background: transparent; transition: background 0.14s ease; }
.trow:last-child { border-bottom: 0; }
.trow:hover { background: hsl(var(--accent) / 0.45); }
.trow.flash { animation: flashrow 6s ease-out; }
@keyframes flashrow { 0% { background: rgba(224,160,88,0.30); } 12% { background: rgba(224,160,88,0.22); } 100% { background: transparent; } }
.t-key { font-family: ui-monospace, monospace; font-weight: 700; font-size: 0.82rem; }
.t-sum { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 0.9rem; }
.t-status { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.8rem; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sdot { width: 7px; height: 7px; border-radius: 999px; flex-shrink: 0; background: hsl(var(--muted-foreground)); }
.sdot.cat-new { background: #8a8f98; } .sdot.cat-indeterminate { background: #e0a458; } .sdot.cat-done { background: #5aa06b; }
.t-prio { font-size: 0.8rem; font-weight: 600; }
.t-assignee { font-size: 0.82rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.t-assignee.dim { color: hsl(var(--muted-foreground)); font-style: italic; }
.t-sla { font-family: ui-monospace, monospace; font-size: 0.78rem; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); text-align: right; }
.t-sla.sla-ok { color: hsl(var(--muted-foreground)); }
.t-sla.sla-warn { color: #e0a458; }
.t-sla.sla-crit { color: #ef8b74; font-weight: 700; }
.t-sla.sla-over { color: #ef6b53; font-weight: 700; }
.t-sla.sla-paused { color: hsl(var(--muted-foreground) / 0.7); }
@media (max-width: 1000px) { .trow { grid-template-columns: 76px minmax(0,1fr) 96px; } .trow .ttag, .trow .t-status, .trow .t-assignee { display: none; } }

/* board */
.board { display: flex; gap: 0.75rem; overflow-x: auto; padding-bottom: 0.5rem; }
.bcol { flex: 0 0 260px; background: hsl(var(--muted) / 0.28); border: 1px solid hsl(var(--border)); border-radius: 12px; padding: 0.6rem; display: flex; flex-direction: column; }
.bcol-head { display: flex; align-items: center; gap: 0.45rem; font-size: 0.78rem; font-weight: 600; padding: 0.15rem 0.35rem 0.6rem; color: hsl(var(--foreground)); }
.bcol-head b { margin-left: auto; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); background: hsl(var(--background)); border-radius: 999px; padding: 0.02rem 0.45rem; font-size: 0.72rem; }
.bcol-body { display: flex; flex-direction: column; gap: 0.5rem; }
.bcard { text-align: left; width: 100%; background: hsl(var(--card)); border: 1px solid hsl(var(--border)); border-radius: 10px; padding: 0.6rem 0.7rem; cursor: pointer; transition: border-color 0.14s ease, transform 0.14s ease; }
.bcard:hover { border-color: hsl(var(--muted-foreground) / 0.5); transform: translateY(-1px); }
.bcard.flash { animation: flashcard 6s ease-out; }
@keyframes flashcard { 0% { border-color: #e0a458; box-shadow: 0 0 0 2px rgba(224,160,88,0.35); } 100% { border-color: hsl(var(--border)); box-shadow: none; } }
.bc-top { display: flex; align-items: center; justify-content: space-between; gap: 0.4rem; margin-bottom: 0.4rem; }
.bc-sum { font-size: 0.85rem; line-height: 1.35; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.bc-foot { display: flex; align-items: center; gap: 0.5rem; margin-top: 0.55rem; }
.bc-foot .t-sla { margin-left: auto; }
.bc-assignee { font-size: 0.66rem; font-weight: 700; width: 22px; height: 22px; border-radius: 999px; display: inline-flex; align-items: center; justify-content: center; background: hsl(var(--muted)); color: hsl(var(--foreground)); }
.bc-assignee.dim { color: hsl(var(--muted-foreground)); }

.ttag { font-size: 10px; letter-spacing: 0.03em; text-transform: uppercase; padding: 0.1rem 0.4rem; border-radius: 5px; font-weight: 700; white-space: nowrap; text-align: center; }
.ttag-incident { background: rgb(190 60 40 / 0.16); color: #ef8b74; }
.ttag-request { background: hsl(var(--muted) / 0.7); color: hsl(var(--foreground)); }
.ttag-change { background: rgb(224 160 88 / 0.16); color: #e0a458; }
.ttag-problem { background: rgb(224 160 88 / 0.18); color: #e6b877; }
.ttag-epic { background: rgb(147 112 90 / 0.22); color: #c2a17f; }
.ttag-story { background: rgb(90 160 107 / 0.16); color: #7bbd8a; }
.ttag-task { background: hsl(var(--muted) / 0.7); color: hsl(var(--muted-foreground)); }

.prio-highest { color: #ef6b53; } .prio-high { color: #e0a458; } .prio-medium { color: hsl(var(--muted-foreground)); } .prio-low, .prio-lowest, .prio-none { color: hsl(var(--muted-foreground)); }
</style>
