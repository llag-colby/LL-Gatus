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
          <CardSettingsMenu :name="siteName" :rows="settingsRows" />
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
        <!-- One filter row for the whole page. Everything that measures history
             reads it: the uptime on each feed card, the timeline, the charts. -->
        <div class="filter-row">
          <div class="min-w-0">
            <div class="eyebrow">Time range</div>
            <p class="filter-note">
              Scopes the feed uptime, the timeline and the uptime charts. The verdict and the status pill always
              describe current state.
            </p>
          </div>
          <RangeSelector v-model="range" label="Site history range" />
        </div>

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
            :class="[`is-${feed.state}`, { 'is-paused': feed.paused }]"
          >
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="eyebrow truncate">{{ feed.role }}</div>
                <div class="feed-carrier truncate">{{ feed.carrier || '—' }}</div>
              </div>
              <!-- A paused feed gets a word, not a lamp: its last known colour is
                   stale by definition, so showing it would read as live status. -->
              <span v-if="feed.paused" class="feed-paused-tag">Paused</span>
              <span v-else class="lamp" :class="`lamp-${feed.state}`"></span>
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
                <dt>{{ feed.uptimeLabel }}</dt>
                <dd class="font-mono">{{ feed.uptimeValue }}</dd>
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
            <div class="min-w-0">
              <div class="eyebrow">Correlated timeline</div>
              <p class="panel-sub">Same time axis for every feed. Read down a column to see what failed together.</p>
              <p class="panel-note">{{ columnNote }}</p>
            </div>
            <div class="scrub-readout" :class="{ live: hoverIndex === null }">
              <span class="scrub-time font-mono">{{ scrub.label }}</span>
              <span class="scrub-feeds">
                <span v-for="f in scrub.feeds" :key="f.role" class="scrub-feed"
                  :class="{ 'is-paused': f.paused }">
                  <span class="lamp lamp-sm" :class="`lamp-${f.state}`"></span>{{ f.role }}<span
                    v-if="f.detail" class="scrub-detail font-mono">{{ f.detail }}</span>
                </span>
              </span>
            </div>
          </div>

          <p v-if="!laneColumns" class="panel-empty">
            {{ loading ? 'Loading uptime history.' : `No history recorded for this site in the last ${range}.` }}
          </p>
          <div v-else class="lanes" @mouseleave="hoverIndex = null">
            <!-- Paused lanes stay in place, dimmed: the history behind them is
                 real, and hiding the row would hide the fact that it's paused. -->
            <div v-for="feed in feeds" :key="feed.key" class="lane" :class="{ 'is-paused': feed.paused }">
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

        <!-- Uptime per feed, stacked rather than side by side. Every chart is the
             same width, holds the same bucket timestamps and sits in the same feed
             order as the lanes, so column N is at the same screen position in all
             of them: reading straight down still answers "what failed together",
             which a row of narrow small multiples side by side would break. -->
        <div class="panel">
          <div class="panel-head">
            <div class="min-w-0">
              <div class="eyebrow">Uptime by feed</div>
              <p class="panel-sub">Recorded uptime per bucket over the last {{ range }}, one chart per feed.</p>
              <p class="panel-note">{{ resolutionSentence }}</p>
            </div>
            <span class="res-tag">{{ resolutionTag }}</span>
          </div>

          <p v-if="seriesError" class="panel-error">
            Uptime history could not be loaded for every feed. Any chart below without data is missing, not empty.
          </p>

          <div class="charts">
            <HistoryChart
              v-for="feed in feeds"
              :key="feed.key"
              :series="feed.series"
              :title="feed.role"
              :subtitle="feed.carrier"
              kind="column"
              ratio
              :height="110"
              :warn-below="UPTIME_WARN_BELOW"
              :down-below="UPTIME_DOWN_BELOW"
              :loading="loading"
              :empty-text="`No uptime recorded for ${feed.role} in this range.`"
            />
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
                <AlertTriangle v-else-if="entry.tone === 'degraded'" class="h-4 w-4" />
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
import { ArrowLeft, RefreshCw, ChevronRight, ArrowUpCircle, ArrowDownCircle, AlertTriangle, PlayCircle } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import Loading from '@/components/Loading.vue'
import Settings from '@/components/Settings.vue'
import CardSettingsMenu from '@/components/CardSettingsMenu.vue'
import RangeSelector from '@/components/RangeSelector.vue'
import HistoryChart from '@/components/HistoryChart.vue'
import { now, isMonitored, historyRange, setHistoryRange, historyRangeMs } from '@/store'
import { generatePrettyTimeAgo, generatePrettyTimeDifference, DISPLAY_TIMEZONE } from '@/utils/time'

const route = useRoute()

// --- What the range controls --------------------------------------------
//
// THE CROSSOVER. Raw check results are capped per endpoint by the storage
// setting maximum-number-of-results (3000 here), which at the 60s interval every
// endpoint uses is a little over two days. So 24h is the longest range raw
// results cover end to end, and it is where the lanes stop reading them: drawing
// 7d from raw results would fill the two most recent days and leave five blank,
// which reads as a five day outage rather than as history we never kept. Above
// the crossover the lanes read the stored uptime buckets instead, which go back
// as far as the retention of the uptime table.
//
// Raw stays in charge below the crossover because it is strictly more precise:
// an uptime bucket is at best one hour wide, so a 24h view built from buckets
// would be 24 columns and would hide a two minute drop entirely, while the raw
// lanes put it in a 15 minute column and colour it.
const BUCKETED_RANGES = ['7d', '30d']

// Columns per range, so one column is always a round unit of time: 1 minute at
// 1h, 5 minutes at 6h, 15 minutes at 24h. A fixed 96 everywhere is what made the
// old lanes misleading, since 96 columns across 30d would be 7.5 hours each and
// would look like precision the data does not have. Above the crossover the
// column count is not set here at all: it is however many buckets the API
// returned, so a column is exactly one recorded bucket and never a resample.
const RAW_COLUMNS = { '1h': 60, '6h': 72, '24h': 96 }

// How many raw results to load per feed. Sized to the range at a 60s interval
// with headroom, and flat above the crossover, where raw results no longer draw
// the lanes and are only still wanted for the current state and the event log.
const RAW_RESULTS = { '1h': 150, '6h': 600, '24h': 1600, '7d': 1600, '30d': 1600 }

// The bulk call runs across every endpoint in the fleet, so it stays shallow: it
// is only asked which endpoints carry this site's name. The per-feed calls load
// the depth the selected range needs, for this site alone.
const DISCOVERY_RESULTS = 60

// Thresholds for turning a bucket's uptime ratio into the page's state
// vocabulary. Anything short of a clean bucket reads degraded, on the same
// principle as the raw lanes taking the worst check in a column: a bucket is
// never averaged into looking healthy. Below half the bucket was down more than
// it was up, which is an outage, not a blip.
const UPTIME_DOWN_BELOW = 0.5
const UPTIME_WARN_BELOW = 0.9999

// Reading order for a site's feeds: the circuits that carry everything, then the
// on-site gear, then the phones riding on top. One map so the feed strips, the
// lanes, the charts and the settings menu all list the same feeds in the same
// sequence — two lists in different orders is how you toggle the wrong switch.
const FEED_ORDER = { 'WAN 1': 0, 'WAN 2': 1, 'Firewall': 2, 'Wireless': 3, 'Phones': 4, 'DNS': 5 }

// Worst-first, so collapsing a group of feeds keeps the one you need to see.
const STATE_RANK = { down: 0, none: 1, degraded: 2, up: 3 }

// DNS arrives as one endpoint per resolver, because a DNS check queries exactly
// one server. The card already draws it as a single sliced row, so this page
// has to agree: otherwise the same thing is one row on the dashboard and three
// strips here. The merged strip reports how many resolvers are answering and
// carries the worst one's lane, uptime and drill-in link, so clicking it lands
// on the resolver actually in trouble.
const condenseDns = (list) => {
  const dns = list.filter((feed) => feed.role === 'DNS')
  if (dns.length < 2) return list
  const rest = list.filter((feed) => feed.role !== 'DNS')
  const worst = dns.reduce((acc, feed) =>
    ((STATE_RANK[feed.state] ?? 9) < (STATE_RANK[acc.state] ?? 9) ? feed : acc))
  const up = dns.filter((feed) => feed.state === 'up').length
  const merged = {
    ...worst,
    role: 'DNS',
    carrier: `${dns.length} resolvers`,
    primaryValue: `${up}/${dns.length}`,
    primaryUnit: 'resolving',
    // Three resolvers have three addresses; none of them belongs on one line.
    address: '',
    paused: dns.every((feed) => feed.paused),
  }
  return [...rest, merged].sort((a, b) => a.order - b.order || a.role.localeCompare(b.role))
}
const orderOf = (role) => (FEED_ORDER[role] !== undefined ? FEED_ORDER[role] : 9)

const siteName = computed(() => decodeURIComponent(route.params.name || ''))

const endpoints = ref([])
const uptimeSeries = ref({}) // key -> uptime-series payload for the selected range
const phoneCounts = ref({})  // key -> counts object from the phones inventory
const seriesError = ref(false)
const loaded = ref(false)
const loading = ref(false)
const hoverIndex = ref(null)

// The shared, persisted selection. Written through the store's setter so the
// choice follows you to the endpoint and phone pages rather than resetting.
const range = computed({
  get: () => historyRange.value,
  set: (value) => setHistoryRange(value),
})

const bucketed = computed(() => BUCKETED_RANGES.indexOf(historyRange.value) !== -1)
const rangeMs = computed(() => historyRangeMs(historyRange.value))
const rawColumns = computed(() => RAW_COLUMNS[historyRange.value] || 96)
const rawResults = computed(() => RAW_RESULTS[historyRange.value] || 1600)

// --- Feed classification -------------------------------------------------
// Same rules the dashboard card uses, so a site reads identically in both
// places: the group string carries both the role and the carrier.
// Same vocabulary as LocationCard's classify(), so a feed is named the same
// thing on the card and in here.
const roleOf = (group) => {
  const g = (group || '').toLowerCase()
  if (/wan\s*1|primary/.test(g)) return 'WAN 1'
  if (/wan\s*2|backup|secondary/.test(g)) return 'WAN 2'
  if (/firewall|gateway|edge/.test(g)) return 'Firewall'
  if (/wireless|wi-?fi|wlan|access\s*point/.test(g)) return 'Wireless'
  if (/phone|voip|sip/.test(g)) return 'Phones'
  if (/^dns\b/.test(g)) return 'DNS'
  return group || 'Feed'
}
const carrierOf = (group) => {
  const m = (group || '').match(/\(([^)]+)\)/)
  return m ? m[1].trim() : ''
}
const isPhoneFeed = (ep) => (ep.key || '').startsWith('phones_') || /phone|voip|sip/i.test(ep.group || '')

// A failing check that carries this marker means nothing reported at all,
// which is not the same as reporting a failure. Matches LocationCard.
const NOT_REPORTING = /^no (phones|unifi|smb) reporting\b/i
const isNotReporting = (r) =>
  !!r && !r.success && (r.errors || []).some((e) => NOT_REPORTING.test(e))

// A collector that reports three states can only push a bool through an external
// endpoint, so it sends DEGRADED as a pass carrying its reason in errors[] —
// that way a partial problem doesn't fire a down alert. A successful result with
// errors therefore means "passed, with a warning" (1 of 2 WAN uplinks down, 4 of
// 23 APs offline) and reads amber. Same rule as LocationCard's isWarning(), so a
// feed is never green here while the dashboard card paints it amber.
const isWarning = (r) => !!r && r.success && (r.errors || []).length > 0

// The first error on a warning is the collector's own sentence, written for a
// human — "4 of 23 access points offline (Hightower Bldg 2 AP2, …)". Pass it
// through verbatim; anything we synthesised would say less.
const warningTextOf = (r) => (isWarning(r) ? (r.errors || [])[0] : '')

// Four states, in precedence order: down beats degraded beats up, and 'none'
// means nothing reported at all rather than anything about health.
const stateOf = (result) => {
  if (!result) return 'none'
  if (result.success) return isWarning(result) ? 'degraded' : 'up'
  return isNotReporting(result) ? 'none' : 'down'
}

// The bucketed equivalent, deliberately using the same four words so a lane
// means the same thing whichever source drew it.
const uptimeStateOf = (value) => {
  if (value === null || value === undefined) return 'none'
  if (value < UPTIME_DOWN_BELOW) return 'down'
  if (value < UPTIME_WARN_BELOW) return 'degraded'
  return 'up'
}

const msOf = (r) => (r && r.duration ? Math.round(r.duration / 1000000) : null)

// --- Time window ---------------------------------------------------------
// The window is now a duration the reader picked, not a count of checks that
// happened to be loaded.
const windowStart = computed(() => now.value - rangeMs.value)

const dateLabel = (ms) => new Intl.DateTimeFormat('en-US', {
  timeZone: DISPLAY_TIMEZONE, month: 'short', day: 'numeric',
}).format(new Date(ms))

const windowLabel = computed(() => {
  // Above the crossover the axis starts at the first bucket the store actually
  // holds, which can be later than the range asked for on a young endpoint, so
  // the label names that date rather than claiming a full 30d.
  if (bucketed.value && bucketAxis.value.length) return dateLabel(bucketAxis.value[0])
  return `${historyRange.value} ago`
})

// --- Uptime series -------------------------------------------------------
// A payload is only used while it matches the current selection. The API echoes
// the range it answered, so a response that lands after the reader has moved on
// is ignored instead of being drawn under the wrong axis.
const seriesOf = (key) => {
  const s = uptimeSeries.value[key]
  return s && s.range === historyRange.value ? s : null
}

// One axis for every feed, built from the union of the bucket timestamps that
// came back. A feed that started reporting late, or that missed a bucket, then
// still lines up with the rest instead of shifting what column N means in its
// own lane, which is the whole basis of reading down a column.
const bucketAxis = computed(() => {
  const seen = new Set()
  for (const ep of endpoints.value) {
    const s = seriesOf(ep.key)
    if (!s) continue
    for (const t of s.timestamps || []) seen.add(t)
  }
  return [...seen].sort((a, b) => a - b)
})

// Both the lane and the chart for one feed, projected onto the shared axis in a
// single pass. A bucket the feed has no entry for is a hole in that feed's
// record ('empty'); a bucket it reported with no executions is the API's null,
// which is a gap in collection ('none'). Neither is painted as an outage.
const alignedFor = (key) => {
  const s = seriesOf(key)
  const byTime = new Map()
  if (s) (s.timestamps || []).forEach((t, i) => byTime.set(t, (s.values || [])[i]))
  const values = []
  const lane = []
  for (const t of bucketAxis.value) {
    if (!byTime.has(t)) {
      values.push(null)
      lane.push({ state: 'empty', ratio: null, at: t })
      continue
    }
    const raw = byTime.get(t)
    const value = raw === null || raw === undefined ? null : raw
    values.push(value)
    lane.push({ state: uptimeStateOf(value), ratio: value, at: t })
  }
  return { series: { timestamps: bucketAxis.value, values }, lane }
}

// Uptime across the whole selected range, weighted by how many checks each
// bucket held. Averaging the ratios instead would let a bucket with four checks
// count as much as one with sixty.
const rangeUptime = (key) => {
  const s = seriesOf(key)
  if (!s) return null
  const values = s.values || []
  const executions = s.executions || []
  let successful = 0
  let total = 0
  values.forEach((v, i) => {
    const count = executions[i]
    if (v === null || v === undefined || !count) return
    successful += v * count
    total += count
  })
  return total > 0 ? successful / total : null
}

// What the store can still tell us about resolution, collapsed across the feeds.
const resolution = computed(() => {
  let hourly = false
  let daily = false
  for (const ep of endpoints.value) {
    const s = seriesOf(ep.key)
    if (!s) continue
    if (s.resolution === 'mixed') { hourly = true; daily = true }
    else if (s.resolution === 'day') daily = true
    else hourly = true
  }
  if (hourly && daily) return 'mixed'
  if (daily) return 'day'
  return 'hour'
})

const resolutionTag = computed(() => {
  if (resolution.value === 'mixed') return 'Hourly and daily'
  if (resolution.value === 'day') return 'Daily'
  return 'Hourly'
})

// Say plainly what the columns are. Gatus merges hourly uptime entries into
// daily ones after roughly 48 hours, so a 7d or 30d view is mostly one column
// per day and there is no hourly detail left behind it to drill into. Claiming
// otherwise would be the easiest lie on this page.
const resolutionSentence = computed(() => {
  if (resolution.value === 'mixed') {
    return 'One column per hour for roughly the last 48 hours, one column per day before that: Gatus merges older hourly uptime into daily entries, so the hourly detail no longer exists.'
  }
  if (resolution.value === 'day') {
    return 'One column per day. Gatus merged the hourly uptime for this range into daily entries, so there is no hourly detail left to show.'
  }
  return 'One column per hour of recorded uptime.'
})

const columnNote = computed(() => {
  if (bucketed.value) {
    return `Each column is one recorded uptime bucket, coloured by that bucket's uptime. ${resolutionSentence.value}`
  }
  const minutes = Math.max(1, Math.round(rangeMs.value / rawColumns.value / 60000))
  return `Each column is ${minutes} ${minutes === 1 ? 'minute' : 'minutes'} of checks, and takes the worst check it holds.`
})

// Bucket a feed's raw results into fixed time columns. Bucketing by TIME rather
// than by index is what makes the lanes comparable — feeds check on their own
// schedules, so column N must mean the same moment for all of them.
const rawLaneFor = (ep) => {
  const start = windowStart.value
  const span = Math.max(1, now.value - start)
  const columns = rawColumns.value
  const buckets = Array.from({ length: columns }, () => [])
  for (const r of ep.results || []) {
    const t = new Date(r.timestamp).getTime()
    // Anything older than the selected window is dropped, not clamped into the
    // first column. The loaded depth can reach past the range, and piling hours
    // of history into one cell would invent an event that never happened.
    if (t < start) continue
    let idx = Math.floor(((t - start) / span) * columns)
    if (idx < 0) idx = 0
    if (idx >= columns) idx = columns - 1
    buckets[idx].push(r)
  }
  return buckets.map((group) => {
    if (!group.length) return { state: 'empty', ratio: null }
    // A bucket takes the worst check it holds — never average away a blip. Same
    // precedence as the site verdict: a hard failure wins, then a warning, and
    // only a column of clean passes paints green.
    const chosen = group.find((r) => !r.success)
      || group.find(isWarning)
      || group[group.length - 1]
    return { state: stateOf(chosen), ratio: null, result: chosen }
  })
}

const laneColumns = computed(() => (bucketed.value ? bucketAxis.value.length : rawColumns.value))

const feeds = computed(() => {
  const useBuckets = bucketed.value
  const built = [...endpoints.value]
    .map((ep) => {
      const results = ep.results || []
      const latest = results.length ? results[results.length - 1] : null
      const role = roleOf(ep.group)
      const phones = isPhoneFeed(ep)
      const counts = phoneCounts.value[ep.key]
      const aligned = alignedFor(ep.key)
      const uptime = rangeUptime(ep.key)

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
        order: orderOf(role),
        carrier: carrierOf(ep.group) || (phones ? 'Wildix PBX' : ''),
        // Always the newest check, whatever the range is set to. See the verdict.
        state: stateOf(latest),
        // Empty unless the latest check passed with a warning. The verdict prints
        // it, so the reason lives on the feed rather than being re-derived there.
        warning: warningTextOf(latest),
        // Paused feeds keep every number they already had — they just stop
        // counting towards the site's verdict. See the verdict computed.
        paused: !isMonitored(ep.key),
        isPhones: phones,
        address: phones ? '' : (latest && latest.hostname) || '',
        primaryValue,
        primaryUnit,
        uptimeLabel: `Uptime ${historyRange.value}`,
        uptimeValue: uptime === null ? '—' : `${(uptime * 100).toFixed(2)}%`,
        lastCheck: latest ? generatePrettyTimeAgo(latest.timestamp, now.value) : 'Never',
        latest,
        lane: useBuckets ? aligned.lane : rawLaneFor(ep),
        series: aligned.series,
        counts,
      }
    })
    .sort((a, b) => a.order - b.order || a.role.localeCompare(b.role))
  return condenseDns(built)
})

// One pause switch per endpoint at this site, in feed order. Built from the raw
// endpoints rather than from feeds.value so the menu never depends on the
// display shaping — a feed you can see is a feed you can pause.
const settingsRows = computed(() => {
  const rows = []
  const dnsKeys = []
  for (const ep of endpoints.value) {
    const label = roleOf(ep.group)
    // DNS is one switch driving every resolver, matching its single strip.
    if (label === 'DNS') {
      dnsKeys.push(ep.key)
      continue
    }
    rows.push({ key: ep.key, label, endpointKey: ep.key })
  }
  if (dnsKeys.length === 1) {
    rows.push({ key: dnsKeys[0], label: 'DNS', endpointKey: dnsKeys[0] })
  } else if (dnsKeys.length > 1) {
    rows.push({ key: 'dns', label: 'DNS', endpointKeys: dnsKeys })
  }
  return rows.sort((a, b) => orderOf(a.label) - orderOf(b.label) || a.label.localeCompare(b.label))
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
    case 'degraded': return 'stbar-degraded'
    case 'down': return 'stbar-down'
    case 'none': return 'stbar-nodata'
    default: return 'lane-empty'
  }
}

// --- Verdict -------------------------------------------------------------
// Plain English first. The pill and the lanes are corroboration; this sentence
// is the answer.
//
// DELIBERATELY NOT RANGE SCOPED, and please leave it that way. Every branch below
// reads feed.state, which is the newest check, so the verdict and the pill answer
// "what is this site doing right now". Widening the range to 30d must never make
// a healthy site read as offline because it was offline last week: that reading
// belongs to the timeline and the charts, which is exactly where the range sends
// it. A site that is up and has been rough all month reads Healthy here, with a
// month of amber underneath it.
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

  // EVERY question below is asked of `live` only. A paused feed is one we chose
  // to stop watching, so letting it push the site to degraded or offline would
  // defeat the point of pausing it — and would keep a known-dead circuit
  // shouting for weeks. Its strip and its lane stay on the page; it simply has
  // no vote here.
  const paused = list.filter((f) => f.paused)
  const live = list.filter((f) => !f.paused)

  // Nothing left to judge. Neither "healthy" nor "offline" is true — we have no
  // signal at all — so say exactly that rather than guessing a colour.
  if (!live.length) {
    return {
      headline: `Monitoring is paused for ${siteName.value}.`,
      detail: `All ${list.length} ${list.length === 1 ? 'feed' : 'feeds'} are paused, so there is no health to report. Resume one from the settings menu to start judging this site again.`,
      pillLabel: 'Paused',
      pillClass: 'st-paused',
      toneClass: 'tone-paused',
    }
  }

  const down = live.filter((f) => f.state === 'down')
  const warned = live.filter((f) => f.state === 'degraded')
  const quiet = live.filter((f) => f.state === 'none')
  const wansDown = down.filter((f) => f.role.startsWith('WAN'))
  const wanCount = live.filter((f) => f.role.startsWith('WAN')).length

  const durationFor = (feed) => {
    const ep = endpoints.value.find((e) => e.key === feed.key)
    const since = ep && outageSince(ep)
    return since ? generatePrettyTimeDifference(now.value, since) : null
  }

  // Appended to whatever the verdict says, so the reader knows the sentence was
  // reached with some feeds sitting out.
  const pausedNote = paused.length
    ? `Monitoring is paused for ${paused.map((f) => f.role).join(' and ')}, so ${paused.length > 1 ? 'they are' : 'it is'} not counted here.`
    : ''
  const withPaused = (detail) => [detail, pausedNote].filter(Boolean).join(' ')

  // The collector's own words for each warning, e.g. "Firewall: 1 of 2 WAN
  // uplinks down". Printed alongside a harder verdict as well as on its own: the
  // down feed is what you fix first, but the warning is why the second call
  // happens, and burying it is the bug this page had.
  const warnNote = warned.length
    ? `${warned.map((f) => `${f.role}: ${f.warning}`).join(' · ')}.`
    : ''

  if (!down.length && !warned.length && !quiet.length) {
    const phones = live.find((f) => f.isPhones && f.counts)
    return {
      headline: paused.length
        ? `Everything still monitored at ${siteName.value} is up.`
        : `Everything at ${siteName.value} is up.`,
      detail: withPaused(phones ? `${phones.counts.online} of ${phones.counts.monitored} desk phones registered.` : ''),
      pillLabel: 'Healthy',
      pillClass: 'st-up',
      toneClass: 'tone-up',
    }
  }

  if (wanCount > 1 && wansDown.length === wanCount) {
    const d = durationFor(wansDown[0])
    return {
      headline: `${siteName.value} is offline — both WAN circuits are down${d ? ` (${d})` : ''}.`,
      detail: withPaused(['Both carriers failing at once usually means power or the on-site edge, not the circuits.', warnNote].filter(Boolean).join(' ')),
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
    const okNames = live.filter((f) => f.state === 'up').map((f) => f.role)
    // "Offline" means everything we are still watching is down — a paused feed
    // is not evidence either way, so it never tips this into the harsher label.
    const allDown = down.length === live.length
    return {
      headline: `${parts.join(' and ')} ${down.length > 1 ? 'are' : 'is'} down.`,
      detail: withPaused([
        okNames.length ? `${okNames.join(' and ')} still healthy.` : '',
        warnNote,
      ].filter(Boolean).join(' ')),
      pillLabel: allDown ? 'Offline' : 'Degraded',
      pillClass: allDown ? 'st-down' : 'st-degraded',
      toneClass: allDown ? 'tone-down' : 'tone-degraded',
    }
  }

  // Nothing is down, but something passed while telling us why it shouldn't have.
  // This branch is the whole point of the tri-state: without it the page said
  // "everything is up" while the dashboard card next to it read Degraded. Ranked
  // below `down` and above `quiet` — a collector saying "4 of 23 access points
  // offline" is more actionable than another feed's silence, and the silence
  // still gets a sentence of its own below.
  if (warned.length) {
    const roles = warned.map((f) => f.role).join(' and ')
    const quietNote = quiet.length
      ? `${quiet.map((f) => f.role).join(' and ')} ${quiet.length > 1 ? 'are' : 'is'} not reporting.`
      : ''
    return {
      headline: `${siteName.value} is degraded — ${roles} ${warned.length > 1 ? 'report' : 'reports'} a problem.`,
      // The reason, not a restatement of the headline: it names the uplink or the
      // access points so the next step is a call, not another click.
      detail: withPaused([warnNote, quietNote].filter(Boolean).join(' ')),
      pillLabel: 'Degraded',
      pillClass: 'st-degraded',
      toneClass: 'tone-degraded',
    }
  }

  // Only quiet feeds left — nothing failing, something simply isn't reporting.
  return {
    headline: `${quiet.map((f) => f.role).join(' and ')} ${quiet.length > 1 ? 'are' : 'is'} not reporting.`,
    detail: withPaused('No data is arriving for that feed, so there is no health to judge.'),
    pillLabel: 'No data',
    pillClass: 'st-none',
    toneClass: 'tone-none',
  }
})

// --- Scrubber ------------------------------------------------------------
// One readout serving both lane sources, so hovering a column reads the same way
// at 1h and at 30d: the time on the left, then every feed's state at that
// moment. Above the crossover it also carries the bucket's uptime, because a
// column there covers an hour or a day and "amber" alone would not say whether
// that was two minutes or ten hours.

// Hourly and daily buckets are told apart the same way the API does it, by
// whether the bucket starts at midnight, so a daily column is labelled with its
// date and an hourly one with its hour rather than a pointless "12 AM".
const startsAtMidnight = (ms) => {
  try {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: DISPLAY_TIMEZONE, hour: 'numeric', minute: '2-digit', hourCycle: 'h23',
    }).formatToParts(new Date(ms))
    const valueOf = (type) => {
      const part = parts.find((p) => p.type === type)
      return part ? Number(part.value) : -1
    }
    return valueOf('hour') === 0 && valueOf('minute') === 0
  } catch (e) {
    return false
  }
}

const scrubClock = (ms) => {
  const options = { timeZone: DISPLAY_TIMEZONE }
  if (bucketed.value && startsAtMidnight(ms)) {
    options.month = 'short'
    options.day = 'numeric'
  } else if (bucketed.value) {
    options.month = 'short'
    options.day = 'numeric'
    options.hour = 'numeric'
    options.hour12 = true
  } else {
    if (rangeMs.value > 6 * 3600000) {
      options.month = 'short'
      options.day = 'numeric'
    }
    options.hour = 'numeric'
    options.minute = '2-digit'
    options.hour12 = true
  }
  return new Intl.DateTimeFormat('en-US', options).format(new Date(ms))
}

const scrub = computed(() => {
  const i = hoverIndex.value
  if (i === null) {
    return {
      label: 'now',
      feeds: feeds.value.map((f) => ({ role: f.role, state: f.state, paused: f.paused, detail: '' })),
    }
  }
  // Both modes resolve the hovered column to a real moment, which is what keeps
  // the readout and the correlation working identically either side of the
  // crossover: bucketed columns carry their own timestamp, raw columns are an
  // even slice of the selected window.
  const at = bucketed.value
    ? bucketAxis.value[i]
    : windowStart.value + (rangeMs.value * (i + 0.5)) / rawColumns.value
  return {
    label: at ? scrubClock(at) : 'now',
    feeds: feeds.value.map((f) => {
      const cell = f.lane[i]
      const ratio = cell && cell.ratio !== null && cell.ratio !== undefined ? cell.ratio : null
      return {
        role: f.role,
        state: cell ? cell.state : 'empty',
        paused: f.paused,
        detail: ratio === null ? '' : `${(ratio * 100).toFixed(ratio >= 0.9995 ? 0 : 1)}%`,
      }
    }),
  }
})

// --- Merged event log ----------------------------------------------------
// Deliberately includes paused feeds. Pausing changes what we watch from now on;
// it does not unhappen the outage we recorded last Tuesday, and a tech reading
// the history needs that outage. Built from endpoints rather than feeds, so the
// paused flag never reaches here.
const timeline = computed(() => {
  const rows = []
  const clockOf = (ts) => new Intl.DateTimeFormat('en-US', {
    timeZone: DISPLAY_TIMEZONE, month: 'short', day: 'numeric',
    hour: 'numeric', minute: '2-digit', hour12: true,
  }).format(new Date(ts))

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
        clock: clockOf(e.timestamp),
        ago: generatePrettyTimeAgo(e.timestamp, now.value),
        // How long the state that STARTED here lasted, which is the number a
        // tech actually reports ("we were down 12 minutes").
        lasted: e.type === 'UNHEALTHY'
          ? (next ? generatePrettyTimeDifference(next.timestamp, e.timestamp)
                  : `${generatePrettyTimeDifference(now.value, e.timestamp)} and counting`)
          : null,
      })
    })

    // Warnings are synthesised here on purpose. Gatus raises events off the
    // pass/fail bool alone, so a degraded push — a pass carrying its reason — is
    // recorded as HEALTHY and produces no event, which would leave a panel that
    // promises "every state change" silent about the one state the collectors
    // added. So read the transitions out of the loaded results instead: the check
    // where a warning first appears is a change, the identical warnings after it
    // are not. Two known limits, both preferred to inventing data — a warning
    // older than the loaded results has no event to fall back on and simply isn't
    // listed, and one already running at the oldest loaded check is dated to that
    // check, so its duration is a floor rather than the true start.
    const results = ep.results || []
    results.forEach((r, idx) => {
      if (!isWarning(r) || isWarning(results[idx - 1])) return
      const ended = results.slice(idx + 1).find((x) => !isWarning(x))
      rows.push({
        t: new Date(r.timestamp).getTime(),
        tone: 'degraded',
        // The collector's sentence carries the numbers, so the row reads as a
        // finding rather than a label.
        text: `${role} degraded — ${warningTextOf(r)}`,
        clock: clockOf(r.timestamp),
        ago: generatePrettyTimeAgo(r.timestamp, now.value),
        lasted: ended
          ? generatePrettyTimeDifference(ended.timestamp, r.timestamp)
          : `${generatePrettyTimeDifference(now.value, r.timestamp)} and counting`,
      })
    })
  }
  return rows.sort((a, b) => b.t - a.t).slice(0, 12)
})

// --- Data ----------------------------------------------------------------
// A response that lands after the reader has changed site or range is dropped
// rather than painted, so switching quickly never leaves one feed on the old
// window while the rest moved on.
let fetchGeneration = 0

const fetchAll = async () => {
  const generation = ++fetchGeneration
  loading.value = true
  seriesError.value = false
  try {
    // Discovery pass, deliberately shallow: it returns every endpoint in the
    // fleet and is only asked which ones carry this site's name.
    const res = await fetch(`/api/v1/endpoints/statuses?page=1&pageSize=${DISCOVERY_RESULTS}`)
    if (res.status !== 200) throw new Error(await res.text())
    const all = await res.json()
    if (generation !== fetchGeneration) return
    const mine = (all || []).filter((ep) => ep.name === siteName.value)
    endpoints.value = mine
    loaded.value = true

    const depth = rawResults.value
    const wanted = historyRange.value

    await Promise.all(mine.map(async (ep) => {
      const encoded = encodeURIComponent(ep.key)
      const [detail, series, inventory] = await Promise.all([
        // Per-feed rather than one deep bulk call: 1600 results across 38 fleet
        // endpoints would be tens of thousands of rows to render five lanes.
        fetch(`/api/v1/endpoints/${encoded}/statuses?page=1&pageSize=${depth}`)
          .then((r) => (r.ok ? r.json() : null))
          .catch(() => null),
        fetch(`/api/v1/endpoints/${encoded}/uptime-series?range=${wanted}`)
          .then((r) => (r.ok ? r.json() : null))
          .catch(() => null),
        isPhoneFeed(ep)
          ? fetch(`/api/v1/phones/${encoded}`)
              .then((r) => (r.ok ? r.json() : null))
              .catch(() => null)
          : Promise.resolve(null),
      ])
      if (generation !== fetchGeneration) return
      if (detail && detail.key) {
        endpoints.value = endpoints.value.map((e) => (e.key === ep.key ? { ...e, ...detail } : e))
      }
      if (series && series.timestamps) {
        uptimeSeries.value = { ...uptimeSeries.value, [ep.key]: series }
      } else {
        seriesError.value = true
      }
      if (inventory && inventory.counts) {
        phoneCounts.value = { ...phoneCounts.value, [ep.key]: inventory.counts }
      }
    }))
  } catch (err) {
    console.error('[SiteOverview] fetch failed:', err)
  } finally {
    if (generation === fetchGeneration) {
      loading.value = false
      loaded.value = true
    }
  }
}

// Live stream keeps the head of each feed fresh. Only the newest result is
// merged in — replacing the whole array would shrink the loaded window back to
// whatever the broadcast carries. The cap follows the selected range, so a 1h
// view stops holding a day of results it cannot draw and a 24h view is not
// trimmed back to a fixed 200.
let es = null
const connectLive = () => {
  try {
    es = new EventSource('/api/v1/live', { withCredentials: true })
    es.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        const incoming = (data.endpoints || []).filter((ep) => ep.name === siteName.value)
        if (!incoming.length) return
        const cap = rawResults.value
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
            results: [...have, newest].slice(-cap),
          }
        })
      } catch (err) { /* ignore malformed frames */ }
    }
  } catch (err) { /* live is a bonus; the page works without it */ }
}

watch(siteName, () => {
  loaded.value = false
  endpoints.value = []
  uptimeSeries.value = {}
  phoneCounts.value = {}
  hoverIndex.value = null
  fetchAll()
})

// A new range means a different column count, so a held hover index would point
// at a different moment than the one under the pointer.
watch(historyRange, () => {
  hoverIndex.value = null
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
/* Paused is an outline, not a fill — the only pill on the page that isn't a
   claim about health, so it shouldn't look like one. */
.status-pill.st-paused {
  background: transparent; color: hsl(var(--muted-foreground));
  box-shadow: inset 0 0 0 1px hsl(var(--border));
}

.empty-state {
  border: 1px dashed hsl(var(--border));
  border-radius: 10px; padding: 2.5rem 1.5rem; text-align: center;
}

/* --- Filter row -------------------------------------------------------
   A rule under it, not a card around it: it is a control for the page rather
   than another panel of content, and the line is what says "everything under
   here is scoped". */
.filter-row {
  display: flex; align-items: center; justify-content: space-between;
  gap: 1rem; flex-wrap: wrap;
  padding-bottom: 0.7rem;
  border-bottom: 1px solid hsl(var(--border));
}
.filter-note { font-size: 0.78rem; color: hsl(var(--muted-foreground)); margin-top: 0.15rem; max-width: 46rem; }

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
/* Dashed edge for the all-paused verdict: the rule is still there, but broken —
   nothing is watching this site. */
.tone-paused { border-left-color: hsl(var(--muted-foreground) / 0.5); border-left-style: dashed; }

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
/* A warned feed carries its colour without being asked, same as a down one: it
   passed its check, but something inside it is broken and the strip has to say so
   from across the room. */
.feed-strip.is-degraded { border-color: color-mix(in srgb, var(--status-degraded) 45%, hsl(var(--border))); }
.feed-strip.is-down { border-color: color-mix(in srgb, var(--status-down) 45%, hsl(var(--border))); }
.feed-strip:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }

/* Paused: quiet on purpose, not broken. Dashed border and a drained card say
   "switched off", while the status colours are dropped entirely so a stale red
   can't be mistaken for a live one. Still fully legible, and it brightens on
   hover/focus so the card stays usable. */
.feed-strip.is-paused {
  border-style: dashed;
  border-color: hsl(var(--border));
  background: hsl(var(--card) / 0.5);
  opacity: 0.62;
}
.feed-strip.is-paused:hover,
.feed-strip.is-paused:focus-visible { opacity: 0.9; }
.feed-strip.is-paused .feed-value { color: hsl(var(--muted-foreground)); }

.feed-paused-tag {
  flex: none;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px; letter-spacing: 0.14em; text-transform: uppercase; font-weight: 700;
  color: hsl(var(--muted-foreground));
  border: 1px dashed hsl(var(--border));
  border-radius: 4px; padding: 0.1rem 0.3rem; line-height: 1.4;
}

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
/* Amber, the same --status-degraded the pill and the lane cells use, so one
   colour means one thing everywhere on the page. */
.lamp-degraded { background: var(--status-degraded); box-shadow: 0 0 7px -1px var(--status-degraded); }
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
/* The resolution sentence. Quieter than the panel subtitle because it is a
   caveat about the data, but never hidden behind a tooltip: a reader who thinks
   a 30d column is an hour is reading the panel wrong. */
.panel-note { font-size: 0.72rem; color: hsl(var(--muted-foreground)); opacity: 0.85; margin-top: 0.2rem; max-width: 52rem; }
.panel-empty { font-size: 0.8rem; color: hsl(var(--muted-foreground)); padding: 0.6rem 0; }
.panel-error {
  font-size: 0.76rem; color: hsl(var(--muted-foreground));
  border: 1px solid hsl(var(--border)); border-left: 3px solid var(--status-degraded);
  border-radius: 8px; padding: 0.4rem 0.6rem; margin-bottom: 0.6rem;
}

/* A fact about the data, so it reads as a label rather than a status. */
.res-tag {
  flex: none;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px; letter-spacing: 0.14em; text-transform: uppercase; font-weight: 700;
  color: hsl(var(--muted-foreground));
  border: 1px solid hsl(var(--border));
  border-radius: 4px; padding: 0.15rem 0.4rem; line-height: 1.4;
}

/* Scrubber readout — one shared line instead of a floating tooltip, so the
   comparison across feeds stays visible while the pointer moves. */
.scrub-readout { display: flex; align-items: center; gap: 0.75rem; flex-wrap: wrap; }
.scrub-time { font-size: 0.8rem; font-weight: 700; min-width: 4.5rem; }
.scrub-readout.live .scrub-time { color: hsl(var(--muted-foreground)); }
.scrub-feeds { display: inline-flex; align-items: center; gap: 0.7rem; flex-wrap: wrap; }
.scrub-feed { display: inline-flex; align-items: center; gap: 0.3rem; font-size: 0.72rem; color: hsl(var(--muted-foreground)); }
.scrub-feed.is-paused { opacity: 0.5; }
/* The bucket's uptime, shown only above the crossover where a column is wide
   enough that its colour alone would not say how much of it was bad. */
.scrub-detail { font-size: 0.7rem; font-weight: 700; color: hsl(var(--foreground)); font-variant-numeric: tabular-nums; }

/* --- Lanes ------------------------------------------------------------ */
.lanes { display: grid; gap: 0.3rem; }
.lane { display: flex; align-items: center; gap: 0.6rem; }
.lane-label {
  width: 4.2rem; flex: none; text-align: right;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px; letter-spacing: 0.08em; text-transform: uppercase;
  color: hsl(var(--muted-foreground));
}
/* The lane of a paused feed keeps its recorded history but recedes, so scanning
   down a column you read the live feeds first. */
.lane.is-paused { opacity: 0.45; }
.lane.is-paused .lane-label { font-style: italic; }
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

/* --- Charts ----------------------------------------------------------
   Stacked, never side by side. Same width and same bucket timestamps in every
   chart means column N sits at the same screen position all the way down, so a
   bad hour lines up between feeds exactly as it does in the lanes. */
.charts { display: grid; gap: 0.6rem; }

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
.mark-degraded { color: var(--status-degraded); }
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
