<template>
  <div class="tel-root bg-background" :style="{ height: `calc(100vh - ${headerHeight}px)` }">
    <!-- Probe pending -->
    <div v-if="state === 'probing'" class="tel-center">
      <Loading size="lg" />
    </div>

    <!-- Sign in -->
    <div v-else-if="state === 'login'" class="tel-auth">
      <form class="tel-card" @submit.prevent="signIn">
        <!-- The console renders every run as a row: time, source, severity.
             The sign-in panel is that same row, before you are let through. -->
        <div class="tel-row" :class="`sev-${chip.sev}`">
          <span class="tel-time">{{ clock }}</span>
          <span class="tel-src">console</span>
          <span class="tel-grow"></span>
          <span class="tel-chip" :class="`chip-${chip.sev}`">{{ chip.label }}</span>
        </div>

        <div class="tel-body">
          <div class="tel-eyebrow">LL-Telemetry</div>
          <h1 class="tel-title">Operations console</h1>
          <p class="tel-lede">Field-script runs across every Long Lewis site.</p>

          <label class="tel-label" for="tel-user">Operator</label>
          <input
            id="tel-user"
            ref="userEl"
            v-model="user"
            class="tel-input"
            type="text"
            autocomplete="username"
            spellcheck="false"
            :disabled="busy"
          />

          <label class="tel-label" for="tel-pass">Password</label>
          <input
            id="tel-pass"
            v-model="password"
            class="tel-input"
            type="password"
            autocomplete="current-password"
            :disabled="busy"
          />

          <p v-if="error" class="tel-error">{{ error }}</p>

          <button class="tel-submit" type="submit" :disabled="busy || !user || !password">
            {{ busy ? 'Checking' : 'Sign in' }}
          </button>

          <p class="tel-foot">
            These credentials are set in <code>.env</code> on the Gatus host and are separate from
            your Gatus login.
          </p>
        </div>
      </form>
    </div>

    <!-- Not configured -->
    <div v-else-if="state === 'unconfigured'" class="tel-pad">
      <div class="notice max-w-2xl">
        <div class="notice-title">LL-Telemetry isn't connected yet</div>
        <div class="notice-body">
          <p class="mb-3">
            The telemetry console is gated behind its own credentials, and none are set on this
            Gatus instance. Nothing is being proxied.
          </p>
          <p class="mb-2">Set these in <code>.env</code> and restart Gatus:</p>
          <ul class="list-disc pl-5 space-y-1">
            <li><code>TELEMETRY_UI_USER</code></li>
            <li><code>TELEMETRY_UI_PASSWORD_BCRYPT</code> (preferred) or <code>TELEMETRY_UI_PASSWORD</code></li>
            <li><code>TELEMETRY_UPSTREAM_URL</code> (defaults to <code>http://lltel-api:8080</code>)</li>
          </ul>
        </div>
      </div>
    </div>

    <!-- Upstream down or slow -->
    <div v-else-if="state === 'unreachable' || state === 'slow'" class="tel-pad">
      <div class="notice notice-error max-w-2xl">
        <div class="notice-title flex items-center gap-2">
          <AlertTriangle class="h-4 w-4" />
          {{ state === 'slow' ? 'The telemetry service is not responding in time' : 'The telemetry service is unreachable' }}
        </div>
        <div class="notice-body">
          <p class="mb-4">
            {{ state === 'slow'
              ? 'Gatus reached the LL-Telemetry API but it took too long to answer. Its database may be under load.'
              : 'Gatus could not reach the LL-Telemetry API. It may still be starting, or the stack may not be running.' }}
          </p>
          <Button variant="outline" size="sm" @click="probe">Try again</Button>
        </div>
      </div>
    </div>

    <!-- The console -->
    <template v-else>
      <div v-if="!isFullscreen" class="tel-bar">
        <router-link to="/" class="tel-link">
          <ArrowLeft class="h-3.5 w-3.5" />
          Dashboard
        </router-link>
        <span class="tel-bar-label">Field-script telemetry</span>
        <span class="tel-grow"></span>
        <button class="tel-link" type="button" @click="signOut">Sign out</button>
      </div>
      <iframe
        :src="consoleUrl"
        class="tel-frame"
        title="LL-Telemetry operations console"
        referrerpolicy="no-referrer"
      ></iframe>
    </template>
  </div>
</template>

<script setup>
import {ref, computed, onMounted, onBeforeUnmount, nextTick} from 'vue'
import {AlertTriangle, ArrowLeft} from 'lucide-vue-next'
import Loading from '@/components/Loading.vue'
import { Button } from '@/components/ui/button'
import { isFullscreen } from '@/store'

defineProps({
  announcements: {type: Array, default: () => []}
})

// Served by Gatus itself, so the console is same-origin with the SPA and the
// session cookie reaches its fetches without a second sign-in.
const consoleUrl = '/api/v1/telemetry/console'
const healthUrl = '/api/v1/telemetry/health'
const sessionUrl = '/api/v1/telemetry/session'

const state = ref('probing') // probing | login | ready | unconfigured | unreachable | slow
const user = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)
const signedIn = ref(false)
const headerHeight = ref(0)
const clock = ref('')
const userEl = ref(null)

// The chip carries the auth state in the console's own severity vocabulary, so
// the sign-in screen teaches the colour language before you are inside.
const chip = computed(() => {
  if (signedIn.value) return {sev: 'ok', label: 'SIGNED IN'}
  if (busy.value) return {sev: 'info', label: 'CHECKING'}
  if (error.value) return {sev: 'error', label: 'DENIED'}
  return {sev: 'notice', label: 'LOCKED'}
})

const pad = n => String(n).padStart(2, '0')
const tick = () => {
  const d = new Date()
  clock.value = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

const measureHeader = () => {
  const header = document.querySelector('header')
  headerHeight.value = header ? header.offsetHeight : 0
}

let observer = null
let clockTimer = null

const probe = async () => {
  state.value = 'probing'
  try {
    const response = await fetch(healthUrl, {cache: 'no-store'})
    if (response.status === 401) {
      state.value = 'login'
      await nextTick()
      if (userEl.value) userEl.value.focus()
    } else if (response.status === 503 && response.headers.get('X-Telemetry-Not-Configured')) {
      state.value = 'unconfigured'
    } else if (response.status === 429) {
      error.value = 'Too many failed sign-ins. Wait a few minutes and try again.'
      state.value = 'login'
    } else if (response.status === 504) {
      state.value = 'slow'
    } else if (response.ok) {
      state.value = 'ready'
      requestAnimationFrame(measureHeader)
    } else {
      // Anything else unexpected is treated as down rather than rendering a
      // console frame that will only fail every call.
      state.value = 'unreachable'
    }
  } catch (e) {
    state.value = 'unreachable'
  }
}

const signIn = async () => {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const response = await fetch(sessionUrl, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({user: user.value, password: password.value})
    })
    if (response.ok) {
      signedIn.value = true
      password.value = ''
      // Hold the SIGNED IN state briefly so the transition is legible.
      setTimeout(() => { signedIn.value = false; probe() }, 450)
      return
    }
    let message = 'Those credentials were not accepted.'
    try {
      const payload = await response.json()
      if (payload && payload.error) message = payload.error
    } catch (e) { /* keep the default */ }
    error.value = message
  } catch (e) {
    error.value = 'Could not reach Gatus to sign in.'
  } finally {
    busy.value = false
  }
}

const signOut = async () => {
  try {
    await fetch(sessionUrl, {method: 'DELETE'})
  } catch (e) { /* the cookie is dropped server-side; fall through to the form */ }
  user.value = ''
  password.value = ''
  error.value = ''
  probe()
}

// A session can expire while the tab sits open. Re-check on refocus so the user
// gets the sign-in screen rather than a console quietly failing every fetch.
const recheck = () => {
  if (state.value === 'ready' && document.visibilityState === 'visible') probe()
}

onMounted(() => {
  measureHeader()
  tick()
  clockTimer = setInterval(tick, 1000)
  const header = document.querySelector('header')
  if (header && typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(measureHeader)
    observer.observe(header)
  }
  window.addEventListener('resize', measureHeader)
  document.addEventListener('visibilitychange', recheck)
  probe()
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', measureHeader)
  document.removeEventListener('visibilitychange', recheck)
  if (clockTimer) clearInterval(clockTimer)
  if (observer) { observer.disconnect(); observer = null }
})
</script>

<style scoped>
/* LL-Telemetry's own tokens, declared on this component rather than :root so
   nothing leaks into the Gatus theme. Values are lifted verbatim from
   LL-Telemetry/DESIGN.md. */
.tel-root {
  --tel-bg: oklch(0.16 0.012 250);
  --tel-panel: oklch(0.20 0.012 250);
  --tel-raise: oklch(0.24 0.013 250);
  --tel-line: oklch(0.30 0.012 250);
  --tel-fg: oklch(0.94 0.006 250);
  --tel-fg-dim: oklch(0.74 0.010 250);
  --tel-fg-mute: oklch(0.56 0.012 250);
  --tel-brand: oklch(0.70 0.132 236);
  --tel-brand-dim: oklch(0.56 0.110 236);
  --tel-ok: oklch(0.74 0.13 158);
  --tel-info: oklch(0.76 0.10 210);
  --tel-notice: oklch(0.72 0.12 236);
  --tel-error: oklch(0.68 0.16 42);
  --tel-mono: ui-monospace, "Cascadia Mono", "Cascadia Code", "SF Mono", "Segoe UI Mono", Consolas, monospace;
  display: flex;
  flex-direction: column;
  width: 100%;
}
.tel-center { flex: 1; display: flex; align-items: center; justify-content: center; }
.tel-pad { padding: 1.5rem; }
.tel-grow { flex: 1; }

/* --- sign in --- */
.tel-auth {
  flex: 1; display: flex; align-items: center; justify-content: center;
  padding: 24px; background: var(--tel-bg);
}
.tel-card {
  width: 100%; max-width: 420px; background: var(--tel-panel);
  border: 1px solid var(--tel-line); border-radius: 2px; overflow: hidden;
}
/* The event row. Severity owns the full-width tint; never a left stripe. */
.tel-row {
  display: flex; align-items: center; gap: 10px; height: 28px; padding: 0 10px;
  background: var(--tel-raise); border-bottom: 1px solid var(--tel-line);
  font-family: var(--tel-mono); font-size: 11px; font-variant-numeric: tabular-nums;
  transition: background 180ms cubic-bezier(0.16, 1, 0.3, 1);
}
.tel-row.sev-error { background: oklch(0.68 0.16 42 / 0.16); }
.tel-row.sev-ok { background: oklch(0.74 0.13 158 / 0.16); }
.tel-time { color: var(--tel-fg-dim); }
.tel-src { color: var(--tel-fg-mute); }
.tel-chip {
  font-size: 10.5px; letter-spacing: 0.08em; padding: 1px 6px; border-radius: 3px;
  border: 1px solid; line-height: 16px;
}
.chip-notice { color: var(--tel-notice); border-color: oklch(0.72 0.12 236 / 0.4); background: oklch(0.72 0.12 236 / 0.16); }
.chip-info   { color: var(--tel-info);   border-color: oklch(0.76 0.10 210 / 0.4); background: oklch(0.76 0.10 210 / 0.16); }
.chip-error  { color: var(--tel-error);  border-color: oklch(0.68 0.16 42 / 0.4);  background: oklch(0.68 0.16 42 / 0.16); }
.chip-ok     { color: var(--tel-ok);     border-color: oklch(0.74 0.13 158 / 0.4); background: oklch(0.74 0.13 158 / 0.16); }

.tel-body { padding: 20px; }
.tel-eyebrow {
  font-size: 10.5px; text-transform: uppercase; letter-spacing: 0.08em;
  color: var(--tel-fg-mute); margin-bottom: 6px;
}
.tel-title { font-size: 22px; font-weight: 600; color: var(--tel-fg); line-height: 1.1; margin: 0; }
.tel-lede { font-size: 13px; color: var(--tel-fg-dim); margin: 6px 0 20px; }
.tel-label {
  display: block; font-size: 10.5px; text-transform: uppercase; letter-spacing: 0.08em;
  color: var(--tel-fg-mute); margin-bottom: 4px;
}
.tel-input {
  width: 100%; height: 32px; padding: 0 9px; margin-bottom: 14px;
  background: var(--tel-bg); border: 1px solid var(--tel-line); border-radius: 2px;
  color: var(--tel-fg); font-family: var(--tel-mono); font-size: 13px;
}
.tel-input:focus { outline: 2px solid var(--tel-brand); outline-offset: 2px; border-color: var(--tel-brand-dim); }
.tel-input:disabled { opacity: 0.6; }
.tel-error { font-size: 12px; color: var(--tel-error); margin: -6px 0 12px; }
.tel-submit {
  width: 100%; height: 34px; border-radius: 2px; border: none;
  background: var(--tel-brand); color: var(--tel-bg);
  font-size: 13px; font-weight: 600; cursor: pointer;
  transition: background 150ms cubic-bezier(0.16, 1, 0.3, 1);
}
.tel-submit:hover:not(:disabled) { background: var(--tel-brand-dim); }
.tel-submit:focus-visible { outline: 2px solid var(--tel-brand); outline-offset: 2px; }
.tel-submit:disabled { opacity: 0.45; cursor: not-allowed; }
.tel-foot { font-size: 12px; color: var(--tel-fg-mute); margin: 14px 0 0; line-height: 1.45; }
.tel-foot code, .notice code {
  font-family: var(--tel-mono); font-size: 0.85em;
  background: var(--tel-raise); padding: 1px 4px; border-radius: 2px;
}

/* --- console --- */
.tel-bar {
  flex-shrink: 0; display: flex; align-items: center; gap: 12px;
  padding: 6px 16px; border-bottom: 1px solid hsl(var(--border));
}
.tel-link {
  display: inline-flex; align-items: center; gap: 4px; background: none; border: none;
  font-size: 12px; color: hsl(var(--muted-foreground)); cursor: pointer;
  transition: color 150ms cubic-bezier(0.16, 1, 0.3, 1);
}
.tel-link:hover { color: hsl(var(--foreground)); }
.tel-link:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; border-radius: 2px; }
.tel-bar-label { font-size: 12px; color: hsl(var(--muted-foreground) / 0.7); }
.tel-frame { flex: 1; width: 100%; display: block; border: 0; background: var(--tel-bg); }

/* Shared notice vocabulary, matching the other detail views. */
.notice { border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 1.5rem; }
.notice-error { border-style: solid; border-color: hsl(var(--destructive) / 0.4); background: hsl(var(--destructive) / 0.05); }
.notice-title { font-weight: 700; margin-bottom: 0.35rem; }
.notice-body { font-size: 0.875rem; color: hsl(var(--muted-foreground)); }

@media (prefers-reduced-motion: reduce) {
  .tel-row, .tel-submit, .tel-link { transition: none; }
}
</style>
