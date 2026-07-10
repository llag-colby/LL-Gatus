<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 space-y-4 phone-panel">

      <!-- Toolbar -->
      <div class="flex items-end justify-between gap-4 flex-wrap">
        <div class="flex items-center gap-3">
          <router-link to="/" class="text-muted-foreground hover:text-foreground transition-colors mb-1"
            data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-5 w-5" />
          </router-link>
          <div>
            <div class="eyebrow">Line panel · {{ phones.length }} extensions</div>
            <h1 class="text-2xl font-bold tracking-tight leading-none mt-0.5">
              {{ locationName }} <span class="text-muted-foreground font-normal">phones</span>
            </h1>
          </div>
        </div>
        <div class="flex items-center gap-2 mb-0.5">
          <span class="flex items-center gap-1.5 text-xs text-muted-foreground">
            <span class="sweep-dot" :class="{ dead: !updatedAt }"></span>
            <span class="font-mono">swept {{ updatedLabel }}</span>
          </span>
          <input v-model="search" type="text" placeholder="filter ext / name / ip / dept"
            class="text-sm font-mono bg-background border rounded-md px-3 py-1.5 w-48 focus:outline-none focus:ring-2 focus:ring-ring" />
          <Button variant="ghost" size="icon" class="h-9 w-9" @click="fetchInventory"
            data-tooltip="Refresh" data-tip-pos="bottom">
            <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
          </Button>
        </div>
      </div>

      <!-- Empty state: no inventory reported yet -->
      <div v-if="loaded && phones.length === 0" class="empty-state">
        <div class="text-base font-semibold mb-1">No phones reported yet</div>
        <div class="text-sm text-muted-foreground">
          The collector hasn't pushed inventory for {{ locationName }}. Once
          <span class="font-mono">phone_collector.py</span> runs, lines appear here within one sweep.
        </div>
      </div>

      <!-- Directory table -->
      <section v-if="phones.length">
        <div class="overflow-x-auto rounded-lg border">
          <table class="w-full text-sm directory">
            <thead>
              <tr>
                <th></th>
                <th>Ext</th>
                <th>Name</th>
                <th>Direct #</th>
                <th>Department</th>
                <th>Model</th>
                <th>Firmware</th>
                <th>IP address</th>
                <th>MAC</th>
                <th>SIP</th>
                <th>Reach</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="p in filteredPhones" :key="'row-' + p.ext + p.mac">
                <td><span class="lamp-dot" :class="p.online ? 'up' : 'down'"></span></td>
                <td class="mono strong">{{ p.ext || '—' }}</td>
                <td>{{ p.name || 'unassigned' }}</td>
                <td class="mono dim">{{ p.did || '—' }}</td>
                <td class="dim">{{ p.department || '—' }}</td>
                <td class="dim">{{ p.model || '—' }}</td>
                <td class="mono dim">{{ p.firmware || '—' }}</td>
                <td class="mono">{{ p.ip || '—' }}</td>
                <td class="mono dim">{{ p.mac || '—' }}</td>
                <td>
                  <span class="pill" :class="p.sipStatus === 'registered' ? 'pill-on' : 'pill-off'">{{ p.sipStatus || 'unknown' }}</span>
                </td>
                <td><span :class="p.reachable ? 'st-text-up' : 'st-text-down'">{{ p.reachable ? 'yes' : 'no' }}</span></td>
              </tr>
              <tr v-if="filteredPhones.length === 0">
                <td colspan="11" class="py-10 text-center text-muted-foreground">No lines match “{{ search }}”.</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft, RefreshCw } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { generatePrettyTimeAgo } from '@/utils/time'

const route = useRoute()

const locationName = computed(() => {
  const slug = (route.params.key || '').replace(/^phones_/, '')
  return slug.split('-').map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ')
})

const loading = ref(false)
const loaded = ref(false)
const search = ref('')
const updatedAt = ref(null)
const phones = ref([])

const filteredPhones = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return phones.value
  return phones.value.filter(p =>
    [p.ext, p.name, p.did, p.department, p.ip, p.mac, p.model].some(v => (v || '').toString().toLowerCase().includes(q)))
})

const updatedLabel = computed(() => {
  if (!updatedAt.value) return 'never'
  try { return generatePrettyTimeAgo(new Date(updatedAt.value)) } catch (e) { return '—' }
})

const fetchInventory = async () => {
  loading.value = true
  try {
    const res = await fetch(`/api/v1/phones/${route.params.key}`, { cache: 'no-store' })
    if (res.ok) {
      const data = await res.json()
      phones.value = Array.isArray(data.phones) ? data.phones : []
      updatedAt.value = data.updatedAt || null
    } else {
      phones.value = []
      updatedAt.value = null
    }
  } catch (e) {
    phones.value = []
    updatedAt.value = null
  } finally {
    loading.value = false
    loaded.value = true
  }
}

let poll = null
onMounted(() => {
  fetchInventory()
  poll = setInterval(fetchInventory, 15000)
})
onUnmounted(() => { if (poll) clearInterval(poll) })
</script>

<style scoped>
.phone-panel { width: 100%; }

.empty-state {
  border: 1px dashed hsl(var(--border));
  border-radius: 10px;
  padding: 2.5rem 1.5rem;
  text-align: center;
}

/* Equipment-panel micro-label */
.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px;
  letter-spacing: 0.18em;
  text-transform: uppercase;
  color: hsl(var(--muted-foreground));
  font-weight: 600;
}

/* Status lamp in the table's first column */
.lamp-dot { width: 8px; height: 8px; border-radius: 999px; display: inline-block; align-self: center; }
.lamp-dot.up   { background: var(--status-up);   box-shadow: 0 0 7px -1px var(--status-up); }
.lamp-dot.down { background: var(--status-down); box-shadow: 0 0 7px -1px var(--status-down); }

/* Sweep indicator dot (static — no pulsing) */
.sweep-dot { width: 7px; height: 7px; border-radius: 999px; background: var(--status-up); box-shadow: 0 0 7px -1px var(--status-up); }
.sweep-dot.dead { background: hsl(var(--muted-foreground) / 0.5); box-shadow: none; }

/* --- Directory table --- */
.directory thead th {
  text-align: left; font-size: 10px; letter-spacing: 0.12em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); font-weight: 600; padding: 0.6rem 0.75rem;
  border-bottom: 1px solid hsl(var(--border)); background: hsl(var(--muted) / 0.4);
}
.directory tbody td { padding: 0.5rem 0.75rem; border-bottom: 1px solid hsl(var(--border) / 0.6); }
.directory tbody tr:last-child td { border-bottom: 0; }
.directory tbody tr:hover td { background: hsl(var(--accent) / 0.4); }
.directory .mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.directory .strong { font-weight: 700; }
.directory .dim { color: hsl(var(--muted-foreground)); }

.pill { font-size: 10px; letter-spacing: 0.04em; text-transform: uppercase; padding: 0.1rem 0.4rem; border-radius: 5px; font-weight: 600; }
.pill-on { background: var(--status-up); color: #fff; }
.pill-off { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }

</style>
