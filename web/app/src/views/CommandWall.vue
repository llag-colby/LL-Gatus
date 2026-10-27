<template>
  <div class="cw" :class="[verdict.tone, { wall: isFullscreen }]">
    <!-- The verdict is the whole point: one word, readable from across the room. -->
    <div class="cw-verdict">
      <div class="cw-vword">{{ verdict.word }}</div>
      <div class="cw-vsub">{{ verdict.sub }}</div>
      <div class="cw-vmeta">
        <span class="cw-clock">{{ clockLabel }}</span>
        <span class="cw-feed" :class="{ stale: staleAny }">{{ feedLabel }}</span>
      </div>
    </div>

    <div class="cw-lanes">
      <!-- SERVICES -->
      <section class="cw-lane" :class="'t-' + lanes.svc.tone">
        <div class="cw-lh">
          <span class="cw-ln">Services</span>
          <router-link v-if="!isFullscreen" to="/" class="cw-go">open</router-link>
        </div>
        <div class="cw-hero">
          <span class="cw-big">{{ lanes.svc.hero }}</span>
          <span class="cw-cap">{{ lanes.svc.cap }}</span>
        </div>
        <div class="cw-track"><i :style="{ width: lanes.svc.pct + '%' }"></i></div>
        <div class="cw-sub">{{ lanes.svc.sub }}</div>
        <ul class="cw-list">
          <li v-for="r in lanes.svc.rows" :key="r.key">
            <span class="cw-dot"></span>
            <span class="cw-item">{{ r.title }}<em v-if="r.note">{{ r.note }}</em></span>
            <b>{{ r.age }}</b>
          </li>
          <li v-if="!lanes.svc.rows.length" class="cw-none">{{ svcLoaded ? 'Every check is passing.' : 'Reading checks…' }}</li>
        </ul>
        <div v-if="lanes.svc.more" class="cw-more">+{{ lanes.svc.more }} more failing</div>
      </section>

      <!-- SERVICE DESK -->
      <section class="cw-lane" :class="'t-' + lanes.desk.tone">
        <div class="cw-lh">
          <span class="cw-ln">Service desk</span>
          <router-link v-if="!isFullscreen" to="/jira" class="cw-go">open</router-link>
        </div>
        <template v-if="lanes.desk.ok">
          <div class="cw-hero">
            <span class="cw-big">{{ lanes.desk.hero }}</span>
            <span class="cw-cap">{{ lanes.desk.cap }}</span>
          </div>
          <div class="cw-track"><i :style="{ width: lanes.desk.pct + '%' }"></i></div>
          <div class="cw-sub">{{ lanes.desk.sub }}</div>
          <ul class="cw-list">
            <li v-for="r in lanes.desk.rows" :key="r.key" :class="r.tone">
              <span class="cw-dot"></span>
              <span class="cw-item">{{ r.title }}<em>{{ r.note }}</em></span>
              <b>{{ r.age }}</b>
            </li>
            <li v-if="!lanes.desk.rows.length" class="cw-none">Nothing is at risk.</li>
          </ul>
        </template>
        <div v-else class="cw-off">{{ lanes.desk.msg }}</div>
      </section>

      <!-- ENDPOINT SECURITY -->
      <section class="cw-lane" :class="'t-' + lanes.sec.tone">
        <div class="cw-lh">
          <span class="cw-ln">Endpoint security</span>
          <router-link v-if="!isFullscreen" to="/s1" class="cw-go">open</router-link>
        </div>
        <template v-if="lanes.sec.ok">
          <div class="cw-hero">
            <span class="cw-big">{{ lanes.sec.hero }}</span>
            <span class="cw-cap">{{ lanes.sec.cap }}</span>
          </div>
          <div class="cw-track"><i :style="{ width: lanes.sec.pct + '%' }"></i></div>
          <div class="cw-sub">{{ lanes.sec.sub }}</div>
          <ul class="cw-list">
            <li v-for="r in lanes.sec.rows" :key="r.key" :class="r.tone">
              <span class="cw-dot"></span>
              <span class="cw-item">{{ r.title }}<em>{{ r.note }}</em></span>
              <b>{{ r.age }}</b>
            </li>
            <li v-if="!lanes.sec.rows.length" class="cw-none">No open threats.</li>
          </ul>
        </template>
        <div v-else class="cw-off">{{ lanes.sec.msg }}</div>
      </section>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { isFullscreen, now as serverNow } from '@/store'

// Three feeds, one board. Plain polling: this page is glanced at, not
// operated, and a poll that fails just leaves the last good numbers up while
// the age label and the stale flag say so.
const POLL_MS = 20000
const STALE_MS = 5 * 60 * 1000

const endpoints = ref([])
const svcLoaded = ref(false)
const jira = ref(null)
const s1 = ref(null)
const stamp = ref({ svc: 0, jira: 0, s1: 0 })

const get = async (url) => {
  const res = await fetch(url, { cache: 'no-store' })
  if (!res.ok) throw new Error(url + ' ' + res.status)
  return res.json()
}
const poll = async () => {
  const [e, j, s] = await Promise.allSettled([
    get('/api/v1/endpoints/statuses?page=1&pageSize=20'),
    get('/api/v1/jira/metrics'),
    get('/api/v1/s1/metrics'),
  ])
  if (e.status === 'fulfilled' && Array.isArray(e.value)) {
    endpoints.value = e.value; svcLoaded.value = true; stamp.value.svc = Date.now()
  }
  if (j.status === 'fulfilled') { jira.value = j.value; stamp.value.jira = Date.now() }
  if (s.status === 'fulfilled') { s1.value = s.value; stamp.value.s1 = Date.now() }
}

// --- helpers ---------------------------------------------------------------
const fmt = (n) => (n == null ? '0' : Number(n).toLocaleString())
const shortAge = (ms) => {
  const m = Math.max(0, Math.round(ms / 60000))
  if (m < 1) return 'now'
  if (m < 60) return m + 'm'
  const h = Math.floor(m / 60)
  if (h < 48) return h + 'h'
  return Math.floor(h / 24) + 'd'
}
const plural = (n, w) => `${fmt(n)} ${w}${n === 1 ? '' : 's'}`

// --- services -------------------------------------------------------------
const lastOf = (ep) => (ep.results && ep.results.length ? ep.results[ep.results.length - 1] : null)
const failingSince = (ep) => {
  const r = ep.results
  let i = r.length - 1
  while (i >= 0 && !r[i].success) i--
  const first = r[i + 1]
  return first ? new Date(first.timestamp).getTime() : Date.now()
}
const svcFailing = computed(() => endpoints.value
  .filter(ep => { const l = lastOf(ep); return l && !l.success })
  .map(ep => ({ ep, since: failingSince(ep) }))
  .sort((a, b) => a.since - b.since))
const svcTotal = computed(() => endpoints.value.filter(ep => lastOf(ep)).length)

// --- verdict and lanes ------------------------------------------------------
const TOP = 6
const lanes = computed(() => {
  void serverNow.value
  const nowMs = Date.now()

  // services
  const total = svcTotal.value, bad = svcFailing.value.length
  const svc = {
    tone: !svcLoaded.value ? 'idle' : bad === 0 ? 'ok' : bad >= 5 || bad / Math.max(1, total) >= 0.1 ? 'crit' : 'warn',
    hero: svcLoaded.value ? fmt(total - bad) : '--',
    cap: total ? `of ${fmt(total)} checks passing` : 'checks',
    pct: total ? ((total - bad) / total) * 100 : 0,
    sub: bad ? `${plural(bad, 'check')} failing` : 'all clear',
    rows: svcFailing.value.slice(0, TOP).map(({ ep, since }) => ({
      key: ep.key, title: ep.name, note: ep.group || '', age: shortAge(nowMs - since),
    })),
    more: Math.max(0, bad - TOP),
  }

  // service desk
  let desk = { ok: false, tone: 'idle', msg: 'Waiting for Jira…' }
  const jr = jira.value
  if (jr && !jr.configured) desk.msg = 'Jira is not connected.'
  else if (jr && !jr.ok) desk.msg = 'Cannot reach Jira.'
  else if (jr && jr.projects && jr.projects[0]) {
    const p = jr.projects[0]
    const issues = p.issues || []
    const breached = Math.max(0, p.slaBreached || 0)
    const unassigned = p.unassigned || 0
    const risky = issues
      .filter(it => it.slaName || it.slaBreached)
      .map(it => {
        const rem = (it.slaActive && it.slaBreachEpoch) ? it.slaBreachEpoch - nowMs : (it.slaRemainingMs || 0)
        return { it, rem: it.slaBreached ? Math.min(rem, -1) : rem }
      })
      .sort((a, b) => a.rem - b.rem)
    desk = {
      ok: true,
      tone: breached > 0 ? 'crit' : unassigned > 0 ? 'warn' : 'ok',
      hero: fmt(p.totalOpen),
      cap: 'open tickets',
      pct: Math.min(100, (p.totalOpen ? (p.totalOpen - breached) / p.totalOpen : 1) * 100),
      sub: `${breached ? plural(breached, 'SLA breach') + ' · ' : ''}${fmt(unassigned)} unassigned · ${fmt(p.resolvedToday)} closed today`,
      rows: risky.slice(0, TOP).map(({ it, rem }) => ({
        key: it.key, title: it.key, tone: rem < 0 ? 'bad' : rem < 3600000 ? 'warn' : '',
        note: it.summary || '',
        age: rem < 0 ? 'over ' + shortAge(-rem) : shortAge(rem) + ' left',
      })),
    }
  }

  // endpoint security
  let sec = { ok: false, tone: 'idle', msg: 'Waiting for SentinelOne…' }
  const sr = s1.value
  if (sr && !sr.configured) sec.msg = 'SentinelOne is not connected.'
  else if (sr && !sr.ok) sec.msg = 'Cannot reach SentinelOne.'
  else if (sr) {
    const th = sr.threats || {}, ag = sr.agents || {}
    const open = sr.open || []
    const crit = open.filter(x => x.needsAction).length
    const rank = (x) => x.needsAction ? 0 : x.confidence === 'malicious' ? 1 : x.confidence === 'suspicious' ? 2 : 3
    const rows = open.slice().sort((a, b) => rank(a) - rank(b) || String(a.createdAt).localeCompare(String(b.createdAt)))
    const pctOnline = ag.total ? Math.round((ag.online / ag.total) * 100) : 0
    sec = {
      ok: true,
      tone: crit > 0 || ag.infected > 0 ? 'crit' : (th.maliciousNotResolved || pctOnline < 90) ? 'warn' : 'ok',
      hero: fmt(th.notResolved),
      cap: 'open threats',
      pct: pctOnline,
      sub: `${crit ? crit + ' need action · ' : ''}${pctOnline}% of ${fmt(ag.total)} agents online`,
      rows: rows.slice(0, TOP).map(x => ({
        key: x.id, tone: x.needsAction ? 'bad' : x.confidence === 'malicious' ? 'warn' : '',
        title: x.endpoint || 'unknown host', note: x.name || '',
        age: shortAge(nowMs - new Date(x.createdAt).getTime()),
      })),
    }
  }
  return { svc, desk, sec }
})

const verdict = computed(() => {
  const all = [lanes.value.svc, lanes.value.desk, lanes.value.sec].filter(l => l.tone !== 'idle')
  if (!all.length) return { tone: 'idle', word: 'LOADING', sub: 'Reading the first data…' }
  const crit = all.filter(l => l.tone === 'crit').length
  const warn = all.filter(l => l.tone === 'warn').length
  if (crit) return { tone: 'crit', word: 'ACTION NEEDED', sub: `${crit} area${crit > 1 ? 's' : ''} need a person now` }
  if (warn) return { tone: 'warn', word: 'WATCH', sub: `${warn} area${warn > 1 ? 's' : ''} drifting` }
  return { tone: 'ok', word: 'ALL CLEAR', sub: 'Services, service desk and endpoints are healthy' }
})

// --- feed health -------------------------------------------------------------
const staleAny = computed(() => {
  void serverNow.value
  const t = Date.now()
  return Object.values(stamp.value).some(s => s > 0 && t - s > STALE_MS)
})
const feedLabel = computed(() => {
  void serverNow.value
  const newest = Math.max(...Object.values(stamp.value))
  if (!newest) return 'waiting for first poll'
  if (staleAny.value) return 'a feed has gone quiet'
  return `updated ${Math.max(0, Math.round((Date.now() - newest) / 1000))}s ago`
})
const clockLabel = computed(() => new Date(serverNow.value)
  .toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }))

let timer = null
onMounted(() => {
  document.title = 'Command'
  poll()
  timer = setInterval(poll, POLL_MS)
})
onUnmounted(() => clearInterval(timer))
</script>

<style scoped>
.cw {
  --tone: var(--j-idle);
  min-height: calc(100vh - 4rem); padding: 1rem 1.25rem; display: flex; flex-direction: column; gap: 1rem;
  background: hsl(var(--background)); color: hsl(var(--foreground));
}
.cw.wall { height: 100vh; min-height: 0; padding: 1rem 1.25rem 1.1rem; }
.cw.ok { --tone: var(--j-ok); } .cw.warn { --tone: var(--j-warn); } .cw.crit { --tone: var(--j-crit); }

/* verdict band: the page's one loud thing */
.cw-verdict {
  flex: none; display: grid; grid-template-columns: auto 1fr auto; align-items: center; gap: 0.4rem 2rem;
  padding: 1.1rem 1.6rem; border-radius: 14px;
  border: 1px solid color-mix(in srgb, var(--tone) 45%, hsl(var(--border)));
  background: color-mix(in srgb, var(--tone) 11%, hsl(var(--card)));
}
.cw-vword { font-family: var(--j-mono); font-size: clamp(2.2rem, 5.2vw, 5rem); font-weight: 800; letter-spacing: -0.04em; line-height: 1; color: var(--tone); }
.cw-vsub { font-size: clamp(1rem, 1.6vw, 1.6rem); font-weight: 600; color: hsl(var(--muted-foreground)); }
.cw-vmeta { display: flex; flex-direction: column; align-items: flex-end; gap: 0.2rem; font-family: var(--j-mono); }
.cw-clock { font-size: clamp(1.5rem, 2.4vw, 2.4rem); font-weight: 700; font-variant-numeric: tabular-nums; }
.cw-feed { font-size: 0.78rem; color: hsl(var(--muted-foreground)); }
.cw-feed.stale { color: var(--j-crit); font-weight: 700; }

.cw-lanes { flex: 1 1 auto; min-height: 0; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 1rem; }
.cw-lane {
  --lt: var(--j-idle);
  display: flex; flex-direction: column; min-height: 0; min-width: 0; padding: 1.1rem 1.25rem; gap: 0.55rem;
  border: 1px solid hsl(var(--border)); border-top: 4px solid var(--lt); border-radius: 14px; background: hsl(var(--card)); overflow: hidden;
}
.cw-lane.t-ok { --lt: var(--j-ok); } .cw-lane.t-warn { --lt: var(--j-warn); } .cw-lane.t-crit { --lt: var(--j-crit); }
.cw-lh { display: flex; justify-content: space-between; align-items: baseline; }
.cw-ln { font-family: var(--j-mono); font-size: 0.8rem; font-weight: 800; letter-spacing: 0.16em; text-transform: uppercase; color: hsl(var(--muted-foreground)); }
.cw-go { font-family: var(--j-mono); font-size: 0.72rem; color: hsl(var(--muted-foreground)); text-decoration: underline; }
.cw-hero { display: flex; align-items: baseline; gap: 0.8rem; flex-wrap: wrap; }
.cw-big { font-family: var(--j-mono); font-size: clamp(3.4rem, 7vw, 7rem); font-weight: 800; line-height: 0.9; letter-spacing: -0.06em; font-variant-numeric: tabular-nums; color: var(--lt); }
.cw-cap { font-size: clamp(0.9rem, 1.2vw, 1.25rem); font-weight: 600; color: hsl(var(--muted-foreground)); }
.cw-track { flex: none; height: 0.5rem; border-radius: 3px; background: hsl(var(--muted)); overflow: hidden; }
.cw-track i { display: block; height: 100%; background: var(--lt); border-radius: 3px; transition: width 600ms ease; }
.cw-sub { flex: none; font-size: clamp(0.85rem, 1.1vw, 1.1rem); color: hsl(var(--muted-foreground)); }

.cw-list { list-style: none; margin: 0.4rem 0 0; padding: 0; flex: 1 1 auto; min-height: 0; overflow: hidden; display: flex; flex-direction: column; border-top: 1px solid hsl(var(--border)); }
.cw-list li { display: grid; grid-template-columns: 0.55rem minmax(0, 1fr) auto; gap: 0.8rem; align-items: center; padding: 0.6rem 0; border-bottom: 1px solid hsl(var(--border) / 0.55); }
.cw-dot { width: 0.55rem; height: 0.55rem; border-radius: 50%; background: var(--j-idle); }
.cw-list li.bad .cw-dot, .t-crit .cw-list li:not(.warn):not(.cw-none) .cw-dot { background: var(--j-crit); }
.cw-list li.warn .cw-dot { background: var(--j-warn); }
.cw-item { min-width: 0; display: flex; flex-direction: column; font-size: clamp(0.95rem, 1.25vw, 1.3rem); font-weight: 700; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cw-item em { font-style: normal; font-weight: 500; font-size: 0.78em; color: hsl(var(--muted-foreground)); overflow: hidden; text-overflow: ellipsis; }
.cw-list b { font-family: var(--j-mono); font-size: clamp(0.9rem, 1.15vw, 1.2rem); font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); white-space: nowrap; }
.cw-list li.bad b { color: var(--j-crit); } .cw-list li.warn b { color: var(--j-warn); }
.cw-list .cw-none { display: block; border: 0; padding: 1rem 0; font-size: 1rem; font-weight: 500; color: hsl(var(--muted-foreground)); }
.cw-more { flex: none; font-family: var(--j-mono); font-size: 0.8rem; color: hsl(var(--muted-foreground)); }
.cw-off { margin-top: 1rem; font-size: 1.1rem; color: hsl(var(--muted-foreground)); }

@media (max-width: 1100px) {
  .cw-lanes { grid-template-columns: 1fr; }
  .cw-verdict { grid-template-columns: 1fr; }
  .cw-vmeta { align-items: flex-start; }
}
@media (prefers-reduced-motion: reduce) { .cw-track i { transition: none; } }
</style>
