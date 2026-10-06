import { computed, reactive, ref } from 'vue'
import { keyPath } from '@/utils/keys'

// Reads a value from window.config, ignoring unreplaced Go template placeholders.
function fromConfig(key) {
  if (typeof window === 'undefined' || !window.config) return null
  const value = window.config[key]
  if (!value || (typeof value === 'string' && value.startsWith('{{'))) return null
  return value
}

const savedSort = (typeof localStorage !== 'undefined' && localStorage.getItem('gatus:sort-by')) || fromConfig('defaultSortBy') || 'name'
const savedShowAvg = typeof localStorage === 'undefined' || localStorage.getItem('gatus:show-average-response-time') !== 'false'

// Shared dashboard controls. Lives outside the router-view so the header (App.vue)
// and the dashboard (Home.vue) share the same search / sort state. Health
// filtering was removed from the UI (kept simple), so it always defaults off.
export const controls = reactive({
  searchQuery: '',
  filterBy: 'none',
  sortBy: savedSort,
  showOnlyFailing: false,
  showRecentFailures: false,
  groupByGroup: savedSort === 'group',
  showAverageResponseTime: savedShowAvg,
})

// Asks the dashboard to re-fetch its data. Home.vue listens for this event.
export function requestRefresh() {
  window.dispatchEvent(new CustomEvent('gatus:refresh'))
}

// --- Toast notifications (bottom-right; rendered by ToastContainer) ---
export const toasts = reactive([])
let toastSeq = 0
export function addToast(message, type = 'info', timeout = 3200) {
  const id = ++toastSeq
  toasts.push({ id, message, type })
  if (timeout) setTimeout(() => removeToast(id), timeout)
  return id
}
export function removeToast(id) {
  const i = toasts.findIndex(t => t.id === id)
  if (i !== -1) toasts.splice(i, 1)
}

// Whether to play the audio alerts when a site changes state.
export const soundEnabled = ref(typeof localStorage === 'undefined' || localStorage.getItem('gatus:sound') !== 'false')
export function setSoundEnabled(value) {
  soundEnabled.value = !!value
  localStorage.setItem('gatus:sound', value ? 'true' : 'false')
}

// --- Customizable status colors (up / degraded / down) ---
// Drives the CSS variables --status-up/-degraded/-down, which every status bar,
// dot, badge and KPI reads. Persisted to localStorage so it survives refresh.
export const STATUS_COLOR_DEFAULTS = { up: '#22c55e', degraded: '#f59e0b', down: '#ef4444' }
function loadStatusColors() {
  try {
    const saved = JSON.parse(localStorage.getItem('gatus:colors') || '{}')
    return { ...STATUS_COLOR_DEFAULTS, ...saved }
  } catch (e) {
    return { ...STATUS_COLOR_DEFAULTS }
  }
}
export const statusColors = reactive(loadStatusColors())
export function applyStatusColors() {
  if (typeof document === 'undefined') return
  const r = document.documentElement
  r.style.setProperty('--status-up', statusColors.up)
  r.style.setProperty('--status-degraded', statusColors.degraded)
  r.style.setProperty('--status-down', statusColors.down)
}
export function setStatusColor(key, value) {
  if (!(key in statusColors)) return
  statusColors[key] = value
  localStorage.setItem('gatus:colors', JSON.stringify({ ...statusColors }))
  applyStatusColors()
}
export function resetStatusColors() {
  Object.assign(statusColors, STATUS_COLOR_DEFAULTS)
  localStorage.setItem('gatus:colors', JSON.stringify({ ...statusColors }))
  applyStatusColors()
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

// Whether the app is in fullscreen wall-display mode. Set by App.vue on
// fullscreenchange; read by Home.vue to render fewer, thicker status bars
// (a wall reads better with chunky bars than a wall of thin ticks).
export const isFullscreen = ref(false)

// Dashboard layout view (independent of fullscreen):
//   'vertical'   — every location card full-width, stacked in one column (default)
//   'horizontal' — the multi-column grid flowing across the screen
export const dashboardView = ref(
  (typeof localStorage !== 'undefined' && localStorage.getItem('gatus:view')) || 'vertical'
)
export function setDashboardView(value) {
  dashboardView.value = value === 'horizontal' ? 'horizontal' : 'vertical'
  if (typeof localStorage !== 'undefined') localStorage.setItem('gatus:view', dashboardView.value)
}

// --- UniFi snapshots (the Firewall and Wireless rows) ---
// An external-endpoint can only carry a pass/fail, so the collector pushes the
// detail behind those two rows to a side channel. One request serves every card
// on the page, which is why it lives here rather than inside LocationCard.
// Keyed by endpoint key, e.g. "firewall_decatur-gmc".
export const unifiSnapshots = ref({})
export async function refreshUniFiSnapshots() {
  try {
    const response = await fetch('/api/v1/unifi', { cache: 'no-store' })
    if (response.ok) unifiSnapshots.value = await response.json()
  } catch (e) {
    // non-fatal — keep the last snapshot rather than blanking the rows
  }
}
refreshUniFiSnapshots()
setInterval(refreshUniFiSnapshots, 20000)

// --- Monitoring pause switches ---
// Per-endpoint "monitor this or not", persisted server-side to /data so it
// survives updates and applies to every browser. Pausing is real, not cosmetic:
// the backend stops running the check, stops accepting collector pushes for it,
// and stops alerting. Recorded history is kept, so un-pausing resumes the same
// timeline rather than starting a new one.
export const monitoringDisabled = ref(new Set())

export function isMonitored(key) {
  return !!key && !monitoringDisabled.value.has(key)
}

// Bumped by every local flip. A background poll that started before a flip is
// stale by the time it lands, and applying it would visibly snap the switch back
// for up to a full poll interval — so a poll only writes if nothing changed
// underneath it while it was in flight.
let monitoringGeneration = 0

export async function refreshMonitoring() {
  const startedAt = monitoringGeneration
  try {
    const response = await fetch('/api/v1/monitoring', { cache: 'no-store' })
    if (response.ok) {
      const data = await response.json()
      if (monitoringGeneration === startedAt) {
        monitoringDisabled.value = new Set(data.disabled || [])
      }
    }
  } catch (e) {
    // non-fatal — keep the switches we already know about
  }
}

// Flips one endpoint. Optimistic so the toggle feels instant, reverted if the
// server disagrees.
export async function setMonitored(key, monitored) {
  if (!key) return
  monitoringGeneration++
  const before = new Set(monitoringDisabled.value)
  const next = new Set(before)
  monitored ? next.delete(key) : next.add(key)
  monitoringDisabled.value = next
  try {
    // keyPath, not encodeURIComponent: this route resolves the key against
    // config.yaml and reads the raw path param, so a %3A from an SMB key
    // ("l:_smb-shares") matches nothing and the pause silently 404s.
    const response = await fetch(`/api/v1/monitoring/${keyPath(key)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ monitored: !!monitored }),
    })
    if (!response.ok) throw new Error(String(response.status))
    const data = await response.json()
    const confirmed = new Set(monitoringDisabled.value)
    data.monitored ? confirmed.delete(key) : confirmed.add(key)
    monitoringDisabled.value = confirmed
    addToast(`${monitored ? 'Resumed' : 'Paused'} monitoring for ${key}`, monitored ? 'success' : 'info')
  } catch (e) {
    monitoringDisabled.value = before
    addToast(`Couldn't change monitoring for ${key}`, 'error')
  }
}

refreshMonitoring()
setInterval(refreshMonitoring, 30000)

// --- Dashboard layout ---
// Which cards and rows are shown, in what order, under what name. Persisted
// server-side to /data so every screen shows the same arranged dashboard: the
// wallboard, the TV and a laptop all agree. Per-viewer taste (colours, sound,
// refresh interval) stays in localStorage instead, because that genuinely is
// per-screen.
//
// PRESENTATION ONLY. Hiding a row hides the row; it does not stop the check, and
// a hidden row that goes down still turns its card red. setMonitored above is
// the control that actually stops something.
export const layout = ref({ cards: {} })

let layoutGeneration = 0

function cardEntry(cardName) {
  return (layout.value.cards && layout.value.cards[cardName]) || {}
}

function rowEntry(cardName, rowKey) {
  return (cardEntry(cardName).rows && cardEntry(cardName).rows[rowKey]) || {}
}

export function isCardHidden(cardName) {
  return !!cardEntry(cardName).hidden
}

export function isRowHidden(cardName, rowKey) {
  return !!rowEntry(cardName, rowKey).hidden
}

// An override wins; otherwise the caller's derived default stands.
export function cardTitleFor(cardName) {
  return cardEntry(cardName).title || cardName
}

export function rowLabelFor(cardName, rowKey, derived) {
  return rowEntry(cardName, rowKey).label || derived
}

// Unarranged cards and rows sort after arranged ones and keep whatever order
// the caller already had, so a newly discovered site lands at the end rather
// than jumping into the middle of a deliberate arrangement.
const UNARRANGED = 1e6

export function cardOrder(cardName) {
  const order = cardEntry(cardName).order
  return typeof order === 'number' ? order : UNARRANGED
}

export function rowOrder(cardName, rowKey) {
  const order = rowEntry(cardName, rowKey).order
  return typeof order === 'number' ? order : UNARRANGED
}

export async function refreshLayout() {
  const startedAt = layoutGeneration
  try {
    const response = await fetch('/api/v1/layout', { cache: 'no-store' })
    if (!response.ok) return
    const data = await response.json()
    if (layoutGeneration === startedAt) {
      layout.value = { cards: data.cards || {} }
    }
  } catch (e) {
    // non-fatal - keep the arrangement we already have
  }
}

// Every mutation goes through here: apply locally so the change is instant, then
// persist the whole document. Reverted with a toast if the server refuses, so
// the screen never claims an arrangement that did not save.
async function commitLayout(next, description) {
  layoutGeneration++
  const before = layout.value
  layout.value = next
  try {
    const response = await fetch('/api/v1/layout', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ cards: next.cards }),
    })
    if (!response.ok) throw new Error(String(response.status))
    const data = await response.json()
    layout.value = { cards: data.cards || {} }
  } catch (e) {
    layout.value = before
    addToast(`Couldn't save the layout${description ? ' - ' + description : ''}`, 'error')
  }
}

// Structured-clone the document before mutating so an optimistic update that
// has to be rolled back restores the previous object rather than one that was
// edited in place underneath it.
function draft() {
  const cards = {}
  for (const [name, card] of Object.entries(layout.value.cards || {})) {
    cards[name] = { ...card, rows: { ...(card.rows || {}) } }
  }
  return { cards }
}

function ensureCard(next, cardName) {
  if (!next.cards[cardName]) next.cards[cardName] = { rows: {} }
  if (!next.cards[cardName].rows) next.cards[cardName].rows = {}
  return next.cards[cardName]
}

export function setCardHidden(cardName, hidden) {
  const next = draft()
  ensureCard(next, cardName).hidden = !!hidden
  return commitLayout(next, `${hidden ? 'hiding' : 'showing'} ${cardName}`)
}

export function setRowHidden(cardName, rowKey, hidden) {
  const next = draft()
  const card = ensureCard(next, cardName)
  card.rows[rowKey] = { ...(card.rows[rowKey] || {}), hidden: !!hidden }
  return commitLayout(next, `${hidden ? 'hiding' : 'showing'} a row on ${cardName}`)
}

export function setCardTitle(cardName, title) {
  const next = draft()
  ensureCard(next, cardName).title = (title || '').trim()
  return commitLayout(next, `renaming ${cardName}`)
}

export function setRowLabel(cardName, rowKey, label) {
  const next = draft()
  const card = ensureCard(next, cardName)
  card.rows[rowKey] = { ...(card.rows[rowKey] || {}), label: (label || '').trim() }
  return commitLayout(next, `renaming a row on ${cardName}`)
}

// Order is rewritten as a dense 0..n-1 sequence from the list the caller shows,
// so there are no gaps to reason about and a reorder cannot collide with an
// unarranged entry sitting at UNARRANGED.
export function setCardOrder(cardNames) {
  const next = draft()
  cardNames.forEach((name, i) => { ensureCard(next, name).order = i })
  return commitLayout(next, 'reordering the dashboard')
}

export function setRowOrder(cardName, rowKeys) {
  const next = draft()
  const card = ensureCard(next, cardName)
  rowKeys.forEach((key, i) => { card.rows[key] = { ...(card.rows[key] || {}), order: i } })
  return commitLayout(next, `reordering ${cardName}`)
}

export async function resetLayout() {
  layoutGeneration++
  const before = layout.value
  layout.value = { cards: {} }
  try {
    const response = await fetch('/api/v1/layout', { method: 'DELETE' })
    if (!response.ok) throw new Error(String(response.status))
    addToast('Dashboard layout reset', 'success')
  } catch (e) {
    layout.value = before
    addToast("Couldn't reset the layout", 'error')
  }
}

refreshLayout()
setInterval(refreshLayout, 30000)

// --- Shared history time range ---
// One selection drives every drill-down, so moving from a site to an endpoint to
// the phone panel keeps you in the same window instead of resetting to a default
// on each page. Persisted, so it also survives a refresh.
//
// The list is constrained to what the backend actually serves: /v1/history and
// /v1/endpoints/:key/uptime-series both accept exactly these five.
export const HISTORY_RANGES = [
  { value: '1h', label: '1h', ms: 3600000 },
  { value: '6h', label: '6h', ms: 6 * 3600000 },
  { value: '24h', label: '24h', ms: 24 * 3600000 },
  { value: '7d', label: '7d', ms: 7 * 24 * 3600000 },
  { value: '30d', label: '30d', ms: 30 * 24 * 3600000 },
]

const savedRange = typeof localStorage !== 'undefined' && localStorage.getItem('gatus:history-range')
export const historyRange = ref(
  HISTORY_RANGES.some(r => r.value === savedRange) ? savedRange : '24h'
)

export function setHistoryRange(value) {
  if (!HISTORY_RANGES.some(r => r.value === value)) return
  historyRange.value = value
  if (typeof localStorage !== 'undefined') localStorage.setItem('gatus:history-range', value)
}

// Milliseconds covered by a range value, for callers that need a window start.
export function historyRangeMs(value) {
  const match = HISTORY_RANGES.find(r => r.value === value)
  return match ? match.ms : 24 * 3600000
}

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

// --- Sign-in, session and roles (DISABLED) ---
// The dashboard has no sign-in. The server no longer serves /api/v1/auth/*, so
// none of the calls below can succeed; they are kept so turning accounts back on
// is a matter of routing them again rather than rewriting the client. The only
// importers left are LoginDialog.vue and UserMenu.vue, which are themselves
// unreferenced and so never reach the bundle. refreshAuth() is no longer run at
// module load, which is what used to make every page start with a fetch.
//
// Roles are ordered: viewer < operator < admin.
export const ROLES = ['viewer', 'operator', 'admin']

export const authState = reactive({
  enabled: false,
  authenticated: false,
  user: null,
})

// Pulls the server's message out of an error body so the UI can show what the
// server actually said, falling back to something actionable when it says
// nothing useful.
async function authErrorMessage(response, fallback) {
  try {
    const data = await response.json()
    if (data && typeof data.error === 'string' && data.error.trim()) return data.error
  } catch (e) {
    // no JSON body — use the fallback
  }
  return fallback
}

// Never throws: a failed poll leaves the last known session in place rather
// than blanking the header.
export async function refreshAuth() {
  try {
    const response = await fetch('/api/v1/auth/me', { cache: 'no-store', credentials: 'include' })
    if (!response.ok) return
    const data = await response.json()
    authState.enabled = !!data.enabled
    authState.authenticated = !!data.authenticated
    authState.user = data.user || null
  } catch (e) {
    // non-fatal — keep the session we already know about
  }
}

export async function login(username, password) {
  let response
  try {
    response = await fetch('/api/v1/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ username, password }),
    })
  } catch (e) {
    throw new Error('Could not reach the server. Check your connection and try again.')
  }
  if (!response.ok) {
    throw new Error(await authErrorMessage(response, 'Sign-in failed. Check your username and password.'))
  }
  await refreshAuth()
}

export async function logout() {
  try {
    await fetch('/api/v1/auth/logout', { method: 'POST', credentials: 'include' })
  } catch (e) {
    // non-fatal — the refresh below settles the real state
  }
  await refreshAuth()
}

export async function changePassword(current, next) {
  let response
  try {
    response = await fetch('/api/v1/auth/password', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ currentPassword: current, newPassword: next }),
    })
  } catch (e) {
    throw new Error('Could not reach the server. Check your connection and try again.')
  }
  if (!response.ok) {
    throw new Error(await authErrorMessage(response, 'Could not change the password. Try again.'))
  }
}

// Anonymous visitors read as 'viewer', which is also what every control checks
// against, so there is one code path instead of two.
export const currentRole = computed(() => {
  const role = authState.authenticated && authState.user ? authState.user.role : null
  return ROLES.includes(role) ? role : 'viewer'
})

// The single permission helper, now a constant. Every visitor is anonymous and
// every control is theirs to use. It stays exported because it is the one hook
// a future gate would need, and because returning true here is what keeps the
// behaviour identical to a deployment that never had accounts.
//
// It takes no argument on purpose: a leftover can('operator') would otherwise
// read as a gate while granting everything. There are no call sites left.
export function can() {
  return true
}

// ---------------------------------------------------------------------------
// Endpoint targets (runtime overrides)
//
// config.yaml is baked into the image, so re-pointing a check used to mean a
// rebuild and a redeploy. The server keeps an override in /data instead and the
// watchdog picks it up on the next tick — see api/endpoint_targets.go.
//
// These are the ONLY write calls in this app that carry a credential. The token
// is per-viewer taste in the same sense the refresh interval is: it belongs to
// whoever is sitting at the browser, not to the dashboard, so it lives in
// localStorage and never goes to the layout document where every screen would
// inherit it.
// ---------------------------------------------------------------------------
export const endpointTargets = ref({})
export const targetEditingEnabled = ref(false)
// Whether the answer above is known yet. Without this the menu renders
// "editing is off" for the moment between opening the tab and the fetch
// landing, which reads as a broken feature rather than a pending request.
export const targetsLoaded = ref(false)

const EDIT_TOKEN_KEY = 'gatus:edit-token'
export const editToken = ref(
  (typeof localStorage !== 'undefined' && localStorage.getItem(EDIT_TOKEN_KEY)) || ''
)
export function setEditToken(value) {
  const next = (value || '').trim()
  editToken.value = next
  try {
    if (next) localStorage.setItem(EDIT_TOKEN_KEY, next)
    else localStorage.removeItem(EDIT_TOKEN_KEY)
  } catch (e) {
    // private browsing or blocked storage — the token still works this session
  }
}

export async function refreshEndpointTargets() {
  try {
    const response = await fetch('/api/v1/endpoints/targets', { cache: 'no-store' })
    if (!response.ok) return
    const data = await response.json()
    targetEditingEnabled.value = !!data.editingEnabled
    const next = {}
    for (const target of data.targets || []) next[target.key] = target
    endpointTargets.value = next
  } catch (e) {
    // keep whatever we had; the menu shows the last known targets
  } finally {
    targetsLoaded.value = true
  }
}

export function targetFor(key) {
  return endpointTargets.value[key] || null
}

// Returns { ok } on success, or { ok: false, status, message } so the caller can
// tell "wrong token" apart from "that is not a valid address" and say so.
async function writeTarget(key, method, body) {
  if (!editToken.value) return { ok: false, status: 401, message: 'An edit token is required.' }
  try {
    // Same reason as setMonitored: the target routes validate the key against
    // config.yaml, so the colon in an SMB key must survive the round trip.
    const response = await fetch(`/api/v1/endpoints/${keyPath(key)}/target`, {
      method,
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${editToken.value}`,
      },
      body: body ? JSON.stringify(body) : undefined,
    })
    if (!response.ok) {
      return { ok: false, status: response.status, message: (await response.text()) || 'Request failed.' }
    }
    const updated = await response.json()
    endpointTargets.value = { ...endpointTargets.value, [updated.key]: updated }
    return { ok: true }
  } catch (e) {
    return { ok: false, status: 0, message: 'Could not reach the server.' }
  }
}

export const setEndpointTarget = (key, url) => writeTarget(key, 'PATCH', { url })
export const clearEndpointTarget = (key) => writeTarget(key, 'DELETE', null)
