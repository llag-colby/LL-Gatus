<template>
  <div class="dashboard-container home-view bg-background">
    <div class="w-full px-4 sm:px-6 py-6">
      <!-- Announcement Banner (Active Announcements) -->
      <AnnouncementBanner :announcements="activeAnnouncements" class="mb-6" />

      <div v-if="loading" class="flex items-center justify-center py-20">
        <Loading size="lg" />
      </div>

      <div v-else-if="locations.length === 0 && filteredSuites.length === 0" class="text-center py-20">
        <AlertCircle class="h-12 w-12 text-muted-foreground mx-auto mb-4" />
        <h3 class="text-lg font-semibold mb-2">No endpoints or suites found</h3>
        <p class="text-muted-foreground">
          {{ controls.searchQuery || controls.showOnlyFailing || controls.showRecentFailures
            ? 'Try adjusting your filters'
            : 'No endpoints or suites are configured' }}
        </p>
      </div>

      <div v-else>
        <!-- Suites Section -->
        <div v-if="filteredSuites.length > 0" class="mb-6">
          <h2 class="text-lg font-semibold text-foreground mb-3">Suites</h2>
          <div class="grid gap-3 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 2xl:grid-cols-5">
            <SuiteCard
              v-for="(suite, index) in paginatedSuites"
              :key="suite.key"
              :suite="suite"
              :maxResults="resultPageSize"
              class="motion-rise"
              :style="{ '--d': Math.min(index, 14) * 40 + 'ms' }"
              @showTooltip="showTooltip"
            />
          </div>
        </div>

        <!-- Locations Section -->
        <div v-if="locations.length > 0">
          <h2 v-if="filteredSuites.length > 0" class="text-lg font-semibold text-foreground mb-3">Locations</h2>
          <div class="dashboard-grid grid"
            :class="(dashboardView === 'horizontal' || isFullscreen)
              ? 'gap-3 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 2xl:grid-cols-5'
              : 'gap-5 grid-cols-1 max-w-5xl mx-auto'"
            :style="{ '--fs-cols': fsCols, '--loc-max-rows': maxRowCount }">
            <LocationCard
              v-for="(location, index) in paginatedLocations"
              :key="location.name"
              :name="location.name"
              :endpoints="location.endpoints"
              :maxResults="barsToShow"
              class="motion-rise"
              :style="{ '--d': Math.min(index, 14) * 40 + 'ms' }"
              @showTooltip="showTooltip"
              @rowcount="noteRowCount"
            />
          </div>
        </div>

        <div v-if="totalPages > 1" class="mt-8 flex items-center justify-center gap-2">
          <Button
            variant="outline"
            size="icon"
            :disabled="currentPage === 1"
            @click="goToPage(currentPage - 1)"
            data-tooltip="Previous page"
          >
            <ChevronLeft class="h-4 w-4" />
          </Button>

          <div class="flex gap-1">
            <Button
              v-for="page in visiblePages"
              :key="page"
              :variant="page === currentPage ? 'default' : 'outline'"
              size="sm"
              @click="goToPage(page)"
            >
              {{ page }}
            </Button>
          </div>

          <Button
            variant="outline"
            size="icon"
            :disabled="currentPage === totalPages"
            @click="goToPage(currentPage + 1)"
            data-tooltip="Next page"
          >
            <ChevronRight class="h-4 w-4" />
          </Button>
        </div>
      </div>

      <!-- Past Announcements Section -->
      <div v-if="archivedAnnouncements.length > 0" class="mt-12 pb-8">
        <PastAnnouncements :announcements="archivedAnnouncements" />
      </div>
    </div>

    <Settings @refreshData="fetchData" />
  </div>
</template>

<script setup>
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
import { AlertCircle, ChevronLeft, ChevronRight } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import LocationCard from '@/components/LocationCard.vue'
import SuiteCard from '@/components/SuiteCard.vue'
import Settings from '@/components/Settings.vue'
import Loading from '@/components/Loading.vue'
import AnnouncementBanner from '@/components/AnnouncementBanner.vue'
import PastAnnouncements from '@/components/PastAnnouncements.vue'
import { controls, soundEnabled, simulations, knownLocations, isFullscreen, dashboardView, isCardHidden, cardOrder } from '@/store'
import { playUp, playDown, playDegraded } from '@/utils/sounds'

const props = defineProps({
  announcements: {
    type: Array,
    default: () => []
  }
})

const activeAnnouncements = computed(() => {
  return props.announcements ? props.announcements.filter(a => !a.archived) : []
})

const archivedAnnouncements = computed(() => {
  return props.announcements ? props.announcements.filter(a => a.archived) : []
})

const emit = defineEmits(['showTooltip'])

const endpointStatuses = ref([])
const suiteStatuses = ref([])
const loading = ref(false)
const currentPage = ref(1)
const itemsPerPage = 96
const resultPageSize = 50
// Bars shown per row. Fewer on the fullscreen wall so each bar is thick and
// readable from a distance; more in the normal grid where cards are smaller.
// A ceiling, not a count. Each card measures how many pills actually fit its
// rows and shows that many of the most recent results; this is just how much
// history it is allowed to draw on. The payload already carries 50 per endpoint.
const barsToShow = computed(() => resultPageSize)

// Cards that are not a rooftop sort to the end of the wall, whatever order is
// selected. Domain Controllers is the AD and DNS tier rather than a site, and
// left to the alphabet it lands between Decatur KIA and Florence, buried in the
// run of dealerships where nobody looks for it. SMB Shares and Hypervisors are
// the same kind of thing — an infrastructure tier, not a store — so they join it.
//
// This deliberately outranks the health sort too: the tier keeps a fixed place
// on the wall so people learn where to find it, rather than moving when it
// breaks. Flip the pinRank comparison below if a failing tier should jump.
const PINNED_LAST = new Set(['Domain Controllers', 'SMB Shares', 'Hypervisors'])
const pinRank = (name) => (PINNED_LAST.has(name) ? 1 : 0)

// --- helpers ---
const latestFailed = (ep) => {
  if (!ep.results || ep.results.length === 0) return false
  return !ep.results[ep.results.length - 1].success
}
const everFailed = (ep) => {
  if (!ep.results || ep.results.length === 0) return false
  return ep.results.some(r => !r.success)
}

// Consolidate endpoints into locations (grouped by endpoint name), then
// apply search / filter / sort at the location level.
const locations = computed(() => {
  const map = new Map()
  for (const ep of endpointStatuses.value) {
    if (!map.has(ep.name)) map.set(ep.name, { name: ep.name, endpoints: [] })
    map.get(ep.name).endpoints.push(ep)
  }
  let list = [...map.values()]

  if (controls.searchQuery) {
    const q = controls.searchQuery.toLowerCase()
    list = list.filter(loc =>
      loc.name.toLowerCase().includes(q) ||
      loc.endpoints.some(e => e.group && e.group.toLowerCase().includes(q))
    )
  }

  if (controls.showOnlyFailing) {
    list = list.filter(loc => loc.endpoints.some(latestFailed))
  }

  if (controls.showRecentFailures) {
    list = list.filter(loc => loc.endpoints.some(everFailed))
  }

  // Cards hidden in the shared layout. Hiding is presentation only: the
  // endpoints behind the card are still checked and still alert.
  list = list.filter(loc => !isCardHidden(loc.name))

  // A card the user has placed sits where they put it. Everything unarranged
  // keeps the old behaviour and follows after, so a newly discovered site lands
  // at the end instead of jumping into the middle of a deliberate arrangement.
  if (controls.sortBy === 'health') {
    list.sort((a, b) => {
      const ao = cardOrder(a.name)
      const bo = cardOrder(b.name)
      if (ao !== bo) return ao - bo
      const ap = pinRank(a.name)
      const bp = pinRank(b.name)
      if (ap !== bp) return ap - bp // pinned cards last, even when unhealthy
      const au = a.endpoints.some(latestFailed) ? 1 : 0
      const bu = b.endpoints.some(latestFailed) ? 1 : 0
      if (au !== bu) return bu - au // unhealthy first
      return a.name.localeCompare(b.name)
    })
  } else {
    list.sort((a, b) =>
      cardOrder(a.name) - cardOrder(b.name) ||
      pinRank(a.name) - pinRank(b.name) ||
      a.name.localeCompare(b.name))
  }

  return list
})

const filteredSuites = computed(() => {
  let filtered = [...(suiteStatuses.value || [])]

  if (controls.searchQuery) {
    const query = controls.searchQuery.toLowerCase()
    filtered = filtered.filter(suite =>
      suite.name.toLowerCase().includes(query) ||
      (suite.group && suite.group.toLowerCase().includes(query))
    )
  }

  if (controls.showOnlyFailing) {
    filtered = filtered.filter(suite => {
      if (!suite.results || suite.results.length === 0) return false
      return !suite.results[suite.results.length - 1].success
    })
  }

  if (controls.showRecentFailures) {
    filtered = filtered.filter(suite => {
      if (!suite.results || suite.results.length === 0) return false
      return suite.results.some(result => !result.success)
    })
  }

  return filtered
})

const totalPages = computed(() => {
  return Math.ceil((locations.value.length + filteredSuites.value.length) / itemsPerPage)
})

const paginatedLocations = computed(() => {
  const start = (currentPage.value - 1) * itemsPerPage
  return locations.value.slice(start, start + itemsPerPage)
})

const paginatedSuites = computed(() => {
  const start = (currentPage.value - 1) * itemsPerPage
  return filteredSuites.value.slice(start, start + itemsPerPage)
})

// Balanced column count for the fullscreen grid: pick the layout that fills the
// screen evenly (fewest empty cells, no lone-orphan row, roughly widescreen).
// Bar height is shared across the wall rather than derived per card, so the
// grid needs the densest visible card's row count. Cards report their own; only
// the ones actually on screen are counted, so a hidden or filtered-out card
// cannot leave a stale number behind and squash everything else.
const rowCounts = reactive({})
const noteRowCount = ({ name, count }) => { rowCounts[name] = count }
const maxRowCount = computed(() => {
  const counts = paginatedLocations.value.map((loc) => rowCounts[loc.name] || 0)
  // Floor of 4: a wall made entirely of two-row cards should not inflate its
  // bars to fill the screen.
  return Math.max(4, ...counts)
})

const fsCols = computed(() => {
  const n = locations.value.length
  if (n <= 1) return 1
  const aspect = 16 / 9
  let best = 1, bestScore = Infinity
  for (let c = 1; c <= n; c++) {
    const r = Math.ceil(n / c)
    const empty = c * r - n
    const lastRow = n - (r - 1) * c
    const orphan = (r > 1 && lastRow === 1) ? 1 : 0
    const aspectDiff = Math.abs(Math.log((c / r) / aspect))
    const score = empty + aspectDiff * 3 + orphan * 6
    if (score < bestScore) { bestScore = score; best = c }
  }
  return best
})

const visiblePages = computed(() => {
  const pages = []
  const maxVisible = 5
  let start = Math.max(1, currentPage.value - Math.floor(maxVisible / 2))
  let end = Math.min(totalPages.value, start + maxVisible - 1)

  if (end - start < maxVisible - 1) {
    start = Math.max(1, end - maxVisible + 1)
  }

  for (let i = start; i <= end; i++) {
    pages.push(i)
  }

  return pages
})

const fetchData = async () => {
  const isInitialLoad = endpointStatuses.value.length === 0 && suiteStatuses.value.length === 0
  if (isInitialLoad) {
    loading.value = true
  }
  try {
    const endpointResponse = await fetch(`/api/v1/endpoints/statuses?page=1&pageSize=${resultPageSize}`)
    if (endpointResponse.status === 200) {
      const data = await endpointResponse.json()
      endpointStatuses.value = data
    } else {
      console.error('[Home][fetchData] Error fetching endpoints:', await endpointResponse.text())
    }

    const suiteResponse = await fetch(`/api/v1/suites/statuses?page=1&pageSize=${resultPageSize}`)
    if (suiteResponse.status === 200) {
      const suiteData = await suiteResponse.json()
      suiteStatuses.value = suiteData || []
    } else {
      console.error('[Home][fetchData] Error fetching suites:', await suiteResponse.text())
      if (!suiteStatuses.value) {
        suiteStatuses.value = []
      }
    }
  } catch (error) {
    console.error('[Home][fetchData] Error:', error)
  } finally {
    if (isInitialLoad) {
      loading.value = false
    }
  }
}

const refreshData = () => {
  endpointStatuses.value = [];
  suiteStatuses.value = [];
  fetchData()
}

// Live updates via Server-Sent Events. A single server-side broadcaster pushes
// the same snapshot to every connected browser, so all screens stay in sync
// without polling or refreshing. The browser auto-reconnects on drop.
let eventSource = null
const connectLive = () => {
  try {
    eventSource = new EventSource('/api/v1/live', { withCredentials: true })
    eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        if (Array.isArray(data.endpoints)) endpointStatuses.value = data.endpoints
        suiteStatuses.value = data.suites || []
        loading.value = false
      } catch (err) {
        console.error('[Home][live] Failed to parse live update:', err)
      }
    }
  } catch (err) {
    console.error('[Home][live] Failed to open live stream:', err)
  }
}

const goToPage = (page) => {
  currentPage.value = page
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

const showTooltip = (result, event, action = 'hover') => {
  emit('showTooltip', result, event, action)
}

// Reset to the first page whenever the search query or filters change.
watch(
  () => [controls.searchQuery, controls.showOnlyFailing, controls.showRecentFailures],
  () => { currentPage.value = 1 }
)

// --- Audio alerts on site state changes (down / up / degraded) ---
// Effective status = real ping status, overridden by any active simulation.
let prevStatuses = {}
let statusInitialized = false
const effectiveStatuses = computed(() => {
  const groups = {}
  for (const ep of endpointStatuses.value) {
    if (!groups[ep.name]) groups[ep.name] = []
    groups[ep.name].push(ep)
  }
  const out = {}
  for (const name in groups) {
    const latest = groups[name]
      .map(e => (e.results && e.results.length ? e.results[e.results.length - 1] : null))
      .filter(Boolean)
    if (!latest.length) { out[name] = 'unknown'; continue }
    const up = latest.filter(r => r.success).length
    out[name] = up === 0 ? 'unhealthy' : (up < latest.length ? 'degraded' : 'healthy')
  }
  // Apply simulations on top of the real statuses.
  for (const name in simulations) out[name] = simulations[name]
  return out
})
watch(effectiveStatuses, (cur) => {
  knownLocations.value = Object.keys(cur).sort()
  // Don't sound on the first load — only on real transitions afterwards.
  if (statusInitialized && soundEnabled.value) {
    for (const name in cur) {
      const before = prevStatuses[name]
      const after = cur[name]
      if (before && before !== after && after !== 'unknown') {
        if (after === 'healthy') playUp()
        else if (after === 'unhealthy') playDown()
        else if (after === 'degraded') playDegraded()
      }
    }
  }
  prevStatuses = { ...cur }
  statusInitialized = true
}, { immediate: true })

onMounted(() => {
  fetchData()     // fast initial paint (and fallback if the live stream is unavailable)
  connectLive()   // live, synced updates
  window.addEventListener('gatus:refresh', refreshData)
})

onUnmounted(() => {
  if (eventSource) {
    eventSource.close()
    eventSource = null
  }
  window.removeEventListener('gatus:refresh', refreshData)
})
</script>
