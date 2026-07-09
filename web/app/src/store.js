import { reactive, ref } from 'vue'

// Reads a value from window.config, ignoring unreplaced Go template placeholders.
function fromConfig(key) {
  if (typeof window === 'undefined' || !window.config) return null
  const value = window.config[key]
  if (!value || (typeof value === 'string' && value.startsWith('{{'))) return null
  return value
}

const savedSort = (typeof localStorage !== 'undefined' && localStorage.getItem('gatus:sort-by')) || fromConfig('defaultSortBy') || 'name'
const savedFilter = (typeof localStorage !== 'undefined' && localStorage.getItem('gatus:filter-by')) || fromConfig('defaultFilterBy') || 'none'
const savedShowAvg = typeof localStorage === 'undefined' || localStorage.getItem('gatus:show-average-response-time') !== 'false'

// Shared dashboard controls. Lives outside the router-view so the header (App.vue)
// and the dashboard (Home.vue) can share the same search / filter / sort state.
export const controls = reactive({
  searchQuery: '',
  filterBy: savedFilter,
  sortBy: savedSort,
  showOnlyFailing: savedFilter === 'failing',
  showRecentFailures: savedFilter === 'unstable',
  groupByGroup: savedSort === 'group',
  showAverageResponseTime: savedShowAvg,
})

// Asks the dashboard to re-fetch its data. Home.vue listens for this event.
export function requestRefresh() {
  window.dispatchEvent(new CustomEvent('gatus:refresh'))
}

// Whether to play the audio alerts when a site changes state.
export const soundEnabled = ref(typeof localStorage === 'undefined' || localStorage.getItem('gatus:sound') !== 'false')
export function setSoundEnabled(value) {
  soundEnabled.value = !!value
  localStorage.setItem('gatus:sound', value ? 'true' : 'false')
}

// --- Outage simulation (client-side test harness; does NOT affect monitoring) ---
// Map of location name -> forced status ('unhealthy' | 'degraded' | 'healthy').
export const simulations = reactive({})
export function setSimulation(name, status) {
  if (status) simulations[name] = status
  else delete simulations[name]
}
export function clearSimulations() {
  Object.keys(simulations).forEach(k => delete simulations[k])
}
// Location names known to the dashboard (populated by Home, used by the panel).
export const knownLocations = ref([])

// Live clock anchored to the SERVER's time, so every browser computes the same
// relative "x ago" labels regardless of its own (possibly wrong) local clock.
let serverOffset = 0
export const now = ref(Date.now())
setInterval(() => { now.value = Date.now() + serverOffset }, 1000)

async function syncServerTime() {
  try {
    const response = await fetch('/api/v1/time', { cache: 'no-store' })
    if (response.ok) {
      const data = await response.json()
      serverOffset = data.time - Date.now()
      now.value = Date.now() + serverOffset
    }
  } catch (e) {
    // non-fatal — fall back to local clock
  }
}
syncServerTime()
setInterval(syncServerTime, 60000)
