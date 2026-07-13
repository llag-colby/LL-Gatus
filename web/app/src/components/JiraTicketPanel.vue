<template>
  <teleport to="body">
    <transition name="drawer">
      <div v-if="issueKey" class="ticket-scrim" @click.self="$emit('close')">
        <aside class="ticket-drawer" role="dialog" aria-modal="true">
          <!-- Header -->
          <div class="td-head">
            <div class="flex items-center gap-2 min-w-0">
              <a v-if="detail && detail.url" :href="detail.url" target="_blank" rel="noopener" class="td-key">{{ issueKey }}</a>
              <span v-else class="td-key">{{ issueKey }}</span>
              <span v-if="detail && detail.type" class="ttag" :class="typeClass(detail.type)">{{ detail.type }}</span>
            </div>
            <button class="td-close" @click="$emit('close')" aria-label="Close"><X class="h-4 w-4" /></button>
          </div>

          <div class="td-body">
            <div v-if="loading" class="td-loading">Loading {{ issueKey }}…</div>
            <div v-else-if="error" class="td-error">Couldn’t load {{ issueKey }}: {{ error }}</div>

            <template v-else-if="detail">
              <h2 class="td-summary">{{ detail.summary }}</h2>

              <!-- status + priority line -->
              <div class="td-tags">
                <span class="td-status"><span class="sdot" :class="'cat-' + (detail.category || 'new')"></span>{{ detail.status || '—' }}</span>
                <span class="td-prio" :class="'prio-' + prioKey(detail.priority)">{{ detail.priority || 'No priority' }}</span>
                <span v-if="anyBreached" class="td-breach">SLA overdue</span>
              </div>

              <!-- meta grid -->
              <dl class="td-meta">
                <div><dt>Assignee</dt><dd :class="{ dim: !detail.assignee }">{{ detail.assignee || 'Unassigned' }}</dd></div>
                <div><dt>Reporter</dt><dd :class="{ dim: !detail.reporter }">{{ detail.reporter || '—' }}</dd></div>
                <div><dt>Created</dt><dd>{{ fmtDate(detail.created) }}</dd></div>
                <div><dt>Updated</dt><dd>{{ fmtDate(detail.updated) }}</dd></div>
              </dl>
              <div v-if="detail.labels && detail.labels.length" class="td-labels">
                <span v-for="l in detail.labels" :key="l" class="lbl">{{ l }}</span>
              </div>

              <!-- SLA -->
              <div v-if="detail.slas && detail.slas.length" class="td-section">
                <h3>SLA</h3>
                <ul class="sla-list">
                  <li v-for="s in detail.slas" :key="s.name">
                    <span class="sla-name">{{ s.name }}</span>
                    <span class="sla-val" :class="{ breach: s.breached }">{{ s.remaining || (s.ongoing ? '—' : 'done') }}</span>
                  </li>
                </ul>
              </div>

              <!-- Description -->
              <div class="td-section">
                <h3>Description</h3>
                <div v-if="detail.descriptionHtml" class="prose" v-html="clean(detail.descriptionHtml)"></div>
                <p v-else class="dim text-sm">No description.</p>
              </div>

              <!-- Comments -->
              <div v-if="detail.comments && detail.comments.length" class="td-section">
                <h3>Recent activity</h3>
                <div v-for="(c, i) in detail.comments" :key="i" class="cmt">
                  <div class="cmt-head"><span class="cmt-who">{{ c.author }}</span><span class="cmt-when">{{ ago(c.created) }}</span></div>
                  <div class="prose prose-sm" v-html="clean(c.html)"></div>
                </div>
              </div>

              <a v-if="detail.url" :href="detail.url" target="_blank" rel="noopener" class="td-open">
                Open in Jira <ExternalLink class="h-3.5 w-3.5" />
              </a>
            </template>
          </div>
        </aside>
      </div>
    </transition>
  </teleport>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { X, ExternalLink } from 'lucide-vue-next'
import DOMPurify from 'dompurify'
import { generatePrettyTimeAgo } from '@/utils/time'

const props = defineProps({ issueKey: { type: String, default: '' } })
const emit = defineEmits(['close'])

const detail = ref(null)
const loading = ref(false)
const error = ref('')
const anyBreached = ref(false)

const clean = (html) => DOMPurify.sanitize(html || '', { FORBID_TAGS: ['img', 'style'], FORBID_ATTR: ['style'] })
const prioKey = (p) => (p || '').toLowerCase().replace(/[^a-z]/g, '') || 'none'
const typeClass = (t) => {
  const s = (t || '').toLowerCase()
  if (s.includes('incident') || s.includes('bug')) return 'ttag-incident'
  if (s.includes('problem')) return 'ttag-problem'
  if (s.includes('request') || s.includes('service')) return 'ttag-request'
  if (s.includes('change')) return 'ttag-change'
  return 'ttag-task'
}
const fmtDate = (iso) => {
  if (!iso) return '—'
  try { return new Date(iso).toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }) } catch (e) { return iso }
}
const ago = (iso) => { try { return generatePrettyTimeAgo(new Date(iso)) } catch (e) { return '' } }

// refresh=true does a quiet in-place update (no loading flash) so the open
// panel keeps showing live comments / SLA movement while a ticket is open.
const load = async (key, refresh = false) => {
  if (!key) return
  if (!refresh) { loading.value = true; error.value = ''; detail.value = null; anyBreached.value = false }
  try {
    const res = await fetch(`/api/v1/jira/issue/${encodeURIComponent(key)}`, { cache: 'no-store' })
    const data = await res.json()
    if (!res.ok) { if (!refresh) error.value = data.error || `HTTP ${res.status}`; return }
    detail.value = data
    anyBreached.value = (data.slas || []).some(s => s.breached)
  } catch (e) {
    if (!refresh) error.value = e.message || 'network error'
  } finally {
    loading.value = false
  }
}

let poll = null
const startPoll = (key) => { stopPoll(); if (key) poll = setInterval(() => load(key, true), 15000) }
const stopPoll = () => { if (poll) { clearInterval(poll); poll = null } }

watch(() => props.issueKey, (k) => { if (k) { load(k); startPoll(k) } else { stopPoll() } })

const onKey = (e) => { if (e.key === 'Escape' && props.issueKey) emit('close') }
onMounted(() => { document.addEventListener('keydown', onKey); if (props.issueKey) { load(props.issueKey); startPoll(props.issueKey) } })
onUnmounted(() => { document.removeEventListener('keydown', onKey); stopPoll() })
</script>

<style scoped>
.ticket-scrim { position: fixed; inset: 0; z-index: 60; background: rgb(6 8 14 / 0.55); display: flex; justify-content: flex-end; }
.ticket-drawer {
  width: min(460px, 94vw); height: 100%; overflow-y: auto; background: hsl(var(--card));
  border-left: 1px solid hsl(var(--border)); box-shadow: -24px 0 60px -20px rgb(0 0 0 / 0.6);
}
.td-head { position: sticky; top: 0; z-index: 1; display: flex; align-items: center; justify-content: space-between; gap: 0.5rem;
  padding: 0.85rem 1.1rem; border-bottom: 1px solid hsl(var(--border)); background: hsl(var(--card)); }
.td-key { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-weight: 700; font-size: 0.9rem; color: hsl(var(--foreground)); text-decoration: none; }
.td-key:hover { text-decoration: underline; text-underline-offset: 3px; }
.td-close { display: inline-flex; align-items: center; justify-content: center; height: 30px; width: 30px; border-radius: 7px; color: hsl(var(--muted-foreground)); }
.td-close:hover { background: hsl(var(--accent) / 0.6); color: hsl(var(--foreground)); }

.td-body { padding: 1.1rem; }
.td-loading, .td-error { padding: 2rem 0; color: hsl(var(--muted-foreground)); font-size: 0.9rem; }
.td-error { color: #f2b8a2; }
.td-summary { font-size: 1.15rem; font-weight: 700; line-height: 1.3; letter-spacing: -0.01em; }

.td-tags { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem; margin-top: 0.75rem; }
.td-status { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.8rem; color: hsl(var(--muted-foreground)); }
.sdot { width: 7px; height: 7px; border-radius: 999px; background: hsl(var(--muted-foreground)); }
.sdot.cat-new { background: #8a8f98; } .sdot.cat-indeterminate { background: #e0a458; } .sdot.cat-done { background: #5aa06b; }
.td-prio { font-size: 0.8rem; font-weight: 600; }
.td-breach { font-size: 0.68rem; font-weight: 700; letter-spacing: 0.05em; text-transform: uppercase; color: #f2b8a2; background: rgb(190 60 40 / 0.18); padding: 0.12rem 0.45rem; border-radius: 5px; }

.td-meta { display: grid; grid-template-columns: 1fr 1fr; gap: 0.85rem 1rem; margin-top: 1.1rem; }
.td-meta dt { font-size: 0.62rem; letter-spacing: 0.11em; text-transform: uppercase; color: hsl(var(--muted-foreground)); font-weight: 600; }
.td-meta dd { font-size: 0.88rem; margin-top: 0.15rem; }
.td-meta dd.dim { color: hsl(var(--muted-foreground)); font-style: italic; }

.td-labels { display: flex; flex-wrap: wrap; gap: 0.35rem; margin-top: 0.9rem; }
.lbl { font-size: 0.72rem; color: hsl(var(--muted-foreground)); background: hsl(var(--muted) / 0.6); border-radius: 5px; padding: 0.1rem 0.45rem; }

.td-section { margin-top: 1.5rem; }
.td-section h3 { font-size: 0.66rem; letter-spacing: 0.12em; text-transform: uppercase; color: hsl(var(--muted-foreground)); font-weight: 700; margin-bottom: 0.55rem; }

.sla-list li { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 0.4rem 0; border-bottom: 1px solid hsl(var(--border) / 0.5); }
.sla-list li:last-child { border-bottom: 0; }
.sla-name { font-size: 0.85rem; }
.sla-val { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.82rem; color: #7bbd8a; }
.sla-val.breach { color: #ef6b53; }

.prose { font-size: 0.9rem; line-height: 1.6; color: hsl(var(--foreground)); word-break: break-word; }
/* Force every element in the pasted Jira HTML to the readable foreground and drop
   any inherited background, so a macro / table / span can't render dark-on-dark. */
.prose :deep(*) { color: hsl(var(--foreground)); background-color: transparent; border-color: hsl(var(--border)); }
.prose :deep(p) { margin: 0 0 0.6rem; }
.prose :deep(a) { color: #e0a458; text-decoration: underline; text-underline-offset: 2px; }
.prose :deep(ul), .prose :deep(ol) { margin: 0 0 0.6rem 1.1rem; }
.prose :deep(h1), .prose :deep(h2), .prose :deep(h3), .prose :deep(h4) { font-weight: 700; margin: 0.6rem 0 0.3rem; }
.prose :deep(code), .prose :deep(pre) { font-family: ui-monospace, monospace; background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.9em; }
.prose :deep(pre) { padding: 0.6rem 0.75rem; overflow-x: auto; }
.prose :deep(table) { border-collapse: collapse; width: 100%; }
.prose :deep(th), .prose :deep(td) { border: 1px solid hsl(var(--border)); padding: 0.3rem 0.5rem; text-align: left; }
.prose :deep(th) { background: hsl(var(--muted) / 0.5); }
.prose :deep(blockquote) { border-left: 3px solid hsl(var(--border)); padding-left: 0.75rem; color: hsl(var(--muted-foreground)); margin: 0 0 0.6rem; }
.prose-sm { font-size: 0.82rem; }

.cmt { padding: 0.65rem 0; border-bottom: 1px solid hsl(var(--border) / 0.5); }
.cmt:last-child { border-bottom: 0; }
.cmt-head { display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 0.25rem; }
.cmt-who { font-size: 0.82rem; font-weight: 600; }
.cmt-when { font-size: 0.72rem; color: hsl(var(--muted-foreground)); }
.dim { color: hsl(var(--muted-foreground)); }

.td-open { display: inline-flex; align-items: center; gap: 0.35rem; margin-top: 1.6rem; font-size: 0.82rem; color: #e0a458; text-decoration: none; }
.td-open:hover { text-decoration: underline; text-underline-offset: 3px; }

.ttag { font-size: 10px; letter-spacing: 0.03em; text-transform: uppercase; padding: 0.1rem 0.4rem; border-radius: 5px; font-weight: 700; white-space: nowrap; }
.ttag-incident { background: rgb(190 60 40 / 0.16); color: #ef8b74; }
.ttag-request { background: hsl(var(--muted) / 0.7); color: hsl(var(--foreground)); }
.ttag-change { background: rgb(224 160 88 / 0.16); color: #e0a458; }
.ttag-problem { background: rgb(224 160 88 / 0.18); color: #e6b877; }
.ttag-task { background: hsl(var(--muted) / 0.7); color: hsl(var(--muted-foreground)); }

.prio-highest { color: #ef6b53; } .prio-high { color: #e0a458; } .prio-medium { color: hsl(var(--muted-foreground)); } .prio-low, .prio-lowest, .prio-none { color: hsl(var(--muted-foreground)); }

/* drawer motion (transform only) */
.drawer-enter-active, .drawer-leave-active { transition: opacity 0.2s ease; }
.drawer-enter-active .ticket-drawer, .drawer-leave-active .ticket-drawer { transition: transform 0.28s cubic-bezier(0.22, 1, 0.36, 1); }
.drawer-enter-from, .drawer-leave-to { opacity: 0; }
.drawer-enter-from .ticket-drawer, .drawer-leave-to .ticket-drawer { transform: translateX(100%); }
</style>
