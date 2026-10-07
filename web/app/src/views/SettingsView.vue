<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 sx">

      <!-- ===================================================================
           SYSTEM BAR. The focal point and the whole reason this page exists:
           every check in the estate drawn as one segment, coloured by state.
           You read the system before you read a word of it. Everything else
           on the page is a drill into this bar.
      ==================================================================== -->
      <header class="sysbar" :class="'tone-' + overall.tone">
        <div class="sysbar-top">
          <router-link to="/" class="back" data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-4 w-4" />
          </router-link>
          <div class="sysbar-id">
            <span class="eyebrow">Monitoring console</span>
            <h1 class="sysbar-h1">Settings</h1>
          </div>

          <div class="sysbar-state">
            <div class="state-line">
              <span class="state-dot" aria-hidden="true"></span>
              <span class="state-head">{{ overall.headline }}</span>
            </div>
            <div class="state-sub">{{ overall.detail }}</div>
          </div>

          <div class="sysbar-acts">
            <button class="btn" @click="copyDiagnostics" :disabled="!rows.length">
              <ClipboardCopy class="h-3.5 w-3.5" />{{ copied ? 'Copied' : 'Copy diagnostics' }}
            </button>
            <button class="btn" @click="loadAll" :disabled="busy">
              <RefreshCw class="h-3.5 w-3.5" :class="{ 'animate-spin': busy }" />Refresh
            </button>
          </div>
        </div>

        <!-- The bar. One segment per check, widest where the most checks are. -->
        <div class="estate" role="img" :aria-label="overall.headline">
          <span v-for="r in sortedRows" :key="r.key"
            class="seg" :class="'seg-' + stateOf(r)"
            :data-tooltip="`${r.name} ${r.group}  ·  ${stateLabel(stateOf(r))}  ·  ${ago(ageOf(r.last))} ago`"
            data-tip-pos="bottom"></span>
        </div>

        <dl class="sysbar-meta">
          <div><dt>Checks</dt><dd class="num">{{ rows.length }}</dd></div>
          <div><dt>Cards</dt><dd class="num">{{ cardCount }}</dd></div>
          <div><dt>Feeds</dt><dd class="num">{{ feeds.length }}</dd></div>
          <div><dt>Build</dt><dd class="num">{{ build || '-' }}</dd></div>
          <div><dt>Server clock</dt><dd class="num sm">{{ serverClock }}</dd></div>
        </dl>
      </header>

      <!-- =================================================================== -->
      <div class="shell">
        <!-- LEFT RAIL. Navigation, with a dot on any section that needs
             attention, so the page tells you where to go. -->
        <nav class="rail" role="tablist" aria-label="Settings sections">
          <button v-for="t in tabs" :key="t.id" class="railitem"
            role="tab" :aria-selected="tab === t.id" :class="{ on: tab === t.id }"
            @click="tab = t.id" @keydown="railKey($event, t.id)">
            <component :is="t.icon" class="h-4 w-4" />
            <span class="railtext">{{ t.label }}</span>
            <span v-if="t.badge" class="railbadge" :class="t.tone">{{ t.badge }}</span>
          </button>
        </nav>

        <!-- CONTENT -->
        <div class="panel" role="tabpanel">

          <!-- ------------------------------------------------- FEEDS ----- -->
          <section v-if="tab === 'feeds'" class="pane">
            <div class="pane-head">
              <div>
                <h2 class="pane-h2">Feeds</h2>
                <p class="pane-p">Who is reporting, and whether they have stopped.</p>
              </div>
            </div>

            <div class="tiles">
              <article v-for="(f, i) in feeds" :key="f.name" class="tile rise"
                :style="{ '--d': i * 45 + 'ms' }" :class="{ hurt: f.stale || f.failing }">
                <header class="tile-top">
                  <span class="tile-ico" :class="f.tone"><component :is="f.icon" class="h-4 w-4" /></span>
                  <div class="tile-id">
                    <h3 class="tile-h3">{{ f.name }}</h3>
                    <span class="tile-src">{{ f.source }}</span>
                  </div>
                  <div class="tile-score">
                    <span class="num big" :class="f.tone">{{ f.fresh }}</span>
                    <span class="num of">/{{ f.total }}</span>
                  </div>
                </header>

                <div class="estate thin">
                  <span v-for="r in f.rows" :key="r.key" class="seg" :class="'seg-' + stateOf(r)"
                    :data-tooltip="`${r.name} ${r.group}  ·  ${stateLabel(stateOf(r))}`" data-tip-pos="top"></span>
                </div>

                <footer class="tile-foot">
                  <span class="chip" :class="f.stale ? 'chip-bad' : 'chip-dim'">
                    {{ f.stale }} stale
                  </span>
                  <span class="chip" :class="f.failing ? 'chip-bad' : 'chip-dim'">
                    {{ f.failing }} failing
                  </span>
                  <span class="chip" :class="f.silent ? 'chip-warn' : 'chip-dim'">
                    {{ f.silent }} silent
                  </span>
                  <span class="tile-age">newest {{ f.newest }}, oldest {{ f.oldest }}</span>
                  <button class="link" @click="openFeed = openFeed === f.name ? '' : f.name">
                    {{ openFeed === f.name ? 'Hide rows' : 'Show rows' }}
                    <ChevronDown class="h-3 w-3" :class="{ flip: openFeed === f.name }" />
                  </button>
                </footer>

                <ul v-if="openFeed === f.name" class="rowlist">
                  <li v-for="r in f.rows" :key="r.key">
                    <span class="dot" :class="'seg-' + stateOf(r)" aria-hidden="true"></span>
                    <router-link class="lnk" :to="`/endpoints/${r.key}`">{{ r.name }} {{ r.group }}</router-link>
                    <span class="num tiny dim">{{ ago(ageOf(r.last)) }}</span>
                    <span v-if="r.err" class="err">{{ r.err }}</span>
                  </li>
                </ul>
              </article>
            </div>

            <p v-if="!feeds.length" class="empty">Nothing has reported yet.</p>
            <p class="foot">
              Stale is over {{ STALE_MIN }} minutes since the last recorded result. For a collector
              feed that means the collector stopped pushing. Silent is a check that reported nothing
              rather than one that failed.
            </p>
          </section>

          <!-- ------------------------------------------------ CHECKS ----- -->
          <section v-else-if="tab === 'checks'" class="pane">
            <div class="pane-head">
              <div>
                <h2 class="pane-h2">Checks</h2>
                <p class="pane-p">Every configured check, searchable.</p>
              </div>
              <div class="filters">
                <label class="search">
                  <Search class="h-3.5 w-3.5" />
                  <input v-model="q" class="searchinp" type="search" placeholder="name, group or key" />
                </label>
                <div class="seg-ctl" role="group" aria-label="Filter by state">
                  <button v-for="f in STATE_FILTERS" :key="f.id" class="seg-btn"
                    :class="{ on: stateFilter === f.id }" @click="stateFilter = f.id">
                    {{ f.label }}<span class="seg-n">{{ stateCounts[f.id] }}</span>
                  </button>
                </div>
              </div>
            </div>

            <div class="list">
              <div v-for="r in filteredRows" :key="r.key" class="lrow">
                <span class="dot" :class="'seg-' + stateOf(r)" aria-hidden="true"></span>
                <div class="lmain">
                  <router-link class="lnk strong" :to="`/endpoints/${r.key}`">{{ r.name }}</router-link>
                  <span class="dim">{{ r.group }}</span>
                  <div class="lkey mono">{{ r.key }}</div>
                </div>
                <div class="lmeta">
                  <span class="num">{{ ago(ageOf(r.last)) }}</span>
                  <span class="lstate" :class="'t-' + stateOf(r)">{{ stateLabel(stateOf(r)) }}</span>
                </div>
                <div v-if="r.err" class="lerr">{{ r.err }}</div>
                <button class="btn tight" @click="togglePause(r.key)" :disabled="busyKeys.has(r.key)">
                  <component :is="isPaused(r.key) ? Play : Pause" class="h-3 w-3" />
                  {{ isPaused(r.key) ? 'Resume' : 'Pause' }}
                </button>
              </div>
              <p v-if="!filteredRows.length" class="empty">No check matches that.</p>
            </div>
          </section>

          <!-- ----------------------------------------------- TARGETS ----- -->
          <section v-else-if="tab === 'targets'" class="pane">
            <div class="pane-head">
              <div>
                <h2 class="pane-h2">Target overrides</h2>
                <p class="pane-p">
                  Re-points made from the dashboard. They live in /data and survive a deploy, so
                  nothing else shows them.
                </p>
              </div>
              <span class="pill" :class="targetEditingEnabled ? 'pill-ok' : 'pill-off'">
                {{ targetEditingEnabled ? 'editing enabled' : 'editing disabled' }}
              </span>
            </div>

            <p v-if="!overridden.length" class="empty">
              No overrides. Every check runs against the address in config.yaml.
            </p>
            <div v-else class="list">
              <div v-for="t in overridden" :key="t.key" class="lrow">
                <span class="dot seg-degraded" aria-hidden="true"></span>
                <div class="lmain">
                  <span class="strong">{{ t.name }}</span> <span class="dim">{{ t.group }}</span>
                  <div class="swap">
                    <span class="mono dim strike">{{ t.configured || 'unset' }}</span>
                    <ArrowRight class="h-3 w-3 dim" />
                    <span class="mono warn">{{ t.effective || 'unset' }}</span>
                  </div>
                </div>
                <button class="btn tight" @click="resetTarget(t.key)"
                  :disabled="!targetEditingEnabled || busyKeys.has(t.key)">
                  <RotateCcw class="h-3 w-3" />Reset
                </button>
              </div>
            </div>

            <div v-if="targetEditingEnabled" class="tokenbox">
              <div class="tokenhead">
                <KeyRound class="h-3.5 w-3.5" />
                <span class="eyebrow">Edit token</span>
                <span class="pill" :class="editToken ? 'pill-ok' : 'pill-off'">
                  {{ editToken ? 'held in this browser' : 'not set' }}
                </span>
              </div>
              <div class="tokenrow">
                <template v-if="editToken">
                  <span class="mono dim">{{ maskedToken }}</span>
                  <button class="btn tight" @click="forgetToken">Forget</button>
                </template>
                <template v-else>
                  <input v-model="tokenInput" class="inp mono" type="password"
                    placeholder="paste GATUS_EDIT_TOKEN" autocomplete="off" spellcheck="false" />
                  <button class="btn tight" @click="saveToken" :disabled="!tokenInput.trim()">Hold it</button>
                </template>
              </div>
              <p class="foot">
                Read it on the box with <code>cat data/edit_token</code>. It is kept in this browser
                only and never sent anywhere but the target routes.
              </p>
            </div>
            <p v-else class="foot">
              The server has no edit token, so the target routes refuse every write. Unset means off,
              not open.
            </p>
          </section>

          <!-- ------------------------------------------------ LAYOUT ----- -->
          <section v-else-if="tab === 'layout'" class="pane">
            <div class="pane-head">
              <div>
                <h2 class="pane-h2">Dashboard layout</h2>
                <p class="pane-p">
                  Stored on the server, so the wallboard, the TV and a laptop all show the same
                  arrangement.
                </p>
              </div>
              <span class="pill pill-off">shared by every screen</span>
            </div>

            <div class="stats">
              <div class="stat" :class="{ hot: hiddenCards.length }">
                <span class="num big">{{ hiddenCards.length }}</span><span class="eyebrow">Hidden cards</span>
              </div>
              <div class="stat" :class="{ hot: layoutStats.hiddenRows }">
                <span class="num big">{{ layoutStats.hiddenRows }}</span><span class="eyebrow">Hidden rows</span>
              </div>
              <div class="stat">
                <span class="num big">{{ renames.length }}</span><span class="eyebrow">Renamed</span>
              </div>
              <div class="stat">
                <span class="num big">{{ layoutStats.arranged }}</span><span class="eyebrow">Arranged</span>
              </div>
            </div>

            <template v-if="hiddenCards.length">
              <h3 class="sub">Hidden cards</h3>
              <div class="list">
                <div v-for="name in hiddenCards" :key="name" class="lrow">
                  <span class="dot dot-off" aria-hidden="true"></span>
                  <div class="lmain"><span class="strong">{{ name }}</span>
                    <div class="lkey dim">Checked and alerting, just not drawn.</div>
                  </div>
                  <button class="btn tight" @click="unhide(name)" :disabled="busy">
                    <Eye class="h-3 w-3" />Show
                  </button>
                </div>
              </div>
            </template>

            <template v-if="renames.length">
              <h3 class="sub">Renamed</h3>
              <div class="list">
                <div v-for="r in renames" :key="r.what" class="lrow">
                  <span class="dot dot-off" aria-hidden="true"></span>
                  <div class="lmain">
                    <div class="swap">
                      <span class="mono dim strike">{{ r.from }}</span>
                      <ArrowRight class="h-3 w-3 dim" />
                      <span class="strong">{{ r.to }}</span>
                    </div>
                    <div class="lkey dim">{{ r.what }}</div>
                  </div>
                </div>
              </div>
            </template>

            <div class="dangerzone">
              <div>
                <span class="eyebrow">Reset</span>
                <p class="pane-p">Clears hidden cards and rows, custom names and the arranged order, on every screen.</p>
              </div>
              <button class="btn danger" @click="doResetLayout" :disabled="busy">
                <RotateCcw class="h-3.5 w-3.5" />Reset layout
              </button>
            </div>
            <p class="foot">
              Presentation only. Hiding a row does not stop its check, and a hidden row that goes
              down still turns its card red. Pausing is on the Checks tab.
            </p>
          </section>

          <!-- ----------------------------------------------- DISPLAY ----- -->
          <section v-else class="pane">
            <div class="pane-head">
              <div>
                <h2 class="pane-h2">Display</h2>
                <p class="pane-p">This browser only. Nothing here reaches the server.</p>
              </div>
              <button class="btn tight" @click="resetDisplay"><RotateCcw class="h-3 w-3" />Defaults</button>
            </div>

            <div class="rows">
              <div class="prow">
                <div class="pinfo"><span class="plabel">Theme</span>
                  <span class="phint">Shared with the toggle in the dashboard header.</span></div>
                <div class="seg-ctl">
                  <button v-for="o in [{v:'system',l:'System'},{v:'light',l:'Light'},{v:'dark',l:'Dark'}]"
                    :key="o.v" class="seg-btn" :class="{ on: theme === o.v }" @click="setTheme(o.v)">{{ o.l }}</button>
                </div>
              </div>

              <div class="prow">
                <div class="pinfo"><span class="plabel">Dashboard view</span>
                  <span class="phint">Wide packs more cards per row.</span></div>
                <div class="seg-ctl">
                  <button v-for="o in [{v:'grid',l:'Grid'},{v:'horizontal',l:'Wide'}]" :key="o.v"
                    class="seg-btn" :class="{ on: dashboardView === o.v }"
                    @click="setDashboardView(o.v)">{{ o.l }}</button>
                </div>
              </div>

              <div class="prow">
                <div class="pinfo"><span class="plabel">History range</span>
                  <span class="phint">The window every chart and drill-in opens on.</span></div>
                <div class="seg-ctl">
                  <button v-for="r in HISTORY_RANGES" :key="r.value" class="seg-btn"
                    :class="{ on: historyRange === r.value }" @click="setHistoryRange(r.value)">{{ r.label }}</button>
                </div>
              </div>

              <div class="prow">
                <div class="pinfo"><span class="plabel">Sound</span>
                  <span class="phint">A tone when a check changes state.</span></div>
                <button class="switch" role="switch" :aria-checked="soundEnabled"
                  :class="{ on: soundEnabled }" @click="setSoundEnabled(!soundEnabled)">
                  <span class="knob"></span>
                </button>
              </div>

              <div class="prow">
                <div class="pinfo"><span class="plabel">Status colours</span>
                  <span class="phint">Used by every bar, chart and dot, including the bar above.</span></div>
                <div class="swatches">
                  <label v-for="k in ['up','degraded','down']" :key="k" class="sw">
                    <input type="color" :value="statusColors[k]" @input="setStatusColor(k, $event.target.value)" />
                    <span class="swname">{{ k }}</span>
                    <span class="mono tiny dim">{{ statusColors[k] }}</span>
                  </label>
                  <button class="btn tight" @click="resetStatusColors"><RotateCcw class="h-3 w-3" />Default</button>
                </div>
              </div>
            </div>
          </section>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import {
  ArrowLeft, ArrowRight, RefreshCw, RotateCcw, Play, Pause, Eye, KeyRound,
  Search, ChevronDown, ClipboardCopy, PhoneCall, Shield, Wifi, FolderTree,
  Server, Activity, Gauge, ListTree, Crosshair, LayoutGrid, SlidersHorizontal,
} from 'lucide-vue-next'
import {
  statusColors, setStatusColor, resetStatusColors,
  soundEnabled, setSoundEnabled,
  dashboardView, setDashboardView,
  historyRange, setHistoryRange, HISTORY_RANGES,
  layout, refreshLayout, setCardHidden, resetLayout,
  monitoringDisabled, refreshMonitoring, setMonitored,
  endpointTargets, targetEditingEnabled, refreshEndpointTargets,
  editToken, setEditToken, clearEndpointTarget,
  now, addToast,
} from '@/store'
import { prettifyTimestamp } from '@/utils/time'

// A result older than this means whatever produces it has stopped. Native
// checks run every 60s and the slowest collector sweeps every 120s, so five
// minutes clears every normal case without hiding a real stall.
const STALE_MIN = 5
const STALE_MS = STALE_MIN * 60 * 1000

const tab = ref('feeds')
const busy = ref(false)
const busyKeys = reactive(new Set())
const build = ref('')
const serverTime = ref(0)
const rows = ref([])
const openFeed = ref('')
const q = ref('')
const stateFilter = ref('all')
const copied = ref(false)

// --- state vocabulary ----------------------------------------------------
// Same words the cards use. A failure whose error says it read nothing is an
// absent signal, not an outage, and gets its own state so it cannot be
// mistaken for one. Keep in step with the collectors.
const NOT_REPORTING = /^no (phones|unifi|smb|hv) reporting\b/i
const ageOf = (r) => {
  if (!r || !r.timestamp) return null
  const t = Date.parse(r.timestamp)
  return Number.isNaN(t) ? null : Math.max(0, now.value - t)
}
const stateOf = (row) => {
  const r = row.last
  if (!r) return 'none'
  const age = ageOf(r)
  if (age === null || age > STALE_MS) return 'stale'
  if (!r.success) {
    const errs = Array.isArray(r.errors) ? r.errors : []
    return errs.some((e) => NOT_REPORTING.test(e)) ? 'silent' : 'down'
  }
  return (Array.isArray(r.errors) && r.errors.length) ? 'degraded' : 'up'
}
const STATE_LABELS = {
  up: 'healthy', degraded: 'warning', down: 'failing',
  silent: 'reported nothing', stale: 'stale', none: 'no result yet',
}
const stateLabel = (s) => STATE_LABELS[s] || s

const ago = (ms) => {
  if (ms === null || ms === undefined) return 'never'
  if (ms < 60000) return `${Math.max(0, Math.round(ms / 1000))}s`
  if (ms < 3600000) return `${Math.round(ms / 60000)}m`
  if (ms < 86400000) return `${Math.round(ms / 3600000)}h`
  return `${Math.round(ms / 86400000)}d`
}

// Worst first, so the bar reads as a severity histogram rather than an
// alphabetical shuffle: trouble collects on the left where the eye starts.
const SEVERITY = { down: 0, stale: 1, silent: 2, degraded: 3, none: 4, up: 5 }
const sortedRows = computed(() =>
  [...rows.value].sort((a, b) => (SEVERITY[stateOf(a)] - SEVERITY[stateOf(b)])
    || a.name.localeCompare(b.name)))

const counts = computed(() => {
  const c = { up: 0, degraded: 0, down: 0, silent: 0, stale: 0, none: 0 }
  for (const r of rows.value) c[stateOf(r)]++
  return c
})

const overall = computed(() => {
  const c = counts.value
  const total = rows.value.length
  if (!total) return { tone: 'idle', headline: 'Nothing reported yet', detail: 'Waiting for the first results.' }
  const bad = c.down + c.stale
  const warn = c.degraded + c.silent
  const parts = []
  if (c.down) parts.push(`${c.down} failing`)
  if (c.stale) parts.push(`${c.stale} stale`)
  if (c.silent) parts.push(`${c.silent} silent`)
  if (c.degraded) parts.push(`${c.degraded} with warnings`)
  if (bad) {
    return { tone: 'down', headline: `${bad} of ${total} checks need attention`, detail: parts.join(', ') }
  }
  if (warn) {
    return { tone: 'degraded', headline: `${total} checks reporting, ${warn} with something to look at`, detail: parts.join(', ') }
  }
  return { tone: 'up', headline: `All ${total} checks reporting`, detail: 'Nothing stale, nothing failing.' }
})

// --- feeds ---------------------------------------------------------------
// Which thing produces a row. The key carries it: collector rows are
// namespaced, anything left is a check Gatus runs itself.
const FEEDS = [
  { name: 'Phones', source: 'phone_collector.py', icon: PhoneCall, match: (k) => k.startsWith('phones_') },
  { name: 'Firewall', source: 'unifi_collector.py', icon: Shield, match: (k) => k.startsWith('firewall_') },
  { name: 'Wireless', source: 'unifi_collector.py', icon: Wifi, match: (k) => k.startsWith('wireless_') },
  { name: 'SMB shares', source: 'smb_collector.py', icon: FolderTree, match: (k) => k.endsWith('_smb-shares') },
  { name: 'Hypervisors', source: 'hv_collector.py', icon: Server, match: (k) => k.endsWith('_hypervisors') },
  { name: 'Direct checks', source: 'gatus itself', icon: Activity, match: () => true },
]

const feeds = computed(() => {
  const out = []
  const taken = new Set()
  for (const feed of FEEDS) {
    const mine = sortedRows.value.filter((r) => !taken.has(r.key) && feed.match(r.key))
    for (const r of mine) taken.add(r.key)
    if (!mine.length) continue
    const ages = mine.map((r) => ageOf(r.last)).filter((a) => a !== null)
    const stale = mine.filter((r) => stateOf(r) === 'stale' || stateOf(r) === 'none').length
    const failing = mine.filter((r) => stateOf(r) === 'down').length
    const silent = mine.filter((r) => stateOf(r) === 'silent').length
    out.push({
      ...feed,
      rows: mine,
      total: mine.length,
      fresh: mine.length - stale,
      stale, failing, silent,
      tone: (stale || failing) ? 'down' : silent ? 'degraded' : 'up',
      newest: ages.length ? ago(Math.min(...ages)) : 'never',
      oldest: ages.length ? ago(Math.max(...ages)) : 'never',
    })
  }
  return out
})

const cardCount = computed(() => new Set(rows.value.map((r) => r.name)).size)

// --- checks tab ----------------------------------------------------------
const STATE_FILTERS = [
  { id: 'all', label: 'All' },
  { id: 'trouble', label: 'Trouble' },
  { id: 'paused', label: 'Paused' },
]
const isPaused = (key) => monitoringDisabled.value.has(key)
const stateCounts = computed(() => ({
  all: rows.value.length,
  trouble: rows.value.filter((r) => ['down', 'stale', 'silent', 'none'].includes(stateOf(r))).length,
  paused: monitoringDisabled.value.size,
}))
const filteredRows = computed(() => {
  const needle = q.value.trim().toLowerCase()
  return sortedRows.value.filter((r) => {
    if (stateFilter.value === 'trouble' && !['down', 'stale', 'silent', 'none'].includes(stateOf(r))) return false
    if (stateFilter.value === 'paused' && !isPaused(r.key)) return false
    if (!needle) return true
    return `${r.name} ${r.group} ${r.key}`.toLowerCase().includes(needle)
  })
})

const togglePause = async (key) => {
  busyKeys.add(key)
  try {
    await setMonitored(key, isPaused(key))
    await refreshMonitoring()
  } finally {
    busyKeys.delete(key)
  }
}

// --- targets -------------------------------------------------------------
const overridden = computed(() =>
  Object.values(endpointTargets.value)
    .filter((t) => t && t.overridden)
    .sort((a, b) => (a.name + a.group).localeCompare(b.name + b.group)))

const tokenInput = ref('')
const maskedToken = computed(() => {
  const t = editToken.value || ''
  return t.length <= 8 ? '•'.repeat(t.length) : `${t.slice(0, 4)}${'•'.repeat(8)}${t.slice(-4)}`
})
const saveToken = () => { setEditToken(tokenInput.value); tokenInput.value = '' }
const forgetToken = () => { setEditToken(''); addToast('Edit token forgotten in this browser', 'info') }

const resetTarget = async (key) => {
  busyKeys.add(key)
  try {
    const result = await clearEndpointTarget(key)
    addToast(result.ok ? 'Target reset to config' : (result.message || 'Could not reset that target'),
      result.ok ? 'success' : 'error')
    await refreshEndpointTargets()
  } finally {
    busyKeys.delete(key)
  }
}

// --- layout --------------------------------------------------------------
const cards = computed(() => (layout.value && layout.value.cards) || {})
const hiddenCards = computed(() =>
  Object.keys(cards.value).filter((n) => cards.value[n] && cards.value[n].hidden).sort())
const layoutStats = computed(() => {
  let hiddenRows = 0, arranged = 0
  for (const name of Object.keys(cards.value)) {
    const card = cards.value[name] || {}
    if (typeof card.order === 'number') arranged++
    for (const key of Object.keys(card.rows || {})) {
      const row = card.rows[key] || {}
      if (row.hidden) hiddenRows++
      if (typeof row.order === 'number') arranged++
    }
  }
  return { hiddenRows, arranged }
})
// A rename is only legible if it says what it was and what it became.
const renames = computed(() => {
  const out = []
  for (const name of Object.keys(cards.value)) {
    const card = cards.value[name] || {}
    if (card.title) out.push({ what: 'card', from: name, to: card.title })
    for (const key of Object.keys(card.rows || {})) {
      const row = card.rows[key] || {}
      if (row.label) out.push({ what: `${name} row`, from: key, to: row.label })
    }
  }
  return out
})

const unhide = async (name) => {
  busy.value = true
  try { await setCardHidden(name, false); await refreshLayout() } finally { busy.value = false }
}
const doResetLayout = async () => {
  busy.value = true
  try { await resetLayout(); await refreshLayout(); addToast('Layout reset for every screen', 'info') }
  finally { busy.value = false }
}

// --- display -------------------------------------------------------------
// The header toggle keeps the theme in a COOKIE named "theme", not in
// localStorage. Writing a different key here would give the page a theme
// control that silently disagreed with the toggle, so this reads and writes
// exactly the same cookie.
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
  // Expire rather than storing "system": the header toggle understands
  // dark, light or absent, and absent is what following the system means.
  document.cookie = value === 'system'
    ? `${THEME_COOKIE_NAME}=; path=/; max-age=0; samesite=strict`
    : `${THEME_COOKIE_NAME}=${value}; path=/; max-age=${THEME_COOKIE_MAX_AGE}; samesite=strict`
  applyTheme()
}
const resetDisplay = () => {
  setTheme('system')
  setDashboardView('grid')
  setHistoryRange('24h')
  setSoundEnabled(true)
  resetStatusColors()
  addToast('Display settings reset for this browser', 'info')
}

// --- tabs ----------------------------------------------------------------
const tabs = computed(() => {
  const trouble = stateCounts.value.trouble
  return [
    { id: 'feeds', label: 'Feeds', icon: Gauge, badge: feeds.value.filter((f) => f.stale || f.failing).length || 0, tone: 'bad' },
    { id: 'checks', label: 'Checks', icon: ListTree, badge: trouble || 0, tone: 'bad' },
    { id: 'targets', label: 'Targets', icon: Crosshair, badge: overridden.value.length || 0, tone: 'warn' },
    { id: 'layout', label: 'Layout', icon: LayoutGrid, badge: hiddenCards.value.length + layoutStats.value.hiddenRows || 0, tone: 'warn' },
    { id: 'display', label: 'Display', icon: SlidersHorizontal, badge: 0, tone: '' },
  ]
})
const railKey = (event, id) => {
  const order = tabs.value.map((t) => t.id)
  const i = order.indexOf(id)
  if (event.key === 'ArrowDown' || event.key === 'ArrowRight') {
    event.preventDefault(); tab.value = order[(i + 1) % order.length]
  } else if (event.key === 'ArrowUp' || event.key === 'ArrowLeft') {
    event.preventDefault(); tab.value = order[(i - 1 + order.length) % order.length]
  }
}

// --- diagnostics ---------------------------------------------------------
// One paste that describes the whole system. Faster than screenshotting five
// panels when something needs explaining to someone else.
const copyDiagnostics = async () => {
  const c = counts.value
  const lines = [
    `LL-Gatus build ${build.value || '?'}  ${serverClock.value}`,
    overall.value.headline,
    `up ${c.up}  warning ${c.degraded}  failing ${c.down}  silent ${c.silent}  stale ${c.stale}  none ${c.none}`,
    '',
    'FEEDS',
    ...feeds.value.map((f) =>
      `  ${f.name.padEnd(14)} ${String(f.fresh).padStart(3)}/${String(f.total).padEnd(3)} fresh` +
      `  stale ${f.stale}  failing ${f.failing}  silent ${f.silent}  oldest ${f.oldest}  (${f.source})`),
  ]
  const trouble = sortedRows.value.filter((r) => ['down', 'stale', 'silent', 'none'].includes(stateOf(r)))
  if (trouble.length) {
    lines.push('', 'NEEDS ATTENTION')
    for (const r of trouble) {
      lines.push(`  ${r.name} ${r.group}`.padEnd(34) +
        `${stateLabel(stateOf(r)).padEnd(18)} ${ago(ageOf(r.last)).padStart(5)} ago` +
        (r.err ? `  ${r.err}` : ''))
    }
  }
  if (overridden.value.length) {
    lines.push('', 'TARGET OVERRIDES')
    for (const t of overridden.value) {
      lines.push(`  ${t.name} ${t.group}: ${t.configured || 'unset'} -> ${t.effective || 'unset'}`)
    }
  }
  if (monitoringDisabled.value.size) {
    lines.push('', `PAUSED (${monitoringDisabled.value.size})`,
      ...[...monitoringDisabled.value].sort().map((k) => `  ${k}`))
  }
  const text = lines.join('\n')
  try {
    await navigator.clipboard.writeText(text)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch (e) {
    // Clipboard needs a secure context, which a plain-http LAN address is not.
    // A toast with the reason beats a button that does nothing.
    addToast('The browser refused clipboard access. Open over HTTPS or copy from the console.', 'error')
    console.log(text)
  }
}

// --- loading -------------------------------------------------------------
const serverClock = computed(() => {
  if (!serverTime.value) return '-'
  try { return prettifyTimestamp(new Date(serverTime.value).toISOString()) } catch (e) { return '-' }
})

const firstError = (r) => {
  const errs = (r && Array.isArray(r.errors)) ? r.errors.filter(Boolean) : []
  return errs.length ? errs[0] : ''
}

const loadRows = async () => {
  try {
    // pageSize=1 is one recent result per endpoint, which is all the state and
    // freshness need; the dashboard payload carries 50 each.
    const response = await fetch('/api/v1/endpoints/statuses?page=1&pageSize=1', { cache: 'no-store' })
    if (!response.ok) return
    const data = await response.json()
    rows.value = (Array.isArray(data) ? data : []).map((e) => {
      const last = (e.results && e.results.length) ? e.results[e.results.length - 1] : null
      return { key: e.key, name: e.name, group: e.group || '', last, err: firstError(last) }
    })
  } catch (e) { /* keep the previous rows */ }
}

const loadMeta = async () => {
  try {
    const v = await fetch('/api/v1/version', { cache: 'no-store' })
    if (v.ok) build.value = (await v.json()).version || ''
  } catch (e) { /* non-fatal */ }
  try {
    // {"time": <unix millis>}, a number, not a timestamp string.
    const t = await fetch('/api/v1/time', { cache: 'no-store' })
    if (t.ok) {
      const body = await t.json()
      serverTime.value = typeof body.time === 'number' ? body.time : 0
    }
  } catch (e) { /* non-fatal */ }
}

const loadAll = async () => {
  busy.value = true
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
.sx { max-width: 1320px; margin: 0 auto; }

/* Type scale. Character comes from weight, size and letterspacing against the
   app's own stack rather than a webfont: this is an internal tool that has to
   render the same on a wallboard with no internet. */
.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px; letter-spacing: 0.14em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); font-weight: 700;
}
.num { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; }
.num.big { font-size: 1.9rem; font-weight: 800; line-height: 1; letter-spacing: -0.02em; }
.num.of { font-size: 1rem; font-weight: 700; color: hsl(var(--muted-foreground)); }
.num.sm { font-size: 0.8rem; }
.num.tiny, .tiny { font-size: 0.7rem; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.dim { color: hsl(var(--muted-foreground)); }
.strong { font-weight: 650; }
.strike { text-decoration: line-through; text-decoration-thickness: 1px; opacity: 0.75; }
.warn { color: var(--status-degraded); }

/* ---- system bar ------------------------------------------------------- */
.sysbar {
  position: relative; overflow: hidden;
  border: 1px solid hsl(var(--border)); border-radius: 18px;
  background:
    radial-gradient(120% 140% at 0% 0%, hsl(var(--muted) / 0.55), transparent 60%),
    linear-gradient(hsl(var(--card)), hsl(var(--card)));
  padding: 1.1rem 1.25rem 0.9rem;
  margin-bottom: 1rem;
}
/* Hairline grid, so the panel is not a flat rectangle. */
.sysbar::before {
  content: ''; position: absolute; inset: 0; pointer-events: none;
  background-image:
    linear-gradient(to right, hsl(var(--border) / 0.5) 1px, transparent 1px),
    linear-gradient(to bottom, hsl(var(--border) / 0.5) 1px, transparent 1px);
  background-size: 28px 28px;
  mask-image: radial-gradient(70% 90% at 15% 0%, #000 0%, transparent 70%);
  opacity: 0.6;
}
/* A state hairline along the top edge: the first thing drawn, the last thing
   you need explained. */
.sysbar::after {
  content: ''; position: absolute; left: 0; right: 0; top: 0; height: 3px;
  background: hsl(var(--muted-foreground) / 0.4);
}
.sysbar.tone-up::after { background: var(--status-up); }
.sysbar.tone-degraded::after { background: var(--status-degraded); }
.sysbar.tone-down::after { background: var(--status-down); }

.sysbar-top { position: relative; display: flex; align-items: flex-start; gap: 1rem; flex-wrap: wrap; }
.back { color: hsl(var(--muted-foreground)); margin-top: 0.45rem; transition: color 0.12s; }
.back:hover { color: hsl(var(--foreground)); }
.back:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 3px; border-radius: 6px; }
.sysbar-id { min-width: 0; }
.sysbar-h1 { font-size: 1.7rem; font-weight: 800; letter-spacing: -0.025em; line-height: 1.05; margin-top: 0.1rem; }

.sysbar-state { margin-left: auto; text-align: right; min-width: 0; }
.state-line { display: flex; align-items: center; gap: 0.45rem; justify-content: flex-end; }
.state-dot { width: 9px; height: 9px; border-radius: 999px; background: hsl(var(--muted-foreground)); flex-shrink: 0; }
.tone-up .state-dot { background: var(--status-up); box-shadow: 0 0 0 4px color-mix(in srgb, var(--status-up) 22%, transparent); }
.tone-degraded .state-dot { background: var(--status-degraded); box-shadow: 0 0 0 4px color-mix(in srgb, var(--status-degraded) 22%, transparent); }
.tone-down .state-dot { background: var(--status-down); box-shadow: 0 0 0 4px color-mix(in srgb, var(--status-down) 22%, transparent); }
.state-head { font-size: 0.95rem; font-weight: 700; }
.state-sub { font-size: 0.75rem; color: hsl(var(--muted-foreground)); margin-top: 0.1rem; }

.sysbar-acts { display: flex; gap: 0.4rem; align-self: flex-start; margin-top: 0.3rem; }

/* The estate bar. */
.estate {
  position: relative; display: flex; gap: 2px; margin: 0.9rem 0 0.75rem;
  height: 22px; align-items: stretch;
}
.estate.thin { height: 9px; margin: 0.6rem 0; }
.seg { flex: 1 1 0; min-width: 2px; border-radius: 2px; background: hsl(var(--muted)); transition: transform 0.1s, filter 0.1s; }
.seg:hover { transform: scaleY(1.22); filter: brightness(1.12); }
.seg-up { background: var(--status-up); }
.seg-degraded { background: var(--status-degraded); }
.seg-down { background: var(--status-down); }
.seg-silent { background: hsl(var(--foreground) / 0.55); }
.seg-stale { background: color-mix(in srgb, var(--status-down) 45%, hsl(var(--muted))); }
.seg-none { background: hsl(var(--muted)); }

.sysbar-meta { position: relative; display: flex; flex-wrap: wrap; gap: 0 1.6rem; margin: 0; }
.sysbar-meta div { display: flex; align-items: baseline; gap: 0.4rem; }
.sysbar-meta dt {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9.5px; letter-spacing: 0.12em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); font-weight: 700;
}
.sysbar-meta dd { margin: 0; font-size: 0.85rem; font-weight: 700; }

/* ---- shell: rail + panel --------------------------------------------- */
.shell { display: grid; grid-template-columns: 1fr; gap: 0.9rem; }
@media (min-width: 900px) { .shell { grid-template-columns: 13.5rem 1fr; align-items: start; } }

.rail {
  display: flex; gap: 0.25rem; overflow-x: auto;
  border: 1px solid hsl(var(--border)); border-radius: 14px;
  background: hsl(var(--card)); padding: 0.4rem;
}
@media (min-width: 900px) { .rail { flex-direction: column; overflow: visible; position: sticky; top: 0.75rem; } }
.railitem {
  display: flex; align-items: center; gap: 0.6rem; width: 100%;
  padding: 0.55rem 0.7rem; border-radius: 10px; border: 0;
  background: transparent; color: hsl(var(--muted-foreground));
  font-size: 0.85rem; font-weight: 600; cursor: pointer; text-align: left;
  white-space: nowrap; transition: background 0.12s, color 0.12s;
}
.railitem:hover { background: hsl(var(--muted) / 0.55); color: hsl(var(--foreground)); }
.railitem.on { background: hsl(var(--muted)); color: hsl(var(--foreground)); }
.railitem.on::before {
  content: ''; position: absolute; left: 0; width: 3px; height: 1.3rem;
  border-radius: 0 3px 3px 0; background: hsl(var(--foreground));
}
@media (max-width: 899px) { .railitem.on::before { display: none; } }
.railitem { position: relative; }
.railtext { flex: 1 1 auto; }
.railbadge {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.68rem; font-weight: 700; padding: 0.05rem 0.35rem;
  border-radius: 999px; background: hsl(var(--muted-foreground) / 0.2);
}
.railbadge.bad { background: color-mix(in srgb, var(--status-down) 22%, transparent); color: var(--status-down); }
.railbadge.warn { background: color-mix(in srgb, var(--status-degraded) 25%, transparent); color: var(--status-degraded); }

.panel { min-width: 0; }
.pane {
  border: 1px solid hsl(var(--border)); border-radius: 14px;
  background: hsl(var(--card)); padding: 1rem 1.15rem;
  animation: fade 0.18s ease-out both;
}
@keyframes fade { from { opacity: 0; transform: translateY(3px); } to { opacity: 1; transform: none; } }
.pane-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; flex-wrap: wrap; margin-bottom: 0.9rem; }
.pane-h2 { font-size: 1.1rem; font-weight: 750; letter-spacing: -0.015em; }
.pane-p { font-size: 0.78rem; color: hsl(var(--muted-foreground)); margin-top: 0.15rem; max-width: 56ch; line-height: 1.45; }
.sub { font-size: 0.75rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.07em; color: hsl(var(--muted-foreground)); margin: 1rem 0 0.45rem; }
.foot { font-size: 0.74rem; color: hsl(var(--muted-foreground)); margin-top: 0.9rem; line-height: 1.5; }
.foot code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background: hsl(var(--muted) / 0.7); padding: 0.05rem 0.3rem; border-radius: 4px; }
.empty { font-size: 0.85rem; color: hsl(var(--muted-foreground)); padding: 1.1rem 0; }

/* ---- feed tiles ------------------------------------------------------- */
.tiles { display: grid; grid-template-columns: 1fr; gap: 0.7rem; }
@media (min-width: 700px) { .tiles { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
.tile {
  border: 1px solid hsl(var(--border)); border-radius: 12px;
  background: hsl(var(--background)); padding: 0.8rem 0.9rem;
  transition: border-color 0.12s, transform 0.12s;
}
.tile:hover { border-color: hsl(var(--muted-foreground) / 0.35); transform: translateY(-1px); }
.tile.hurt { border-color: color-mix(in srgb, var(--status-down) 40%, hsl(var(--border))); }
.rise { animation: rise 0.3s cubic-bezier(0.2, 0.7, 0.2, 1) both; animation-delay: var(--d, 0ms); }
@keyframes rise { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }
.tile-top { display: flex; align-items: flex-start; gap: 0.6rem; }
.tile-ico {
  display: grid; place-items: center; width: 1.9rem; height: 1.9rem; border-radius: 9px;
  background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); flex-shrink: 0;
}
.tile-ico.up { background: color-mix(in srgb, var(--status-up) 18%, transparent); color: var(--status-up); }
.tile-ico.degraded { background: color-mix(in srgb, var(--status-degraded) 20%, transparent); color: var(--status-degraded); }
.tile-ico.down { background: color-mix(in srgb, var(--status-down) 18%, transparent); color: var(--status-down); }
.tile-id { min-width: 0; flex: 1 1 auto; }
.tile-h3 { font-size: 0.92rem; font-weight: 700; line-height: 1.1; }
.tile-src { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.68rem; color: hsl(var(--muted-foreground)); }
.tile-score { display: flex; align-items: baseline; gap: 0.1rem; }
.tile-score .up { color: var(--status-up); }
.tile-score .degraded { color: var(--status-degraded); }
.tile-score .down { color: var(--status-down); }
.tile-foot { display: flex; align-items: center; flex-wrap: wrap; gap: 0.35rem 0.5rem; }
.chip {
  font-size: 0.68rem; font-weight: 700; padding: 0.1rem 0.4rem; border-radius: 6px;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}
.chip-dim { background: hsl(var(--muted) / 0.8); color: hsl(var(--muted-foreground)); }
.chip-bad { background: color-mix(in srgb, var(--status-down) 20%, transparent); color: var(--status-down); }
.chip-warn { background: color-mix(in srgb, var(--status-degraded) 22%, transparent); color: var(--status-degraded); }
.tile-age { font-size: 0.68rem; color: hsl(var(--muted-foreground)); margin-left: auto; }
.link {
  display: inline-flex; align-items: center; gap: 0.2rem; border: 0; background: none;
  font-size: 0.7rem; font-weight: 650; color: hsl(var(--foreground)); cursor: pointer; padding: 0;
}
.link:hover { text-decoration: underline; }
.flip { transform: rotate(180deg); }
.rowlist { list-style: none; padding: 0.6rem 0 0; margin: 0.6rem 0 0; border-top: 1px solid hsl(var(--border)); display: flex; flex-direction: column; gap: 0.3rem; }
.rowlist li { display: flex; align-items: center; gap: 0.5rem; font-size: 0.76rem; min-width: 0; }
.rowlist .err { color: var(--status-down); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dot { width: 7px; height: 7px; border-radius: 999px; flex-shrink: 0; background: hsl(var(--muted)); }
.dot-off { background: hsl(var(--muted-foreground) / 0.45); }

/* ---- lists ------------------------------------------------------------ */
.list { display: flex; flex-direction: column; gap: 2px; }
.lrow {
  display: grid; grid-template-columns: auto 1fr auto auto; align-items: center; gap: 0.6rem;
  padding: 0.5rem 0.65rem; border: 1px solid hsl(var(--border)); border-radius: 10px;
  background: hsl(var(--background)); font-size: 0.82rem;
}
.lrow:hover { border-color: hsl(var(--muted-foreground) / 0.3); }
.lmain { min-width: 0; }
.lkey { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.68rem; color: hsl(var(--muted-foreground)); }
.lmeta { display: flex; align-items: center; gap: 0.6rem; font-size: 0.72rem; }
.lstate { font-size: 0.68rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.05em; }
.t-up { color: var(--status-up); }
.t-degraded { color: var(--status-degraded); }
.t-down, .t-stale { color: var(--status-down); }
.t-silent, .t-none { color: hsl(var(--muted-foreground)); }
.lerr { grid-column: 1 / -1; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.68rem; color: var(--status-down); overflow: hidden; text-overflow: ellipsis; }
.lnk { color: inherit; }
.lnk:hover { text-decoration: underline; }
.swap { display: flex; align-items: center; gap: 0.4rem; flex-wrap: wrap; font-size: 0.74rem; margin-top: 0.1rem; }

/* ---- controls --------------------------------------------------------- */
.btn {
  display: inline-flex; align-items: center; gap: 0.35rem;
  font-size: 0.75rem; font-weight: 650; padding: 0.35rem 0.6rem; border-radius: 9px;
  border: 1px solid hsl(var(--border)); background: hsl(var(--background));
  color: hsl(var(--foreground)); cursor: pointer; white-space: nowrap;
  transition: background 0.12s, border-color 0.12s;
}
.btn:hover:not(:disabled) { background: hsl(var(--muted) / 0.7); border-color: hsl(var(--muted-foreground) / 0.35); }
.btn:disabled { opacity: 0.45; cursor: not-allowed; }
.btn:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }
.btn.tight { font-size: 0.7rem; padding: 0.25rem 0.5rem; }
.btn.danger { color: var(--status-down); border-color: color-mix(in srgb, var(--status-down) 40%, transparent); }

.seg-ctl { display: inline-flex; padding: 2px; border: 1px solid hsl(var(--border)); border-radius: 10px; background: hsl(var(--muted) / 0.45); }
.seg-btn {
  display: inline-flex; align-items: center; gap: 0.3rem;
  font-size: 0.74rem; font-weight: 650; padding: 0.28rem 0.6rem; border: 0;
  border-radius: 8px; background: transparent; color: hsl(var(--muted-foreground)); cursor: pointer;
  white-space: nowrap; transition: background 0.12s, color 0.12s;
}
.seg-btn:hover { color: hsl(var(--foreground)); }
.seg-btn.on { background: hsl(var(--background)); color: hsl(var(--foreground)); box-shadow: 0 1px 2px hsl(var(--foreground) / 0.08); }
.seg-n { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.65rem; opacity: 0.7; }

.filters { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
.search { display: flex; align-items: center; gap: 0.4rem; padding: 0.3rem 0.55rem; border: 1px solid hsl(var(--border)); border-radius: 9px; background: hsl(var(--background)); color: hsl(var(--muted-foreground)); }
.searchinp { border: 0; background: none; outline: none; font-size: 0.78rem; color: hsl(var(--foreground)); width: 11rem; }
.inp { font-size: 0.78rem; padding: 0.3rem 0.55rem; border-radius: 9px; border: 1px solid hsl(var(--border)); background: hsl(var(--background)); color: hsl(var(--foreground)); }
.inp:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }

.pill { font-size: 0.68rem; font-weight: 700; padding: 0.15rem 0.5rem; border-radius: 999px; white-space: nowrap; }
.pill-ok { background: color-mix(in srgb, var(--status-up) 18%, transparent); color: var(--status-up); }
.pill-off { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }

.tokenbox { margin-top: 1rem; border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 0.8rem 0.9rem; }
.tokenhead { display: flex; align-items: center; gap: 0.45rem; color: hsl(var(--muted-foreground)); margin-bottom: 0.55rem; }
.tokenrow { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }

.stats { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.6rem; }
@media (min-width: 700px) { .stats { grid-template-columns: repeat(4, minmax(0, 1fr)); } }
.stat { display: flex; flex-direction: column; gap: 0.3rem; padding: 0.7rem 0.8rem; border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--background)); }
.stat.hot .num { color: var(--status-degraded); }

.dangerzone {
  display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap;
  margin-top: 1.1rem; padding: 0.8rem 0.9rem; border-radius: 12px;
  border: 1px solid color-mix(in srgb, var(--status-down) 30%, hsl(var(--border)));
  background: color-mix(in srgb, var(--status-down) 5%, transparent);
}

.rows { display: flex; flex-direction: column; }
.prow { display: flex; align-items: center; justify-content: space-between; gap: 1.2rem; flex-wrap: wrap; padding: 0.8rem 0; border-bottom: 1px solid hsl(var(--border)); }
.prow:last-child { border-bottom: 0; }
.pinfo { display: flex; flex-direction: column; min-width: 0; }
.plabel { font-size: 0.86rem; font-weight: 650; }
.phint { font-size: 0.73rem; color: hsl(var(--muted-foreground)); margin-top: 0.1rem; }

.switch { width: 2.5rem; height: 1.4rem; border-radius: 999px; border: 1px solid hsl(var(--border)); background: hsl(var(--muted)); cursor: pointer; padding: 0; position: relative; transition: background 0.15s; flex-shrink: 0; }
.switch .knob { position: absolute; top: 2px; left: 2px; width: 1.05rem; height: 1.05rem; border-radius: 999px; background: hsl(var(--background)); box-shadow: 0 1px 2px hsl(var(--foreground) / 0.2); transition: transform 0.15s; }
.switch.on { background: var(--status-up); border-color: transparent; }
.switch.on .knob { transform: translateX(1.1rem); }
.switch:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }

.swatches { display: flex; align-items: center; flex-wrap: wrap; gap: 0.8rem; }
.sw { display: flex; align-items: center; gap: 0.35rem; }
.swname { font-size: 0.73rem; text-transform: capitalize; color: hsl(var(--muted-foreground)); }
.sw input[type="color"] { width: 1.5rem; height: 1.5rem; padding: 0; border: 1px solid hsl(var(--border)); border-radius: 7px; background: none; cursor: pointer; }

@media (prefers-reduced-motion: reduce) {
  .rise, .pane { animation: none; }
  .seg, .tile, .switch .knob { transition: none; }
}
</style>
