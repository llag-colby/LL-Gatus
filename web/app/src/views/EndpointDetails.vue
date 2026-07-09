<template>
  <div class="dashboard-container bg-background min-h-screen">
    <div class="w-full px-4 sm:px-6 py-5">
      <div v-if="!endpointStatus || !endpointStatus.name" class="flex items-center justify-center py-20">
        <Loading size="lg" />
      </div>

      <div v-else class="space-y-5">
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
        <div class="grid gap-4 grid-cols-2 lg:grid-cols-4">
          <Card>
            <CardHeader class="pb-1"><CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">Current Status</CardTitle></CardHeader>
            <CardContent>
              <div :class="['text-2xl font-bold', currentHealthStatus === 'healthy' ? 'text-green-600' : currentHealthStatus === 'unhealthy' ? 'text-red-600' : '']">
                {{ currentHealthStatus === 'healthy' ? 'Operational' : currentHealthStatus === 'unhealthy' ? 'Issues Detected' : 'Unknown' }}
              </div>
            </CardContent>
          </Card>
          <Card>
            <CardHeader class="pb-1"><CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">Avg Response Time</CardTitle></CardHeader>
            <CardContent><div class="text-2xl font-bold tabular-nums">{{ pageAverageResponseTime }}</div></CardContent>
          </Card>
          <Card>
            <CardHeader class="pb-1"><CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">Response Time Range</CardTitle></CardHeader>
            <CardContent><div class="text-2xl font-bold tabular-nums">{{ pageResponseTimeRange }}</div></CardContent>
          </Card>
          <Card>
            <CardHeader class="pb-1"><CardTitle class="text-xs font-medium text-muted-foreground uppercase tracking-wider">Last Check</CardTitle></CardHeader>
            <CardContent><div class="text-2xl font-bold">{{ lastCheckTime }}</div></CardContent>
          </Card>
        </div>

        <!-- Main content: chart + sidebar, checks + badges — full width, side by side -->
        <div class="grid gap-5 grid-cols-1 xl:grid-cols-3">
          <!-- Response Time Trend (wide) -->
          <Card v-if="showResponseTimeChartAndBadges" class="xl:col-span-2">
            <CardHeader class="pb-2">
              <div class="flex items-center justify-between">
                <CardTitle>Response Time Trend</CardTitle>
                <select v-model="selectedChartDuration"
                  class="text-sm bg-background border rounded-md px-3 py-1 focus:outline-none focus:ring-2 focus:ring-ring">
                  <option value="24h">24 hours</option>
                  <option value="7d">7 days</option>
                  <option value="30d">30 days</option>
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
              />
            </CardContent>
          </Card>

          <!-- Uptime + Current Health (sidebar) -->
          <Card :class="showResponseTimeChartAndBadges ? 'xl:col-span-1' : 'xl:col-span-3'">
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
              <div class="mt-4 pt-4 border-t flex items-center justify-center">
                <img :src="generateHealthBadgeImageURL()" alt="health badge" />
              </div>
            </CardContent>
          </Card>

          <!-- Recent Checks (wide) -->
          <Card class="xl:col-span-2">
            <CardHeader class="pb-2"><CardTitle>Recent Checks</CardTitle></CardHeader>
            <CardContent>
              <div class="space-y-4">
                <EndpointCard
                  v-if="endpointStatus"
                  :endpoint="endpointStatus"
                  :maxResults="resultPageSize"
                  :showAverageResponseTime="showAverageResponseTime"
                  @showTooltip="showTooltip"
                  class="border-0 shadow-none bg-transparent p-0"
                />
                <div v-if="endpointStatus && endpointStatus.key" class="pt-4 border-t">
                  <Pagination @page="changePage" :numberOfResultsPerPage="resultPageSize" :currentPageProp="currentPage" />
                </div>
              </div>
            </CardContent>
          </Card>

          <!-- Response time badges (sidebar) -->
          <Card v-if="showResponseTimeChartAndBadges" class="xl:col-span-1">
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
        </div>

        <!-- Events (full width, spread horizontally) -->
        <Card v-if="events && events.length > 0">
          <CardHeader class="pb-2"><CardTitle>Events</CardTitle></CardHeader>
          <CardContent>
            <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              <div v-for="event in events" :key="event.timestamp" class="flex items-start gap-3 p-3 rounded-lg border bg-card">
                <div class="mt-0.5 shrink-0">
                  <ArrowUpCircle v-if="event.type === 'HEALTHY'" class="h-5 w-5 text-green-500" />
                  <ArrowDownCircle v-else-if="event.type === 'UNHEALTHY'" class="h-5 w-5 text-red-500" />
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

    <Settings @refreshData="fetchData" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { ArrowLeft, RefreshCw, ArrowUpCircle, ArrowDownCircle, PlayCircle, Activity, Timer } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Card, CardHeader, CardTitle, CardContent } from '@/components/ui/card'
import StatusBadge from '@/components/StatusBadge.vue'
import EndpointCard from '@/components/EndpointCard.vue'
import Settings from '@/components/Settings.vue'
import Pagination from '@/components/Pagination.vue'
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
const selectedChartDuration = ref('24h')
const isRefreshing = ref(false)

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
    if (result.duration) {
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
    if (duration) {
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
                event.fancyText = 'Endpoint was unhealthy for ' + generatePrettyTimeDifference(nextEvent.timestamp, event.timestamp)
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

const changePage = (page) => {
  currentPage.value = page
  fetchData()
}

const showTooltip = (result, event, action = 'hover') => {
  emit('showTooltip', result, event, action)
}

const prettifyTimestamp = (timestamp) => {
  return new Date(timestamp).toLocaleString('en-US', { timeZone: 'America/Chicago', hour12: true })
}

const generateHealthBadgeImageURL = () => {
  return `/api/v1/endpoints/${endpointStatus.value.key}/health/badge.svg`
}

const generateUptimeBadgeImageURL = (duration) => {
  return `/api/v1/endpoints/${endpointStatus.value.key}/uptimes/${duration}/badge.svg`
}

const generateResponseTimeBadgeImageURL = (duration) => {
  return `/api/v1/endpoints/${endpointStatus.value.key}/response-times/${duration}/badge.svg`
}

onMounted(() => {
  fetchData()
})
</script>