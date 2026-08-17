<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-3">
      <div v-if="!endpointStatus || !endpointStatus.name" class="flex items-center justify-center py-20">
        <Loading size="lg" />
      </div>

      <div v-else class="space-y-3">
        <!-- Header bar -->
        <div class="flex flex-wrap items-center gap-x-5 gap-y-3">
          <Button variant="ghost" size="sm" @click="goBack" data-tooltip="Back to dashboard">
            <ArrowLeft class="h-4 w-4 mr-2" /> Back
          </Button>
          <div class="min-w-0">
            <h1 class="text-2xl sm:text-3xl font-bold tracking-tight leading-tight truncate">{{ endpointStatus.name }}</h1>
            <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground mt-1">
              <span v-if="endpointStatus.group">{{ endpointStatus.group }}</span>
              <span v-if="endpointStatus.group && hostname" class="opacity-40">•</span>
              <span v-if="hostname" class="font-mono">{{ hostname }}</span>
            </div>
          </div>
          <div class="ml-auto flex items-center gap-2">
            <MonitorToggle :endpoint-key="route.params.key" compact />
            <StatusBadge :status="currentHealthStatus" />
            <Button variant="ghost" size="icon" class="h-9 w-9" @click="exportCSV" data-tooltip="Export as CSV">
              <Download class="h-5 w-5" />
            </Button>
            <Button variant="ghost" size="icon" class="h-9 w-9" @click="toggleShowAverageResponseTime"
              :data-tooltip="showAverageResponseTime ? 'Showing average response time' : 'Showing min to max response time'">
              <Activity v-if="showAverageResponseTime" class="h-5 w-5" /><Timer v-else class="h-5 w-5" />
            </Button>
            <Button variant="ghost" size="icon" class="h-9 w-9" @click="forcePing" :disabled="isPinging"
              data-tooltip="Force ping now" data-tip-pos="bottom">
              <Zap class="h-5 w-5" :class="{ 'animate-pulse text-primary': isPinging }" />
            </Button>
            <Button variant="ghost" size="icon" class="h-9 w-9" @click="fetchData" :disabled="isRefreshing" data-tooltip="Refresh data">
              <RefreshCw :class="['h-4 w-4', isRefreshing && 'animate-spin']" />
            </Button>
          </div>
        </div>

        <!-- One window for the whole page: the range below drives the response
             time trend, the uptime chart and the summary badges. It is the
             shared range, so moving to the site overview or the phones,
             firewall and wireless panels keeps the same window. -->
        <div class="flex flex-wrap items-center gap-2">
          <RangeSelector v-model="range" label="History range" />
          <Button size="sm" :variant="isLive ? 'secondary' : 'outline'" :aria-pressed="isLive" @click="toggleLive"
            data-tooltip="Stream the last 10 minutes of pings">
            <SatelliteDish class="h-4 w-4 mr-1.5" :class="{ 'animate-pulse text-primary': isLive }" />
            Live
          </Button>
          <span v-if="isLive" class="text-xs text-muted-foreground">
            The trend is streaming the last 10 minutes. Uptime and the summary still cover {{ rangeText }}.
          </span>
        </div>

        <!-- KPI strip -->
        <div class="grid gap-3 grid-cols-2 lg:grid-cols-4">
          <Card>
            <CardHeader class="pb-1"><CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">Current Status</CardTitle></CardHeader>
            <CardContent>
              <div :class="['text-2xl font-bold', currentHealthStatus === 'healthy' ? 'st-text-up' : currentHealthStatus === 'unhealthy' ? 'st-text-down' : '']">
                {{ currentHealthStatus === 'healthy' ? 'Operational' : currentHealthStatus === 'unhealthy' ? 'Issues Detected' : 'Unknown' }}
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader class="pb-1"><CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">Connection</CardTitle></CardHeader>
            <CardContent>
              <div class="space-y-1.5">
                <div v-if="connectionIP" class="flex items-baseline justify-between gap-2">
                  <span class="text-xs text-muted-foreground">IP</span>
                  <span class="font-mono text-base font-semibold truncate">{{ connectionIP }}</span>
                </div>
                <div class="flex items-baseline justify-between gap-2">
                  <span class="text-xs text-muted-foreground">Reachable</span>
                  <span :class="['text-sm font-semibold', isReachable ? 'st-text-up' : 'st-text-down']">{{ isReachable ? 'Yes' : 'No' }}</span>
                </div>
                <div v-if="dnsRcode" class="flex items-baseline justify-between gap-2">
                  <span class="text-xs text-muted-foreground">DNS</span>
                  <span class="font-mono text-sm font-semibold">{{ dnsRcode }}</span>
                </div>
                <div v-if="httpStatus" class="flex items-baseline justify-between gap-2">
                  <span class="text-xs text-muted-foreground">HTTP</span>
                  <span class="font-mono text-sm font-semibold">{{ httpStatus }}</span>
                </div>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader class="pb-1"><CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">Response Time</CardTitle></CardHeader>
            <CardContent>
              <div class="text-2xl font-bold tabular-nums">{{ pageAverageResponseTime }}</div>
              <!-- This pair describes the checks listed below, not the selected
                   window, so it names the checks to avoid reading as the range. -->
              <div class="text-xs text-muted-foreground mt-0.5">{{ pageResponseTimeRange }} across these checks</div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader class="pb-1"><CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">Last Check</CardTitle></CardHeader>
            <CardContent><div class="text-2xl font-bold">{{ lastCheckTime }}</div></CardContent>
          </Card>
        </div>

        <!-- Main: left = chart + recent checks; right = uptime + response time + events -->
        <div class="flex flex-col xl:flex-row gap-3 items-stretch">
          <!-- Left column (primary visuals) -->
          <div class="w-full xl:flex-1 min-w-0 flex flex-col gap-3">
            <Card v-if="showResponseTimeChartAndBadges">
              <CardHeader class="pb-2">
                <div class="flex items-center justify-between gap-3">
                  <CardTitle>Response Time Trend</CardTitle>
                  <!-- Names the window the chart actually drew, which is not
                       always the one selected: see RANGE_TO_CHART_DURATION. -->
                  <span class="text-xs text-muted-foreground">{{ chartWindowLabel }}</span>
                </div>
              </CardHeader>
              <CardContent>
                <ResponseTimeChart
                  v-if="endpointStatus && endpointStatus.key"
                  :endpointKey="endpointStatus.key"
                  :duration="chartDuration"
                  :events="endpointStatus.events || []"
                  :results="liveResults"
                />
              </CardContent>
            </Card>

            <!-- Uptime for the selected window. A column per bucket, because a
                 bucket with no checks is a real gap and a line would draw
                 straight through it as though the endpoint had been up. -->
            <HistoryChart
              :series="uptimeSeries"
              title="Uptime"
              :subtitle="uptimeSubtitle"
              kind="column"
              ratio
              :height="150"
              :warnBelow="UPTIME_WARN_BELOW"
              :downBelow="UPTIME_DOWN_BELOW"
              :loading="uptimeLoading"
              :emptyText="uptimeEmptyText"
              :note="uptimeNote"
            />

            <Card class="flex-1">
              <CardHeader class="pb-2"><CardTitle>Recent Checks</CardTitle></CardHeader>
              <CardContent>
                <EndpointCard
                  v-if="endpointStatus"
                  :endpoint="endpointStatus"
                  :maxResults="resultPageSize"
                  :showAverageResponseTime="showAverageResponseTime"
                  @showTooltip="showTooltip"
                  class="border-0 shadow-none bg-transparent p-0"
                />
              </CardContent>
            </Card>
          </div>

          <!-- Right column (uptime + response time + events) -->
          <div class="w-full xl:w-80 2xl:w-96 shrink-0 flex flex-col gap-3">
            <!-- Two grids of four badges used to sit here, none of them tied to
                 the chart beside them. They are now one pair for the selected
                 window, labelled with the period the badge really measures. -->
            <Card>
              <CardHeader class="pb-2">
                <div class="flex items-center justify-between gap-2">
                  <CardTitle>Summary</CardTitle>
                  <span class="text-xs text-muted-foreground">{{ badgePeriodLabel }}</span>
                </div>
              </CardHeader>
              <CardContent>
                <div class="grid grid-cols-2 gap-x-4 gap-y-3">
                  <div class="text-center">
                    <p class="text-xs text-muted-foreground mb-1">Uptime</p>
                    <img :src="generateUptimeBadgeImageURL(badgePeriod)" :alt="`Uptime, ${badgePeriodLabel.toLowerCase()}`" class="mx-auto" />
                  </div>
                  <div v-if="showResponseTimeChartAndBadges" class="text-center">
                    <p class="text-xs text-muted-foreground mb-1">Response time</p>
                    <img :src="generateResponseTimeBadgeImageURL(badgePeriod)" :alt="`Response time, ${badgePeriodLabel.toLowerCase()}`" class="mx-auto" />
                  </div>
                </div>
              </CardContent>
            </Card>

            <Card v-if="events && events.length > 0" class="flex-1">
              <CardHeader class="pb-2">
                <div class="flex items-center justify-between">
                  <CardTitle>Events</CardTitle>
                  <div v-if="totalEventPages > 1" class="flex items-center gap-1">
                    <Button variant="ghost" size="icon" class="h-7 w-7" :disabled="eventsPage === 0"
                      @click="eventsPage = Math.max(0, eventsPage - 1)" data-tooltip="Newer">
                      <ChevronLeft class="h-4 w-4" />
                    </Button>
                    <span class="text-xs text-muted-foreground tabular-nums">{{ eventsPage + 1 }}/{{ totalEventPages }}</span>
                    <Button variant="ghost" size="icon" class="h-7 w-7" :disabled="eventsPage >= totalEventPages - 1"
                      @click="eventsPage = Math.min(totalEventPages - 1, eventsPage + 1)" data-tooltip="Older">
                      <ChevronRight class="h-4 w-4" />
                    </Button>
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                <div class="space-y-2">
                  <div v-for="event in pagedEvents" :key="event.timestamp" class="flex items-start gap-3 p-2.5 rounded-lg border bg-card">
                    <div class="mt-0.5 shrink-0">
                      <ArrowUpCircle v-if="event.type === 'HEALTHY'" class="h-5 w-5 st-text-up" />
                      <ArrowDownCircle v-else-if="event.type === 'UNHEALTHY'" class="h-5 w-5 st-text-down" />
                      <PlayCircle v-else class="h-5 w-5 text-muted-foreground" />
                    </div>
                    <div class="min-w-0">
                      <p class="font-medium text-sm">{{ event.fancyText }}</p>
                      <p class="text-xs text-muted-foreground mt-0.5">{{ prettifyTimestamp(event.timestamp) }} • {{ event.fancyTimeAgo }}</p>
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>
          </div>
        </div>
      </div>
    </div>

    <Settings @refreshData="fetchData" />
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ArrowLeft, RefreshCw, ArrowUpCircle, ArrowDownCircle, PlayCircle, Activity, Timer, ChevronLeft, ChevronRight, Download, Zap, SatelliteDish } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'
import StatusBadge from '@/components/StatusBadge.vue'
import EndpointCard from '@/components/EndpointCard.vue'
import MonitorToggle from '@/components/MonitorToggle.vue'
import Settings from '@/components/Settings.vue'
import RangeSelector from '@/components/RangeSelector.vue'
import HistoryChart from '@/components/HistoryChart.vue'
import { addToast, historyRange, setHistoryRange } from '@/store'
import Loading from '@/components/Loading.vue'
import ResponseTimeChart from '@/components/ResponseTimeChart.vue'
import { generatePrettyTimeAgo, generatePrettyTimeDifference } from '@/utils/time'

const router = useRouter()
const route = useRoute()
const emit = defineEmits(['showTooltip'])

const endpointStatus = ref(null) // For paginated historical data
const currentStatus = ref(null) // For current/latest status (always page 1)
const events = ref([])
const currentPage = ref(1)
const resultPageSize = 50
const showResponseTimeChartAndBadges = ref(false)
const showAverageResponseTime = ref(localStorage.getItem('gatus:show-average-response-time') !== 'false')
const isRefreshing = ref(false)
const liveResults = ref([])
const eventsPage = ref(0)

// Events paged 3 at a time (newest first).
const totalEventPages = computed(() => Math.max(1, Math.ceil(events.value.length / 3)))
const pagedEvents = computed(() => events.value.slice(eventsPage.value * 3, eventsPage.value * 3 + 3))

// --- Time window ----------------------------------------------------------
// The page used to carry its own duration select and its own localStorage key.
// It now reads the shared range, so a window chosen here follows you to the site
// overview and to the phones, firewall and wireless panels, and back.
const range = computed({
  get: () => historyRange.value,
  set: (value) => setHistoryRange(value),
})

const RANGE_TEXT = {
  '1h': 'the last hour',
  '6h': 'the last 6 hours',
  '24h': 'the last 24 hours',
  '7d': 'the last 7 days',
  '30d': 'the last 30 days',
}
const rangeText = computed(() => RANGE_TEXT[historyRange.value] || 'the selected range')

// Live is a mode of this one chart, not a time range, so it is a toggle beside
// the range rather than a sixth range value. historyRange is shared with views
// that have no live feed: giving it a 'live' value would hand those pages a
// window they cannot plot. Turning Live on swaps only the response time trend to
// the last ten minutes of raw pings; the range keeps driving the uptime chart
// and the summary badges, and turning it off returns to the selected range.
const legacyDuration = localStorage.getItem('gatus:chart-duration')
if (legacyDuration !== null) {
  // Carry over the one part of the old key that the shared range cannot express.
  if (legacyDuration === 'live') localStorage.setItem('gatus:chart-live', 'true')
  localStorage.removeItem('gatus:chart-duration')
}
const isLive = ref(localStorage.getItem('gatus:chart-live') === 'true')
const toggleLive = () => {
  isLive.value = !isLive.value
  localStorage.setItem('gatus:chart-live', isLive.value ? 'true' : 'false')
}

// ResponseTimeChart fetches /response-times/{duration}/history, which serves
// 30d, 7d, 2d, 24h, 16h, 5h and 1h. The shared range list is 1h, 6h, 24h, 7d and
// 30d, so every value but 6h maps straight across. 6h has no match: 5h is the
// nearest the API serves, and 16h would overshoot by ten hours. The chart header
// names the window that was actually drawn, so a 6h selection reads "Last 5
// hours" rather than quietly claiming an hour of data it does not have. Nothing
// outside this map ever reaches the chart, so it cannot request a duration the
// backend rejects.
const RANGE_TO_CHART_DURATION = { '1h': '1h', '6h': '5h', '24h': '24h', '7d': '7d', '30d': '30d' }
const CHART_DURATION_TEXT = { '1h': 'Last hour', '5h': 'Last 5 hours', '24h': 'Last 24 hours', '7d': 'Last 7 days', '30d': 'Last 30 days' }
const chartDuration = computed(() => (isLive.value ? 'live' : RANGE_TO_CHART_DURATION[historyRange.value] || '24h'))
const chartWindowLabel = computed(() =>
  isLive.value ? 'Live, last 10 minutes' : (CHART_DURATION_TEXT[chartDuration.value] || 'Last 24 hours')
)

// The badge API serves 30d, 7d, 24h and 1h only. 1h maps exactly. 6h has no
// badge, so it rounds UP to 24h rather than down to 1h: a badge is a single
// number with no axis, and one measuring a shorter window than the selection
// would silently drop most of what was asked for, while a wider one at least
// contains all of it. The card states the period the badge really measures, so
// the widening is visible rather than assumed.
const RANGE_TO_BADGE_PERIOD = { '1h': '1h', '6h': '24h', '24h': '24h', '7d': '7d', '30d': '30d' }
const BADGE_PERIOD_TEXT = { '1h': 'Last hour', '24h': 'Last 24 hours', '7d': 'Last 7 days', '30d': 'Last 30 days' }
const badgePeriod = computed(() => RANGE_TO_BADGE_PERIOD[historyRange.value] || '24h')
const badgePeriodLabel = computed(() => BADGE_PERIOD_TEXT[badgePeriod.value] || 'Last 24 hours')

// --- Uptime series --------------------------------------------------------
// Thresholds are fractions of the bucket that succeeded. A bucket is only green
// when nothing in it failed: 0.999 is effectively "no failed check", since no
// endpoint here runs a thousand checks in a bucket, while still leaving room for
// float noise on a full bucket. Below 0.95 the bucket lost more than one check
// in twenty, which for an hourly bucket is minutes of downtime rather than a
// single blip, so it reads as down.
const UPTIME_WARN_BELOW = 0.999
const UPTIME_DOWN_BELOW = 0.95

const uptimeSeries = ref({ timestamps: [], values: [] })
const uptimeExecutions = ref([])
const uptimeResolution = ref('')
const uptimeLoading = ref(false)
const uptimeError = ref('')
// Only the newest request may write: switching range twice quickly would
// otherwise let the slower first response overwrite the second.
let uptimeRequestId = 0

const fetchUptimeSeries = async () => {
  const requestId = ++uptimeRequestId
  uptimeLoading.value = true
  try {
    const response = await fetch(`/api/v1/endpoints/${route.params.key}/uptime-series?range=${historyRange.value}`, {
      credentials: 'include'
    })
    if (requestId !== uptimeRequestId) return
    if (response.status === 200) {
      const data = await response.json()
      if (requestId !== uptimeRequestId) return
      uptimeSeries.value = { timestamps: data.timestamps || [], values: data.values || [] }
      uptimeExecutions.value = data.executions || []
      uptimeResolution.value = data.resolution || ''
      uptimeError.value = ''
    } else if (response.status === 404) {
      uptimeSeries.value = { timestamps: [], values: [] }
      uptimeExecutions.value = []
      uptimeResolution.value = ''
      uptimeError.value = 'No uptime history is recorded for this endpoint.'
    } else {
      uptimeError.value = 'Uptime history did not load. Refresh to try again.'
      console.error('[Details][fetchUptimeSeries] Error:', await response.text())
    }
  } catch (error) {
    if (requestId !== uptimeRequestId) return
    uptimeError.value = 'Uptime history did not load. Refresh to try again.'
    console.error('[Details][fetchUptimeSeries] Error:', error)
  } finally {
    if (requestId === uptimeRequestId) uptimeLoading.value = false
  }
}

// Weighted by executions, so a bucket holding four checks cannot count the same
// as one holding sixty. Buckets with no executions are left out entirely.
const uptimeSubtitle = computed(() => {
  const values = uptimeSeries.value.values || []
  let weighted = 0
  let executions = 0
  for (let i = 0; i < values.length; i++) {
    const count = Number(uptimeExecutions.value[i]) || 0
    const value = values[i]
    if (count <= 0 || value === null || value === undefined) continue
    weighted += value * count
    executions += count
  }
  if (executions === 0) return ''
  const percent = (weighted / executions) * 100
  return `${percent >= 99.995 ? percent.toFixed(0) : percent.toFixed(2)}% over ${rangeText.value}`
})

// Says plainly what one column is. Gatus compacts uptime older than roughly 48
// hours into daily rows, so a 7d or 30d view is days per column no matter how it
// looks, and a gap is missing collection rather than an outage.
const UPTIME_RESOLUTION_TEXT = {
  hour: 'One column per hour.',
  day: 'One column per day: uptime older than about 48 hours is kept as daily totals.',
  mixed: 'Recent columns are hourly, older ones are daily: uptime older than about 48 hours is kept as daily totals.',
}
const uptimeNote = computed(() => {
  const resolution = UPTIME_RESOLUTION_TEXT[uptimeResolution.value]
  if (!resolution) return ''
  return `${resolution} Grey columns are buckets with no checks recorded, which is a gap in collection rather than an outage.`
})

const uptimeEmptyText = computed(() => uptimeError.value || 'No uptime recorded in this range yet.')

const latestResult = computed(() => {
  // Use currentStatus for the actual latest result
  if (!currentStatus.value || !currentStatus.value.results || currentStatus.value.results.length === 0) {
    return null
  }
  return currentStatus.value.results[currentStatus.value.results.length - 1]
})

const currentHealthStatus = computed(() => {
  if (!latestResult.value) return 'unknown'
  if (!latestResult.value.success) return 'unhealthy'
  // A pass that carries a reason is a warning, not a clean bill of health: the
  // push-based collectors report their degraded state as success=true with the
  // reason in errors[], so calling this "healthy" would hide "1 of 2 WAN uplinks
  // down". Same rule as LocationCard's isWarning().
  return (latestResult.value.errors || []).length > 0 ? 'degraded' : 'healthy'
})

const hostname = computed(() => {
  return latestResult.value?.hostname || null
})

// Connection details we can surface from the latest check result.
const connectionIP = computed(() => latestResult.value?.ip || latestResult.value?.hostname || null)
const isReachable = computed(() => currentHealthStatus.value === 'healthy')
const dnsRcode = computed(() => latestResult.value?.dnsRcode || null)
const httpStatus = computed(() => latestResult.value?.status || latestResult.value?.httpStatus || null)

const toggleShowAverageResponseTime = () => {
  showAverageResponseTime.value = !showAverageResponseTime.value
  localStorage.setItem('gatus:show-average-response-time', showAverageResponseTime.value ? 'true' : 'false')
}

const pageAverageResponseTime = computed(() => {
  // Use endpointStatus for current page's average response time
  if (!endpointStatus.value || !endpointStatus.value.results || endpointStatus.value.results.length === 0) {
    return 'N/A'
  }
  let total = 0
  let count = 0
  for (const result of endpointStatus.value.results) {
    if (result.success && result.duration) {
      total += result.duration
      count++
    }
  }
  if (count === 0) return 'N/A'
  return `${Math.round(total / count / 1000000)}ms`
})

const pageResponseTimeRange = computed(() => {
  // Use endpointStatus for current page's response time range
  if (!endpointStatus.value || !endpointStatus.value.results || endpointStatus.value.results.length === 0) {
    return 'N/A'
  }
  let min = Infinity
  let max = 0
  let hasData = false

  for (const result of endpointStatus.value.results) {
    const duration = result.duration
    if (result.success && duration) {
      min = Math.min(min, duration)
      max = Math.max(max, duration)
      hasData = true
    }
  }
  
  if (!hasData) return 'N/A'
  const minMs = Math.trunc(min / 1000000)
  const maxMs = Math.trunc(max / 1000000)
  // If min and max are the same, show single value
  if (minMs === maxMs) {
    return `${minMs}ms`
  }
  return `${minMs}-${maxMs}ms`
})

const lastCheckTime = computed(() => {
  // Use currentStatus for real-time last check time
  if (!currentStatus.value || !currentStatus.value.results || currentStatus.value.results.length === 0) {
    return 'Never'
  }
  return generatePrettyTimeAgo(currentStatus.value.results[currentStatus.value.results.length - 1].timestamp)
})


const fetchData = async () => {
  isRefreshing.value = true
  try {
    const response = await fetch(`/api/v1/endpoints/${route.params.key}/statuses?page=${currentPage.value}&pageSize=${resultPageSize}`, {
      credentials: 'include'
    })
    
    if (response.status === 200) {
      const data = await response.json()
      endpointStatus.value = data
      
      // Always update currentStatus when on page 1 (including when returning to it)
      if (currentPage.value === 1) {
        currentStatus.value = data
        // Seed the live view immediately (SSE keeps it fresh after this).
        liveResults.value = data.results || []
      }
      
      let processedEvents = []
      if (data.events && data.events.length > 0) {
        for (let i = data.events.length - 1; i >= 0; i--) {
          let event = data.events[i]
          if (i === data.events.length - 1) {
            if (event.type === 'UNHEALTHY') {
              event.fancyText = 'Endpoint is unhealthy'
            } else if (event.type === 'HEALTHY') {
              event.fancyText = 'Endpoint is healthy'
            } else if (event.type === 'START') {
              event.fancyText = 'Monitoring started'
            }
          } else {
            let nextEvent = data.events[i + 1]
            if (event.type === 'HEALTHY') {
              event.fancyText = 'Endpoint became healthy'
            } else if (event.type === 'UNHEALTHY') {
              if (nextEvent) {
                event.fancyText = 'Endpoint was unhealthy for ~' + generatePrettyTimeDifference(nextEvent.timestamp, event.timestamp)
              } else {
                event.fancyText = 'Endpoint became unhealthy'
              }
            } else if (event.type === 'START') {
              event.fancyText = 'Monitoring started'
            }
          }
          event.fancyTimeAgo = generatePrettyTimeAgo(event.timestamp)
          processedEvents.push(event)
        }
      }
      events.value = processedEvents
      
      if (data.results && data.results.length > 0) {
        for (let i = 0; i < data.results.length; i++) {
          if (data.results[i].duration > 0) {
            showResponseTimeChartAndBadges.value = true
            break
          }
        }
      }
    } else {
      console.error('[Details][fetchData] Error:', await response.text())
    }
  } catch (error) {
    console.error('[Details][fetchData] Error:', error)
  } finally {
    isRefreshing.value = false
    // Refreshing the page refreshes the uptime chart too: it is the same data
    // seen at a different resolution, and a force ping can move it.
    fetchUptimeSeries()
  }
}

// Force ping: run one check right now instead of waiting out the endpoint's
// interval. The backend stores the result like any scheduled check, so we just
// re-fetch afterwards to pull it into the timeline.
const isPinging = ref(false)
const forcePing = async () => {
  if (isPinging.value) return
  isPinging.value = true
  try {
    const res = await fetch(`/api/v1/endpoints/${route.params.key}/check`, {
      method: 'POST',
      credentials: 'include'
    })
    const data = await res.json().catch(() => ({}))
    if (res.status === 429) {
      addToast(`Checked a moment ago, try again in ${Math.ceil((data.retryAfterMs || 3000) / 1000)}s`, 'error')
    } else if (!res.ok) {
      // Includes the 409 a paused endpoint returns: the server explains why in
      // data.error, so it is shown as sent rather than replaced.
      addToast(data.error || 'Ping failed to run, try again', 'error')
    } else if (data.success) {
      addToast(`Ping OK: ${data.durationMs}ms`, 'success')
    } else {
      addToast(`Ping failed${data.errors && data.errors.length ? ': ' + data.errors[0] : ''}`, 'error')
    }
    if (res.ok) await fetchData()
  } catch (e) {
    addToast('Ping failed to run, try again', 'error')
  } finally {
    isPinging.value = false
  }
}

const goBack = () => {
  router.push('/')
}

const showTooltip = (result, event, action = 'hover') => {
  emit('showTooltip', result, event, action)
}

const prettifyTimestamp = (timestamp) => {
  return new Date(timestamp).toLocaleString('en-US', { timeZone: 'America/Chicago', hour12: true })
}

const generateUptimeBadgeImageURL = (duration) => {
  return `/api/v1/endpoints/${endpointStatus.value.key}/uptimes/${duration}/badge.svg`
}

const generateResponseTimeBadgeImageURL = (duration) => {
  return `/api/v1/endpoints/${endpointStatus.value.key}/response-times/${duration}/badge.svg`
}

const exportCSV = () => {
  const rows = (currentStatus.value && currentStatus.value.results) || (endpointStatus.value && endpointStatus.value.results) || []
  if (!rows.length) return
  const lines = ['Timestamp (CST),Response Time (ms),Status,IP']
  for (const r of rows) {
    const ts = new Date(r.timestamp).toLocaleString('en-US', { timeZone: 'America/Chicago', hour12: true })
    const ms = r.duration ? Math.round(r.duration / 1000000) : ''
    const status = r.success ? 'Success' : 'Fail'
    const ip = r.hostname || ''
    lines.push(`"${ts}",${ms},${status},${ip}`)
  }
  const blob = new Blob([lines.join('\r\n')], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  const name = (endpointStatus.value && endpointStatus.value.name) || 'endpoint'
  a.download = `${name}_${route.params.key}.csv`.replace(/[^a-z0-9._-]/gi, '_')
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

// Live results (last pings) via SSE, used by the chart's "Live" view.
let liveES = null
const connectLive = () => {
  try {
    liveES = new EventSource('/api/v1/live', { withCredentials: true })
    liveES.onmessage = (e) => {
      try {
        const data = JSON.parse(e.data)
        const ep = (data.endpoints || []).find(x => x.key === route.params.key)
        if (ep && Array.isArray(ep.results)) liveResults.value = ep.results
      } catch (err) { /* ignore */ }
    }
  } catch (err) { /* ignore */ }
}

// Picking a new range reloads the uptime series. The response time chart watches
// its own duration prop, and the badges are plain image URLs, so both follow the
// selection without any work here.
watch(historyRange, fetchUptimeSeries)

onMounted(() => {
  // fetchData loads the uptime series as part of its refresh.
  fetchData()
  connectLive()
})

onUnmounted(() => {
  if (liveES) {
    liveES.close()
    liveES = null
  }
  // Any uptime request still in flight is now stale: bumping the id stops it
  // writing to refs after the view is gone.
  uptimeRequestId++
})
</script>

<style scoped>
/* Compact all card padding on the detail page so it fits without scrolling. */
.detail-page :deep(.p-6) {
  padding: 0.8rem 1rem;
}
.detail-page :deep(.p-6.pt-0) {
  padding-top: 0;
}
</style>