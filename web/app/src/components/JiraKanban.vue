<template>
  <div class="kanban">
    <!-- Board toolbar -->
    <div class="ktoolbar">
      <div class="kt-left">
        <div class="boardpick">
          <label class="bp-label">Board</label>
          <select v-model.number="boardId" class="bp-select" :disabled="!boards.length">
            <optgroup v-for="g in boardGroups" :key="g.label" :label="g.label">
              <option v-for="b in g.boards" :key="b.id" :value="b.id">{{ b.name }}</option>
            </optgroup>
            <option v-if="!boards.length" :value="0">no boards found</option>
          </select>
        </div>
        <div v-if="board.ok" class="kt-facts">
          <span class="fact"><b>{{ visibleCount }}</b> cards</span>
          <span v-if="overWip.length" class="fact fact-bad">
            <b>{{ overWip.length }}</b> {{ overWip.length === 1 ? 'column' : 'columns' }} over WIP
          </span>
          <span v-if="board.doneWindowDays" class="fact fact-dim">done · last {{ board.doneWindowDays }}d</span>
          <span v-if="board.truncated" class="fact fact-dim" data-tooltip="More cards exist than the fetch cap. Raise JIRA_BOARD_MAX_CARDS to see them." data-tip-pos="bottom">capped</span>
        </div>
      </div>
      <div class="kt-right">
        <input v-model="filter" type="text" placeholder="filter cards" class="kfilter" />
        <div class="lanepick">
          <label class="bp-label">Lanes</label>
          <select v-model="swimlane" class="bp-select">
            <option value="none">None</option>
            <option value="assignee">Assignee</option>
            <option value="priority">Priority</option>
            <option value="epic">Epic</option>
            <option value="type">Type</option>
          </select>
        </div>
        <span class="live-ind" :class="{ on: live }"><span class="ldot"></span>{{ live ? 'live' : updatedLabel }}</span>
        <Button variant="ghost" size="icon" class="h-9 w-9" @click="fetchBoard" data-tooltip="Refresh board" data-tip-pos="bottom">
          <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
        </Button>
      </div>
    </div>

    <!-- States -->
    <div v-if="loading && !board.columns.length" class="notice">
      <div class="notice-title">Loading the board…</div>
      <p class="notice-body">Reading the column configuration and cards from Jira.</p>
    </div>
    <div v-else-if="boardsError" class="notice notice-error">
      <div class="notice-title flex items-center gap-2"><AlertTriangle class="h-4 w-4" /> Can’t list boards</div>
      <pre class="err-pre">{{ boardsError }}</pre>
    </div>
    <div v-else-if="!boards.length" class="notice">
      <div class="notice-title">No agile boards on these projects</div>
      <p class="notice-body">
        The account can’t see a board for {{ projectHint }}. Create a Kanban board in Jira, or point
        <code>JIRA_PROJECTS</code> at a project that has one.
      </p>
    </div>
    <div v-else-if="board.error && !board.columns.length" class="notice notice-error">
      <div class="notice-title flex items-center gap-2"><AlertTriangle class="h-4 w-4" /> Can’t read this board</div>
      <pre class="err-pre">{{ board.error }}</pre>
    </div>
    <div v-else-if="!board.columns.length" class="notice">
      <div class="notice-title">This board has no columns</div>
      <p class="notice-body">Add columns to the board in Jira and they’ll show up here.</p>
    </div>

    <!-- Board -->
    <template v-else>
      <div v-if="board.error" class="partial">
        <AlertTriangle class="h-3.5 w-3.5" /> Showing the last good read — {{ board.error }}
      </div>

      <div class="bwrap">
        <!-- no swimlanes: one straight set of columns -->
        <div v-if="swimlane === 'none'" class="bgrid">
          <section v-for="col in columns" :key="col.name" class="kcol" :class="[
            'cat-' + (col.category || 'new'),
            { collapsed: collapsed.has(col.name), over: isOver(col) }
          ]">
            <header class="kcol-head" @click="toggleCollapse(col.name)">
              <button class="kc-toggle" :aria-label="collapsed.has(col.name) ? 'Expand ' + col.name : 'Collapse ' + col.name">
                <ChevronDown class="h-3.5 w-3.5" :class="{ turn: collapsed.has(col.name) }" />
              </button>
              <span class="kc-name">{{ col.name }}</span>
              <span class="kc-count" :class="{ bad: isOver(col) }">{{ col.cards.length }}</span>
            </header>
            <div v-if="!collapsed.has(col.name)" class="kc-meter">
              <template v-if="col.max">
                <span v-for="i in slotCount(col)" :key="i" class="slot" :class="slotClass(col, i)"></span>
              </template>
              <span v-else class="share"><span class="share-fill" :style="{ width: sharePct(col) + '%' }"></span></span>
            </div>
            <div v-if="!collapsed.has(col.name)" class="kc-note" :class="noteClass(col)">{{ noteFor(col) }}</div>
            <div v-if="!collapsed.has(col.name)" class="kc-body">
              <button v-for="c in col.cards" :key="c.key" class="kcard" :class="[ageClass(c), { fresh: fresh.has(c.key), moved: moved.has(c.key) }]"
                @click="$emit('open', c.key)">
                <span v-if="moved.has(c.key)" class="movepip">moved</span>
                <div class="kc-top">
                  <span class="k-key">{{ c.key }}</span>
                  <span class="ttag" :class="typeClass(c.type)">{{ shortType(c.type) }}</span>
                </div>
                <div class="kc-sum">{{ c.summary || '—' }}</div>
                <div v-if="c.epic && c.epicKey !== c.key" class="kc-epic">{{ c.epic }}</div>
                <div class="kc-foot">
                  <span class="pdot" :class="'prio-' + prioKey(c.priority)" :data-tooltip="(c.priority || 'No priority') + ' priority'" data-tip-pos="top"></span>
                  <span v-if="slaView(c).show" class="k-sla" :class="slaView(c).cls">{{ slaView(c).text }}</span>
                  <span v-else-if="ageDays(c) >= 3" class="k-age" :data-tooltip="'Untouched for ' + Math.floor(ageDays(c)) + ' days'" data-tip-pos="top">{{ Math.floor(ageDays(c)) }}d idle</span>
                  <span class="avatar" :class="{ dim: !c.assignee }" :style="avatarStyle(c.assignee)"
                    :data-tooltip="c.assignee || 'Unassigned'" data-tip-pos="top">{{ initials(c.assignee) }}</span>
                </div>
              </button>
              <div v-if="!col.cards.length" class="kc-empty">{{ filter ? 'Nothing matches the filter' : 'Empty' }}</div>
            </div>
          </section>
        </div>

        <!-- swimlanes -->
        <div v-else class="lanes">
          <div class="lane-head-row" :style="laneGrid">
            <div class="lane-gutter"></div>
            <div v-for="col in columns" :key="col.name" class="lane-colhead" :class="'cat-' + (col.category || 'new')">
              <span class="kc-name">{{ col.name }}</span>
              <span class="kc-count" :class="{ bad: isOver(col) }">{{ col.cards.length }}</span>
              <div class="kc-meter">
                <template v-if="col.max">
                  <span v-for="i in slotCount(col)" :key="i" class="slot" :class="slotClass(col, i)"></span>
                </template>
                <span v-else class="share"><span class="share-fill" :style="{ width: sharePct(col) + '%' }"></span></span>
              </div>
            </div>
          </div>
          <div v-for="lane in lanes" :key="lane.label" class="lane" :style="laneGrid">
            <div class="lane-gutter">
              <span class="lane-name">{{ lane.label }}</span>
              <span class="lane-count">{{ lane.total }}</span>
            </div>
            <div v-for="col in columns" :key="col.name" class="lane-cell">
              <button v-for="c in lane.byColumn[col.name] || []" :key="c.key" class="kcard" :class="[ageClass(c), { fresh: fresh.has(c.key), moved: moved.has(c.key) }]"
                @click="$emit('open', c.key)">
                <div class="kc-top">
                  <span class="k-key">{{ c.key }}</span>
                  <span class="ttag" :class="typeClass(c.type)">{{ shortType(c.type) }}</span>
                </div>
                <div class="kc-sum">{{ c.summary || '—' }}</div>
                <div class="kc-foot">
                  <span class="pdot" :class="'prio-' + prioKey(c.priority)"></span>
                  <span v-if="slaView(c).show" class="k-sla" :class="slaView(c).cls">{{ slaView(c).text }}</span>
                  <span class="avatar" :class="{ dim: !c.assignee }" :style="avatarStyle(c.assignee)">{{ initials(c.assignee) }}</span>
                </div>
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- Legend + off-board cards -->
      <div class="klegend">
        <span class="lg"><span class="slot filled"></span><span class="slot over"></span>WIP capacity{{ board.constraintType === 'issueCountExclSubs' ? ' (excl. sub-tasks)' : '' }}</span>
        <span class="lg"><i class="edge age-warn"></i>3d untouched</span>
        <span class="lg"><i class="edge age-stale"></i>7d untouched</span>
        <span class="lg lg-dim">read-only mirror of the Jira board — cards move when Jira moves them</span>
      </div>
      <div v-if="unmapped.length" class="offboard">
        <div class="off-title">{{ unmapped.length }} not on the board</div>
        <p class="off-body">These tickets sit in a status no column maps to, so Jira hides them too.</p>
        <div class="off-list">
          <button v-for="c in unmapped" :key="c.key" class="offchip" @click="$emit('open', c.key)">
            <span class="k-key">{{ c.key }}</span><span class="off-status">{{ c.status || '—' }}</span>
          </button>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { RefreshCw, AlertTriangle, ChevronDown } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { generatePrettyTimeAgo } from '@/utils/time'

defineEmits(['open'])

const LS_BOARD = 'gatus.jira.boardId'
const LS_LANE = 'gatus.jira.swimlane'

const boards = ref([])
const boardsError = ref('')
const boardId = ref(Number(localStorage.getItem(LS_BOARD) || 0))
const board = ref({ ok: false, columns: [], unmapped: [], total: 0 })
const loading = ref(false)
const live = ref(false)
const filter = ref('')
const swimlane = ref(localStorage.getItem(LS_LANE) || 'none')
const collapsed = ref(new Set())
const now = ref(Date.now())

// --- board list ----------------------------------------------------------
const boardGroups = computed(() => {
  const groups = new Map()
  for (const b of boards.value) {
    const label = b.projectKey ? `${b.projectKey} · ${b.projectName || ''}`.trim() : 'Other boards'
    if (!groups.has(label)) groups.set(label, { label, boards: [] })
    groups.get(label).boards.push(b)
  }
  return [...groups.values()]
})
const projectHint = computed(() => {
  const keys = [...new Set(boards.value.map(b => b.projectKey).filter(Boolean))]
  return keys.length ? keys.join(', ') : 'the configured projects'
})

const fetchBoards = async () => {
  try {
    const res = await fetch('/api/v1/jira/boards', { cache: 'no-store' })
    const data = await res.json()
    boardsError.value = data.ok === false ? (data.error || 'Jira rejected the board listing') : ''
    boards.value = data.boards || []
    const ids = boards.value.map(b => b.id)
    if (!ids.includes(boardId.value) && ids.length) boardId.value = ids[0]
  } catch (e) {
    boardsError.value = 'The board listing request failed. Is Gatus still reachable?'
  }
}

// --- board snapshot + change tracking -----------------------------------
const placement = new Map() // key -> column name, so a card that moves can announce it
let placementInit = false
const fresh = ref(new Set())
const moved = ref(new Set())

const decay = (target, keys, ms = 6000) => {
  const add = new Set(target.value)
  keys.forEach(k => add.add(k))
  target.value = add
  setTimeout(() => {
    const drop = new Set(target.value)
    keys.forEach(k => drop.delete(k))
    target.value = drop
  }, ms)
}

const applyBoard = (data) => {
  const next = new Map()
  for (const col of data.columns || []) {
    for (const c of col.cards || []) next.set(c.key, col.name)
  }
  if (placementInit) {
    const newKeys = [], movedKeys = []
    for (const [key, col] of next) {
      const before = placement.get(key)
      if (before === undefined) newKeys.push(key)
      else if (before !== col) movedKeys.push(key)
    }
    if (newKeys.length) decay(fresh, newKeys)
    if (movedKeys.length) decay(moved, movedKeys)
  }
  placement.clear()
  for (const [k, v] of next) placement.set(k, v)
  placementInit = true
  board.value = { columns: [], unmapped: [], total: 0, ...data }
}

const fetchBoard = async () => {
  if (!boardId.value) return
  loading.value = true
  try {
    const res = await fetch(`/api/v1/jira/board/${boardId.value}`, { cache: 'no-store' })
    if (res.ok) applyBoard(await res.json())
  } catch (e) { /* keep the last good board */ } finally { loading.value = false }
}

let es = null
const connectLive = () => {
  if (es) { es.close(); es = null }
  live.value = false
  if (!boardId.value) return
  try {
    es = new EventSource(`/api/v1/jira/board/${boardId.value}/live`)
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        if (data && (data.columns || data.error)) applyBoard(data)
        live.value = true
      } catch (err) { /* ignore a malformed frame */ }
    }
    es.onerror = () => { live.value = false }
  } catch (e) { live.value = false }
}

watch(boardId, (id) => {
  if (!id) return
  localStorage.setItem(LS_BOARD, String(id))
  placementInit = false
  placement.clear()
  board.value = { ok: false, columns: [], unmapped: [], total: 0 }
  collapsed.value = new Set()
  fetchBoard()
  connectLive()
})
watch(swimlane, (v) => localStorage.setItem(LS_LANE, v))

// --- filtering + column shaping -----------------------------------------
const matches = (c) => {
  const q = filter.value.trim().toLowerCase()
  if (!q) return true
  return [c.key, c.summary, c.assignee, c.type, c.status, c.epic, (c.labels || []).join(' ')]
    .some(v => (v || '').toString().toLowerCase().includes(q))
}
const columns = computed(() => (board.value.columns || []).map(col => ({
  ...col,
  cards: (col.cards || []).filter(matches),
})))
const unmapped = computed(() => (board.value.unmapped || []).filter(matches))
const visibleCount = computed(() => columns.value.reduce((n, c) => n + c.cards.length, 0))
// Swimlane rows and the sticky column header share one grid definition.
const laneGrid = computed(() => ({ gridTemplateColumns: `132px repeat(${columns.value.length}, 292px)` }))
const maxColumnCount = computed(() => Math.max(1, ...columns.value.map(c => c.cards.length)))
const overWip = computed(() => columns.value.filter(isOver))

function isOver(col) { return !!col.max && col.cards.length > col.max }
function slotCount(col) { return Math.max(col.max || 0, col.cards.length) }
function slotClass(col, i) {
  const filled = i <= col.cards.length
  if (i > (col.max || 0)) return filled ? 'over' : 'ghost'
  return filled ? 'filled' : ''
}
function sharePct(col) { return (col.cards.length / maxColumnCount.value) * 100 }
function noteFor(col) {
  if (isOver(col)) return `over by ${col.cards.length - col.max}`
  if (col.min && col.cards.length < col.min) return `${col.min - col.cards.length} below min ${col.min}`
  if (col.max) return `max ${col.max}`
  if (col.min) return `min ${col.min}`
  return col.statuses && col.statuses.length ? col.statuses.join(' · ') : ''
}
function noteClass(col) {
  if (isOver(col)) return 'note-bad'
  if (col.min && col.cards.length < col.min) return 'note-warn'
  return col.max || col.min ? '' : 'note-dim'
}
const toggleCollapse = (name) => {
  const s = new Set(collapsed.value)
  s.has(name) ? s.delete(name) : s.add(name)
  collapsed.value = s
}

// --- swimlanes ----------------------------------------------------------
const laneKeyOf = (c) => {
  switch (swimlane.value) {
    case 'assignee': return c.assignee || 'Unassigned'
    case 'priority': return c.priority || 'No priority'
    case 'epic': return c.epic || 'No epic'
    case 'type': return c.type || 'No type'
    default: return ''
  }
}
const PRIO_ORDER = ['Highest', 'High', 'Medium', 'Low', 'Lowest', 'No priority']
const lanes = computed(() => {
  const map = new Map()
  for (const col of columns.value) {
    for (const c of col.cards) {
      const label = laneKeyOf(c)
      if (!map.has(label)) map.set(label, { label, total: 0, byColumn: {} })
      const lane = map.get(label)
      lane.total++
      ;(lane.byColumn[col.name] = lane.byColumn[col.name] || []).push(c)
    }
  }
  const out = [...map.values()]
  const trailing = (l) => /^(Unassigned|No priority|No epic|No type)$/.test(l.label) ? 1 : 0
  if (swimlane.value === 'priority') {
    out.sort((a, b) => PRIO_ORDER.indexOf(a.label) - PRIO_ORDER.indexOf(b.label))
  } else {
    out.sort((a, b) => trailing(a) - trailing(b) || b.total - a.total || a.label.localeCompare(b.label))
  }
  return out
})

// --- card presentation --------------------------------------------------
const prioKey = (p) => (p || '').toLowerCase().replace(/[^a-z]/g, '') || 'none'
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
const initials = (name) => !name ? '?' : name.split(/\s+/).map(w => w[0]).slice(0, 2).join('').toUpperCase()
const AVATARS = ['#e0a458', '#b08968', '#8a8f98', '#7d8471', '#a3907a', '#9c6f5e']
const avatarStyle = (name) => {
  if (!name) return {}
  let h = 0
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0
  const c = AVATARS[h % AVATARS.length]
  return { background: c + '2e', color: c, borderColor: c + '55' }
}

// Card aging: days since the ticket was last touched.
const ageDays = (c) => {
  const t = Date.parse(c.updated || c.created || '')
  return Number.isNaN(t) ? 0 : (now.value - t) / 86400000
}
const ageClass = (c) => {
  const d = ageDays(c)
  return d >= 7 ? 'age-stale' : d >= 3 ? 'age-warn' : ''
}

// --- live SLA countdown (same rules as the ticket list) -----------------
const fmtDur = (ms) => {
  let s = Math.floor(Math.abs(ms) / 1000)
  const d = Math.floor(s / 86400); s -= d * 86400
  const h = Math.floor(s / 3600); s -= h * 3600
  const m = Math.floor(s / 60); s -= m * 60
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m ${String(s).padStart(2, '0')}s`
}
const slaView = (c) => {
  if (!c.slaName && !c.slaBreached) return { show: false, cls: '', text: '' }
  const rem = (c.slaActive && c.slaBreachEpoch) ? (c.slaBreachEpoch - now.value) : c.slaRemainingMs
  if (c.slaBreached || rem < 0) return { show: true, cls: 'sla-over', text: 'overdue ' + fmtDur(rem) }
  if (c.slaPaused) return { show: true, cls: 'sla-paused', text: fmtDur(rem) + ' paused' }
  const mins = rem / 60000
  return { show: true, cls: mins < 15 ? 'sla-crit' : mins < 60 ? 'sla-warn' : 'sla-ok', text: fmtDur(rem) }
}

const updatedLabel = computed(() => {
  if (!board.value.updatedAt) return 'never'
  try { return 'updated ' + generatePrettyTimeAgo(new Date(board.value.updatedAt)) } catch (e) { return '—' }
})

let tick = null, fallback = null
onMounted(async () => {
  await fetchBoards()
  if (boardId.value) { fetchBoard(); connectLive() }
  tick = setInterval(() => { now.value = Date.now() }, 1000)
  fallback = setInterval(() => { if (!live.value) fetchBoard() }, 30000)
})
onUnmounted(() => {
  if (es) es.close()
  if (tick) clearInterval(tick)
  if (fallback) clearInterval(fallback)
})
</script>

<style scoped>
.kanban { display: flex; flex-direction: column; gap: 0.85rem; }

/* toolbar */
.ktoolbar { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; }
.kt-left, .kt-right { display: flex; align-items: center; gap: 0.75rem; flex-wrap: wrap; }
.boardpick, .lanepick { display: inline-flex; align-items: center; gap: 0.45rem; }
.bp-label { font-size: 10px; letter-spacing: 0.12em; text-transform: uppercase; font-weight: 700; color: hsl(var(--muted-foreground)); }
.bp-select { font-size: 0.85rem; background: hsl(var(--card)); color: hsl(var(--foreground)); border: 1px solid hsl(var(--border)); border-radius: 8px; padding: 0.35rem 0.5rem; max-width: 15rem; }
.bp-select:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }
.kfilter { font-size: 0.85rem; font-family: ui-monospace, monospace; background: hsl(var(--background)); border: 1px solid hsl(var(--border)); border-radius: 8px; padding: 0.35rem 0.6rem; width: 11rem; }
.kfilter:focus { outline: none; box-shadow: 0 0 0 1px hsl(var(--ring)); }
.kt-facts { display: inline-flex; align-items: center; gap: 0.8rem; }
.fact { font-size: 0.76rem; color: hsl(var(--muted-foreground)); }
.fact b { color: hsl(var(--foreground)); font-variant-numeric: tabular-nums; }
.fact-bad { color: #ef6b53; } .fact-bad b { color: #ef6b53; }
.fact-dim { font-size: 0.7rem; opacity: 0.65; text-transform: lowercase; }

.live-ind { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.72rem; font-family: ui-monospace, monospace; color: hsl(var(--muted-foreground)); text-transform: uppercase; letter-spacing: 0.08em; }
.live-ind .ldot { width: 7px; height: 7px; border-radius: 999px; background: hsl(var(--muted-foreground) / 0.5); }
.live-ind.on { color: #7bbd8a; } .live-ind.on .ldot { background: #5aa06b; }

/* states */
.notice { border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 1.5rem; }
.notice-error { border-style: solid; border-color: hsl(var(--destructive) / 0.4); background: hsl(var(--destructive) / 0.05); }
.notice-title { font-weight: 700; margin-bottom: 0.35rem; }
.notice-body { font-size: 0.875rem; color: hsl(var(--muted-foreground)); }
.notice code { font-family: ui-monospace, monospace; background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em; }
.err-pre { font-size: 12px; white-space: pre-wrap; word-break: break-word; color: hsl(var(--destructive)); background: hsl(var(--destructive) / 0.08); border-radius: 6px; padding: 0.6rem 0.75rem; font-family: ui-monospace, monospace; }
.partial { display: flex; align-items: center; gap: 0.45rem; font-size: 0.76rem; color: #e0a458; background: rgb(224 160 88 / 0.09); border: 1px solid rgb(224 160 88 / 0.28); border-radius: 8px; padding: 0.4rem 0.6rem; }

/* board scroller */
.bwrap { overflow-x: auto; padding-bottom: 0.4rem; }
.bgrid { display: flex; gap: 0.7rem; align-items: flex-start; min-height: 12rem; }

.kcol { flex: 0 0 292px; display: flex; flex-direction: column; background: hsl(var(--muted) / 0.24); border: 1px solid hsl(var(--border)); border-radius: 12px; padding: 0.55rem 0.55rem 0.6rem; position: relative; }
.kcol::before { content: ''; position: absolute; inset: 0 0 auto 0; height: 2px; border-radius: 12px 12px 0 0; background: #8a8f98; opacity: 0.75; }
.kcol.cat-indeterminate::before { background: #e0a458; }
.kcol.cat-done::before { background: #5aa06b; }
.kcol.over::before { background: #ef6b53; opacity: 1; }
.kcol.collapsed { flex: 0 0 58px; }

.kcol-head { display: flex; align-items: center; gap: 0.4rem; padding: 0.3rem 0.15rem 0.4rem; cursor: pointer; }
.kc-toggle { display: inline-flex; color: hsl(var(--muted-foreground)); background: transparent; }
.kc-toggle .turn { transform: rotate(-90deg); }
.kcol-head:hover .kc-toggle { color: hsl(var(--foreground)); }
.kc-name { font-size: 0.68rem; letter-spacing: 0.1em; text-transform: uppercase; font-weight: 700; color: hsl(var(--foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.kc-count { margin-left: auto; font-size: 0.72rem; font-weight: 700; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); background: hsl(var(--background)); border-radius: 999px; padding: 0.02rem 0.45rem; }
.kc-count.bad { color: #1a1206; background: #ef6b53; }
.kcol.collapsed .kcol-head { flex-direction: column; gap: 0.5rem; }
.kcol.collapsed .kc-name { writing-mode: vertical-rl; max-height: 11rem; }
.kcol.collapsed .kc-count { margin-left: 0; }

/* WIP capacity meter — the board's own column constraints, made visible */
.kc-meter { display: flex; gap: 2px; height: 4px; margin: 0 0.15rem 0.35rem; }
.slot { flex: 1; min-width: 3px; border-radius: 2px; background: hsl(var(--border)); }
.slot.filled { background: #8a8f98; }
.kcol.cat-indeterminate .slot.filled { background: #e0a458; }
.kcol.cat-done .slot.filled { background: #5aa06b; }
.lane-colhead.cat-indeterminate .slot.filled { background: #e0a458; }
.lane-colhead.cat-done .slot.filled { background: #5aa06b; }
.slot.over { background: #ef6b53; }
.slot.ghost { background: transparent; box-shadow: inset 0 0 0 1px hsl(var(--border)); }
.share { flex: 1; border-radius: 2px; background: hsl(var(--border) / 0.7); overflow: hidden; }
.share-fill { display: block; height: 100%; background: hsl(var(--muted-foreground) / 0.55); border-radius: 2px; }

.kc-note { font-size: 0.62rem; letter-spacing: 0.05em; text-transform: uppercase; font-weight: 600; color: hsl(var(--muted-foreground)); padding: 0 0.15rem 0.5rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.kc-note.note-bad { color: #ef6b53; }
.kc-note.note-warn { color: #e0a458; }
.kc-note.note-dim { opacity: 0.5; text-transform: none; letter-spacing: 0; font-weight: 500; }

.kc-body { display: flex; flex-direction: column; gap: 0.45rem; }
.kc-empty { font-size: 0.74rem; color: hsl(var(--muted-foreground)); opacity: 0.6; padding: 0.9rem 0.3rem; text-align: center; border: 1px dashed hsl(var(--border) / 0.8); border-radius: 9px; }

/* cards */
.kcard { position: relative; text-align: left; width: 100%; background: hsl(var(--card)); border: 1px solid hsl(var(--border)); border-left: 2px solid transparent; border-radius: 10px; padding: 0.55rem 0.65rem; cursor: pointer; transition: border-color 0.14s ease, transform 0.14s ease, box-shadow 0.14s ease; }
.kcard:hover { border-color: hsl(var(--muted-foreground) / 0.5); transform: translateY(-1px); }
.kcard:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }
.kcard.age-warn { border-left-color: #e0a458; }
.kcard.age-stale { border-left-color: #ef6b53; }
.kc-top { display: flex; align-items: center; justify-content: space-between; gap: 0.4rem; margin-bottom: 0.35rem; }
.k-key { font-family: ui-monospace, monospace; font-weight: 700; font-size: 0.76rem; }
.kc-sum { font-size: 0.84rem; line-height: 1.35; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.kc-epic { margin-top: 0.3rem; font-size: 0.66rem; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; border-left: 2px solid hsl(var(--muted-foreground) / 0.45); padding-left: 0.35rem; }
.kc-foot { display: flex; align-items: center; gap: 0.45rem; margin-top: 0.5rem; }
/* the priority dot anchors left; the SLA countdown and avatar ride the right edge */
.pdot { width: 8px; height: 8px; border-radius: 999px; flex-shrink: 0; margin-right: auto; background: hsl(var(--muted-foreground) / 0.6); }
.pdot.prio-highest { background: #ef6b53; box-shadow: 0 0 0 3px rgb(239 107 83 / 0.16); }
.pdot.prio-high { background: #e0a458; }
.pdot.prio-medium { background: #8a8f98; }
.pdot.prio-low, .pdot.prio-lowest, .pdot.prio-none { background: hsl(var(--muted-foreground) / 0.45); }
.k-sla, .k-age { font-family: ui-monospace, monospace; font-size: 0.7rem; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); }
.k-sla.sla-warn { color: #e0a458; }
.k-sla.sla-crit { color: #ef8b74; font-weight: 700; }
.k-sla.sla-over { color: #ef6b53; font-weight: 700; }
.k-sla.sla-paused { opacity: 0.7; }
.k-age { opacity: 0.6; font-size: 0.66rem; }
.avatar { width: 21px; height: 21px; border-radius: 999px; flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center; font-size: 0.6rem; font-weight: 700; border: 1px solid transparent; background: hsl(var(--muted)); color: hsl(var(--foreground)); }
.avatar.dim { background: transparent; color: hsl(var(--muted-foreground) / 0.7); border-color: hsl(var(--border)); }

.movepip { position: absolute; top: -7px; right: 8px; font-size: 9px; font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase; color: #1a1206; background: #e0a458; border-radius: 4px; padding: 0.05rem 0.3rem; }

/* swimlanes */
.lanes { display: flex; flex-direction: column; min-width: max-content; }
.lane-head-row, .lane { display: grid; gap: 0.7rem; }
.lane-head-row { position: sticky; top: 0; z-index: 2; background: hsl(var(--background)); padding-bottom: 0.5rem; }
.lane-colhead { border-top: 2px solid #8a8f98; padding-top: 0.45rem; display: grid; grid-template-columns: 1fr auto; gap: 0.35rem 0.4rem; align-items: center; }
.lane-colhead.cat-indeterminate { border-top-color: #e0a458; }
.lane-colhead.cat-done { border-top-color: #5aa06b; }
.lane-colhead .kc-meter { grid-column: 1 / -1; margin: 0.1rem 0 0; }
.lane { border-top: 1px solid hsl(var(--border)); padding: 0.6rem 0; align-items: start; }
.lane-gutter { display: flex; flex-direction: column; gap: 0.2rem; padding-top: 0.15rem; }
.lane-name { font-size: 0.74rem; font-weight: 700; line-height: 1.25; }
.lane-count { font-size: 0.64rem; letter-spacing: 0.08em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
.lane-cell { display: flex; flex-direction: column; gap: 0.45rem; }

/* legend + off-board */
.klegend { display: flex; flex-wrap: wrap; gap: 0.35rem 1.15rem; font-size: 0.7rem; color: hsl(var(--muted-foreground)); }
.lg { display: inline-flex; align-items: center; gap: 0.4rem; }
.lg .slot { flex: 0 0 10px; height: 4px; }
.lg .edge { width: 3px; height: 12px; border-radius: 2px; display: inline-block; }
.lg .edge.age-warn { background: #e0a458; } .lg .edge.age-stale { background: #ef6b53; }
.lg-dim { opacity: 0.6; }

.offboard { border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 0.75rem 0.9rem; }
.off-title { font-size: 0.74rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.07em; color: hsl(var(--muted-foreground)); }
.off-body { font-size: 0.76rem; color: hsl(var(--muted-foreground)); opacity: 0.75; margin-top: 0.15rem; }
.off-list { display: flex; flex-wrap: wrap; gap: 0.35rem; margin-top: 0.55rem; }
.offchip { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.72rem; background: hsl(var(--muted) / 0.45); border: 1px solid hsl(var(--border)); border-radius: 999px; padding: 0.15rem 0.6rem; cursor: pointer; }
.offchip:hover { border-color: hsl(var(--muted-foreground) / 0.5); }
.off-status { color: hsl(var(--muted-foreground)); }

/* type tags — same vocabulary as the ticket list */
.ttag { font-size: 9.5px; letter-spacing: 0.03em; text-transform: uppercase; padding: 0.08rem 0.35rem; border-radius: 5px; font-weight: 700; white-space: nowrap; }
.ttag-incident { background: rgb(190 60 40 / 0.16); color: #ef8b74; }
.ttag-request { background: hsl(var(--muted) / 0.7); color: hsl(var(--foreground)); }
.ttag-change { background: rgb(224 160 88 / 0.16); color: #e0a458; }
.ttag-problem { background: rgb(224 160 88 / 0.18); color: #e6b877; }
.ttag-epic { background: rgb(147 112 90 / 0.22); color: #c2a17f; }
.ttag-story { background: rgb(90 160 107 / 0.16); color: #7bbd8a; }
.ttag-task { background: hsl(var(--muted) / 0.7); color: hsl(var(--muted-foreground)); }

@media (prefers-reduced-motion: no-preference) {
  .kcard.fresh { animation: cardin 6s ease-out; }
  .kcard.moved { animation: cardmove 6s ease-out; }
}
@keyframes cardin {
  0% { border-color: #e0a458; box-shadow: 0 0 0 2px rgb(224 160 88 / 0.35); transform: translateY(4px); opacity: 0.4; }
  14% { transform: translateY(0); opacity: 1; }
  100% { border-color: hsl(var(--border)); box-shadow: none; }
}
@keyframes cardmove {
  0% { border-color: #e0a458; box-shadow: 0 0 0 2px rgb(224 160 88 / 0.28); }
  100% { border-color: hsl(var(--border)); box-shadow: none; }
}

@media (max-width: 640px) {
  .kcol { flex: 0 0 250px; }
  .kfilter { width: 8.5rem; }
}
</style>
