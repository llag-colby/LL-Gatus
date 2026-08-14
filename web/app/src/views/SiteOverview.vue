<template>
  <div class="dashboard-container detail-page site-overview bg-background">
    <div class="w-full px-4 sm:px-6 py-4 space-y-4">

      <!-- Header -->
      <div class="flex items-end justify-between gap-4 flex-wrap">
        <div class="flex items-center gap-3">
          <router-link to="/" class="text-muted-foreground hover:text-foreground transition-colors mb-1"
            data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-5 w-5" />
          </router-link>
          <div>
            <div class="eyebrow">Site overview · {{ feeds.length }} {{ feeds.length === 1 ? 'feed' : 'feeds' }}</div>
            <h1 class="text-2xl font-bold tracking-tight leading-none mt-0.5">{{ siteName }}</h1>
          </div>
        </div>
        <div class="flex items-center gap-2 mb-0.5">
          <span class="status-pill" :class="verdict.pillClass">{{ verdict.pillLabel }}</span>
          <Button variant="ghost" size="icon" class="h-9 w-9" @click="fetchAll" :disabled="loading"
            data-tooltip="Refresh" data-tip-pos="bottom">
            <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
          </Button>
        </div>
      </div>

      <div v-if="!loaded" class="flex items-center justify-center py-20">
        <Loading size="lg" />
      </div>

      <div v-else-if="feeds.length === 0" class="empty-state">
        <div class="text-base font-semibold mb-1">No feeds for this site</div>
        <div class="text-sm text-muted-foreground">
          Nothing is configured under the name <span class="font-mono">{{ siteName }}</span>.
        </div>
      </div>

      <template v-else>
        <!-- Verdict. The one line a tech reads before doing anything else. -->
        <div class="verdict" :class="verdict.toneClass">
          <p class="verdict-line">{{ verdict.headline }}</p>
          <p v-if="verdict.detail" class="verdict-detail">{{ verdict.detail }}</p>
        </div>

        <!-- Feed strips — one per circuit/system, each an identity card you can
             read out to a carrier on the phone. -->
        <div class="grid gap-3" :class="feedGridClass">
          <router-link
            v-for="feed in feeds"
            :key="feed.key"
            :to="`/endpoints/${feed.key}`"
            class="feed-strip group"
            :class="`is-${feed.state}`"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="eyebrow truncate">{{ feed.role }}</div>
                <div class="feed-carrier truncate">{{ feed.carrier || '—' }}</div>
              </div>
              <span class="lamp" :class="`lamp-${feed.state}`"></span>
            </div>

            <div class="feed-metric">
              <span class="feed-value">{{ feed.primaryValue }}</span>
              <span class="feed-unit">{{ feed.primaryUnit }}</span>
            </div>

            <dl class="feed-facts">
              <div v-if="feed.address">
                <dt>Address</dt><dd class="font-mono">{{ feed.address }}</dd>
              </div>
              <div>
                <dt>Uptime 24h</dt>
                <dd class="font-mono">{{ feed.uptime24h }}</dd>
              </div>
              <div>
                <dt>Last check</dt><dd>{{ feed.lastCheck }}</dd>
              </div>
            </dl>

            <span class="feed-go">Open <ChevronRight class="h-3 w-3" /></span>
          </router-link>
        </div>

        <!-- SIGNATURE: every feed on one shared time axis. Reading down a column
             tells you whether a drop hit one circuit or the whole building —
             the difference between calling the carrier and driving out there. -->
        <div class="panel">
          <div class="panel-head">
            <div>
              <div class="eyebrow">Correlated timeline</div>
              <p class="panel-sub">Same time axis for every feed. Read down a column to see what failed together.</p>
            </div>
            <div class="scrub-readout" :class="{ live: hoverIndex === null }">
              <span class="scrub-time font-mono">{{ scrub.label }}</span>
              <span class="scrub-feeds">
                <span v-for="f in scrub.feeds" :key="f.role" class="scrub-feed">
                  <span class="lamp lamp-sm" :class="`lamp-${f.state}`"></span>{{ f.role }}
                </span>
              </span>
            </div>
          </div>

          <div class="lanes" @mouseleave="hoverIndex = null">
            <div v-for="feed in feeds" :key="feed.key" class="lane">
              <span class="lane-label">{{ feed.role }}</span>
              <div class="lane-track">
                <span
                  v-for="(cell, i) in feed.lane"
                  :key="i"
                  class="lane-cell"
                  :class="[cellClass(cell), { hot: hoverIndex === i }]"
                  @mouseenter="hoverIndex = i"
                />
              </div>
            </div>
            <div class="lane axis">
              <span class="lane-label"></span>
              <div class="lane-track axis-track">
                <span class="font-mono">{{ windowLabel }}</span>
                <span class="font-mono">now</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Merged history across every feed, so one list answers "what changed". -->
        <div class="panel">
          <div class="panel-head">
            <div>
              <div class="eyebrow">What happened</div>
              <p class="panel-sub">Every state change at this site, newest first.</p>
            </div>
          </div>

          <div v-if="timeline.length === 0" class="text-sm text-muted-foreground py-2">
            No state changes recorded yet.
          </div>
          <ol v-else class="log">
            <li v-for="(entry, i) in timeline" :key="i" class="log-row">
              <span class="log-mark" :class="`mark-${entry.tone}`">
                <ArrowUpCircle v-if="entry.tone === 'up'" class="h-4 w-4" />
                <ArrowDownCircle v-else-if="entry.tone === 'down'" class="h-4 w-4" />
                <PlayCircle v-else class="h-4 w-4" />
              </span>
              <span class="log-body">
                <span class="log-text">{{ entry.text }}</span>
                <span class="log-meta font-mono">{{ entry.clock }} · {{ entry.ago }}</span>
              </span>
              <span v-if="entry.lasted" class="log-lasted font-mono">{{ entry.lasted }}</span>
            </li>
          </ol>
        </div>
      </template>
    </div>

    <Settings @refreshData="fetchAll" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft, RefreshCw, ChevronRight, ArrowUpCircle, ArrowDownCircle, PlayCircle } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import Loading from '@/components/Loading.vue'
import Settings from '@/components/Settings.vue'
import { now } from '@/store'
import { generatePrettyTimeAgo, generatePrettyTimeDifference, DISPLAY_TIMEZONE } from '@/utils/time'

const route = useRoute()

// How much history the page loads, and how many columns the timeline is cut
// into. 200 checks at the usual 60s interval is a little over three hours —
// long enough to cover a shift's worth of flapping without a heavy fetch.
const RESULT_WINDOW = 200
const LANE_COLUMNS = 96

const siteName = computed(() => decodeURIComponent(route.params.name || ''))

const endpoints = ref([])
const uptimes = ref({})     // key -> { '24h': number|null }
const phoneCounts = ref({}) // key -> counts object from the phones inventory
const loaded = ref(false)
const loading = ref(false)
const hoverIndex = ref(null)

// --- Feed classification -------------------------------------------------
// Same rules the dashboard card uses, so a site reads identically in both
// places: the group string carries both the role and the carrier.
const roleOf = (group) => {
  const g = (group || '').toLowerCase()
  if (/wan\s*1|primary/.test(g)) return 'WAN 1'
  if (/wan\s*2|backup|secondary/.test(g)) return 'WAN 2'
  if (/phone|voip|sip/.test(g)) return 'Phones'
  return group || 'Feed'
}
const carrierOf = (group) => {
  const m = (group || '').match(/\(([^)]+)\)/)
  return m ? m[1].trim() : ''
}
const isPhoneFeed = (ep) => (ep.key || '').startsWith('phones_') || /phone|voip|sip/i.test(ep.group || '')

// A failing check that carries this marker means nothing reported at all,
// which is not the same as reporting a failure. Matches LocationCard.
const NOT_REPORTING = /^no phones reporting\b/i
const isNotReporting = (r) =>
  !!r && !r.success && (r.errors || []).some((e) => NOT_REPORTING.test(e))

const stateOf = (result) => {
  if (!result) return 'none'
  if (result.success) return 'up'
  return isNotReporting(result) ? 'none' : 'down'
}

const msOf = (r) => (r && r.duration ? Math.round(r.duration / 1000000) : null)

// --- Time window ---------------------------------------------------------
const windowStart = computed(() => {
  let oldest = Infinity
  for (const ep of endpoints.value) {
    const first = (ep.results || [])[0]
    if (first) oldest = Math.min(oldest, new Date(first.timestamp).getTime())
  }
  return Number.isFinite(oldest) ? oldest : now.value - 3600000
})

const windowLabel = computed(() => {
  const span = Math.max(1, now.value - windowStart.value)
  const hours = span / 3600000
  if (hours >= 48) return `${Math.round(hours / 24)}d ago`
  if (hours >= 1.5) return `${Math.round(hours)}h ago`
  return `${Math.max(1, Math.round(span / 60000))}m ago`
})

// Bucket a feed's results into fixed time columns. Bucketing by TIME rather
// than by index is what makes the lanes comparable — feeds check on their own
// schedules, so column N must mean the same moment for all of them.
const laneFor = (ep) => {
  const start = windowStart.value
  const span = Math.max(1, now.value - start)
  const buckets = Array.from({ length: LANE_COLUMNS }, () => [])
  for (const r of ep.results || []) {
    const t = new Date(r.timestamp).getTime()
    let idx = Math.floor(((t - start) / span) * LANE_COLUMNS)
    if (idx < 0) idx = 0
    if (idx >= LANE_COLUMNS) idx = LANE_COLUMNS - 1
    buckets[idx].push(r)
  }
  return buckets.map((group) => {
    if (!group.length) return { state: 'empty', result: null }
    // A bucket is only "up" if nothing in it failed — never average away a blip.
    const bad = group.find((r) => !r.success)
    const chosen = bad || group[group.length - 1]
    return { state: stateOf(chosen), result: chosen }
  })
}

const feeds = computed(() => {
  const order = { 'WAN 1': 0, 'WAN 2': 1, 'Phones': 2 }
  return [...endpoints.value]
    .map((ep) => {
      const results = ep.results || []
      const latest = results.length ? results[results.length - 1] : null
      const role = roleOf(ep.group)
      const phones = isPhoneFeed(ep)
      const counts = phoneCounts.value[ep.key]
      const up = uptimes.value[ep.key]

      let primaryValue = '—'
      let primaryUnit = ''
      if (phones) {
        if (counts) {
          primaryValue = String(counts.online)
          primaryUnit = counts.monitored ? `/ ${counts.monitored} online` : 'online'
        }
      } else if (latest && latest.success && msOf(latest) !== null) {
        primaryValue = String(msOf(latest))
        primaryUnit = 'ms'
      } else if (latest) {
        primaryValue = 'Down'
      }

      return {
        key: ep.key,
        role,
        order: order[role] !== undefined ? order[role] : 9,
        carrier: carrierOf(ep.group) || (phones ? 'Wildix PBX' : ''),
        state: stateOf(latest),
        isPhones: phones,
        address: phones ? '' : (latest && latest.hostname) || '',
        primaryValue,
        primaryUnit,
        uptime24h: up && up['24h'] != null ? `${(up['24h'] * 100).toFixed(2)}%` : '—',
        lastCheck: latest ? generatePrettyTimeAgo(latest.timestamp, now.value) : 'Never',
        latest,
        lane: laneFor(ep),
        counts,
      }
    })
    .sort((a, b) => a.order - b.order || a.role.localeCompare(b.role))
})

const feedGridClass = computed(() => {
  const n = feeds.value.length
  if (n <= 1) return 'grid-cols-1'
  if (n === 2) return 'grid-cols-1 sm:grid-cols-2'
  return 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3'
})

const cellClass = (cell) => {
  switch (cell.state) {
    case 'up': return 'stbar-up'
    case 'down': return 'stbar-down'
    case 'none': return 'stbar-nodata'
    default: return 'lane-empty'
  }
}

// --- Verdict -------------------------------------------------------------
// Plain English first. The pill and the lanes are corroboration; this sentence
// is the answer.
const outageSince = (ep) => {
  const results = ep.results || []
  let since = null
  for (let i = results.length - 1; i >= 0; i--) {
    if (results[i].success) break
    since = results[i].timestamp
  }
  return since
}

const verdict = computed(() => {
  const list = feeds.value
  if (!list.length) return { headline: '', pillLabel: 'Unknown', pillClass: 'st-none', toneClass: 'tone-none' }

  const down = list.filter((f) => f.state === 'down')
  const quiet = list.filter((f) => f.state === 'none')
  const wansDown = down.filter((f) => f.role.startsWith('WAN'))
  const wanCount = list.filter((f) => f.role.startsWith('WAN')).length

  const durationFor = (feed) => {
    const ep = endpoints.value.find((e) => e.key === feed.key)
    const since = ep && outageSince(ep)
    return since ? generatePrettyTimeDifference(now.value, since) : null
  }

  if (!down.length && !quiet.length) {
    const phones = list.find((f) => f.isPhones && f.counts)
    return {
      headline: `Everything at ${siteName.value} is up.`,
      detail: phones ? `${phones.counts.online} of ${phones.counts.monitored} desk phones registered.` : '',
      pillLabel: 'Healthy',
      pillClass: 'st-up',
      toneClass: 'tone-up',
    }
  }

  if (wanCount > 1 && wansDown.length === wanCount) {
    const d = durationFor(wansDown[0])
    return {
      headline: `${siteName.value} is offline — both WAN circuits are down${d ? ` (${d})` : ''}.`,
      detail: 'Both carriers failing at once usually means power or the on-site edge, not the circuits.',
      pillLabel: 'Offline',
      pillClass: 'st-down',
      toneClass: 'tone-down',
    }
  }

  if (down.length) {
    const parts = down.map((f) => {
      const d = durationFor(f)
      return `${f.role}${d ? ` for ${d}` : ''}`
    })
    const okNames = list.filter((f) => f.state === 'up').map((f) => f.role)
    return {
      headline: `${parts.join(' and ')} ${down.length > 1 ? 'are' : 'is'} down.`,
      detail: okNames.length ? `${okNames.join(' and ')} still healthy.` : '',
      pillLabel: down.length === list.length ? 'Offline' : 'Degraded',
      pillClass: down.length === list.length ? 'st-down' : 'st-degraded',
      toneClass: down.length === list.length ? 'tone-down' : 'tone-degraded',
    }
  }

  // Only quiet feeds left — nothing failing, something simply isn't reporting.
  return {
    headline: `${quiet.map((f) => f.role).join(' and ')} ${quiet.length > 1 ? 'are' : 'is'} not reporting.`,
    detail: 'No data is arriving for that feed, so there is no health to judge.',
    pillLabel: 'No data',
    pillClass: 'st-none',
    toneClass: 'tone-none',
  }
})

// --- Scrubber ------------------------------------------------------------
const scrub = computed(() => {
  const i = hoverIndex.value
  if (i === null) {
    return {
      label: 'now',
      feeds: feeds.value.map((f) => ({ role: f.role, state: f.state })),
    }
  }
  const start = windowStart.value
  const span = Math.max(1, now.value - start)
  const at = start + (span * (i + 0.5)) / LANE_COLUMNS
  const clock = new Intl.DateTimeFormat('en-US', {
    timeZone: DISPLAY_TIMEZONE, hour: 'numeric', minute: '2-digit', hour12: true,
  }).format(new Date(at))
  return {
    label: clock,
    feeds: feeds.value.map((f) => ({ role: f.role, state: f.lane[i] ? f.lane[i].state : 'empty' })),
  }
})

// --- Merged event log ----------------------------------------------------
const timeline = computed(() => {
  const rows = []
  for (const ep of endpoints.value) {
    const role = roleOf(ep.group)
    const evts = ep.events || []
    evts.forEach((e, idx) => {
      const next = evts[idx + 1]
      let text
      if (e.type === 'START') text = `${role} monitoring started`
      else if (e.type === 'HEALTHY') text = `${role} recovered`
      else text = `${role} went down`
      rows.push({
        t: new Date(e.timestamp).getTime(),
        tone: e.type === 'HEALTHY' ? 'up' : e.type === 'UNHEALTHY' ? 'down' : 'start',
        text,
        clock: new Intl.DateTimeFormat('en-US', {
          timeZone: DISPLAY_TIMEZONE, month: 'short', day: 'numeric',
          hour: 'numeric', minute: '2-digit', hour12: true,
        }).format(new Date(e.timestamp)),
        ago: generatePrettyTimeAgo(e.timestamp, now.value),
        // How long the state that STARTED here lasted, which is the number a
        // tech actually reports ("we were down 12 minutes").
        lasted: e.type === 'UNHEALTHY'
          ? (next ? generatePrettyTimeDifference(next.timestamp, e.timestamp)
                  : `${generatePrettyTimeDifference(now.value, e.timestamp)} and counting`)
          : null,
      })
    })
  }
  return rows.sort((a, b) => b.t - a.t).slice(0, 12)
})

// --- Data ----------------------------------------------------------------
const fetchAll = async () => {
  loading.value = true
  try {
    const res = await fetch(`/api/v1/endpoints/statuses?page=1&pageSize=${RESULT_WINDOW}`, { credentials: 'include' })
    if (res.status !== 200) throw new Error(await res.text())
    const all = await res.json()
    const mine = (all || []).filter((ep) => ep.name === siteName.value)
    endpoints.value = mine

    await Promise.all(mine.map(async (ep) => {
      const up = await fetch(`/api/v1/endpoints/${encodeURIComponent(ep.key)}/uptimes/24h`, { credentials: 'include' })
        .then((r) => (r.ok ? r.text() : null)).catch(() => null)
      uptimes.value = { ...uptimes.value, [ep.key]: { '24h': up != null ? parseFloat(up) : null } }

      if (isPhoneFeed(ep)) {
        const inv = await fetch(`/api/v1/phones/${encodeURIComponent(ep.key)}`, { credentials: 'include' })
          .then((r) => (r.ok ? r.json() : null)).catch(() => null)
        if (inv && inv.counts) phoneCounts.value = { ...phoneCounts.value, [ep.key]: inv.counts }
      }
    }))
  } catch (err) {
    console.error('[SiteOverview] fetch failed:', err)
  } finally {
    loading.value = false
    loaded.value = true
  }
}

// Live stream keeps the head of each feed fresh. Only the newest result is
// merged in — replacing the whole array would shrink the loaded window back to
// whatever the broadcast carries.
let es = null
const connectLive = () => {
  try {
    es = new EventSource('/api/v1/live', { withCredentials: true })
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        const incoming = (data.endpoints || []).filter((ep) => ep.name === siteName.value)
        if (!incoming.length) return
        endpoints.value = endpoints.value.map((mine) => {
          const fresh = incoming.find((x) => x.key === mine.key)
          if (!fresh || !fresh.results || !fresh.results.length) return mine
          const newest = fresh.results[fresh.results.length - 1]
          const have = mine.results || []
          const last = have[have.length - 1]
          if (last && new Date(newest.timestamp) <= new Date(last.timestamp)) {
            return { ...mine, events: fresh.events || mine.events }
          }
          return {
            ...mine,
            events: fresh.events || mine.events,
            results: [...have, newest].slice(-RESULT_WINDOW),
          }
        })
      } catch (err) { /* ignore malformed frames */ }
    }
  } catch (err) { /* live is a bonus; the page works without it */ }
}

watch(siteName, () => {
  loaded.value = false
  endpoints.value = []
  fetchAll()
})

onMounted(() => {
  fetchAll()
  connectLive()
})

onUnmounted(() => {
  if (es) { es.close(); es = null }
})
</script>

<style scoped>
.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: hsl(var(--muted-foreground));
  font-weight: 600;
}

.status-pill {
  font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em;
  padding: 0.2rem 0.65rem; border-radius: 6px; color: #fff;
}
.status-pill.st-up { background: var(--status-up); }
.status-pill.st-degraded { background: var(--status-degraded); }
.status-pill.st-down { background: var(--status-down); }
.status-pill.st-none { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }

.empty-state {
  border: 1px dashed hsl(var(--border));
  border-radius: 10px; padding: 2.5rem 1.5rem; text-align: center;
}

/* --- Verdict ---------------------------------------------------------
   A rule on the leading edge in the status colour carries the tone, so the
   sentence itself can stay plain and legible. */
.verdict {
  border: 1px solid hsl(var(--border));
  border-left-width: 3px;
  border-radius: 10px;
  padding: 0.85rem 1.1rem;
  background: hsl(var(--card));
}
.verdict-line { font-size: 1.05rem; font-weight: 600; line-height: 1.35; }
.verdict-detail { font-size: 0.85rem; color: hsl(var(--muted-foreground)); margin-top: 0.2rem; }
.tone-up { border-left-color: var(--status-up); }
.tone-degraded { border-left-color: var(--status-degraded); }
.tone-down { border-left-color: var(--status-down); }
.tone-none { border-left-color: hsl(var(--muted-foreground) / 0.5); }

/* --- Feed strips ------------------------------------------------------ */
.feed-strip {
  display: block; position: relative;
  border: 1px solid hsl(var(--border));
  border-radius: 10px;
  padding: 0.85rem 1rem 0.9rem;
  background: hsl(var(--card));
  transition: border-color var(--dur-2, 180ms) ease, transform var(--dur-2, 180ms) ease, box-shadow var(--dur-2, 180ms) ease;
}
.feed-strip:hover { transform: translateY(-1px); box-shadow: 0 6px 18px -10px rgb(0 0 0 / 0.35); }
.feed-strip.is-up:hover { border-color: var(--status-up); }
.feed-strip.is-down { border-color: color-mix(in srgb, var(--status-down) 45%, hsl(var(--border))); }
.feed-strip:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }

.feed-carrier { font-size: 0.9rem; font-weight: 600; margin-top: 0.1rem; }

.feed-metric { display: flex; align-items: baseline; gap: 0.3rem; margin: 0.55rem 0 0.6rem; }
.feed-value {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 1.9rem; font-weight: 700; line-height: 1; letter-spacing: -0.02em;
  font-variant-numeric: tabular-nums;
}
.feed-unit { font-size: 0.75rem; color: hsl(var(--muted-foreground)); }
.is-down .feed-value { color: var(--status-down); }

.feed-facts { display: grid; gap: 0.25rem; font-size: 0.75rem; }
.feed-facts > div { display: flex; align-items: baseline; justify-content: space-between; gap: 0.75rem; }
.feed-facts dt { color: hsl(var(--muted-foreground)); }
.feed-facts dd { font-weight: 600; text-align: right; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.feed-go {
  display: inline-flex; align-items: center; gap: 0.15rem;
  font-size: 0.7rem; color: hsl(var(--muted-foreground));
  margin-top: 0.6rem; opacity: 0; transition: opacity var(--dur-2, 180ms) ease;
}
.group:hover .feed-go { opacity: 1; }

/* Status lamp */
.lamp { width: 9px; height: 9px; border-radius: 999px; display: inline-block; flex: none; }
.lamp-sm { width: 7px; height: 7px; }
.lamp-up { background: var(--status-up); box-shadow: 0 0 7px -1px var(--status-up); }
.lamp-down { background: var(--status-down); box-shadow: 0 0 7px -1px var(--status-down); }
.lamp-none, .lamp-empty { background: hsl(var(--muted-foreground) / 0.45); }

/* --- Panels ----------------------------------------------------------- */
.panel {
  border: 1px solid hsl(var(--border));
  border-radius: 10px;
  padding: 0.9rem 1.1rem 1rem;
  background: hsl(var(--card));
}
.panel-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; flex-wrap: wrap; margin-bottom: 0.75rem; }
.panel-sub { font-size: 0.78rem; color: hsl(var(--muted-foreground)); margin-top: 0.15rem; }

/* Scrubber readout — one shared line instead of a floating tooltip, so the
   comparison across feeds stays visible while the pointer moves. */
.scrub-readout { display: flex; align-items: center; gap: 0.75rem; flex-wrap: wrap; }
.scrub-time { font-size: 0.8rem; font-weight: 700; min-width: 4.5rem; }
.scrub-readout.live .scrub-time { color: hsl(var(--muted-foreground)); }
.scrub-feeds { display: inline-flex; align-items: center; gap: 0.7rem; }
.scrub-feed { display: inline-flex; align-items: center; gap: 0.3rem; font-size: 0.72rem; color: hsl(var(--muted-foreground)); }

/* --- Lanes ------------------------------------------------------------ */
.lanes { display: grid; gap: 0.3rem; }
.lane { display: flex; align-items: center; gap: 0.6rem; }
.lane-label {
  width: 4.2rem; flex: none; text-align: right;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px; letter-spacing: 0.08em; text-transform: uppercase;
  color: hsl(var(--muted-foreground));
}
.lane-track { flex: 1; display: flex; gap: 1px; min-width: 0; }
.lane-cell {
  flex: 1 1 0; height: 18px; border-radius: 1px; min-width: 0;
  transition: opacity 120ms ease, transform 120ms ease;
}
.lane-cell.lane-empty { background: hsl(var(--muted-foreground) / 0.12); }
/* The hovered column lifts across every lane at once — that shared highlight
   is the whole point of stacking them. */
.lane-cell.hot { transform: scaleY(1.25); }
.lanes:hover .lane-cell:not(.hot) { opacity: 0.55; }

.axis { margin-top: 0.15rem; }
.axis-track {
  justify-content: space-between;
  font-size: 10px; color: hsl(var(--muted-foreground)); letter-spacing: 0.06em;
}

/* --- Log -------------------------------------------------------------- */
.log { display: grid; gap: 0.15rem; }
.log-row {
  display: flex; align-items: center; gap: 0.7rem;
  padding: 0.45rem 0.2rem;
  border-bottom: 1px solid hsl(var(--border) / 0.6);
}
.log-row:last-child { border-bottom: 0; }
.log-mark { flex: none; display: inline-flex; }
.mark-up { color: var(--status-up); }
.mark-down { color: var(--status-down); }
.mark-start { color: hsl(var(--muted-foreground)); }
.log-body { display: flex; flex-direction: column; min-width: 0; flex: 1; }
.log-text { font-size: 0.85rem; font-weight: 500; }
.log-meta { font-size: 0.7rem; color: hsl(var(--muted-foreground)); }
.log-lasted {
  flex: none; font-size: 0.72rem; color: hsl(var(--muted-foreground));
  background: hsl(var(--muted) / 0.6); padding: 0.15rem 0.45rem; border-radius: 5px;
}

@media (max-width: 640px) {
  .lane-label { width: 3.2rem; }
  .lane-cell { height: 14px; }
}

@media (prefers-reduced-motion: reduce) {
  .feed-strip, .lane-cell { transition: none; }
  .feed-strip:hover { transform: none; }
  .lane-cell.hot { transform: none; outline: 1px solid hsl(var(--foreground)); }
}
</style>
