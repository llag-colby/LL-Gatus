<template>
  <!-- The tooltip lives on the wrapper, not the switch: a disabled button gets
       no pointer events, so a bubble bound to it would never appear. -->
  <div
    class="mtoggle"
    :class="{ 'is-compact': compact, 'is-paused': !monitored, 'is-locked': !allowed }"
    :data-tooltip="allowed ? null : 'Sign in to change monitoring'"
    data-tip-pos="bottom"
  >
    <button
      ref="switchEl"
      type="button"
      class="sw"
      :class="{ on: monitored }"
      role="switch"
      :aria-checked="monitored ? 'true' : 'false'"
      :aria-labelledby="labelId"
      :aria-describedby="noteId"
      :disabled="!allowed"
      :aria-disabled="!allowed ? 'true' : 'false'"
      @click="toggle"
      @keydown.space.prevent="toggle"
      @keydown.enter.prevent="toggle"
    >
      <span class="knob" aria-hidden="true"></span>
    </button>

    <div class="mt-text">
      <span :id="labelId" class="mt-label">{{ label }}</span>
      <span :id="noteId" class="mt-note">{{ note }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, useId } from 'vue'
import { can, isMonitored, setMonitored } from '@/store'

const props = defineProps({
  endpointKey: { type: String, required: true },
  label: { type: String, default: 'Monitoring' },
  compact: { type: Boolean, default: false },
})

const labelId = useId()
const noteId = useId()
const switchEl = ref(null)

// Reads through the store helper so the computed tracks `monitoringDisabled`
// (a ref holding a Set — it is replaced on every change, so the dependency is
// the ref itself, which is exactly what isMonitored touches).
const monitored = computed(() => isMonitored(props.endpointKey))

// Pausing changes state, so it needs operator. `can` is true whenever accounts
// are switched off, which keeps an account-less deployment behaving as before.
const allowed = computed(() => can('operator'))

// Guarded as well as disabled: the markup is a hint, this is the control. A
// stale render or a console click must not reach the store.
const toggle = () => {
  if (!can('operator')) return
  setMonitored(props.endpointKey, !monitored.value)
}

// Says what pausing actually does. Paused is a choice, not a fault, so it reads
// amber rather than red — and it names the one thing people worry about losing.
const note = computed(() => {
  if (props.compact) return monitored.value ? 'Checks running' : 'Paused · history kept'
  return monitored.value
    ? 'Checks and alerts are running.'
    : 'Checks and alerts are stopped. History is kept.'
})
</script>

<style scoped>
.mtoggle {
  display: inline-flex;
  align-items: center;
  gap: 0.55rem;
  min-width: 0;
}

/* --- the switch --- */
.sw {
  position: relative;
  flex-shrink: 0;
  width: 38px;
  height: 21px;
  padding: 0;
  border: 1px solid hsl(var(--border));
  border-radius: 999px;
  background: hsl(var(--muted));
  cursor: pointer;
}
.sw .knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 15px;
  height: 15px;
  border-radius: 999px;
  background: #8a8f98;
  box-shadow: 0 1px 2px rgb(0 0 0 / 0.35);
}
.sw.on {
  background: rgb(90 160 107 / 0.28);
  border-color: rgb(90 160 107 / 0.65);
}
.sw.on .knob {
  background: #5aa06b;
  transform: translateX(17px);
}
.sw:hover { border-color: hsl(var(--muted-foreground) / 0.55); }
/* Visibly inert but still on screen: a viewer should see that the control
   exists and is out of reach, not an empty space. */
.sw:disabled { cursor: not-allowed; opacity: 0.5; }
.sw:disabled:hover { border-color: hsl(var(--border)); }
.sw:focus-visible {
  outline: 2px solid hsl(var(--ring));
  outline-offset: 2px;
}

.is-compact .sw { width: 32px; height: 18px; }
.is-compact .sw .knob { top: 1px; left: 1px; width: 14px; height: 14px; }
.is-compact .sw.on .knob { transform: translateX(14px); }

@media (prefers-reduced-motion: no-preference) {
  .sw { transition: background 0.16s ease, border-color 0.16s ease; }
  .sw .knob { transition: transform 0.16s cubic-bezier(0.22, 1, 0.36, 1), background 0.16s ease; }
}

/* --- label + state line --- */
.mt-text {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
  min-width: 0;
}
.mt-label {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.mt-note {
  font-size: 0.74rem;
  line-height: 1.25;
  color: hsl(var(--muted-foreground));
}
.is-paused .mt-label,
.is-paused .mt-note { color: #e0a458; }
.is-locked .mt-label,
.is-locked .mt-note { opacity: 0.7; }

.is-compact { gap: 0.45rem; }
.is-compact .mt-text { flex-direction: row; align-items: baseline; gap: 0.4rem; }
.is-compact .mt-note { font-size: 0.7rem; white-space: nowrap; }

@media (max-width: 480px) {
  .mt-note { white-space: normal; }
}
</style>
