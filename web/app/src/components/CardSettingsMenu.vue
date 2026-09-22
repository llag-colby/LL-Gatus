<template>
  <div class="csm">
    <button
      ref="gearEl"
      type="button"
      class="gear"
      :class="{ open }"
      aria-haspopup="dialog"
      :aria-expanded="open ? 'true' : 'false'"
      :aria-label="`Settings for ${name}`"
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
          <div :id="headingId" class="pop-title">{{ title }}</div>
          <!-- Two tabs, not one merged list. Pausing stops a check for everyone;
               hiding only stops drawing it. Those must never look like the same
               switch. -->
          <div class="tabs" role="tablist">
            <button
              type="button" class="tab" :class="{ on: tab === 'monitoring' }"
              role="tab" :aria-selected="tab === 'monitoring'"
              @click="tab = 'monitoring'"
            >Monitoring</button>
            <button
              type="button" class="tab" :class="{ on: tab === 'layout' }"
              role="tab" :aria-selected="tab === 'layout'"
              @click="tab = 'layout'"
            >Layout</button>
            <button
              type="button" class="tab" :class="{ on: tab === 'target' }"
              role="tab" :aria-selected="tab === 'target'"
              @click="showTargets"
            >Target</button>
          </div>
          <p :id="hintId" class="pop-hint">
            <template v-if="tab === 'monitoring'">
              Turning a row off stops its checks and alerts. Recorded history is kept.
            </template>
            <template v-else-if="tab === 'layout'">
              Hiding only stops a row being drawn. It is still checked, still alerts, and
              still counts towards this card's status. Everyone sees the same layout.
            </template>
            <template v-else>
              Re-points a check at a different address. It applies on that check's next
              run, and the recorded history carries straight on - the address is not part
              of what identifies an endpoint.
            </template>
          </p>
        </div>

        <ul v-if="tab === 'monitoring'" class="pop-rows">
          <li v-for="row in rows" :key="row.key" class="pop-row" :class="{ unset: !keysOf(row).length }">
            <div class="row-text">
              <span class="row-label">{{ row.label }}</span>
              <span v-if="!keysOf(row).length" class="row-note">No endpoint configured</span>
              <span v-else-if="keysOf(row).length > 1" class="row-note">
                {{ keysOf(row).length }} checks
              </span>
            </div>

            <button
              v-if="keysOf(row).length"
              type="button"
              class="sw"
              :class="{ on: rowMonitored(row) }"
              role="switch"
              :aria-checked="rowMonitored(row) ? 'true' : 'false'"
              :aria-label="`Monitor ${row.label} at ${name}`"
              @click="toggleRow(row)"
              @keydown.space.prevent="toggleRow(row)"
              @keydown.enter.prevent="toggleRow(row)"
            >
              <span class="knob" aria-hidden="true"></span>
            </button>
            <span v-else class="sw-empty" aria-hidden="true">—</span>
          </li>
        </ul>

        <ul v-else-if="tab === 'layout'" class="pop-rows">
          <li v-for="(row, i) in rows" :key="row.key" class="pop-row lay-row" :class="{ off: row.hidden }">
            <div class="row-text">
              <input
                v-if="renamingKey === row.key"
                ref="renameEl"
                v-model="renameValue"
                class="row-rename"
                type="text"
                :maxlength="60"
                :aria-label="`Name for the ${row.label} row`"
                @keydown.enter.prevent="commitRename(row)"
                @keydown.esc.stop.prevent="cancelRename"
                @blur="commitRename(row)"
              />
              <button
                v-else
                type="button"
                class="row-label row-rename-trigger"
                :aria-label="`Rename the ${row.label} row`"
                @click="startRename(row)"
              >{{ row.label }}</button>
            </div>

            <div class="lay-actions">
              <button
                type="button" class="ico" :disabled="i === 0 || busy"
                :aria-label="`Move ${row.label} up`" @click="move(i, -1)"
              ><ChevronUp class="ico-svg" aria-hidden="true" /></button>
              <button
                type="button" class="ico" :disabled="i === rows.length - 1 || busy"
                :aria-label="`Move ${row.label} down`" @click="move(i, 1)"
              ><ChevronDown class="ico-svg" aria-hidden="true" /></button>
              <button
                type="button" class="ico" :disabled="busy"
                :aria-label="row.hidden ? `Show ${row.label}` : `Hide ${row.label}`"
                :data-tooltip="row.hidden ? 'Hidden' : 'Visible'"
                @click="toggleHidden(row)"
              >
                <EyeOff v-if="row.hidden" class="ico-svg" aria-hidden="true" />
                <Eye v-else class="ico-svg" aria-hidden="true" />
              </button>
            </div>
          </li>
        </ul>

        <div v-else class="tgt-panel">
          <p v-if="!targetEditingEnabled" class="tgt-off">
            Editing is off on the server. Set <code>GATUS_EDIT_TOKEN</code> in
            <code>.env</code> and restart to turn it on.
          </p>
          <template v-else>
            <!-- The token stays in this browser. It is the only credential the
                 dashboard holds, and it gates writes only - everything on the
                 page is readable without it. -->
            <label class="tgt-token">
              <span>Edit token</span>
              <input
                type="password" autocomplete="off" spellcheck="false"
                placeholder="required to save"
                :value="editToken"
                @input="onToken"
              />
            </label>
            <ul class="pop-rows">
              <li v-for="t in targetRows" :key="t.key" class="tgt-row">
                <div class="tgt-head">
                  <span class="row-label">{{ t.label }}</span>
                  <span v-if="!t.view" class="row-note">Not reported by the server</span>
                  <span v-else-if="!t.view.editable" class="row-note">URL hidden for this endpoint</span>
                  <span v-else-if="t.view.overridden" class="row-note over">
                    config says {{ t.view.configured }}
                  </span>
                </div>
                <div class="tgt-edit">
                  <input
                    class="tgt-input" type="text" spellcheck="false" autocomplete="off"
                    :disabled="!t.view || !t.view.editable || busy"
                    :value="draftFor(t.key)"
                    :aria-label="`Target for ${t.label}`"
                    @input="setDraft(t.key, $event.target.value)"
                    @keydown.enter.prevent="saveTarget(t)"
                  />
                  <button
                    type="button" class="tgt-btn"
                    :disabled="!t.view || !t.view.editable || !isDirty(t.key) || busy"
                    @click="saveTarget(t)"
                  >Save</button>
                  <button
                    v-if="t.view && t.view.overridden"
                    type="button" class="tgt-btn ghost" :disabled="busy"
                    @click="resetTarget(t)"
                  >Reset</button>
                </div>
                <p v-if="errorFor(t.key)" class="tgt-err">{{ errorFor(t.key) }}</p>
              </li>
              <li v-if="!targetRows.length" class="tgt-row">
                <span class="row-note">No configured endpoints on this card.</span>
              </li>
            </ul>
          </template>
        </div>

        <div class="pop-foot">
          <template v-if="tab === 'monitoring'">
            <Button
              variant="outline"
              size="sm"
              class="w-full text-xs h-8"
              :disabled="configuredRows.length === 0 || busy"
              @click="toggleAll"
            >
              {{ allPaused ? 'Resume all' : 'Pause all' }}
            </Button>
            <div class="foot-note">{{ footNote }}</div>
          </template>
          <template v-else-if="tab === 'layout'">
            <div class="foot-pair">
              <Button variant="outline" size="sm" class="flex-1 text-xs h-8" :disabled="busy" @click="hideCard">
                Hide this card
              </Button>
              <Button variant="ghost" size="sm" class="text-xs h-8 text-muted-foreground" :disabled="busy" @click="resetCard">
                Reset
              </Button>
            </div>
            <div class="foot-note">{{ layoutFootNote }}</div>
          </template>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, nextTick, onUnmounted, ref, useId } from 'vue'
import { ChevronDown, ChevronUp, Eye, EyeOff, Settings } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import {
  isMonitored, setMonitored,
  cardTitleFor, setCardHidden, setRowHidden, setRowLabel, setRowOrder, setCardTitle,
  endpointTargets, targetEditingEnabled, editToken, setEditToken,
  refreshEndpointTargets, setEndpointTarget, clearEndpointTarget,
} from '@/store'

const props = defineProps({
  name: { type: String, required: true },
  rows: { type: Array, required: true },
})

const headingId = useId()
const hintId = useId()

const open = ref(false)
const busy = ref(false)
const tab = ref('monitoring')
const renamingKey = ref(null)
const renameValue = ref('')
const renameEl = ref(null)
const top = ref(0)
const left = ref(0)
const gearEl = ref(null)
const panelEl = ref(null)

const MARGIN = 8
const GAP = 6
const FALLBACK_WIDTH = 250

// A row may stand for several endpoints — DNS is one query per resolver but a
// single row on the card, so it gets a single switch here that moves all of
// them together. Rows carrying a lone endpointKey keep working unchanged.
const keysOf = (row) => (row && row.endpointKeys) || (row && row.endpointKey ? [row.endpointKey] : [])

// --- target editing ------------------------------------------------------
// Drafts are held here rather than bound straight to the store so a half-typed
// address never becomes the value the rest of the UI believes in, and so a
// failed save leaves what the operator typed on screen to be corrected.
const drafts = ref({})
const errors = ref({})

const showTargets = () => {
  tab.value = 'target'
  // Fetched on demand. These are the only addresses the dashboard reads, and
  // most visits to this menu are about pausing or hiding, not re-pointing.
  refreshEndpointTargets()
}

// One editor per ENDPOINT, not per row. A row can stand for several checks -
// DNS is one row and three resolvers - and each of those has its own address.
const targetRows = computed(() =>
  props.rows.flatMap((row) => {
    const keys = keysOf(row)
    return keys.map((key) => {
      const view = endpointTargets.value[key] || null
      return {
        key,
        view,
        label: keys.length > 1 && view ? `${row.label} · ${view.group}` : row.label,
      }
    })
  })
)

const effectiveOf = (key) => (endpointTargets.value[key] || {}).effective || ''
const draftFor = (key) => (key in drafts.value ? drafts.value[key] : effectiveOf(key))
const isDirty = (key) => draftFor(key).trim() !== effectiveOf(key)
const errorFor = (key) => errors.value[key] || ''

const clearError = (key) => {
  if (!errors.value[key]) return
  const next = { ...errors.value }
  delete next[key]
  errors.value = next
}
const setDraft = (key, value) => {
  drafts.value = { ...drafts.value, [key]: value }
  clearError(key)
}
const dropDraft = (key) => {
  const next = { ...drafts.value }
  delete next[key]
  drafts.value = next
}
const onToken = (event) => {
  setEditToken(event.target.value)
  errors.value = {}
}

// The server's message is shown verbatim: it is the only thing that can tell
// "that is not a valid address" apart from "wrong token", and paraphrasing it
// would lose the distinction.
const applyResult = async (key, result) => {
  if (result.ok) {
    dropDraft(key)
    clearError(key)
    return
  }
  errors.value = {
    ...errors.value,
    [key]: result.status === 401 ? 'Token rejected. Check the edit token above.' : result.message,
  }
}

const saveTarget = (t) => {
  if (!t.view || !t.view.editable || !isDirty(t.key)) return
  return withBusy(async () => applyResult(t.key, await setEndpointTarget(t.key, draftFor(t.key).trim())))
}
const resetTarget = (t) =>
  withBusy(async () => applyResult(t.key, await clearEndpointTarget(t.key)))

const configuredRows = computed(() => props.rows.filter((r) => keysOf(r).length > 0))
// Counts are per CHECK, not per row: "2 of 7 checks paused" has to stay true
// when one of those rows is three checks wearing one switch.
const configuredKeys = computed(() => configuredRows.value.flatMap(keysOf))
// A grouped switch reads ON only when every check behind it is running, so a
// partial pause is visible rather than hidden behind a lit switch.
const rowMonitored = (row) => {
  const keys = keysOf(row)
  return keys.length > 0 && keys.every((k) => isMonitored(k))
}
const allPaused = computed(() =>
  configuredKeys.value.length > 0 && configuredKeys.value.every((k) => !isMonitored(k))
)
const pausedCount = computed(() => configuredKeys.value.filter((k) => !isMonitored(k)).length)

const footNote = computed(() => {
  const total = configuredKeys.value.length
  if (total === 0) return 'This card has no endpoints to monitor.'
  const paused = pausedCount.value
  if (paused === 0) return `All ${total} checks are running.`
  return `${paused} of ${total} checks paused.`
})

// --- layout actions --------------------------------------------------------
// All of these write the shared arrangement, so they are optimistic in the store
// and roll back with a toast if the save fails.

const title = computed(() => cardTitleFor(props.name))

const hiddenCount = computed(() => props.rows.filter((r) => r.hidden).length)
const layoutFootNote = computed(() => {
  const total = props.rows.length
  if (total === 0) return 'This card has no rows to arrange.'
  const hidden = hiddenCount.value
  if (hidden === 0) return `All ${total} rows are shown.`
  return `${hidden} of ${total} rows hidden.`
})

const withBusy = async (fn) => {
  busy.value = true
  try {
    await fn()
  } finally {
    busy.value = false
    place()
  }
}

const toggleHidden = (row) => withBusy(() => setRowHidden(props.name, row.key, !row.hidden))

// Order is written as the full sequence rather than a pair of swaps, so the
// stored arrangement is always dense and complete for this card.
const move = (index, delta) => {
  const target = index + delta
  if (target < 0 || target >= props.rows.length) return
  const keys = props.rows.map((r) => r.key)
  const [moved] = keys.splice(index, 1)
  keys.splice(target, 0, moved)
  return withBusy(() => setRowOrder(props.name, keys))
}

const startRename = async (row) => {
  renamingKey.value = row.key
  renameValue.value = row.label
  await nextTick()
  const el = Array.isArray(renameEl.value) ? renameEl.value[0] : renameEl.value
  el?.focus()
  el?.select()
}

const cancelRename = () => {
  renamingKey.value = null
  renameValue.value = ''
}

const commitRename = (row) => {
  if (renamingKey.value !== row.key) return
  const next = renameValue.value.trim()
  cancelRename()
  // Unchanged, or cleared back to the derived label: an empty override means
  // "use whatever the config implies", which is how you undo a rename.
  if (next === row.label) return
  return withBusy(() => setRowLabel(props.name, row.key, next))
}

const hideCard = () => withBusy(async () => {
  await setCardHidden(props.name, true)
  close()
})

// Clears this card's overrides only: its title, and every row's label, order and
// hidden flag. The rest of the dashboard keeps its arrangement.
const resetCard = () => withBusy(async () => {
  await setCardTitle(props.name, '')
  await setRowOrder(props.name, [])
  await Promise.all(props.rows.map((row) => setRowHidden(props.name, row.key, false)))
  await Promise.all(props.rows.map((row) => setRowLabel(props.name, row.key, '')))
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
  // An in-flight rename dies with the panel rather than reappearing, half
  // typed, the next time it opens.
  cancelRename()
  removeListeners()
  if (gearEl.value) gearEl.value.focus()
}

const toggleOpen = () => {
  if (open.value) close()
  else openMenu()
}

const toggleRow = async (row) => {
  const keys = keysOf(row)
  if (keys.length === 0) return
  const next = !rowMonitored(row)
  for (const key of keys) await setMonitored(key, next)
}

const toggleAll = async () => {
  const monitored = allPaused.value
  busy.value = true
  try {
    for (const key of configuredKeys.value) await setMonitored(key, monitored)
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
.sw:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }
@media (prefers-reduced-motion: no-preference) {
  .sw { transition: background 0.16s ease, border-color 0.16s ease; }
  .sw .knob { transition: transform 0.16s cubic-bezier(0.22, 1, 0.36, 1), background 0.16s ease; }
}

/* --- footer --- */
/* --- tabs --- */
.tabs {
  display: inline-flex;
  gap: 0.15rem;
  margin: 0.45rem 0 0.15rem;
  padding: 0.12rem;
  border-radius: 7px;
  background: hsl(var(--muted) / 0.6);
}
.tab {
  font-size: 0.68rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  padding: 0.18rem 0.5rem;
  border-radius: 5px;
  color: hsl(var(--muted-foreground));
  transition: color 0.14s ease, background 0.14s ease;
}
.tab:hover { color: hsl(var(--foreground)); }
.tab.on { color: hsl(var(--foreground)); background: hsl(var(--background)); box-shadow: 0 1px 2px rgb(0 0 0 / 0.08); }
.tab:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }

/* --- layout rows --- */
/* A hidden row stays legible rather than greyed to the edge of readability:
   it is the one you are most likely to be looking for. */
.lay-row.off .row-label { color: hsl(var(--muted-foreground)); text-decoration: line-through; }
.lay-actions { display: inline-flex; align-items: center; gap: 0.1rem; flex-shrink: 0; }
.ico {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.5rem;
  height: 1.5rem;
  border-radius: 5px;
  color: hsl(var(--muted-foreground));
  transition: color 0.14s ease, background 0.14s ease;
}
.ico:hover:not(:disabled) { color: hsl(var(--foreground)); background: hsl(var(--accent) / 0.6); }
.ico:disabled { opacity: 0.3; cursor: default; }
.ico:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }
.ico-svg { width: 0.85rem; height: 0.85rem; }
.row-rename-trigger {
  text-align: left;
  border-bottom: 1px dashed transparent;
  transition: border-color 0.14s ease;
}
.row-rename-trigger:hover { border-bottom-color: hsl(var(--muted-foreground) / 0.6); }
.row-rename-trigger:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; border-radius: 3px; }
.row-rename {
  width: 100%;
  font-size: 0.78rem;
  font-weight: 600;
  padding: 0.1rem 0.3rem;
  border: 1px solid hsl(var(--border));
  border-radius: 5px;
  background: hsl(var(--background));
  color: hsl(var(--foreground));
}
.row-rename:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }
.foot-pair { display: flex; align-items: center; gap: 0.35rem; }

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

/* --- target editing ------------------------------------------------------
   Deliberately plainer than the other two tabs. Re-pointing a check is the one
   thing in this menu that changes what the monitor actually does, so it reads
   as a form to be filled in carefully rather than a row of switches to flick. */
.tgt-panel { padding: 0.35rem 0 0; }
.tgt-off { font-size: 0.75rem; line-height: 1.45; color: hsl(var(--muted-foreground)); padding: 0.5rem 0.1rem; }
.tgt-off code { font-family: var(--j-mono); font-size: 0.92em; background: hsl(var(--muted) / 0.6); border-radius: 4px; padding: 0.05rem 0.28rem; }

.tgt-token { display: flex; flex-direction: column; gap: 0.25rem; padding: 0 0.1rem 0.5rem; border-bottom: 1px solid hsl(var(--border)); margin-bottom: 0.4rem; }
.tgt-token > span { font-size: 0.62rem; font-weight: 700; letter-spacing: 0.12em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
.tgt-token input { font-family: var(--j-mono); font-size: 0.78rem; background: hsl(var(--background)); border: 1px solid hsl(var(--border)); border-radius: 6px; padding: 0.3rem 0.5rem; width: 100%; }
.tgt-token input:focus { outline: none; border-color: hsl(var(--ring)); }

.tgt-row { display: flex; flex-direction: column; gap: 0.3rem; padding: 0.45rem 0.1rem; border-bottom: 1px solid hsl(var(--border) / 0.6); }
.tgt-row:last-child { border-bottom: 0; }
.tgt-head { display: flex; align-items: baseline; gap: 0.5rem; min-width: 0; }
.tgt-head .row-label { flex-shrink: 0; }
/* An overridden row says what config.yaml still holds, so "reset" is never a
   guess about what you are going back to. */
.tgt-head .row-note.over { font-family: var(--j-mono); font-size: 0.66rem; color: var(--j-warn); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.tgt-edit { display: flex; align-items: center; gap: 0.3rem; }
.tgt-input { flex: 1 1 auto; min-width: 0; font-family: var(--j-mono); font-size: 0.78rem; background: hsl(var(--background)); border: 1px solid hsl(var(--border)); border-radius: 6px; padding: 0.3rem 0.5rem; }
.tgt-input:focus { outline: none; border-color: hsl(var(--ring)); }
.tgt-input:disabled { opacity: 0.5; cursor: not-allowed; }
.tgt-btn { flex-shrink: 0; font-size: 0.7rem; font-weight: 600; padding: 0.3rem 0.55rem; border: 1px solid hsl(var(--border)); border-radius: 6px; background: hsl(var(--muted) / 0.6); color: hsl(var(--foreground)); cursor: pointer; }
.tgt-btn:hover:not(:disabled) { background: hsl(var(--accent)); }
.tgt-btn:disabled { opacity: 0.45; cursor: not-allowed; }
.tgt-btn.ghost { background: transparent; color: hsl(var(--muted-foreground)); }
.tgt-err { font-size: 0.7rem; line-height: 1.4; color: var(--j-crit); }
</style>
