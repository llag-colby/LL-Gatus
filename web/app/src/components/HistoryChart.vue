<template>
  <figure class="hchart" :class="{ stale: loading && hasData }">
    <figcaption class="hchart-head">
      <div class="hchart-titles">
        <span class="hchart-title">{{ title }}</span>
        <span v-if="subtitle" class="hchart-sub">{{ subtitle }}</span>
      </div>
      <div class="hchart-actions">
        <span v-if="spanLabel" class="hchart-span">{{ spanLabel }}</span>
        <span v-if="latestLabel" class="hchart-latest">{{ latestLabel }}</span>
        <button
          type="button"
          class="hchart-toggle"
          :aria-pressed="showTable"
          @click="showTable = !showTable"
        >{{ showTable ? 'Chart' : 'Table' }}</button>
      </div>
    </figcaption>

    <!-- Table view. Not a fallback: it is the readable twin of the chart, and the
         only way some values are reachable without hovering. -->
    <div v-if="showTable" class="hchart-tablewrap">
      <table class="hchart-table">
        <thead>
          <tr><th scope="col">Time</th><th scope="col" class="num">{{ unit || 'Value' }}</th></tr>
        </thead>
        <tbody>
          <tr v-for="row in tableRows" :key="row.t">
            <td>{{ row.clock }}</td>
            <td class="num" :class="row.toneClass">{{ row.text }}</td>
          </tr>
        </tbody>
      </table>
      <p v-if="!tableRows.length" class="hchart-empty">{{ emptyText }}</p>
    </div>

    <template v-else>
      <div ref="plotEl" class="hchart-plot" :style="{ height: height + 'px' }">
        <svg
          v-if="hasData"
          class="hchart-svg"
          :width="W"
          :height="height"
          role="img"
          :aria-label="ariaLabel"
          @pointermove="onPointerMove"
          @pointerleave="hoverIndex = null"
        >
          <!-- Recessive hairline grid: solid, one shade off the surface. -->
          <g class="grid">
            <line v-for="tick in yTicks" :key="'g' + tick.v" :x1="pad.l" :x2="W - pad.r"
              :y1="tick.y" :y2="tick.y" />
          </g>

          <!-- Area form for a continuous measure. -->
          <template v-if="kind === 'area'">
            <path v-if="bandPath" class="band" :d="bandPath" />
            <path class="area" :d="areaPath" />
            <path class="line" :d="linePath" />
          </template>

          <!-- Column form for bucketed ratios, where the gaps are meaningful. -->
          <g v-else class="cols">
            <rect
              v-for="col in columns"
              :key="col.i"
              :x="col.x" :y="col.y" :width="col.w" :height="col.h"
              :rx="col.r"
              :class="col.toneClass"
            />
          </g>

          <!-- Direct label on the worst bucket only. A number on every point is
               noise; the one that matters is the one worth naming. -->
          <g v-if="extreme" class="extreme">
            <line :x1="extreme.x" :x2="extreme.x" :y1="pad.t" :y2="height - pad.b" />
            <text :x="extreme.labelX" :y="pad.t + 10" :text-anchor="extreme.anchor">{{ extreme.text }}</text>
          </g>

          <g v-if="hover" class="crosshair">
            <line :x1="hover.x" :x2="hover.x" :y1="pad.t" :y2="height - pad.b" />
            <circle v-if="kind === 'area'" :cx="hover.x" :cy="hover.y" r="4" />
          </g>

          <g class="axis">
            <text v-for="tick in yTicks" :key="'t' + tick.v" :x="pad.l - 6" :y="tick.y + 3"
              text-anchor="end">{{ tick.label }}</text>
            <text :x="pad.l" :y="height - 4" text-anchor="start">{{ xStartLabel }}</text>
            <text :x="W - pad.r" :y="height - 4" text-anchor="end">{{ xEndLabel }}</text>
          </g>
        </svg>

        <!-- Exhaustive on purpose: an earlier version could match no branch and
             render an empty box, which looked like a broken chart. -->
        <p v-else-if="loading" class="hchart-empty">Loading</p>
        <p v-else class="hchart-empty">{{ emptyText }}</p>

        <div v-if="hover" class="hchart-tip" :style="hover.tipStyle">
          <span class="tip-time">{{ hover.clock }}</span>
          <span class="tip-value" :class="hover.toneClass">{{ hover.text }}</span>
        </div>
      </div>

      <p v-if="note" class="hchart-note">{{ note }}</p>
    </template>
  </figure>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { DISPLAY_TIMEZONE } from '@/utils/time'

// A single-series chart. One series means no legend is needed, so the title
// names it. Two measures of different scale are never put on one plot: callers
// render two of these instead, which is why there is no second-axis prop.
//
// Hand-rolled SVG rather than chart.js: these render several to a page, CSS
// custom properties theme them without the class-mutation observer chart.js
// needs, and the mark geometry the design calls for (2px gaps, 4px rounded
// data-ends, one direct label) is spelled out here rather than fought for
// through a plugin API.
const props = defineProps({
  // { timestamps: [msEpoch], values: [Number|null], min: [Number]|null, max: [Number]|null }
  series: { type: Object, default: () => ({ timestamps: [], values: [] }) },
  title: { type: String, required: true },
  subtitle: { type: String, default: '' },
  unit: { type: String, default: '' },
  // 'area' for a count over time, 'column' for bucketed ratios such as uptime.
  kind: { type: String, default: 'area' },
  height: { type: Number, default: 132 },
  // Ratio series (0..1) are formatted and scaled as percentages.
  ratio: { type: Boolean, default: false },
  // Values at or below these fractions of the scale read as degraded / down.
  // Only used by the column form, where the colour means state.
  warnBelow: { type: Number, default: null },
  downBelow: { type: Number, default: null },
  loading: { type: Boolean, default: false },
  emptyText: { type: String, default: 'Nothing recorded in this range yet.' },
  note: { type: String, default: '' },
})

const plotEl = ref(null)
const width = ref(0)
const hoverIndex = ref(null)
const showTable = ref(false)

const pad = { l: 38, r: 8, t: 14, b: 18 }

// Never geometry off a zero width. A measured 0 (the element has not been laid
// out yet, or a ResizeObserver callback has not run) used to leave the plot with
// nothing drawn at all, which read as a broken chart rather than a pending one.
// Drawing at a sane default and re-measuring on the next frame is always better
// than drawing nothing.
const FALLBACK_WIDTH = 320
const W = computed(() => (width.value > 0 ? width.value : FALLBACK_WIDTH))

const timestamps = computed(() => props.series?.timestamps || [])
const values = computed(() => props.series?.values || [])
const hasData = computed(() => timestamps.value.length > 0 && values.value.some(v => v !== null && v !== undefined))

const numeric = computed(() => values.value.filter(v => v !== null && v !== undefined && !Number.isNaN(v)))

const yMax = computed(() => {
  if (props.ratio) return 1
  const max = numeric.value.length ? Math.max(...numeric.value) : 0
  if (max <= 0) return 1
  // Round up to a tidy ceiling so the axis reads in whole steps.
  const step = Math.pow(10, Math.floor(Math.log10(max)))
  return Math.ceil(max / step) * step
})

const plotW = computed(() => Math.max(0, W.value - pad.l - pad.r))
const plotH = computed(() => Math.max(0, props.height - pad.t - pad.b))

const xAt = (i) => {
  const n = timestamps.value.length
  if (n <= 1) return pad.l + plotW.value / 2
  return pad.l + (i / (n - 1)) * plotW.value
}
const yAt = (v) => pad.t + plotH.value * (1 - Math.min(1, Math.max(0, v / yMax.value)))

const fmt = (v) => {
  if (v === null || v === undefined || Number.isNaN(v)) return 'no data'
  if (props.ratio) return `${(v * 100).toFixed(v >= 0.9995 ? 0 : 2)}%`
  const rounded = Math.abs(v) >= 100 || Number.isInteger(v) ? Math.round(v) : Math.round(v * 10) / 10
  return props.unit ? `${rounded} ${props.unit}` : String(rounded)
}

// --- area geometry --------------------------------------------------------
// Nulls break the line rather than being drawn through, so a gap in collection
// looks like a gap instead of an invented straight line between two real points.
const segments = computed(() => {
  const out = []
  let current = []
  values.value.forEach((v, i) => {
    if (v === null || v === undefined || Number.isNaN(v)) {
      if (current.length) out.push(current)
      current = []
      return
    }
    current.push({ x: xAt(i), y: yAt(v) })
  })
  if (current.length) out.push(current)
  return out
})

const linePath = computed(() =>
  segments.value.map(seg => seg.map((p, i) => `${i ? 'L' : 'M'}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join('')).join(' ')
)
const areaPath = computed(() => {
  const base = (props.height - pad.b).toFixed(1)
  return segments.value.map(seg => {
    const head = seg.map((p, i) => `${i ? 'L' : 'M'}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join('')
    return `${head}L${seg[seg.length - 1].x.toFixed(1)},${base}L${seg[0].x.toFixed(1)},${base}Z`
  }).join(' ')
})

// A min/max band, drawn only when the backend returned hourly rollups. It shows
// what an average hides: a bucket whose average is fine but whose floor was not.
const bandPath = computed(() => {
  const min = props.series?.min
  const max = props.series?.max
  if (!Array.isArray(min) || !Array.isArray(max) || !min.length) return ''
  const top = []
  const bottom = []
  timestamps.value.forEach((_, i) => {
    if (min[i] === null || max[i] === null || min[i] === undefined || max[i] === undefined) return
    top.push(`${xAt(i).toFixed(1)},${yAt(max[i]).toFixed(1)}`)
    bottom.unshift(`${xAt(i).toFixed(1)},${yAt(min[i]).toFixed(1)}`)
  })
  if (top.length < 2) return ''
  return `M${top.join('L')}L${bottom.join('L')}Z`
})

// --- column geometry ------------------------------------------------------
const toneFor = (v) => {
  if (v === null || v === undefined) return 'col-none'
  if (props.downBelow !== null && v < props.downBelow) return 'col-down'
  if (props.warnBelow !== null && v < props.warnBelow) return 'col-warn'
  return 'col-ok'
}

const columns = computed(() => {
  const n = timestamps.value.length
  if (!n || plotW.value <= 0) return []
  const slot = plotW.value / n
  // A 2px surface gap between fills, never a border drawn around them.
  const w = Math.max(1, slot - 2)
  const base = props.height - pad.b
  return timestamps.value.map((t, i) => {
    const v = values.value[i]
    const y = v === null || v === undefined ? base : yAt(v)
    const h = Math.max(v === null || v === undefined ? 0 : 2, base - y)
    return {
      i,
      x: pad.l + i * slot + 1,
      y: base - h,
      w,
      h,
      r: Math.min(4, w / 2),
      toneClass: toneFor(v),
    }
  })
})

// --- the one direct label -------------------------------------------------
const extreme = computed(() => {
  if (!hasData.value || plotW.value <= 0) return null
  let idx = -1
  let worst = null
  values.value.forEach((v, i) => {
    if (v === null || v === undefined) return
    if (worst === null || v < worst) { worst = v; idx = i }
  })
  if (idx < 0) return null
  // Only worth labelling when it is actually a dip, not a flat line.
  const best = Math.max(...numeric.value)
  if (worst >= best) return null
  const x = props.kind === 'column' ? columns.value[idx]?.x + columns.value[idx]?.w / 2 : xAt(idx)
  if (x === undefined || Number.isNaN(x)) return null
  const nearRight = x > pad.l + plotW.value * 0.7
  return {
    x,
    labelX: nearRight ? x - 5 : x + 5,
    anchor: nearRight ? 'end' : 'start',
    text: `low ${fmt(worst)}`,
  }
})

// --- hover ----------------------------------------------------------------
const onPointerMove = (event) => {
  const n = timestamps.value.length
  if (!n || plotW.value <= 0) return
  const rect = event.currentTarget.getBoundingClientRect()
  const x = event.clientX - rect.left
  const ratio = (x - pad.l) / plotW.value
  const i = Math.round(ratio * (n - 1))
  hoverIndex.value = Math.min(n - 1, Math.max(0, i))
}

const clockOf = (ms) => {
  try {
    return new Intl.DateTimeFormat('en-US', {
      timeZone: DISPLAY_TIMEZONE, month: 'short', day: 'numeric',
      hour: 'numeric', minute: '2-digit',
    }).format(new Date(ms))
  } catch (e) {
    return ''
  }
}

const hover = computed(() => {
  const i = hoverIndex.value
  if (i === null || !hasData.value || plotW.value <= 0) return null
  const v = values.value[i]
  const x = props.kind === 'column' && columns.value[i]
    ? columns.value[i].x + columns.value[i].w / 2
    : xAt(i)
  const left = Math.min(Math.max(x, 52), Math.max(52, W.value - 52))
  return {
    x,
    y: v === null || v === undefined ? props.height - pad.b : yAt(v),
    clock: clockOf(timestamps.value[i]),
    text: fmt(v),
    toneClass: props.kind === 'column' ? toneFor(v) : '',
    tipStyle: { left: `${left}px` },
  }
})

const latestLabel = computed(() => {
  for (let i = values.value.length - 1; i >= 0; i--) {
    const v = values.value[i]
    if (v !== null && v !== undefined) return fmt(v)
  }
  return ''
})

// Points are spaced evenly by index so the shape of what exists fills the plot
// rather than hiding in a sliver at the right edge. That would misrepresent how
// much of the window is actually covered, so the covered span is stated here.
// Recording starts at first deploy, so a 30 day view can legitimately hold an
// hour of data, and the reader has to be able to tell.
const spanLabel = computed(() => {
  const ts = timestamps.value
  if (ts.length < 2) return ''
  const minutes = Math.round((ts[ts.length - 1] - ts[0]) / 60000)
  if (minutes < 1) return ''
  if (minutes < 90) return `spans ${minutes}m`
  const hours = Math.round(minutes / 60)
  if (hours < 48) return `spans ${hours}h`
  return `spans ${Math.round(hours / 24)}d`
})

const ariaLabel = computed(() => {
  const n = numeric.value.length
  if (!n) return `${props.title}: no data`
  return `${props.title}: ${n} points, latest ${latestLabel.value}. Switch to the table view for every value.`
})

const yTicks = computed(() => {
  const steps = [0, 0.5, 1]
  return steps.map(s => {
    const v = yMax.value * s
    return { v, y: yAt(v), label: props.ratio ? `${Math.round(v * 100)}%` : String(Math.round(v)) }
  })
})

const xStartLabel = computed(() => (timestamps.value.length ? clockOf(timestamps.value[0]) : ''))
const xEndLabel = computed(() => (timestamps.value.length ? clockOf(timestamps.value[timestamps.value.length - 1]) : ''))

// Newest first: the table is read for "what is it now" far more than "what was it".
const tableRows = computed(() =>
  timestamps.value
    .map((t, i) => ({
      t,
      clock: clockOf(t),
      text: fmt(values.value[i]),
      toneClass: props.kind === 'column' ? toneFor(values.value[i]) : '',
    }))
    .reverse()
)

// --- sizing ---------------------------------------------------------------
let observer = null
// Only ever accept a real measurement. A transient 0 while the element is being
// laid out must not replace a good width, or the plot collapses mid-render.
const measure = () => {
  if (!plotEl.value) return
  const measured = plotEl.value.clientWidth
  if (measured > 0) width.value = measured
}

const observePlot = () => {
  if (!plotEl.value) return
  if (typeof ResizeObserver === 'undefined') return
  if (observer) observer.disconnect()
  observer = new ResizeObserver(measure)
  observer.observe(plotEl.value)
}

onMounted(() => {
  measure()
  // A second pass after layout settles: the first measurement can land before
  // the grid has sized its columns.
  requestAnimationFrame(measure)
  observePlot()
  if (typeof ResizeObserver === 'undefined') window.addEventListener('resize', measure)
})
onUnmounted(() => {
  if (observer) observer.disconnect()
  window.removeEventListener('resize', measure)
})
// Toggling the table destroys and recreates the plot element, so the old
// observation target is gone: re-measure and re-observe the new one.
watch(showTable, (isTable) => {
  if (isTable) return
  requestAnimationFrame(() => {
    measure()
    observePlot()
  })
})
</script>

<style scoped>
/* A lifted surface, not hsl(var(--card)). In this theme --card is the SAME
   colour as --background in dark mode, so a card-coloured panel is invisible
   against the page: the chart read as an empty black box. The muted tint is the
   raised-surface pattern the rest of this app uses. */
.hchart {
  margin: 0;
  border: 1px solid hsl(var(--border));
  border-radius: 12px;
  background: hsl(var(--muted) / 0.28);
  padding: 0.7rem 0.8rem 0.5rem;
  transition: opacity 0.18s ease;
}
/* Hold the previous render while refetching rather than flashing a skeleton. */
.hchart.stale { opacity: 0.55; }

.hchart-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 0.6rem;
  margin-bottom: 0.35rem;
}
.hchart-titles { min-width: 0; }
.hchart-title {
  display: block;
  font-size: 0.7rem;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  font-weight: 700;
  color: hsl(var(--foreground));
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.hchart-sub {
  display: block;
  font-size: 0.66rem;
  color: hsl(var(--muted-foreground));
  margin-top: 0.1rem;
}
.hchart-actions { display: inline-flex; align-items: center; gap: 0.5rem; flex-shrink: 0; }
/* Proportional figures on a standalone value; tabular only where digits align. */
.hchart-latest { font-size: 0.95rem; font-weight: 700; color: hsl(var(--foreground)); }
.hchart-span { font-size: 0.6rem; color: hsl(var(--muted-foreground)); white-space: nowrap; }
.hchart-toggle {
  font-size: 0.62rem;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  font-weight: 700;
  color: hsl(var(--muted-foreground));
  background: transparent;
  border: 1px solid hsl(var(--border));
  border-radius: 6px;
  padding: 0.1rem 0.35rem;
  cursor: pointer;
}
.hchart-toggle:hover { color: hsl(var(--foreground)); }
.hchart-toggle:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 1px; }

/* The plot gets its own recessed ground so an empty range still reads as a
   chart waiting for data rather than a hole in the page. */
.hchart-plot {
  position: relative;
  width: 100%;
  min-width: 120px;
  background: hsl(var(--background) / 0.5);
  border-radius: 8px;
}
.hchart-svg { display: block; touch-action: none; }

.grid line { stroke: hsl(var(--border)); stroke-width: 1; }
.axis text { fill: hsl(var(--muted-foreground)); font-size: 9px; font-variant-numeric: tabular-nums; }

/* The measurement hue. Deliberately not a status colour: these series are
   magnitude over time, not good or bad. */
.area { fill: rgb(96 165 250 / 0.16); stroke: none; }
.line { fill: none; stroke: #60a5fa; stroke-width: 2; stroke-linejoin: round; stroke-linecap: round; }
.band { fill: rgb(96 165 250 / 0.12); stroke: none; }

/* Columns carry state, so they use the app's status tokens. Height encodes the
   value as well, so the meaning never rests on colour alone. */
.col-ok { fill: var(--status-up, #22c55e); }
.col-warn { fill: var(--status-degraded, #f59e0b); }
.col-down { fill: var(--status-down, #ef4444); }
.col-none { fill: hsl(var(--muted-foreground) / 0.25); }

.extreme line { stroke: hsl(var(--muted-foreground) / 0.45); stroke-width: 1; }
.extreme text { fill: hsl(var(--muted-foreground)); font-size: 9px; font-weight: 700; }

.crosshair line { stroke: hsl(var(--muted-foreground) / 0.55); stroke-width: 1; }
.crosshair circle { fill: #60a5fa; stroke: hsl(var(--card)); stroke-width: 2; }

.hchart-tip {
  position: absolute;
  top: 2px;
  transform: translateX(-50%);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.05rem;
  pointer-events: none;
  background: hsl(var(--popover));
  border: 1px solid hsl(var(--border));
  border-radius: 7px;
  padding: 0.2rem 0.45rem;
  box-shadow: 0 4px 12px rgb(0 0 0 / 0.25);
  white-space: nowrap;
}
.tip-time { font-size: 0.6rem; color: hsl(var(--muted-foreground)); }
.tip-value { font-size: 0.75rem; font-weight: 700; font-variant-numeric: tabular-nums; color: hsl(var(--foreground)); }
.tip-value.col-warn { color: var(--status-degraded, #f59e0b); }
.tip-value.col-down { color: var(--status-down, #ef4444); }

.hchart-empty {
  margin: 0;
  padding: 1.6rem 0.3rem;
  text-align: center;
  font-size: 0.76rem;
  color: hsl(var(--muted-foreground));
}
.hchart-note { margin: 0.35rem 0 0; font-size: 0.64rem; color: hsl(var(--muted-foreground)); opacity: 0.8; }

.hchart-tablewrap { max-height: 15rem; overflow: auto; }
.hchart-table { width: 100%; border-collapse: collapse; font-size: 0.74rem; }
.hchart-table th {
  position: sticky;
  top: 0;
  background: hsl(var(--card));
  text-align: left;
  font-size: 0.6rem;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: hsl(var(--muted-foreground));
  padding: 0.25rem 0.3rem;
  border-bottom: 1px solid hsl(var(--border));
}
.hchart-table td { padding: 0.2rem 0.3rem; border-bottom: 1px solid hsl(var(--border) / 0.5); }
.hchart-table .num { text-align: right; font-variant-numeric: tabular-nums; }
.hchart-table td.col-warn { color: var(--status-degraded, #f59e0b); font-weight: 600; }
.hchart-table td.col-down { color: var(--status-down, #ef4444); font-weight: 700; }

@media (prefers-reduced-motion: reduce) {
  .hchart { transition: none; }
}
</style>
