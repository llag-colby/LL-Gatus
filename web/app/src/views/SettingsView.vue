<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 settings-panel">

      <!-- MASTHEAD. A panel plate, not a title over a form: what the page is on
           the left, the numbers that describe this system on the right. -->
      <header class="plate">
        <div class="plate-left">
          <router-link to="/" class="back" data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-4 w-4" />
          </router-link>
          <div class="plate-id">
            <div class="tick">Monitoring</div>
            <h1 class="plate-h1">Settings</h1>
          </div>
        </div>

        <div class="plate-right">
          <div class="readout">
            <span class="readout-k">Build</span>
            <span class="readout-v num">{{ build || '—' }}</span>
            <span class="readout-sub"><span class="readout-dim">{{ serverClock }}</span></span>
          </div>
          <div class="readout">
            <span class="readout-k">Checks</span>
            <span class="readout-v num">{{ allRows.length }}</span>
            <span class="readout-sub"><span class="readout-dim">{{ cardCount }} cards</span></span>
          </div>
          <div class="readout" :class="{ hot: staleRows.length > 0 }">
            <span class="readout-k">Stale</span>
            <span class="readout-v num">{{ staleRows.length }}</span>
            <span class="readout-sub"><span class="readout-dim">over {{ STALE_MIN }} min old</span></span>
          </div>
          <div class="readout" :class="{ hot: pausedKeys.length > 0 }">
            <span class="readout-k">Paused</span>
            <span class="readout-v num">{{ pausedKeys.length }}</span>
            <span class="readout-sub"><span class="readout-dim">checks stopped</span></span>
          </div>
        </div>
      </header>

      <div class="body solo">
        <div class="main">

          <!-- FEEDS. The first thing to know on an operations page is whether
               the things that report are still reporting. Grouped by what does
               the reporting, because that is the unit that breaks. -->
          <section class="block">
            <div class="block-head">
              <span class="tick">Feeds</span>
              <span class="head-note">
                <button class="mini" @click="loadAll" :disabled="busy">
                  <RefreshCw class="h-3 w-3" :class="{ 'animate-spin': busy }" /> Refresh
                </button>
              </span>
            </div>
            <div class="tbl-wrap">
              <table class="tbl">
                <thead>
                  <tr>
                    <th>Feed</th><th class="num">Rows</th><th class="num">Fresh</th>
                    <th class="num">Stale</th><th class="num">Failing</th>
                    <th class="num">Silent</th><th>Newest</th><th>Oldest</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="f in feeds" :key="f.name">
                    <td class="strong">{{ f.name }}<span class="src">{{ f.source }}</span></td>
                    <td class="num mono">{{ f.total }}</td>
                    <td class="num mono" :class="f.fresh === f.total ? 'ok' : ''">{{ f.fresh }}</td>
                    <td class="num mono" :class="f.stale ? 'bad' : 'dim'">{{ f.stale || '-' }}</td>
                    <td class="num mono" :class="f.failing ? 'bad' : 'dim'">{{ f.failing || '-' }}</td>
                    <td class="num mono" :class="f.silent ? 'warn' : 'dim'">{{ f.silent || '-' }}</td>
                    <td class="mono dim">{{ f.newest }}</td>
                    <td class="mono" :class="f.stale ? 'bad' : 'dim'">{{ f.oldest }}</td>
                  </tr>
                  <tr v-if="!feeds.length"><td colspan="8" class="dim">No checks reported yet.</td></tr>
                </tbody>
              </table>
            </div>
            <p class="block-note">
              Stale means over {{ STALE_MIN }} minutes since the last recorded result, which for a
              collector feed means the collector stopped pushing. Silent is a result that reported
              nothing rather than failing.
            </p>
          </section>

          <!-- PAUSED. Was the whole page; now one section, with the names
               resolved and a way to undo it from here. -->
          <section class="block">
            <div class="block-head">
              <span class="tick">Paused checks</span>
              <span class="head-note">global</span>
            </div>
            <p v-if="monitoringError" class="msg msg-error" role="alert">{{ monitoringError }}</p>
            <p v-else-if="!pausedKeys.length" class="msg msg-dim">
              Nothing is paused. Every configured check is running.
            </p>
            <div v-else class="tbl-wrap">
              <table class="tbl">
                <thead><tr><th>Check</th><th>Key</th><th></th></tr></thead>
                <tbody>
                  <tr v-for="p in pausedDetailed" :key="p.key">
                    <td class="strong">
                      <router-link class="lnk" :to="`/endpoints/${p.key}`">{{ p.label }}</router-link>
                    </td>
                    <td class="mono dim">{{ p.key }}</td>
                    <td class="num">
                      <button class="mini" @click="resume(p.key)" :disabled="busyKeys.has(p.key)">
                        <Play class="h-3 w-3" /> Resume
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p class="block-note">
              Pausing stops the check and its alerts for everyone and for the wallboards.
              Recorded history is kept.
            </p>
          </section>

          <!-- TARGET OVERRIDES. Runtime re-points live in /data and survive a
               deploy, so they are invisible unless something lists them. -->
          <section class="block">
            <div class="block-head">
              <span class="tick">Target overrides</span>
              <span class="head-note">{{ targetEditingEnabled ? 'editing enabled' : 'editing disabled' }}</span>
            </div>
            <p v-if="!overridden.length" class="msg msg-dim">
              No overrides. Every check is pointed where config.yaml says.
            </p>
            <div v-else class="tbl-wrap">
              <table class="tbl">
                <thead><tr><th>Check</th><th>In config</th><th>Running against</th><th></th></tr></thead>
                <tbody>
                  <tr v-for="t in overridden" :key="t.key">
                    <td class="strong">{{ t.name }} <span class="dim">{{ t.group }}</span></td>
                    <td class="mono dim">{{ t.configured || '—' }}</td>
                    <td class="mono warn">{{ t.effective || '—' }}</td>
                    <td class="num">
                      <button class="mini" @click="resetTarget(t.key)"
                        :disabled="!targetEditingEnabled || busyKeys.has(t.key)">
                        <RotateCcw class="h-3 w-3" /> Reset
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div v-if="overridden.length && targetEditingEnabled && !editToken" class="tokenrow">
              <input v-model="tokenInput" type="password" class="inp mono"
                placeholder="GATUS_EDIT_TOKEN" autocomplete="off" spellcheck="false" />
              <button class="mini" @click="saveToken" :disabled="!tokenInput.trim()">Use token</button>
              <span class="dim">Needed to reset. Read it on the box with <code>cat data/edit_token</code>.</span>
            </div>
            <p v-else-if="!targetEditingEnabled" class="block-note">
              Editing is off because the server has no edit token. Unset means off, not open.
            </p>
          </section>

          <!-- SHARED LAYOUT. Server-side, so one person hiding a card hides it
               on the wallboard too. That deserves to be visible somewhere. -->
          <section class="block">
            <div class="block-head">
              <span class="tick">Dashboard layout</span>
              <span class="head-note">shared by every screen</span>
            </div>
            <div class="facts">
              <div class="fact"><span class="fk">Hidden cards</span><span class="fv num">{{ hiddenCards.length }}</span></div>
              <div class="fact"><span class="fk">Hidden rows</span><span class="fv num">{{ layoutStats.hiddenRows }}</span></div>
              <div class="fact"><span class="fk">Renamed</span><span class="fv num">{{ layoutStats.renamed }}</span></div>
              <div class="fact"><span class="fk">Arranged</span><span class="fv num">{{ layoutStats.arranged }}</span></div>
            </div>
            <ul v-if="hiddenCards.length" class="keys">
              <li v-for="name in hiddenCards" :key="name">
                <span class="key">
                  <i class="key-led led-off" aria-hidden="true"></i><span>{{ name }}</span>
                  <button class="mini ml" @click="unhide(name)"><Eye class="h-3 w-3" /> Show</button>
                </span>
              </li>
            </ul>
            <div class="actionrow">
              <button class="mini danger" @click="doResetLayout" :disabled="busy">
                <RotateCcw class="h-3 w-3" /> Reset layout for every screen
              </button>
              <span class="dim">Clears hidden cards and rows, custom names and the arranged order.</span>
            </div>
            <p class="block-note">
              Presentation only. Hiding a row does not stop its check, and a hidden row that goes
              down still turns its card red.
            </p>
          </section>

          <!-- THIS BROWSER. The old page only said these existed somewhere
               else. They are controls, so they belong on the settings page. -->
          <section class="block">
            <div class="block-head">
              <span class="tick">This browser</span>
              <span class="head-note">not sent to the server</span>
            </div>
            <div class="prefs">
              <label class="pref">
                <span class="pk">Theme</span>
                <select class="inp" :value="theme" @change="setTheme($event.target.value)">
                  <option value="system">Follow system</option>
                  <option value="dark">Dark</option>
                  <option value="light">Light</option>
                </select>
              </label>
              <label class="pref">
                <span class="pk">Dashboard view</span>
                <select class="inp" :value="dashboardView" @change="setDashboardView($event.target.value)">
                  <option value="grid">Grid</option>
                  <option value="horizontal">Wide</option>
                </select>
              </label>
              <label class="pref">
                <span class="pk">History range</span>
                <select class="inp" :value="historyRange" @change="setHistoryRange($event.target.value)">
                  <option v-for="r in HISTORY_RANGES" :key="r.value" :value="r.value">{{ r.label }}</option>
                </select>
              </label>
              <label class="pref">
                <span class="pk">Sound</span>
                <select class="inp" :value="soundEnabled ? 'on' : 'off'"
                  @change="setSoundEnabled($event.target.value === 'on')">
                  <option value="on">On</option>
                  <option value="off">Off</option>
                </select>
              </label>
            </div>
            <div class="swatches">
              <label v-for="k in ['up','degraded','down']" :key="k" class="sw">
                <input type="color" :value="statusColors[k]" @input="setStatusColor(k, $event.target.value)" />
                <span class="pk">{{ k }}</span>
                <span class="mono dim">{{ statusColors[k] }}</span>
              </label>
              <button class="mini" @click="resetStatusColors">
                <RotateCcw class="h-3 w-3" /> Default colours
              </button>
            </div>
          </section>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { ArrowLeft, RefreshCw, RotateCcw, Play, Eye } from 'lucide-vue-next'
import {
  statusColors, setStatusColor, resetStatusColors,
  soundEnabled, setSoundEnabled,
  dashboardView, setDashboardView,
  historyRange, setHistoryRange, HISTORY_RANGES,
  layout, refreshLayout, setCardHidden, resetLayout,
  monitoringDisabled, refreshMonitoring, setMonitored,
  endpointTargets, targetEditingEnabled, refreshEndpointTargets,
  editToken, setEditToken, clearEndpointTarget,
  now,
} from '@/store'
import { prettifyTimestamp } from '@/utils/time'

// A result older than this means whatever produces it has stopped. Native
// checks run on a 60s interval and the slowest collector sweeps every 120s, so
// five minutes clears every normal case without hiding a real stall.
const STALE_MIN = 5
const STALE_MS = STALE_MIN * 60 * 1000

const busy = ref(false)
const busyKeys = reactive(new Set())
const build = ref('')
const serverTime = ref(0)
const rows = ref([])
const monitoringError = ref('')

// --- feeds ---------------------------------------------------------------
// Which thing produces this row. The key carries it: collector rows are
// namespaced, everything else is a check Gatus runs itself.
const FEEDS = [
  { name: 'Phones', source: 'phone_collector.py', match: (k) => k.startsWith('phones_') },
  { name: 'Firewall', source: 'unifi_collector.py', match: (k) => k.startsWith('firewall_') },
  { name: 'Wireless', source: 'unifi_collector.py', match: (k) => k.startsWith('wireless_') },
  { name: 'SMB shares', source: 'smb_collector.py', match: (k) => k.endsWith('_smb-shares') },
  { name: 'Hypervisors', source: 'hv_collector.py', match: (k) => k.endsWith('_hypervisors') },
  { name: 'Direct checks', source: 'gatus', match: () => true },
]

// A failure whose error says it read nothing is an absent signal, not an
// outage. Same vocabulary as the cards. Keep in step with the collectors.
const NOT_REPORTING = /^no (phones|unifi|smb|hv) reporting\b/i
const isSilent = (r) =>
  !!r && !r.success && (Array.isArray(r.errors) ? r.errors : []).some((e) => NOT_REPORTING.test(e))

const allRows = computed(() => rows.value)
const ageOf = (r) => {
  if (!r || !r.timestamp) return null
  const t = Date.parse(r.timestamp)
  return Number.isNaN(t) ? null : now.value - t
}
const staleRows = computed(() => allRows.value.filter((row) => {
  const age = ageOf(row.last)
  return age === null || age > STALE_MS
}))

const ago = (ms) => {
  if (ms === null || ms === undefined) return 'never'
  if (ms < 60000) return `${Math.max(0, Math.round(ms / 1000))}s`
  if (ms < 3600000) return `${Math.round(ms / 60000)}m`
  return `${Math.round(ms / 3600000)}h`
}

const feeds = computed(() => {
  const out = []
  const taken = new Set()
  for (const feed of FEEDS) {
    const mine = allRows.value.filter((r) => !taken.has(r.key) && feed.match(r.key))
    for (const r of mine) taken.add(r.key)
    if (!mine.length) continue
    const ages = mine.map((r) => ageOf(r.last)).filter((a) => a !== null)
    const stale = mine.filter((r) => { const a = ageOf(r.last); return a === null || a > STALE_MS }).length
    out.push({
      name: feed.name,
      source: feed.source,
      total: mine.length,
      fresh: mine.length - stale,
      stale,
      failing: mine.filter((r) => r.last && !r.last.success && !isSilent(r.last)).length,
      silent: mine.filter((r) => isSilent(r.last)).length,
      newest: ages.length ? ago(Math.min(...ages)) : 'never',
      oldest: ages.length ? ago(Math.max(...ages)) : 'never',
    })
  }
  return out
})

const cardCount = computed(() => new Set(allRows.value.map((r) => r.name)).size)

// --- paused --------------------------------------------------------------
const pausedKeys = computed(() => [...monitoringDisabled.value].sort())
const pausedDetailed = computed(() => pausedKeys.value.map((key) => {
  const row = allRows.value.find((r) => r.key === key)
  return { key, label: row ? `${row.name} ${row.group}`.trim() : key }
}))

const resume = async (key) => {
  busyKeys.add(key)
  try {
    await setMonitored(key, true)
    await refreshMonitoring()
  } finally {
    busyKeys.delete(key)
  }
}

// --- target overrides ----------------------------------------------------
const overridden = computed(() =>
  Object.values(endpointTargets.value)
    .filter((t) => t && t.overridden)
    .sort((a, b) => (a.name + a.group).localeCompare(b.name + b.group)))

const tokenInput = ref('')
const saveToken = () => { setEditToken(tokenInput.value); tokenInput.value = '' }

const resetTarget = async (key) => {
  busyKeys.add(key)
  try {
    const result = await clearEndpointTarget(key)
    if (!result.ok) monitoringError.value = result.message || 'Could not reset that target.'
    await refreshEndpointTargets()
  } finally {
    busyKeys.delete(key)
  }
}

// --- shared layout -------------------------------------------------------
const cards = computed(() => (layout.value && layout.value.cards) || {})
const hiddenCards = computed(() =>
  Object.keys(cards.value).filter((n) => cards.value[n] && cards.value[n].hidden).sort())
const layoutStats = computed(() => {
  let hiddenRows = 0, renamed = 0, arranged = 0
  for (const name of Object.keys(cards.value)) {
    const card = cards.value[name] || {}
    if (card.title) renamed++
    if (typeof card.order === 'number') arranged++
    for (const key of Object.keys(card.rows || {})) {
      const row = card.rows[key] || {}
      if (row.hidden) hiddenRows++
      if (row.label) renamed++
      if (typeof row.order === 'number') arranged++
    }
  }
  return { hiddenRows, renamed, arranged }
})

const unhide = async (name) => {
  busy.value = true
  try { await setCardHidden(name, false); await refreshLayout() } finally { busy.value = false }
}
const doResetLayout = async () => {
  busy.value = true
  try { await resetLayout(); await refreshLayout() } finally { busy.value = false }
}

// --- theme ---------------------------------------------------------------
// The header toggle stores this in a COOKIE named "theme", not in
// localStorage. Writing a second key here would have given the page a theme
// control that silently disagreed with the toggle. Same cookie, same name,
// same max-age, so the two are one setting.
const THEME_COOKIE_NAME = 'theme'
const THEME_COOKIE_MAX_AGE = 60 * 60 * 24 * 365

const theme = ref('system')

const themeFromCookie = () =>
  document.cookie.match(new RegExp(`${THEME_COOKIE_NAME}=(dark|light);?`))?.[1] || ''

const readTheme = () => { theme.value = themeFromCookie() || 'system' }

const applyTheme = () => {
  const cookie = themeFromCookie()
  const dark = cookie === 'dark' ||
    (!cookie && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', dark)
}

const setTheme = (value) => {
  theme.value = value
  if (value === 'system') {
    // Expire it rather than writing "system": the toggle only understands
    // dark/light/absent, and absent is what "follow the system" means to it.
    document.cookie = `${THEME_COOKIE_NAME}=; path=/; max-age=0; samesite=strict`
  } else {
    document.cookie =
      `${THEME_COOKIE_NAME}=${value}; path=/; max-age=${THEME_COOKIE_MAX_AGE}; samesite=strict`
  }
  applyTheme()
}

// --- loading -------------------------------------------------------------
// /api/v1/time answers {"time": <unix millis>}, a number. Feeding that to the
// timestamp formatter produced nothing useful, so it is formatted from millis.
const serverClock = computed(() => {
  if (!serverTime.value) return '-'
  try { return prettifyTimestamp(new Date(serverTime.value).toISOString()) } catch (e) { return '-' }
})

const loadRows = async () => {
  try {
    // pageSize=1 keeps this to one recent result per endpoint, which is all the
    // freshness columns need; the full payload is 50 per endpoint.
    const response = await fetch('/api/v1/endpoints/statuses?page=1&pageSize=1', { cache: 'no-store' })
    if (!response.ok) return
    const data = await response.json()
    rows.value = (Array.isArray(data) ? data : []).map((e) => ({
      key: e.key, name: e.name, group: e.group || '',
      last: (e.results && e.results.length) ? e.results[e.results.length - 1] : null,
    }))
  } catch (e) { /* keep the previous rows */ }
}

const loadMeta = async () => {
  try {
    const v = await fetch('/api/v1/version', { cache: 'no-store' })
    if (v.ok) build.value = (await v.json()).version || ''
  } catch (e) { /* non-fatal */ }
  try {
    const t = await fetch('/api/v1/time', { cache: 'no-store' })
    if (t.ok) {
      const body = await t.json()
      serverTime.value = typeof body.time === 'number' ? body.time : 0
    }
  } catch (e) { /* non-fatal */ }
}

const loadAll = async () => {
  busy.value = true
  monitoringError.value = ''
  try {
    await Promise.allSettled([
      loadRows(), loadMeta(), refreshMonitoring(), refreshLayout(), refreshEndpointTargets(),
    ])
  } finally {
    busy.value = false
  }
}

let poll = null
onMounted(() => {
  readTheme()
  loadAll()
  poll = setInterval(loadRows, 30000)
})
onUnmounted(() => { if (poll) clearInterval(poll) })
</script>

<style scoped>
.settings-panel { max-width: 1200px; margin: 0 auto; }

.tick {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px; letter-spacing: 0.12em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); font-weight: 700;
}

.plate {
  display: flex; align-items: flex-end; justify-content: space-between;
  flex-wrap: wrap; gap: 1rem;
  border: 1px solid hsl(var(--border)); border-radius: 14px;
  background: hsl(var(--card)); padding: 0.9rem 1.15rem; margin-bottom: 1rem;
}
.plate-left { display: flex; align-items: center; gap: 0.8rem; }
.back { color: hsl(var(--muted-foreground)); transition: color 0.12s; }
.back:hover { color: hsl(var(--foreground)); }
.back:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; border-radius: 6px; }
.plate-h1 { font-size: 1.5rem; font-weight: 800; letter-spacing: -0.01em; line-height: 1.1; }
.plate-right { display: flex; flex-wrap: wrap; }
.readout { padding: 0 1rem; border-left: 1px solid hsl(var(--border)); min-width: 7rem; }
.readout:first-child { border-left: 0; }
.readout-k { display: block; font-size: 10px; text-transform: uppercase; letter-spacing: 0.1em; color: hsl(var(--muted-foreground)); font-weight: 700; }
.readout-v { display: block; font-size: 1.5rem; font-weight: 800; line-height: 1.15; }
.readout-v.num { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; }
.readout-sub { display: block; font-size: 0.7rem; }
.readout-dim { color: hsl(var(--muted-foreground)); }
.readout.hot .readout-v { color: #e0a458; }

.body { display: block; }
.main { display: flex; flex-direction: column; gap: 0.9rem; }

.block { border: 1px solid hsl(var(--border)); border-radius: 14px; background: hsl(var(--card)); padding: 0.9rem 1.15rem; }
.block-head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-bottom: 0.7rem; }
.head-note { font-size: 0.72rem; color: hsl(var(--muted-foreground)); }
.block-note { font-size: 0.75rem; color: hsl(var(--muted-foreground)); margin-top: 0.7rem; line-height: 1.45; }

.msg { font-size: 0.85rem; }
.msg-error { color: hsl(var(--destructive)); }
.msg-dim { color: hsl(var(--muted-foreground)); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; }
.dim { color: hsl(var(--muted-foreground)); }
.strong { font-weight: 600; }
.ok { color: #7bbd8a; }
.warn { color: #e0a458; }
.bad { color: #ef6b53; }

/* --- tables --- */
.tbl-wrap { overflow-x: auto; border: 1px solid hsl(var(--border)); border-radius: 10px; }
.tbl { width: 100%; border-collapse: collapse; font-size: 0.8rem; }
.tbl th { text-align: left; font-size: 10px; text-transform: uppercase; letter-spacing: 0.06em; color: hsl(var(--muted-foreground)); font-weight: 700; padding: 0.45rem 0.7rem; border-bottom: 1px solid hsl(var(--border)); white-space: nowrap; }
.tbl td { padding: 0.4rem 0.7rem; border-bottom: 1px solid hsl(var(--border) / 0.5); white-space: nowrap; }
.tbl tbody tr:last-child td { border-bottom: 0; }
.tbl .num, .tbl th.num { text-align: right; font-variant-numeric: tabular-nums; }
.src { display: block; font-size: 0.68rem; color: hsl(var(--muted-foreground)); font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-weight: 400; }
.lnk { color: inherit; text-decoration: underline; text-decoration-color: hsl(var(--muted-foreground) / 0.5); text-underline-offset: 2px; }
.lnk:hover { text-decoration-color: hsl(var(--foreground)); }

/* --- key list --- */
.keys { list-style: none; padding: 0; margin: 0.7rem 0 0; display: flex; flex-direction: column; gap: 2px; }
.key { display: flex; align-items: center; gap: 0.5rem; padding: 0.4rem 0.6rem; border: 1px solid hsl(var(--border)); border-radius: 8px; font-size: 0.8rem; }
.key-led { width: 7px; height: 7px; border-radius: 999px; background: #e0a458; flex-shrink: 0; }
.key-led.led-off { background: hsl(var(--muted-foreground) / 0.5); }
.ml { margin-left: auto; }

/* --- facts strip --- */
.facts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1px; background: hsl(var(--border)); border: 1px solid hsl(var(--border)); border-radius: 10px; overflow: hidden; }
@media (min-width: 640px) { .facts { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
.fact { background: hsl(var(--card)); padding: 0.5rem 0.8rem; display: flex; justify-content: space-between; gap: 0.75rem; }
.fk { font-size: 0.75rem; color: hsl(var(--muted-foreground)); }
.fv { font-size: 0.9rem; font-weight: 700; }
.fv.num { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; }

/* --- controls --- */
.mini {
  display: inline-flex; align-items: center; gap: 0.3rem;
  font-size: 0.72rem; font-weight: 600;
  padding: 0.25rem 0.55rem; border-radius: 7px;
  border: 1px solid hsl(var(--border)); background: hsl(var(--background));
  color: hsl(var(--foreground)); cursor: pointer; white-space: nowrap;
}
.mini:hover:not(:disabled) { background: hsl(var(--muted) / 0.6); }
.mini:disabled { opacity: 0.45; cursor: not-allowed; }
.mini:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }
.mini.danger { color: hsl(var(--destructive)); border-color: hsl(var(--destructive) / 0.4); }

.inp {
  font-size: 0.8rem; padding: 0.3rem 0.5rem; border-radius: 7px;
  border: 1px solid hsl(var(--border)); background: hsl(var(--background));
  color: hsl(var(--foreground)); min-width: 0;
}
.inp:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }

.tokenrow, .actionrow { display: flex; align-items: center; flex-wrap: wrap; gap: 0.5rem; margin-top: 0.7rem; font-size: 0.75rem; }
.tokenrow .inp { flex: 0 1 18rem; }
.tokenrow code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; }

.prefs { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.6rem; }
@media (min-width: 768px) { .prefs { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
.pref { display: flex; flex-direction: column; gap: 0.25rem; min-width: 0; }
.pk { font-size: 0.72rem; color: hsl(var(--muted-foreground)); text-transform: capitalize; }

.swatches { display: flex; align-items: center; flex-wrap: wrap; gap: 0.9rem; margin-top: 0.8rem; }
.sw { display: flex; align-items: center; gap: 0.4rem; font-size: 0.75rem; }
.sw input[type="color"] { width: 1.6rem; height: 1.6rem; padding: 0; border: 1px solid hsl(var(--border)); border-radius: 6px; background: none; cursor: pointer; }
</style>
