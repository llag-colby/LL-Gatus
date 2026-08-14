<template>
  <div class="il-load" :style="sizeVars" role="status" aria-label="Loading">
    <span
      v-for="i in BAR_COUNT"
      :key="i"
      class="il-load-bar"
      :style="{ '--i': i - 1 }"
    />
  </div>
</template>

<script setup>
import { computed } from 'vue'

// This dashboard's whole visual language is a row of status bars, so the
// loading state is a probe sweep across that same row rather than a generic
// spinning ring — it reads as "polling the sites", which is what it is.
const BAR_COUNT = 5

const props = defineProps({
  size: {
    type: String,
    default: 'md',
    validator: (value) => ['xs', 'sm', 'md', 'lg', 'xl'].includes(value)
  },
})

// bar width / bar height / gap, in px. Deliberately NOT Tailwind utilities:
// global fullscreen rules key off utility classes, and a shared class landing
// on a fixed-size indicator is what deformed the previous spinner.
const SIZES = {
  xs: ['2px', '10px', '2px'],
  sm: ['2.5px', '14px', '3px'],
  md: ['3px', '20px', '4px'],
  lg: ['4px', '30px', '5px'],
  xl: ['5px', '40px', '6px'],
}

const sizeVars = computed(() => {
  const [w, h, gap] = SIZES[props.size] || SIZES.md
  return { '--il-bar-w': w, '--il-bar-h': h, '--il-bar-gap': gap }
})
</script>

<style scoped>
.il-load {
  display: inline-flex;
  align-items: flex-end;
  gap: var(--il-bar-gap);
  line-height: 0;
}

.il-load-bar {
  flex: none;              /* never let a flex parent squash these */
  box-sizing: border-box;
  width: var(--il-bar-w);
  height: var(--il-bar-h);
  border-radius: 1px;
  transform-origin: bottom;
  background-color: hsl(var(--primary) / 0.18);
  animation: il-load-sweep 1.15s var(--ease-out-quart, ease-out) infinite;
  animation-delay: calc(var(--i) * 105ms);
}

/* The sweep travels left to right, then holds — a scan, not a throb. Each bar
   is lit for a short window so at most two are bright at once. */
@keyframes il-load-sweep {
  0%, 55%, 100% {
    transform: scaleY(0.5);
    background-color: hsl(var(--primary) / 0.18);
  }
  22% {
    transform: scaleY(1);
    background-color: hsl(var(--primary));
  }
}

@media (prefers-reduced-motion: reduce) {
  .il-load-bar {
    animation: none;
    transform: scaleY(0.7);
    background-color: hsl(var(--primary) / 0.4);
  }
}
</style>
