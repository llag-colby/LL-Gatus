<template>
  <div class="rangesel" role="tablist" :aria-label="label">
    <button
      v-for="(range, index) in ranges"
      :key="range.value"
      ref="tabs"
      role="tab"
      type="button"
      class="rangesel-btn"
      :class="{ active: range.value === modelValue }"
      :aria-selected="range.value === modelValue"
      :tabindex="range.value === modelValue ? 0 : -1"
      @click="select(range.value)"
      @keydown="onKeydown($event, index)"
    >{{ range.label }}</button>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { HISTORY_RANGES } from '@/store'

// One filter row scopes every chart below it, rather than a picker per chart.
// Buttons rather than a select: the options are few, the choice is worth showing
// in full, and a roving-tabindex tablist is keyboard operable without opening a
// menu first.
const props = defineProps({
  modelValue: { type: String, required: true },
  ranges: { type: Array, default: () => HISTORY_RANGES },
  label: { type: String, default: 'Time range' },
})
const emit = defineEmits(['update:modelValue'])

const tabs = ref([])

const select = (value) => {
  if (value !== props.modelValue) emit('update:modelValue', value)
}

// Arrow keys move between ranges, Home and End jump to the ends. Moving focus
// also selects, which is the expected behaviour for a tablist whose panels are
// already rendered.
const onKeydown = (event, index) => {
  const last = props.ranges.length - 1
  let next = null
  if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = index === last ? 0 : index + 1
  else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = index === 0 ? last : index - 1
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = last
  if (next === null) return
  event.preventDefault()
  select(props.ranges[next].value)
  const el = tabs.value[next]
  if (el && el.focus) el.focus()
}
</script>

<style scoped>
.rangesel {
  display: inline-flex;
  gap: 2px;
  padding: 3px;
  background: hsl(var(--muted) / 0.45);
  border: 1px solid hsl(var(--border));
  border-radius: 10px;
}
.rangesel-btn {
  min-width: 2.6rem;
  padding: 0.25rem 0.55rem;
  border-radius: 7px;
  font-size: 0.78rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: hsl(var(--muted-foreground));
  background: transparent;
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease;
}
.rangesel-btn:hover {
  color: hsl(var(--foreground));
}
.rangesel-btn.active {
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  box-shadow: 0 1px 3px rgb(0 0 0 / 0.22);
}
.rangesel-btn:focus-visible {
  outline: 2px solid hsl(var(--ring));
  outline-offset: 1px;
}
@media (prefers-reduced-motion: reduce) {
  .rangesel-btn { transition: none; }
}
</style>
