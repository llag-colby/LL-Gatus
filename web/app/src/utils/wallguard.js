// Wall guard: keeps an unattended NOC display alive and honest.
//
// A browser tab left on a monitor for weeks meets every failure eventually:
// the server redeploys, the network blips, the laptop sleeps, a script throws
// and leaves a white page, memory creeps. None of those announce themselves, so
// the page cannot rely on anyone noticing. This module makes the page recover
// on its own:
//
//   - scheduled reload   every RELOAD_HOURS (+ jitter so screens do not all hit
//                        the server in the same second)
//   - version watch      a new build on the server reloads the page onto it
//   - outage recovery    after the server has been unreachable, reload on return
//   - wake recovery      reload after the tab was hidden or the machine slept
//   - blank-page watch   an empty app root reloads the page
//   - loop guard         never more than MAX_RELOADS inside LOOP_WINDOW_MS
//   - screen wake lock   asks the browser not to blank the display
//
// Wall mode (the dashboard's fullscreen layout, minus the controls) turns on
// with ?wall=1 and is remembered, so a screen that reboots comes back to it.
// A page cannot force real browser fullscreen without a click; for that, start
// the browser in kiosk mode (chrome --kiosk <url>), which also survives reloads.
import { ref } from 'vue'

const RELOAD_HOURS = 6
const JITTER_MS = 10 * 60 * 1000
const VERSION_POLL_MS = 60 * 1000
const OUTAGE_POLLS = 3
const WAKE_GAP_MS = 10 * 60 * 1000
const LOOP_WINDOW_MS = 10 * 60 * 1000
const MAX_RELOADS = 3
const WALL_KEY = 'gatus.wall'
const RELOADS_KEY = 'gatus.wall.reloads'

export const wallMode = ref(false)

const safe = (fn, fallback) => { try { return fn() } catch (e) { return fallback } }

const readWallFlag = () => {
  const q = new URLSearchParams(window.location.search)
  if (q.has('wall')) {
    const on = q.get('wall') !== '0' && q.get('wall') !== 'off'
    safe(() => (on ? localStorage.setItem(WALL_KEY, '1') : localStorage.removeItem(WALL_KEY)))
    return on
  }
  return safe(() => localStorage.getItem(WALL_KEY) === '1', false)
}

// Reload unless we have already done so too often: a build that crashes on
// load must degrade to a visible error, not to a reload storm.
const reloadSafely = (why) => {
  const now = Date.now()
  const recent = safe(() => JSON.parse(sessionStorage.getItem(RELOADS_KEY) || '[]'), [])
    .filter(t => now - t < LOOP_WINDOW_MS)
  if (recent.length >= MAX_RELOADS) return false
  recent.push(now)
  safe(() => sessionStorage.setItem(RELOADS_KEY, JSON.stringify(recent)))
  console.info(`[wallguard] reloading: ${why}`)
  window.location.reload()
  return true
}

let installed = false
export const installWallGuard = ({ onWallChange } = {}) => {
  if (installed) return
  installed = true

  wallMode.value = readWallFlag()
  if (onWallChange) onWallChange(wallMode.value)

  // --- scheduled reload ---
  const delay = RELOAD_HOURS * 3600 * 1000 + Math.round((Math.random() * 2 - 1) * JITTER_MS)
  setTimeout(() => reloadSafely(`scheduled ${RELOAD_HOURS}h refresh`), delay)

  // --- version watch and outage recovery ---
  let baseline = null, misses = 0, hadOutage = false
  const poll = async () => {
    try {
      const r = await fetch('/api/v1/version', { cache: 'no-store' })
      if (!r.ok) throw new Error(`status ${r.status}`)
      const { version } = await r.json()
      if (baseline === null) baseline = version
      else if (version && baseline && version !== baseline) reloadSafely('new build deployed')
      if (hadOutage) { hadOutage = false; reloadSafely('server returned after an outage') }
      misses = 0
    } catch (e) {
      misses += 1
      if (misses >= OUTAGE_POLLS) hadOutage = true
    }
  }
  poll()
  setInterval(poll, VERSION_POLL_MS)

  // --- wake recovery ---
  let hiddenAt = 0, lastBeat = Date.now()
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) { hiddenAt = Date.now(); return }
    if (hiddenAt && Date.now() - hiddenAt > WAKE_GAP_MS) reloadSafely('tab was hidden for a long time')
    hiddenAt = 0
  })
  // A timer that fires far later than scheduled means the machine slept.
  setInterval(() => {
    const gap = Date.now() - lastBeat
    lastBeat = Date.now()
    if (gap > WAKE_GAP_MS) reloadSafely('machine woke from sleep')
  }, 30000)

  // --- blank page watch: a script error that unmounts the app leaves nothing ---
  setInterval(() => {
    const root = document.getElementById('app') || document.getElementById('global')
    const blank = !root || root.childElementCount === 0
    if (blank) reloadSafely('app root is empty')
  }, 60000)

  // --- keep the screen on ---
  const holdAwake = async () => {
    try { if ('wakeLock' in navigator) await navigator.wakeLock.request('screen') } catch (e) { /* denied or unsupported */ }
  }
  holdAwake()
  document.addEventListener('visibilitychange', () => { if (!document.hidden) holdAwake() })

  // --- real fullscreen on the first interaction, when wall mode asks for it ---
  if (wallMode.value) {
    const enter = () => {
      if (!document.fullscreenElement) document.getElementById('global')?.requestFullscreen?.().catch(() => {})
    }
    window.addEventListener('pointerdown', enter, { once: true })
    window.addEventListener('keydown', enter, { once: true })
  }
}
