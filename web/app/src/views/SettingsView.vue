<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 settings-panel">

      <!-- MASTHEAD. Not a title over a form: a panel plate. What the page is
           on the left, the number that describes this system on the right. It
           gives the page a hard top edge and a focal point. -->
      <header class="plate" :class="{ 'is-in': true }">
        <div class="plate-left">
          <router-link to="/" class="back" data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-4 w-4" />
          </router-link>
          <div class="plate-id">
            <div class="tick">Monitoring</div>
            <h1 class="plate-h1">Settings</h1>
          </div>
        </div>

        <div class="plate-right">
          <div class="readout" :class="{ hot: pausedKeys.length > 0 }">
            <span class="readout-k">Paused</span>
            <span class="readout-v num">{{ pausedKeys.length }}</span>
            <span class="readout-sub"><span class="readout-dim">checks stopped</span></span>
          </div>
        </div>
      </header>

      <div class="body solo">
        <div class="main">

          <section class="block">
            <div class="block-head">
              <span class="tick">Paused checks</span>
              <span class="head-note">global</span>
            </div>

            <p v-if="monitoringError" class="msg msg-error" role="alert">{{ monitoringError }}</p>
            <p v-else-if="monitoringLoading && pausedKeys.length === 0" class="msg msg-dim">Loading paused checks.</p>
            <p v-else-if="pausedKeys.length === 0" class="msg msg-dim">
              Nothing is paused. Every configured check is running.
            </p>
            <ul v-else class="keys">
              <li v-for="key in pausedKeys" :key="key">
                <router-link class="key" :to="`/endpoints/${key}`">
                  <i class="key-led" aria-hidden="true"></i><span class="mono">{{ key }}</span>
                </router-link>
              </li>
            </ul>
            <p class="block-note">
              Pausing stops the check and its alerts for everyone and for the wallboards.
              Recorded history is kept. Resume from the card or the drill-in.
            </p>
          </section>

          <section class="block">
            <div class="block-head">
              <span class="tick">Preferences</span>
              <span class="head-note">this browser</span>
            </div>
            <p class="block-note">
              Status colours, sound, refresh interval, theme and the dashboard layout are set
              from the control pill in the bottom-left corner and from the dashboard header.
              They are stored in this browser only, so every screen keeps its own, and none of
              it is sent to the server.
            </p>
          </section>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ArrowLeft } from 'lucide-vue-next'

// This page used to be the account console: a sign-in readout, a password
// form, a role clearance matrix and the user roster. There are no accounts any
// more, so what is left is the one thing here that was never about a person -
// which checks are paused, globally, for everybody.

const readBody = async (response) => {
  try {
    return await response.json()
  } catch (e) {
    return null
  }
}

// The server sends {"error":"..."}. Show that text as written.
const serverMessage = (body, response, fallback) => {
  if (body && typeof body.error === 'string' && body.error.trim()) return body.error
  return `${fallback} (${response.status})`
}

const thrownMessage = (e, fallback) => (e && e.message ? e.message : fallback)

const pausedKeys = ref([])
const monitoringLoading = ref(false)
const monitoringError = ref('')

const loadMonitoring = async () => {
  monitoringLoading.value = true
  monitoringError.value = ''
  try {
    const response = await fetch('/api/v1/monitoring', { cache: 'no-store' })
    const body = await readBody(response)
    if (!response.ok) throw new Error(serverMessage(body, response, 'Could not load paused checks.'))
    const list = body && (Array.isArray(body.paused) ? body.paused : body.disabled)
    pausedKeys.value = Array.isArray(list) ? [...list].sort() : []
  } catch (e) {
    pausedKeys.value = []
    monitoringError.value = thrownMessage(e, 'Could not load paused checks.')
  } finally {
    monitoringLoading.value = false
  }
}

onMounted(loadMonitoring)
</script>

<style scoped>
/* ------------------------------------------------------------------ *
 *  Instrument panel.
 *
 *  The rest of this app already speaks equipment: uplink ladders, AP
 *  roster maps, status bars, capacity meters. This page had gone SaaS
 *  generic - bordered boxes of forms - which is why it read as dead
 *  next to the pages around it.
 *
 *  Mono is the DISPLAY face here, not just a label font. No webfont is
 *  loaded on purpose: this is an offline LAN tool and a font CDN is a
 *  dependency it must not have.
 * ------------------------------------------------------------------ */

.settings-panel { width: 100%; display: flex; flex-direction: column; gap: 1rem; }

/* The mono tick label, used for every section head. The leading rule is
   the device that ties the page together. */
.tick {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.2em;
  text-transform: uppercase;
  font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.tick::before {
  content: '';
  width: 14px;
  height: 2px;
  border-radius: 2px;
  background: hsl(var(--muted-foreground) / 0.55);
  flex-shrink: 0;
}

/* --- masthead ----------------------------------------------------- */
.plate {
  display: flex;
  align-items: stretch;
  justify-content: space-between;
  gap: 1.25rem;
  flex-wrap: wrap;
  padding: 0.9rem 1.1rem;
  border: 1px solid hsl(var(--border));
  border-top: 1px solid hsl(var(--border));
  border-radius: 12px;
  background: hsl(var(--muted) / 0.28);
}
.plate-left { display: flex; align-items: center; gap: 0.85rem; min-width: 0; }
.back { color: hsl(var(--muted-foreground)); transition: color 0.15s ease; }
.back:hover { color: hsl(var(--foreground)); }
.back:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 3px; border-radius: 6px; }
.plate-h1 {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 1.35rem;
  font-weight: 700;
  letter-spacing: 0.02em;
  line-height: 1.05;
  margin-top: 0.3rem;
}

/* A readout, hairline-separated like a gauge cluster. */
.plate-right { display: flex; align-items: stretch; gap: 0; flex-wrap: wrap; }
.readout {
  display: flex;
  flex-direction: column;
  gap: 0.12rem;
  padding: 0 1.1rem;
  border-left: 1px solid hsl(var(--border));
  min-width: 8rem;
}
.readout:first-child { border-left: 0; }
.readout-k {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 9px; letter-spacing: 0.16em; text-transform: uppercase; font-weight: 700;
  color: hsl(var(--muted-foreground));
}
.readout-v { font-size: 1.05rem; font-weight: 700; line-height: 1.15; color: hsl(var(--foreground)); }
.readout-v.num { font-size: 1.5rem; font-variant-numeric: tabular-nums; letter-spacing: -0.01em; }
.readout-sub { display: inline-flex; align-items: center; gap: 0.4rem; }
.readout-dim { font-size: 0.66rem; color: hsl(var(--muted-foreground)); }
.readout.hot .readout-v { color: hsl(var(--foreground)); }

/* --- body layout -------------------------------------------------- */
.body {
  display: grid;
  grid-template-columns: minmax(0, 720px);
  gap: 1rem;
  align-items: start;
}
.main { display: flex; flex-direction: column; gap: 1rem; min-width: 0; }
@media (max-width: 1040px) {
  .body { grid-template-columns: minmax(0, 1fr); }
}

/* Blocks are open, not boxed-in: a top hairline and a head, so the page
   reads as one panel with sections rather than a tray of cards. */
.block {
  border: 1px solid hsl(var(--border));
  border-radius: 12px;
  background: hsl(var(--muted) / 0.24);
  padding: 0.85rem 1rem 1rem;
}
.block-head {
  display: flex; align-items: center; justify-content: space-between; gap: 0.75rem;
  padding-bottom: 0.7rem; margin-bottom: 0.85rem;
  border-bottom: 1px solid hsl(var(--border));
}
.head-note {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.66rem; color: hsl(var(--muted-foreground));
}
.block-note {
  margin: 0.8rem 0 0; font-size: 0.72rem; line-height: 1.5;
  color: hsl(var(--muted-foreground));
}

/* --- paused keys -------------------------------------------------- */
.keys { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: 0.4rem; }
.key {
  display: inline-flex; align-items: center; gap: 0.4rem;
  font-size: 0.74rem;
  padding: 0.2rem 0.55rem;
  border: 1px solid rgb(224 160 88 / 0.35);
  background: rgb(224 160 88 / 0.08);
  border-radius: 999px;
  color: hsl(var(--foreground));
}
.key:hover { border-color: rgb(224 160 88 / 0.7); }
.key:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; }
.key-led { width: 6px; height: 6px; border-radius: 999px; background: hsl(var(--muted-foreground)); flex-shrink: 0; }

/* --- messages ----------------------------------------------------- */
.msg { font-size: 0.76rem; line-height: 1.45; margin: 0.4rem 0 0; }
.msg-error { color: hsl(var(--destructive)); }
.msg-dim { color: hsl(var(--muted-foreground)); }

.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

/* One quiet arrival, not a sequence of effects. */
@media (prefers-reduced-motion: no-preference) {
  .plate.is-in { animation: plate-in 0.32s cubic-bezier(0.22, 1, 0.36, 1) both; }
  .block { animation: block-in 0.3s cubic-bezier(0.22, 1, 0.36, 1) both; }
  .main .block:nth-child(2) { animation-delay: 0.08s; }
}
@keyframes plate-in { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: none; } }
@keyframes block-in { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: none; } }
</style>
