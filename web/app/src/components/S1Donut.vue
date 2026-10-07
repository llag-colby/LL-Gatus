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

const options = {
  responsive: true,
  maintainAspectRatio: false,
  // A thick ring, not a pie: the hole carries the total, and a ring's
  // arc length is easier to compare than a wedge's area.
  cutout: '64%',
  plugins: {
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
  padding: 0.55rem 0.65rem 0.5rem; background: hsl(var(--card) / 0.55);
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
:global(.wall) .dwrap { height: 11rem; }
:global(.wall) .dt { font-size: 12px; }
:global(.wall) .dc-n { font-size: 2.1rem; }
:global(.wall) .dlegend li { font-size: 0.85rem; }
</style>
