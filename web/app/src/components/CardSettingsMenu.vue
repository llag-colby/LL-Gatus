<template>
  <div class="csm">
    <button
      ref="gearEl"
      type="button"
      class="gear"
      :class="{ open }"
      aria-haspopup="dialog"
      :aria-expanded="open ? 'true' : 'false'"
      :aria-label="`Monitoring settings for ${name}`"
      @click="toggleOpen"
    >
      <Settings class="gear-ico" aria-hidden="true" />
    </button>

    <!-- Teleported to body and positioned in viewport coordinates: the card
         clips with overflow in places, and a fixed panel parented elsewhere can
         never be cropped by it. -->
    <Teleport to="body">
      <div
        v-if="open"
        ref="panelEl"
        class="csm-pop"
        role="dialog"
        :aria-labelledby="headingId"
        :aria-describedby="hintId"
        tabindex="-1"
        :style="{ top: `${top}px`, left: `${left}px` }"
        @keydown.esc.stop.prevent="close"
        @keydown.tab="wrapFocus"
      >
        <div class="pop-head">
          <div class="eyebrow">Monitoring</div>
          <div :id="headingId" class="pop-title">{{ name }}</div>
          <p :id="hintId" class="pop-hint">
            Turning a row off stops its checks and alerts. Recorded history is kept.
          </p>
          <p v-if="!allowed" class="pop-locked">Sign in to change monitoring.</p>
        </div>

        <ul class="pop-rows">
          <li v-for="row in rows" :key="row.key" class="pop-row" :class="{ unset: !row.endpointKey }">
            <div class="row-text">
              <span class="row-label">{{ row.label }}</span>
              <span v-if="!row.endpointKey" class="row-note">No endpoint configured</span>
            </div>

            <button
              v-if="row.endpointKey"
              type="button"
              class="sw"
              :class="{ on: isMonitored(row.endpointKey) }"
              role="switch"
              :aria-checked="isMonitored(row.endpointKey) ? 'true' : 'false'"
              :aria-label="`Monitor ${row.label} at ${name}`"
              :disabled="!allowed"
              :aria-disabled="!allowed ? 'true' : 'false'"
              @click="toggleRow(row)"
              @keydown.space.prevent="toggleRow(row)"
              @keydown.enter.prevent="toggleRow(row)"
            >
              <span class="knob" aria-hidden="true"></span>
            </button>
            <span v-else class="sw-empty" aria-hidden="true">—</span>
          </li>
        </ul>

        <div class="pop-foot">
          <!-- Pause all is hidden rather than disabled for viewers. It is the one
               bulk action here, and a dead full-width button under a column of
               dead switches reads as a broken panel; the line above the rows
               already says what is missing and why. -->
          <Button
            v-if="allowed"
            variant="outline"
            size="sm"
            class="w-full text-xs h-8"
            :disabled="configuredRows.length === 0 || busy"
            @click="toggleAll"
          >
            {{ allPaused ? 'Resume all' : 'Pause all' }}
          </Button>
          <div class="foot-note">{{ footNote }}</div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, nextTick, onUnmounted, ref, useId } from 'vue'
import { Settings } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { can, isMonitored, setMonitored } from '@/store'

const props = defineProps({
  name: { type: String, required: true },
  rows: { type: Array, required: true },
})

const headingId = useId()
const hintId = useId()

const open = ref(false)
const busy = ref(false)
const top = ref(0)
const left = ref(0)
const gearEl = ref(null)
const panelEl = ref(null)

const MARGIN = 8
const GAP = 6
const FALLBACK_WIDTH = 250

// Every switch in here writes, so the whole panel needs operator. Reading the
// current state stays open: the panel still says what is running and what is
// paused, it just cannot be changed.
const allowed = computed(() => can('operator'))

const configuredRows = computed(() => props.rows.filter((r) => r && r.endpointKey))
const allPaused = computed(() =>
  configuredRows.value.length > 0 && configuredRows.value.every((r) => !isMonitored(r.endpointKey))
)
const pausedCount = computed(() => configuredRows.value.filter((r) => !isMonitored(r.endpointKey)).length)

const footNote = computed(() => {
  const total = configuredRows.value.length
  if (total === 0) return 'This card has no endpoints to monitor.'
  const paused = pausedCount.value
  if (paused === 0) return `All ${total} checks are running.`
  return `${paused} of ${total} checks paused.`
})

// --- positioning: viewport coordinates, clamped on both axes ---------------
const place = async () => {
  const trigger = gearEl.value
  if (!trigger) return
  await nextTick()
  const rect = trigger.getBoundingClientRect()
  const panel = panelEl.value
  const width = panel ? panel.offsetWidth || FALLBACK_WIDTH : FALLBACK_WIDTH
  const height = panel ? panel.offsetHeight || 0 : 0

  // Right-aligned under the gear (it sits at the card's right edge), flipped
  // above when there isn't room below.
  let nextTop = rect.bottom + GAP
  if (height && nextTop + height > window.innerHeight - MARGIN) {
    const above = rect.top - height - GAP
    nextTop = above >= MARGIN ? above : Math.max(MARGIN, window.innerHeight - height - MARGIN)
  }
  let nextLeft = rect.right - width

  nextLeft = Math.max(MARGIN, Math.min(nextLeft, window.innerWidth - width - MARGIN))
  nextTop = Math.max(MARGIN, Math.min(nextTop, window.innerHeight - (height || 0) - MARGIN))

  // Integer coordinates only — half-pixel offsets soften the text.
  left.value = Math.round(nextLeft)
  top.value = Math.round(nextTop)
}

// --- open / close ----------------------------------------------------------
const focusables = () =>
  panelEl.value
    ? Array.from(panelEl.value.querySelectorAll('button:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'))
    : []

const wrapFocus = (event) => {
  const items = focusables()
  if (items.length === 0) return
  const first = items[0]
  const last = items[items.length - 1]
  const active = document.activeElement
  if (event.shiftKey && (active === first || active === panelEl.value)) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    first.focus()
  }
}

const onDocumentMousedown = (event) => {
  if (panelEl.value && panelEl.value.contains(event.target)) return
  if (gearEl.value && gearEl.value.contains(event.target)) return
  close()
}

const onDocumentKeydown = (event) => {
  if (event.key === 'Escape') close()
}

// Stay anchored while things move; give up and close once the gear has scrolled
// out of view, so the panel never floats free of what it belongs to.
const onViewportChange = () => {
  const rect = gearEl.value && gearEl.value.getBoundingClientRect()
  if (!rect || rect.bottom < 0 || rect.top > window.innerHeight || rect.right < 0 || rect.left > window.innerWidth) {
    close()
    return
  }
  place()
}

const addListeners = () => {
  document.addEventListener('mousedown', onDocumentMousedown)
  document.addEventListener('keydown', onDocumentKeydown)
  window.addEventListener('resize', onViewportChange)
  window.addEventListener('scroll', onViewportChange, true)
}

const removeListeners = () => {
  document.removeEventListener('mousedown', onDocumentMousedown)
  document.removeEventListener('keydown', onDocumentKeydown)
  window.removeEventListener('resize', onViewportChange)
  window.removeEventListener('scroll', onViewportChange, true)
}

const openMenu = async () => {
  open.value = true
  addListeners()
  await place()
  // Measure again now that the panel has real dimensions, then take focus.
  await place()
  if (panelEl.value) panelEl.value.focus()
}

function close() {
  if (!open.value) return
  open.value = false
  removeListeners()
  if (gearEl.value) gearEl.value.focus()
}

const toggleOpen = () => {
  if (open.value) close()
  else openMenu()
}

// Both writers re-check the permission themselves: disabled markup is a hint,
// the guard is what actually stops the request.
const toggleRow = (row) => {
  if (!can('operator') || !row.endpointKey) return
  setMonitored(row.endpointKey, !isMonitored(row.endpointKey))
}

const toggleAll = async () => {
  if (!can('operator')) return
  const monitored = allPaused.value
  busy.value = true
  try {
    for (const row of configuredRows.value) await setMonitored(row.endpointKey, monitored)
  } finally {
    busy.value = false
    place()
  }
}

onUnmounted(removeListeners)
</script>

<style scoped>
.csm { display: inline-flex; }

/* --- gear trigger --- */
.gear {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  border: 1px solid transparent;
  background: transparent;
  color: hsl(var(--muted-foreground));
  cursor: pointer;
}
.gear:hover { color: hsl(var(--foreground)); background: hsl(var(--accent) / 0.6); }
.gear.open { color: hsl(var(--foreground)); background: hsl(var(--accent) / 0.6); border-color: hsl(var(--border)); }
.gear:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }
.gear-ico { width: 15px; height: 15px; }
@media (prefers-reduced-motion: no-preference) {
  .gear { transition: color 0.14s ease, background 0.14s ease; }
}

/* --- popover --- */
.csm-pop {
  position: fixed;
  z-index: 60;
  width: 250px;
  max-width: calc(100vw - 16px);
  background: hsl(var(--card));
  color: hsl(var(--foreground));
  border: 1px solid hsl(var(--border));
  border-radius: 12px;
  box-shadow: 0 10px 30px -8px rgb(0 0 0 / 0.45);
  padding: 0.7rem 0.75rem 0.65rem;
  text-align: left;
}
.csm-pop:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }

.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.pop-title {
  font-size: 0.86rem;
  font-weight: 700;
  line-height: 1.2;
  margin-top: 0.1rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pop-hint {
  margin: 0.25rem 0 0;
  font-size: 0.7rem;
  line-height: 1.35;
  color: hsl(var(--muted-foreground));
}
.pop-locked {
  margin: 0.3rem 0 0;
  font-size: 0.7rem;
  line-height: 1.35;
  font-weight: 600;
  color: hsl(var(--foreground));
}

/* --- rows --- */
.pop-rows {
  list-style: none;
  margin: 0.6rem 0 0;
  padding: 0.5rem 0 0;
  border-top: 1px solid hsl(var(--border));
}
.pop-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  padding: 0.28rem 0;
  min-width: 0;
}
.row-text { display: flex; flex-direction: column; gap: 0.05rem; min-width: 0; }
.row-label {
  font-size: 0.78rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.row-note { font-size: 0.66rem; color: hsl(var(--muted-foreground)); opacity: 0.75; }
.pop-row.unset .row-label { color: hsl(var(--muted-foreground)); }
.sw-empty {
  flex-shrink: 0;
  width: 32px;
  text-align: center;
  font-size: 0.72rem;
  color: hsl(var(--muted-foreground));
  opacity: 0.45;
}

/* --- compact switch (sage on, slate off) --- */
.sw {
  position: relative;
  flex-shrink: 0;
  width: 32px;
  height: 18px;
  padding: 0;
  border: 1px solid hsl(var(--border));
  border-radius: 999px;
  background: hsl(var(--muted));
  cursor: pointer;
}
.sw .knob {
  position: absolute;
  top: 1px;
  left: 1px;
  width: 14px;
  height: 14px;
  border-radius: 999px;
  background: #8a8f98;
  box-shadow: 0 1px 2px rgb(0 0 0 / 0.35);
}
.sw.on { background: rgb(90 160 107 / 0.28); border-color: rgb(90 160 107 / 0.65); }
.sw.on .knob { background: #5aa06b; transform: translateX(14px); }
.sw:hover { border-color: hsl(var(--muted-foreground) / 0.55); }
.sw:disabled { cursor: not-allowed; opacity: 0.5; }
.sw:disabled:hover { border-color: hsl(var(--border)); }
.sw:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }
@media (prefers-reduced-motion: no-preference) {
  .sw { transition: background 0.16s ease, border-color 0.16s ease; }
  .sw .knob { transition: transform 0.16s cubic-bezier(0.22, 1, 0.36, 1), background 0.16s ease; }
}

/* --- footer --- */
.pop-foot {
  margin-top: 0.6rem;
  padding-top: 0.55rem;
  border-top: 1px solid hsl(var(--border));
}
.foot-note {
  margin-top: 0.35rem;
  font-size: 0.66rem;
  color: hsl(var(--muted-foreground));
}
</style>
