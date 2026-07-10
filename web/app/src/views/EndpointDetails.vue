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
            <StatusBadge :status="currentHealthStatus" />
            <Button variant="ghost" size="icon" class="h-9 w-9" @click="exportCSV" data-tooltip="Export as CSV">
              <Download class="h-5 w-5" />
            </Button>
            <Button variant="ghost" size="icon" class="h-9 w-9" @click="toggleShowAverageResponseTime"
              :data-tooltip="showAverageResponseTime ? 'Showing average response time' : 'Showing min–max response time'">
              <Activity v-if="showAverageResponseTime" class="h-5 w-5" /><Timer v-else class="h-5 w-5" />
            </Button>
            <Button variant="ghost" size="icon" class="h-9 w-9" @click="fetchData" :disabled="isRefreshing" data-tooltip="Refresh data">
              <RefreshCw :class="['h-4 w-4', isRefreshing && 'animate-spin']" />
            </Button>
          </div>
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
              <div class="text-xs text-muted-foreground mt-0.5">{{ pageResponseTimeRange }} range</div>
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
                <div class="flex items-center justify-between">
                  <CardTitle>Response Time Trend</CardTitle>
                  <select v-model="selectedChartDuration"
                    class="text-sm bg-background border rounded-md px-3 py-1 focus:outline-none focus:ring-2 focus:ring-ring">
                    <option value="live">Live</option>
                    <option value="1h">1 hour</option>
                    <option value="5h">5 hours</option>
                    <option value="16h">16 hours</option>
                    <option value="24h">24 hours</option>
                    <option value="2d">2 days</option>
                    <option value="7d">7 days</option>
                    <option value="30d">1 month</option>
                  </select>
                </div>
              </CardHeader>
              <CardContent>
                <ResponseTimeChart
                  v-if="endpointStatus && endpointStatus.key"
                  :endpointKey="endpointStatus.key"
                  :duration="selectedChartDuration"
                  :serverUrl="serverUrl"
                  :events="endpointStatus.events || []"
                  :results="liveResults"
                />
              </CardContent>
            </Card>

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
            <Card>
              <CardHeader class="pb-2"><CardTitle>Uptime</CardTitle></CardHeader>
              <CardContent>
                <div class="grid grid-cols-2 gap-x-4 gap-y-3">
                  <div v-for="period in ['30d', '7d', '24h', '1h']" :key="period" class="text-center">
                    <p class="text-xs text-muted-foreground mb-1">
                      {{ period === '30d' ? 'Last 30 days' : period === '7d' ? 'Last 7 days' : period === '24h' ? 'Last 24 hours' : 'Last hour' }}
                    </p>
                    <img :src="generateUptimeBadgeImageURL(period)" :alt="`${period} uptime`" class="mx-auto" />
                  </div>
                </div>
              </CardContent>
            </Card>

            <Card v-if="showResponseTimeChartAndBadges">
              <CardHeader class="pb-2"><CardTitle>Response Time</CardTitle></CardHeader>
              <CardContent>
                <div class="grid grid-cols-2 gap-x-4 gap-y-3">
                  <div v-for="period in ['30d', '7d', '24h', '1h']" :key="period" class="text-center">
                    <p class="text-xs text-muted-foreground mb-1">
                      {{ period === '30d' ? 'Last 30 days' : period === '7d' ? 'Last 7 days' : period === '24h' ? 'Last 24 hours' : 'Last hour' }}
                    </p>
                    <img :src="generateResponseTimeBadgeImageURL(period)" :alt="`${period} response time`" class="mx-auto" />
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
import { ArrowLeft, RefreshCw, ArrowUpCircle, ArrowDownCircle, PlayCircle, Activity, Timer, ChevronLeft, ChevronRight, Download } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'
import StatusBadge from '@/components/StatusBadge.vue'
import EndpointCard from '@/components/EndpointCard.vue'
import Settings from '@/components/Settings.vue'
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
const selectedChartDuration = ref(localStorage.getItem('gatus:chart-duration') || '24h')
watch(selectedChartDuration, (value) => localStorage.setItem('gatus:chart-duration', value))
const isRefreshing = ref(false)
const liveResults = ref([])
const eventsPage = ref(0)

// Events paged 3 at a time (newest first).
const totalEventPages = computed(() => Math.max(1, Math.ceil(events.value.length / 3)))
const pagedEvents = computed(() => events.value.slice(eventsPage.value * 3, eventsPage.value * 3 + 3))

const latestResult = computed(() => {
  // Use currentStatus for the actual latest result
  if (!currentStatus.value || !currentStatus.value.results || currentStatus.value.results.length === 0) {
    return null
  }
  return currentStatus.value.results[currentStatus.value.results.length - 1]
})

const currentHealthStatus = computed(() => {
  if (!latestResult.value) return 'unknown'
  return latestResult.value.success ? 'healthy' : 'unhealthy'
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

onMounted(() => {
  fetchData()
  connectLive()
})

onUnmounted(() => {
  if (liveES) {
    liveES.close()
    liveES = null
  }
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