<template>
  <article class="dtile" :style="{ '--lead': leadColor }">
    <header class="dh">
      <h3 class="dt">{{ title }}</h3>
      <span v-if="sub" class="ds">{{ sub }}</span>
    </header>

    <div class="dwrap">
      <Doughnut v-if="total > 0" :data="data" :options="options" />
      <div v-else class="dempty">0</div>
      <!-- Centre readout. Chart.js has no built-in centre label, and the total
           is the thing you want when the ring is the breakdown of it. -->
      <div class="dcentre" aria-hidden="true">
        <span class="dc-n">{{ fmt(total) }}</span>
        <span v-if="centreLabel" class="dc-l">{{ centreLabel }}</span>
      </div>
    </div>

    <!-- The legend carries the numbers, because a ring alone cannot be read
         precisely and a SOC wall is read at a glance from a distance. -->
    <ul class="dlegend">
      <li v-for="(r, i) in rows" :key="r.name">
        <span class="dsw" :style="{ background: palette[i % palette.length] }"></span>
        <span class="dln">{{ r.name }}</span>
        <b class="dlv">{{ fmt(r.count) }}</b>
        <span class="dlp">{{ share(r.count) }}</span>
      </li>
    </ul>
  </article>
</template>

<script setup>
import { computed } from 'vue'
import { Doughnut } from 'vue-chartjs'
import {
  Chart as ChartJS, ArcElement, DoughnutController, Tooltip, Legend,
} from 'chart.js'

ChartJS.register(ArcElement, DoughnutController, Tooltip, Legend)

const props = defineProps({
  title: { type: String, required: true },
  sub: { type: String, default: '' },
  centreLabel: { type: String, default: '' },
  rows: { type: Array, default: () => [] },
  // Explicit colours per slice, in row order. Passed in rather than derived so
  // a given state keeps the same colour across every chart on the page -
  // "not mitigated" must be red everywhere or the wall teaches the wrong
  // reflex.
  palette: { type: Array, default: () => ['#8b5cf6', '#3b82f6', '#22c55e', '#f59e0b', '#ef4444', '#64748b'] },
})

const total = computed(() => props.rows.reduce((n, r) => n + (r.count || 0), 0))
// The biggest slice's colour, used for the tile's top edge.
const leadColor = computed(() => {
  if (!props.rows.length) return 'transparent'
  let best = 0
  props.rows.forEach((r, i) => { if ((r.count || 0) > (props.rows[best].count || 0)) best = i })
  return props.palette[best % props.palette.length]
})
const fmt = (n) => (n || 0).toLocaleString()
const share = (n) => (total.value > 0 ? `${Math.round((n / total.value) * 100)}%` : '—')

const data = computed(() => ({
  labels: props.rows.map(r => r.name),
  datasets: [{
    data: props.rows.map(r => r.count),
    backgroundColor: props.rows.map((_, i) => props.palette[i % props.palette.length]),
    borderWidth: 0,
    // A visible gap between slices reads better at distance than a hairline
    // border, which disappears on a wall-mounted panel.
    spacing: 2,
    hoverOffset: 6,
  }],
}))

// Relative luminance of a hex colour, per WCAG, then black or white
// whichever contrasts more. Cheap and palette-agnostic: the alternative is
// hard-coding a label colour per theme, which is what was wrong before.
const readableOn = (hex) => {
  const m = /^#?([0-9a-f]{6})$/i.exec(String(hex || ''))
  if (!m) return '#ffffff'
  const n = parseInt(m[1], 16)
  const chan = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((v) => {
    const c = v / 255
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
  })
  const lum = 0.2126 * chan[0] + 0.7152 * chan[1] + 0.0722 * chan[2]
  return lum > 0.45 ? 'rgba(15,20,28,0.92)' : 'rgba(255,255,255,0.96)'
}

// Prints each slice's share inside the ring, so the chart is readable without
// cross-referencing the legend beneath it.
const sliceLabels = {
  id: 'sliceLabels',
  afterDatasetsDraw(chart, _args, opts) {
    const { ctx } = chart
    const meta = chart.getDatasetMeta(0)
    if (!meta || meta.hidden) return
    const data = chart.data.datasets[0].data
    const sum = data.reduce((a, b) => a + (b || 0), 0)
    if (!sum) return
    meta.data.forEach((arc, i) => {
      const share = (data[i] || 0) / sum
      // A thin wedge cannot hold text without sitting on its neighbour's
      // label, and the legend underneath carries the number anyway. 12% is
      // measured against the ring thickness rather than guessed: below it the
      // arc is shorter than the text is wide.
      if (share < 0.12) return
      const { x, y } = arc.tooltipPosition()
      ctx.save()
      // The label's colour comes from the SLICE's own luminance, not from the
      // theme. A fixed white was wrong on any pale slice - in light mode the
      // grey and amber wedges rendered white-on-pale and the figure was
      // effectively invisible. Per-slice contrast is correct for both themes
      // and for whatever palette is passed in.
      ctx.fillStyle = readableOn(props.palette[i % props.palette.length])
      ctx.font = `800 ${opts.size || 11}px ui-monospace, SFMono-Regular, Menlo, monospace`
      ctx.textAlign = 'center'
      ctx.textBaseline = 'middle'
      ctx.fillText(`${Math.round(share * 100)}%`, x, y)
      ctx.restore()
    })
  },
}
ChartJS.register(sliceLabels)

const options = {
  responsive: true,
  maintainAspectRatio: false,
  // A thick ring, not a pie: the hole carries the total, and a ring's
  // arc length is easier to compare than a wedge's area.
  cutout: '64%',
  plugins: {
    sliceLabels: { size: 11 },
    legend: { display: false },
    tooltip: {
      callbacks: {
        label: (ctx) => {
          const sum = ctx.dataset.data.reduce((a, b) => a + b, 0)
          const pctText = sum > 0 ? ` (${Math.round((ctx.parsed / sum) * 100)}%)` : ''
          return ` ${ctx.label}: ${ctx.parsed.toLocaleString()}${pctText}`
        },
      },
    },
  },
  // No entrance animation: this repaints on every SSE tick, and a ring that
  // re-grows each time is noise on a monitor somebody is watching all day.
  animation: { duration: 0 },
}
</script>

<style scoped>
.dtile {
  position: relative; display: flex; flex-direction: column; min-width: 0;
  border: 1px solid hsl(var(--border)); border-radius: 10px;
  padding: 0.55rem 0.65rem 0.5rem; background: hsl(var(--card));
}
/* The tile's edge takes the colour of its largest slice, so the panel is
   identifiable at a glance before any label is read. */
.dtile::before {
  content: ''; position: absolute; inset: 0 0 auto 0; height: 2px;
  border-radius: 10px 10px 0 0; background: var(--lead, transparent);
}
.dh {
  display: flex; align-items: baseline; justify-content: space-between; gap: 0.4rem;
  padding-bottom: 0.35rem; margin-bottom: 0.4rem;
  border-bottom: 1px solid hsl(var(--border) / 0.7);
}
.dt {
  margin: 0; font-family: var(--j-mono); font-size: 10px; font-weight: 800;
  letter-spacing: 0.14em; text-transform: uppercase;
  color: hsl(var(--foreground)); white-space: nowrap;
}
.ds { font-size: 0.66rem; color: hsl(var(--muted-foreground)); white-space: nowrap; }

.dwrap { position: relative; height: 8.5rem; }
.dcentre {
  position: absolute; inset: 0; display: flex; flex-direction: column;
  align-items: center; justify-content: center; pointer-events: none;
}
.dc-n {
  font-family: var(--j-mono); font-variant-numeric: tabular-nums;
  font-size: 1.55rem; font-weight: 800; letter-spacing: -0.04em; line-height: 1;
}
.dc-l {
  font-family: var(--j-mono); font-size: 8px; font-weight: 700; letter-spacing: 0.14em;
  text-transform: uppercase; color: hsl(var(--muted-foreground)); margin-top: 0.2rem;
}
.dempty {
  display: grid; place-items: center; height: 100%;
  font-family: var(--j-mono); font-size: 2rem; font-weight: 800;
  color: hsl(var(--muted-foreground)); opacity: 0.25;
}

.dlegend { list-style: none; margin: 0.45rem 0 0; padding: 0; display: grid; gap: 0.15rem; }
.dlegend li {
  display: flex; align-items: center; gap: 0.35rem;
  font-family: var(--j-mono); font-size: 0.7rem; font-variant-numeric: tabular-nums;
}
.dsw { width: 0.5rem; height: 0.5rem; border-radius: 2px; flex: none; }
.dln { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dlv { font-weight: 700; }
.dlp { color: hsl(var(--muted-foreground)); min-width: 2.4rem; text-align: right; }

/* Wall mode: everything steps up, because this is read from across a room. */
/* On the wall the ring fills its grid row rather than taking a fixed
   height, which is what let the page grow past the screen. */
:global(.wall) .dwrap { height: auto; flex: 1 1 auto; min-height: 0; }
:global(.wall) .dlegend { flex: none; }
:global(.wall) .dt { font-size: 12px; }
:global(.wall) .dc-n { font-size: 2.1rem; }
:global(.wall) .dlegend li { font-size: 0.85rem; }
</style>
