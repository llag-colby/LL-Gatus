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
            :style="{ '--fs-cols': fsCols, '--fs-rows': fsRows }">
            <LocationCard
              v-for="(location, index) in paginatedLocations"
              :key="location.name"
              :name="location.name"
              :endpoints="location.endpoints"
              :maxResults="barsToShow"
              class="motion-rise"
              :style="cardStyle(location, index)"
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

// Fullscreen wall geometry.
//
// Cards report how many rows they drew. That used to feed ONE number, the
// densest card on the wall, which every bar was then sized against. It worked
// while every card had three to six rows. It breaks as soon as one card is much
// denser: Hypervisors draws twelve rows (eleven hosts plus Overall), so
// 50cqh/12 crushed the pills on EVERY card to the floor value, while all the
// text is sized in cqw and did not shrink with it. Tiny pills, full-size text,
// overlapping rows.
//
// So density is per card now, and a dense card is given more of the screen
// instead of being squashed into the same cell as a three-row card. A card with
// R rows spans ceil(R / ROWS_PER_UNIT) grid rows, which keeps rows-per-pixel
// roughly equal across the wall. Because each card is its own size container,
// sizing bars from the card's OWN row count then yields the same physical bar
// height everywhere, which is what the single shared number was reaching for.
const rowCounts = reactive({})
const noteRowCount = ({ name, count }) => { rowCounts[name] = count }

// Rows that fit comfortably in one grid cell. A card denser than this takes
// another cell rather than shrinking its contents.
const ROWS_PER_UNIT = 7

const cardRows = (name) => rowCounts[name] || 0
// Default 6 until a card reports: close to typical, so the first paint is not
// wildly wrong and then jumps.
const cardRowsOrDefault = (name) => cardRows(name) || 6
const cardSpan = (name) => Math.max(1, Math.ceil(cardRowsOrDefault(name) / ROWS_PER_UNIT))

// Style for one card: its own density, and how many grid rows it occupies.
// gridRow only applies in fullscreen, where grid-auto-rows is uniform; the
// normal grid ignores it because rows there are content-sized anyway.
const cardStyle = (location, index) => ({
  '--d': Math.min(index, 14) * 40 + 'ms',
  '--card-rows': cardRowsOrDefault(location.name),
  gridRow: isFullscreen.value ? `span ${cardSpan(location.name)}` : null,
})

// Column count is chosen against total grid UNITS, not card count: a wall of
// sixteen cards where one is double height needs seventeen cells, and picking
// columns for sixteen leaves the last row short and the aspect wrong.
const fsUnits = computed(() =>
  paginatedLocations.value.reduce((sum, loc) => sum + cardSpan(loc.name), 0))

// Columns are chosen so each CARD comes out wider than it is tall.
//
// The old scoring targeted the GRID's aspect at 16/9, which is the wrong thing:
// with seventeen units it picked six columns by three rows, making every card
// 320x355 on a 1080p wall. A card is a list of horizontal rows, so a portrait
// card wastes its width and starves its rows of height, which is most of why
// the wall looked wrong once the card count grew.
//
// CARD_ASPECT is the shape one card should be. 1.8 keeps rows comfortably wide
// without letterboxing them. The empty-cell and orphan terms only break ties.
const CARD_ASPECT = 1.8
const SCREEN_ASPECT = 16 / 9

const fsCols = computed(() => {
  const n = fsUnits.value
  if (n <= 1) return 1
  let best = 1, bestScore = Infinity
  for (let c = 1; c <= n; c++) {
    const r = Math.ceil(n / c)
    const empty = c * r - n
    const lastRow = n - (r - 1) * c
    const orphan = (r > 1 && lastRow === 1) ? 1 : 0
    // Aspect of a single card at this layout, on a 16:9 wall.
    const cardAspect = (SCREEN_ASPECT / c) / (1 / r)
    const aspectDiff = Math.abs(Math.log(cardAspect / CARD_ASPECT))
    const score = empty * 0.6 + aspectDiff * 4 + orphan * 1.5
    if (score < bestScore) { bestScore = score; best = c }
  }
  return best
})

// Grid rows the wall needs, so the container can divide its height evenly and
// nothing gets an implicit extra row that pushes a card off screen.
const fsRows = computed(() => Math.max(1, Math.ceil(fsUnits.value / fsCols.value)))

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
