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

// Live clock (ticks every second) so relative "x ago" labels update on their
// own between data refreshes instead of sitting frozen.
export const now = ref(Date.now())
setInterval(() => { now.value = Date.now() }, 1000)
