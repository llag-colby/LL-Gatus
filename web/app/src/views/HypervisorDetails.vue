<template>
  <div class="dashboard-container detail-page bg-background">
    <div class="w-full px-4 sm:px-6 py-4 space-y-4 hv-panel">

      <!-- Toolbar -->
      <div class="flex items-end justify-between gap-4 flex-wrap">
        <div class="flex items-center gap-3">
          <router-link to="/" class="back-link text-muted-foreground hover:text-foreground transition-colors mb-1"
            data-tooltip="Back to dashboard" data-tip-pos="bottom">
            <ArrowLeft class="h-5 w-5" />
          </router-link>
          <div class="min-w-0">
            <div class="eyebrow">Hypervisor · {{ site }}{{ model ? ' · ' + model : '' }}</div>
            <h1 class="text-2xl font-bold tracking-tight leading-none mt-0.5 truncate">
              {{ hostName }}
              <span v-if="address" class="text-muted-foreground font-normal text-base mono">{{ address }}</span>
            </h1>
          </div>
        </div>
        <div class="flex items-center gap-3 mb-0.5">
          <span class="live-ind" :class="{ on: reportingFresh }"><span class="ldot"></span>{{ updatedLabel }}</span>
          <Button variant="ghost" size="icon" class="refresh h-9 w-9" @click="refreshAll"
            data-tooltip="Refresh" data-tip-pos="bottom">
            <RefreshCw class="h-5 w-5" :class="{ 'animate-spin': loading }" />
          </Button>
          <MonitorToggle :endpoint-key="routeKey" />
        </div>
      </div>

      <!-- Paused -->
      <div v-if="!monitored" class="notice notice-paused">
        <div class="notice-title flex items-center gap-2"><Pause class="h-4 w-4" /> Monitoring is paused</div>
        <p class="notice-body">
          Gatus is not accepting collector pushes for <code>{{ routeKey }}</code>, so this host is not being
          probed or inventoried. The history below is frozen at the last recorded result.
        </p>
      </div>

      <!-- Loading -->
      <div v-if="!loaded" class="notice">
        <div class="notice-title">Reading this host</div>
        <p class="notice-body">Fetching the latest inventory snapshot and the recorded history.</p>
      </div>

      <template v-else>
        <!-- Nothing has ever reported -->
        <div v-if="notReported" class="notice">
          <div class="notice-title">No inventory for this host yet</div>
          <p class="notice-body">
            <code>hv-collector</code> pushes to <code>{{ routeKey }}</code> and nothing has arrived.
            Check that the container is running (<code>docker logs hv-collector</code>) and that
            <code>PHONES_PUSH_TOKEN</code> is set. A result lands within one sweep, about 60 to 120 seconds.
          </p>
        </div>

        <template v-else>
          <!-- Status band -->
          <div class="flex flex-wrap items-center gap-3">
            <span class="status-pill" :class="statusMeta.cls">{{ statusMeta.label }}</span>
            <span class="band-counts">{{ bandSummary }}</span>
            <span v-if="reason" class="band-warn">{{ reason }}</span>
          </div>

          <!-- Credentials missing: liveness only -->
          <div v-if="notConfigured" class="notice notice-nodata">
            <div class="notice-title flex items-center gap-2">
              <KeyRound class="h-4 w-4" /> Liveness only, no inventory
            </div>
            <p class="notice-body">
              The host answered a TCP probe, so it is alive, but <code>hv-collector</code> has no
              WinRM account and cannot read CPU, memory, volumes or guests. Add one to
              <code>.env</code> and restart the collector:
            </p>
            <pre class="env-pre">HV_USER=longlewis\svc-gatus
HV_PASS=...</pre>
            <p class="notice-body mt-2">
              It falls back to <code>SMB_USER</code> / <code>SMB_PASS</code> if those are set, so one
              service account can cover both collectors. Reading <code>Get-CimInstance</code> and
              <code>Get-VM</code> over WinRM needs local Administrators on the host, or a delegated
              WinRM session configuration.
            </p>
          </div>

          <!-- KPI strip -->
          <section class="kpis">
            <div class="kpi">
              <div class="eyebrow">CPU</div>
              <div class="kpi-val" :class="pctClass(cpu.loadPercent, HV_WARN.cpu)">
                {{ cpu.loadPercent !== undefined && cpu.loadPercent !== null ? cpu.loadPercent + '%' : '-' }}
              </div>
              <div class="kpi-sub">{{ cpuSub }}</div>
            </div>
            <div class="kpi">
              <div class="eyebrow">Memory</div>
              <div class="kpi-val" :class="pctClass(memory.usedPct, HV_WARN.mem)">
                {{ memory.usedPct !== undefined && memory.usedPct !== null ? memory.usedPct + '%' : '-' }}
              </div>
              <div class="kpi-sub">{{ memSub }}</div>
            </div>
            <div class="kpi">
              <div class="eyebrow">Guests running</div>
              <div class="kpi-val">{{ vmSummary.running }}<span class="kpi-of">/{{ vmSummary.total }}</span></div>
              <div class="kpi-sub">{{ vmSub }}</div>
            </div>
            <div class="kpi">
              <div class="eyebrow">Fullest volume</div>
              <div class="kpi-val" :class="pctClass(worstVolume.usedPct, HV_WARN.disk)">
                {{ worstVolume.usedPct !== null ? worstVolume.usedPct + '%' : '-' }}
              </div>
              <div class="kpi-sub">{{ worstVolume.drive ? worstVolume.drive + ' · ' + fmtGB(worstVolume.freeGB) + ' free' : '-' }}</div>
            </div>
            <div class="kpi">
              <div class="eyebrow">Uptime</div>
              <div class="kpi-val">{{ fmtUptime(os.uptimeHours) }}</div>
              <div class="kpi-sub">{{ bootSub }}</div>
            </div>
            <div class="kpi">
              <div class="eyebrow">System errors 24h</div>
              <div class="kpi-val" :class="errorsCls">{{ detail.errors24h !== null && detail.errors24h !== undefined ? detail.errors24h : '-' }}</div>
              <div class="kpi-sub">level 1 to 2 in the System log</div>
            </div>
          </section>

          <!-- Load bars -->
          <section v-if="hasLive" class="grid gap-3 md:grid-cols-2">
            <div class="meter">
              <div class="meter-head">
                <span class="meter-title">Processor load</span>
                <span class="meter-num mono" :class="pctClass(cpu.loadPercent, HV_WARN.cpu)">{{ cpu.loadPercent }}%</span>
              </div>
              <div class="bar"><div class="fill" :class="pctClass(cpu.loadPercent, HV_WARN.cpu)" :style="{ width: clamp(cpu.loadPercent) + '%' }"></div></div>
              <p class="meter-note">
                Point sample at the sweep, averaged across {{ cpu.sockets || '?' }}
                {{ cpu.sockets === 1 ? 'socket' : 'sockets' }}. Amber from {{ HV_WARN.cpu - 15 }}%,
                degraded at {{ HV_WARN.cpu }}%.
              </p>
            </div>
            <div class="meter">
              <div class="meter-head">
                <span class="meter-title">Memory in use</span>
                <span class="meter-num mono" :class="pctClass(memory.usedPct, HV_WARN.mem)">{{ memory.usedPct }}%</span>
              </div>
              <div class="bar"><div class="fill" :class="pctClass(memory.usedPct, HV_WARN.mem)" :style="{ width: clamp(memory.usedPct) + '%' }"></div></div>
              <p class="meter-note">
                {{ fmtGB(memory.usedGB) }} of {{ fmtGB(memory.totalGB) }} committed,
                {{ fmtGB(memory.freeGB) }} free.<template v-if="counts.vmsAssignedGB">
                Running guests are assigned {{ fmtGB(counts.vmsAssignedGB) }}.</template>
                Degraded at {{ HV_WARN.mem }}%.
              </p>
            </div>
          </section>

          <!-- Volumes -->
          <section v-if="volumes.length">
            <h2 class="sec-title">Volumes <span class="sec-dim">· {{ volumes.length }}</span></h2>
            <div class="tbl-wrap">
              <table class="tbl">
                <thead>
                  <tr><th>Drive</th><th>Label</th><th>FS</th><th class="num">Size</th><th class="num">Free</th><th class="used">Used</th></tr>
                </thead>
                <tbody>
                  <tr v-for="v in volumes" :key="v.drive">
                    <td class="mono strong">{{ v.drive }}</td>
                    <td class="dim">{{ v.label || '(no label)' }}</td>
                    <td class="dim mono">{{ v.fs || '-' }}</td>
                    <td class="num mono">{{ fmtGB(v.totalGB) }}</td>
                    <td class="num mono" :class="pctClass(v.usedPct, HV_WARN.disk)">{{ fmtGB(v.freeGB) }}</td>
                    <td class="used">
                      <div class="cell-meter">
                        <div class="bar thin"><div class="fill" :class="pctClass(v.usedPct, HV_WARN.disk)" :style="{ width: clamp(v.usedPct) + '%' }"></div></div>
                        <span class="mono cell-pct" :class="pctClass(v.usedPct, HV_WARN.disk)">{{ v.usedPct }}%</span>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>

          <!-- Guests -->
          <section v-if="vms.length">
            <h2 class="sec-title">
              Guests
              <span class="sec-dim">· {{ vmSummary.running }} running, {{ vmSummary.off }} off<template v-if="vmSummary.other">, {{ vmSummary.other }} other</template></span>
            </h2>
            <div v-if="badVms.length" class="partial">
              <AlertTriangle class="h-4 w-4" />
              <span>
                {{ badVms.length }} {{ badVms.length === 1 ? 'guest is' : 'guests are' }} neither running nor
                cleanly off: {{ badVms.map(v => v.name + ' (' + v.state + ')').join(', ') }}. A guest parked in
                Paused or Saved after a failed migration can sit there unnoticed for weeks, which is why it is
                called out here rather than averaged into a count.
              </span>
            </div>
            <div class="tbl-wrap">
              <table class="tbl">
                <thead>
                  <tr>
                    <th>Guest</th><th>State</th><th class="num">vCPU</th><th class="num">CPU</th>
                    <th class="num">Memory</th><th class="num">Demand</th><th class="num">Uptime</th>
                    <th>Heartbeat</th><th>Gen</th><th>Replication</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="vm in vms" :key="vm.name" :class="{ 'row-bad': !VM_OK_STATES.includes(vm.state) }">
                    <td class="strong">{{ vm.name }}</td>
                    <td><span class="vm-state" :class="vmStateClass(vm.state)">{{ vm.state }}</span></td>
                    <td class="num mono">{{ vm.vcpu ?? '-' }}</td>
                    <td class="num mono">{{ vm.cpuPercent !== null && vm.cpuPercent !== undefined ? vm.cpuPercent + '%' : '-' }}</td>
                    <td class="num mono">{{ vm.memoryGB ? fmtGB(vm.memoryGB) : '-' }}</td>
                    <td class="num mono dim">{{ vm.memoryDemandGB ? fmtGB(vm.memoryDemandGB) : '-' }}</td>
                    <td class="num mono">{{ vm.state === 'Running' ? fmtUptime(vm.uptimeHours) : '-' }}</td>
                    <td class="dim">{{ vm.heartbeat || '-' }}</td>
                    <td class="num dim mono">{{ vm.generation ?? '-' }}</td>
                    <td class="dim">{{ vm.replication && vm.replication !== 'NotApplicable' ? vm.replication : '-' }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p class="tbl-note">
              Memory is assigned; demand is what the guest is asking for. Demand above assigned
              means Dynamic Memory is throttling it.
            </p>
          </section>

          <div v-else-if="hasLive && hyperv.present === false" class="notice notice-warn">
            <div class="notice-title flex items-center gap-2">
              <AlertTriangle class="h-4 w-4" /> The Hyper-V role did not answer
            </div>
            <p class="notice-body">
              The inventory ran but <code>Get-VM</code> failed, so there is no guest list.
              <template v-if="hyperv.error"><code>{{ hyperv.error }}</code></template>
              Either the role is not installed on this host, or the WinRM account cannot read it.
            </p>
          </div>

          <!-- Stage ladder -->
          <section>
            <h2 class="sec-title">What the collector did, in order</h2>
            <div class="ladder">
              <div v-for="(st, i) in ladder" :key="st.id" class="rung" :class="`rung-${st.state}`">
                <div class="rung-mark">
                  <Check v-if="st.state === 'pass'" class="h-3.5 w-3.5" />
                  <X v-else-if="st.state === 'fail'" class="h-3.5 w-3.5" />
                  <Minus v-else class="h-3.5 w-3.5" />
                </div>
                <div class="rung-body">
                  <div class="rung-top">
                    <span class="rung-label">{{ st.label }}</span>
                    <span class="rung-ms mono">
                      {{ st.ms !== null ? `${st.ms} ms` : (st.state === 'skipped' ? 'not reached' : '-') }}
                    </span>
                  </div>
                  <div class="rung-blurb">{{ st.blurb }}</div>
                  <div v-if="st.detail" class="rung-ok mono">answered on {{ st.detail }}</div>
                  <div v-if="st.error" class="rung-err">{{ st.error }}</div>
                </div>
                <div v-if="i < ladder.length - 1" class="rung-line" :class="{ dead: st.state !== 'pass' }"></div>
              </div>
            </div>
          </section>

          <!-- Hardware and OS -->
          <section v-if="hasLive">
            <h2 class="sec-title">Host</h2>
            <div class="facts">
              <div class="fact"><span class="fk">Model</span><span class="fv">{{ hostInfo.model || model || '-' }}</span></div>
              <div class="fact"><span class="fk">Serial</span><span class="fv mono">{{ hostInfo.serial || '-' }}</span></div>
              <div class="fact"><span class="fk">BIOS</span><span class="fv mono">{{ hostInfo.biosVersion || '-' }}</span></div>
              <div class="fact"><span class="fk">Domain</span><span class="fv">{{ hostInfo.domain || '-' }}</span></div>
              <div class="fact"><span class="fk">Processor</span><span class="fv" :title="cpu.name">{{ cpu.name || '-' }}</span></div>
              <div class="fact"><span class="fk">Topology</span><span class="fv mono">{{ topology }}</span></div>
              <div class="fact"><span class="fk">Base clock</span><span class="fv mono">{{ cpu.maxClockMHz ? (cpu.maxClockMHz / 1000).toFixed(2) + ' GHz' : '-' }}</span></div>
              <div class="fact"><span class="fk">Installed RAM</span><span class="fv mono">{{ fmtGB(memory.totalGB) }}</span></div>
              <div class="fact"><span class="fk">Operating system</span><span class="fv">{{ os.caption || '-' }}</span></div>
              <div class="fact"><span class="fk">Build</span><span class="fv mono">{{ os.version || '-' }}{{ os.build ? ' (' + os.build + ')' : '' }}</span></div>
              <div class="fact"><span class="fk">OS installed</span><span class="fv">{{ fmtDate(os.installedOn) }}</span></div>
              <div class="fact"><span class="fk">Last boot</span><span class="fv">{{ fmtDate(os.lastBoot) }}</span></div>
              <div class="fact" v-if="cluster.name"><span class="fk">Cluster</span><span class="fv">{{ cluster.name }}</span></div>
              <div class="fact" v-if="hyperv.logicalProcessors"><span class="fk">Hyper-V logical CPUs</span><span class="fv mono">{{ hyperv.logicalProcessors }}</span></div>
              <div class="fact" v-if="hyperv.virtualHardDiskPath"><span class="fk">VHD path</span><span class="fv mono small" :title="hyperv.virtualHardDiskPath">{{ hyperv.virtualHardDiskPath }}</span></div>
              <div class="fact" v-if="hyperv.virtualMachinePath"><span class="fk">VM config path</span><span class="fv mono small" :title="hyperv.virtualMachinePath">{{ hyperv.virtualMachinePath }}</span></div>
            </div>
          </section>

          <!-- Adapters -->
          <section v-if="adapters.length">
            <h2 class="sec-title">Network adapters <span class="sec-dim">· {{ adapters.length }}</span></h2>
            <div class="tbl-wrap">
              <table class="tbl">
                <thead><tr><th>Adapter</th><th>Status</th><th>Link speed</th><th>MAC</th></tr></thead>
                <tbody>
                  <tr v-for="a in adapters" :key="a.name + a.mac">
                    <td class="strong">{{ a.name }}</td>
                    <td><span class="vm-state" :class="a.status === 'Up' ? 'vm-running' : 'vm-off'">{{ a.status }}</span></td>
                    <td class="mono dim">{{ a.linkSpeed || '-' }}</td>
                    <td class="mono dim">{{ a.mac || '-' }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>

          <!-- Fleet -->
          <section v-if="fleet.length">
            <h2 class="sec-title">The rest of the fleet <span class="sec-dim">· {{ fleet.length }} other hosts</span></h2>
            <div class="tbl-wrap">
              <table class="tbl">
                <thead><tr><th>Host</th><th>Site</th><th>State</th><th class="num">CPU</th><th class="num">Memory</th><th class="num">Guests</th><th></th></tr></thead>
                <tbody>
                  <tr v-for="f in fleet" :key="f.key" class="row-link" @click="go(f.key)">
                    <td class="strong mono">{{ f.host }}</td>
                    <td class="dim">{{ f.site }}</td>
                    <td><span class="vm-state" :class="f.cls">{{ f.label }}</span></td>
                    <td class="num mono" :class="pctClass(f.cpu, HV_WARN.cpu)">{{ f.cpu !== null ? f.cpu + '%' : '-' }}</td>
                    <td class="num mono" :class="pctClass(f.mem, HV_WARN.mem)">{{ f.mem !== null ? f.mem + '%' : '-' }}</td>
                    <td class="num mono">{{ f.vms }}</td>
                    <td class="num"><ChevronRight class="h-4 w-4 chev" /></td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>

          <!-- Recorded checks -->
          <section>
            <h2 class="sec-title">Recorded checks <span class="sec-dim">· last {{ paddedResults.length }}</span></h2>
            <div class="bars-wrap">
              <div class="bars">
                <span v-for="(r, i) in paddedResults" :key="i" class="stcell" :class="barClass(r)"
                  :data-tooltip="barTip(r)" data-tip-pos="top"></span>
              </div>
            </div>
            <div class="axis">
              <span>{{ oldestLabel }}</span>
              <span>{{ monitored ? 'now' : 'paused' }}</span>
            </div>
          </section>

          <!-- History -->
          <section>
            <div class="hist-head">
              <h2 class="sec-title mb-0">History</h2>
              <RangeSelector v-model="range" label="History range" />
            </div>
            <div class="grid gap-3 lg:grid-cols-2 xl:grid-cols-3">
              <HistoryChart title="CPU load" subtitle="Percent, sampled once per sweep." unit="%"
                :series="seriesOf('cpuPct')" kind="area" :loading="historyLoading"
                :empty-text="metricEmptyText" :note="metricNote" />
              <HistoryChart title="Memory in use" subtitle="Percent of installed RAM committed." unit="%"
                :series="seriesOf('memUsedPct')" kind="area" :loading="historyLoading"
                :empty-text="metricEmptyText" :note="metricNote" />
              <HistoryChart title="Fullest volume" subtitle="The highest used percent across all local volumes." unit="%"
                :series="seriesOf('volMaxUsedPct')" kind="area" :loading="historyLoading"
                :empty-text="metricEmptyText" :note="metricNote" />
              <HistoryChart title="Guests running" :subtitle="guestChartSub"
                :series="seriesOf('vmsRunning')" kind="area" :loading="historyLoading"
                :empty-text="metricEmptyText" :note="metricNote" />
              <HistoryChart title="System errors" subtitle="Level 1 to 2 events in the System log, last 24 hours."
                :series="seriesOf('errors24h')" kind="area" :loading="historyLoading"
                :empty-text="metricEmptyText" :note="metricNote" />
              <HistoryChart title="Uptime" subtitle="Share of sweeps where the host was reachable. A degraded sweep counts as a pass."
                :series="uptimeSeries" kind="column" ratio :warn-below="UPTIME_WARN" :down-below="UPTIME_DOWN"
                :loading="historyLoading" :empty-text="uptimeEmptyText" :note="uptimeNote" />
            </div>
          </section>
        </template>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, RefreshCw, Pause, Check, X, Minus, ChevronRight, AlertTriangle, KeyRound } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import MonitorToggle from '@/components/MonitorToggle.vue'
import RangeSelector from '@/components/RangeSelector.vue'
import HistoryChart from '@/components/HistoryChart.vue'
import { generatePrettyTimeAgo, prettifyTimestamp } from '@/utils/time'
import { isMonitored, now, historyRange, setHistoryRange } from '@/store'
import {
  HYPERVISORS, hostFor, hostFromKey, hvStageLadder, hvIsNotReporting,
  VM_OK_STATES, vmStateClass, HV_WARN, pctClass, fmtGB, fmtUptime, keyPath,
} from '@/utils/hypervisors'

const route = useRoute()
const router = useRouter()
const routeKey = computed(() => route.params.key || '')

const MAX_BARS = 40
const POLL_MS = 20000
const HISTORY_POLL_MS = 300000

const loading = ref(false)
const loaded = ref(false)
const notReported = ref(false)
const snapshot = ref({})
const snapshots = ref({})
const results = ref([])

const monitored = computed(() => isMonitored(routeKey.value))

// --- Identity: snapshot first, catalog as the cold-start fallback ---------
const catalog = computed(() => hostFor(routeKey.value))
const detail = computed(() => snapshot.value.detail || {})
const counts = computed(() => snapshot.value.counts || {})
const status = computed(() => snapshot.value.status || '')

const hostName = computed(() =>
  snapshot.value.host || (catalog.value ? catalog.value.host : hostFromKey(routeKey.value)))
const site = computed(() => snapshot.value.site || (catalog.value ? catalog.value.site : ''))
const address = computed(() => snapshot.value.address || detail.value.address || '')

const os = computed(() => detail.value.os || {})
const hostInfo = computed(() => detail.value.host || {})
const cpu = computed(() => detail.value.cpu || {})
const memory = computed(() => detail.value.memory || {})
const volumes = computed(() => (Array.isArray(detail.value.volumes) ? detail.value.volumes : []))
const vms = computed(() => (Array.isArray(detail.value.vms) ? detail.value.vms : []))
const hyperv = computed(() => detail.value.hyperv || {})
const cluster = computed(() => detail.value.cluster || {})
const adapters = computed(() => (Array.isArray(detail.value.adapters) ? detail.value.adapters : []))
const model = computed(() =>
  hostInfo.value.model || (detail.value.inventory || {}).model || (catalog.value ? catalog.value.model : ''))

// A snapshot with real inventory in it, as opposed to one that only got as far
// as the liveness probe.
const hasLive = computed(() => !!(cpu.value.name || memory.value.totalGB))

const ladder = computed(() => hvStageLadder(detail.value.steps))

const latest = computed(() => (results.value.length ? results.value[results.value.length - 1] : null))
const latestErrors = computed(() => {
  const r = latest.value
  return r && Array.isArray(r.errors) ? r.errors.filter(Boolean) : []
})
const reason = computed(() => latestErrors.value.join(' · '))
const notConfigured = computed(() => hvIsNotReporting(latest.value))

const STATUS_META = {
  healthy: { label: 'Healthy', cls: 'st-up' },
  degraded: { label: 'Needs attention', cls: 'st-degraded' },
  down: { label: 'Unreachable', cls: 'st-down' },
}
const statusMeta = computed(() => STATUS_META[status.value] || { label: 'Unknown', cls: 'st-none' })

const clamp = (n) => Math.min(100, Math.max(0, Number(n) || 0))
const fmtDate = (iso) => {
  if (!iso) return '-'
  try { return prettifyTimestamp(iso) } catch (e) { return String(iso).slice(0, 19).replace('T', ' ') }
}

const topology = computed(() => {
  const c = cpu.value
  if (!c.coresTotal && !c.logicalTotal) return '-'
  const parts = []
  if (c.sockets) parts.push(`${c.sockets} ${c.sockets === 1 ? 'socket' : 'sockets'}`)
  if (c.coresTotal) parts.push(`${c.coresTotal} cores`)
  if (c.logicalTotal) parts.push(`${c.logicalTotal} threads`)
  return parts.join(' · ')
})

const cpuSub = computed(() => (cpu.value.logicalTotal ? `${cpu.value.logicalTotal} logical CPUs` : 'no sample'))
const memSub = computed(() =>
  memory.value.totalGB ? `${fmtGB(memory.value.usedGB)} of ${fmtGB(memory.value.totalGB)}` : 'no sample')

const vmSummary = computed(() => {
  const list = vms.value
  const running = list.filter((v) => v.state === 'Running').length
  const off = list.filter((v) => v.state === 'Off').length
  return { running, off, other: list.length - running - off, total: list.length }
})
const badVms = computed(() => vms.value.filter((v) => !VM_OK_STATES.includes(v.state)))
const vmSub = computed(() => {
  if (!vms.value.length) return hyperv.value.present ? 'no guests defined' : 'no Hyper-V data'
  return counts.value.vmsAssignedGB ? `${fmtGB(counts.value.vmsAssignedGB)} assigned` : 'running guests'
})
const guestChartSub = computed(() =>
  vmSummary.value.total ? `${vmSummary.value.total} guests defined on this host.` : 'Guests reported as running.')

const worstVolume = computed(() => {
  let worst = { drive: '', usedPct: null, freeGB: null }
  for (const v of volumes.value) {
    if (v.usedPct === null || v.usedPct === undefined) continue
    if (worst.usedPct === null || v.usedPct > worst.usedPct) {
      worst = { drive: v.drive, usedPct: v.usedPct, freeGB: v.freeGB }
    }
  }
  return worst
})

const errorsCls = computed(() => {
  const n = detail.value.errors24h
  if (n === null || n === undefined) return ''
  if (n >= 50) return 'bad'
  if (n >= 10) return 'warn'
  return 'ok'
})

const bootSub = computed(() => (os.value.lastBoot ? `booted ${fmtDate(os.value.lastBoot)}` : 'no sample'))

const bandSummary = computed(() => {
  if (!snapshot.value.status) return 'nothing reported yet'
  const parts = []
  if (address.value) parts.push(address.value)
  if (vmSummary.value.total) parts.push(`${vmSummary.value.running} of ${vmSummary.value.total} guests running`)
  if (os.value.uptimeHours !== undefined && os.value.uptimeHours !== null) {
    parts.push(`up ${fmtUptime(os.value.uptimeHours)}`)
  }
  return parts.join(' · ') || 'reported'
})

const reportingFresh = computed(() => {
  if (!snapshot.value.updatedAt) return false
  const t = Date.parse(snapshot.value.updatedAt)
  // Sweeps are 60 to 120s jittered, so allow a little over two of them.
  return !Number.isNaN(t) && now.value - t < 280000
})
const updatedLabel = computed(() => {
  if (!snapshot.value.updatedAt) return 'never reported'
  try { return 'read ' + generatePrettyTimeAgo(snapshot.value.updatedAt, now.value) } catch (e) { return '-' }
})

const paddedResults = computed(() => {
  const list = [...results.value]
  while (list.length < MAX_BARS) list.unshift(null)
  return list.slice(-MAX_BARS)
})
const oldestLabel = computed(() => {
  const first = paddedResults.value.find(Boolean)
  if (!first) return 'no history yet'
  try { return generatePrettyTimeAgo(first.timestamp, now.value) } catch (e) { return '-' }
})

const barClass = (r) => {
  if (!r) return 'empty'
  if (!r.success) return hvIsNotReporting(r) ? 'stbar-nodata' : 'stbar-down'
  if (Array.isArray(r.errors) && r.errors.length) return 'stbar-degraded'
  return 'stbar-up'
}
const barTip = (r) => {
  if (!r) return 'No sweep recorded in this slot'
  const parts = []
  try { parts.push(prettifyTimestamp(r.timestamp)) } catch (e) { /* leave the timestamp out */ }
  if (!r.success) parts.push('unreachable')
  else parts.push((r.errors || []).length ? 'alive, with a warning' : 'healthy')
  const errs = (Array.isArray(r.errors) ? r.errors : []).filter(Boolean)
  if (errs.length) parts.push(errs.join(' · '))
  return parts.join(' · ')
}

// --- Fleet ---------------------------------------------------------------
const FLEET_STATE = {
  healthy: { cls: 'vm-running', label: 'healthy' },
  degraded: { cls: 'vm-warn', label: 'attention' },
  down: { cls: 'vm-bad', label: 'unreachable' },
}
const fleet = computed(() =>
  Object.keys(HYPERVISORS)
    .filter((k) => k !== routeKey.value)
    .map((k) => {
      const snap = snapshots.value[k] || {}
      const c = snap.counts || {}
      const meta = HYPERVISORS[k]
      const st = FLEET_STATE[snap.status] || { cls: 'vm-off', label: 'no data' }
      return {
        key: k,
        host: snap.host || meta.host,
        site: snap.site || meta.site,
        cls: st.cls,
        label: st.label,
        cpu: c.cpuPct !== undefined ? c.cpuPct : null,
        mem: c.memUsedPct !== undefined ? c.memUsedPct : null,
        vms: c.vmsTotal !== undefined ? `${c.vmsRunning ?? 0}/${c.vmsTotal}` : '-',
      }
    })
    .sort((a, b) => a.host.localeCompare(b.host)))

const go = (key) => router.push(`/endpoints/${key}`)

// --- History ------------------------------------------------------------
const range = computed({ get: () => historyRange.value, set: (v) => setHistoryRange(v) })

const EMPTY_SERIES = Object.freeze({ timestamps: [], values: [] })
const metricSeries = ref({})
const metricResolution = ref('')
const metricError = ref('')
const uptimeSeries = ref({ timestamps: [], values: [] })
const uptimeResolution = ref('')
const uptimeError = ref('')
const historyLoading = ref(false)

const seriesOf = (name) => metricSeries.value[name] || EMPTY_SERIES

// Sweeps land every 60 to 120s, so an hour holds 30 to 60 of them and one bad
// sweep is under 4 percent of a bucket. Warn from two, and keep 90 percent for
// an hour that lost real minutes.
const UPTIME_WARN = 0.96
const UPTIME_DOWN = 0.9

const METRIC_NOTES = {
  raw: 'One point per sweep, roughly every 60 to 120 seconds.',
  hour: "Hourly averages. The shaded band is each hour's low and high.",
}
const metricNote = computed(() => METRIC_NOTES[metricResolution.value] || '')
const metricEmptyText = computed(() => metricError.value
  || 'No history in this range yet. Recording starts at the first sweep after a deploy and earlier periods cannot be filled in.')

const UPTIME_NOTES = {
  hour: 'One column per hour.',
  day: 'One column per day. Gatus compacts uptime older than about 48 hours into daily buckets.',
  mixed: 'One column per hour for about the last 48 hours, one per day before that, because Gatus compacts older uptime into daily buckets.',
}
const uptimeNote = computed(() => UPTIME_NOTES[uptimeResolution.value] || '')
const uptimeEmptyText = computed(() => uptimeError.value
  || 'No uptime buckets in this range yet. They fill in as sweeps are recorded.')

const fetchMetricHistory = async () => {
  try {
    const res = await fetch(`/api/v1/history/${keyPath(routeKey.value)}?range=${range.value}`, { cache: 'no-store' })
    if (!res.ok) {
      metricSeries.value = {}
      metricResolution.value = ''
      metricError.value = 'Gatus could not read the recorded history for this range.'
      return
    }
    const data = await res.json()
    metricSeries.value = (data && data.series) || {}
    metricResolution.value = (data && data.resolution) || ''
    metricError.value = ''
  } catch (e) {
    metricSeries.value = {}
    metricResolution.value = ''
    metricError.value = 'Could not reach Gatus for the recorded history.'
  }
}

const fetchUptimeSeries = async () => {
  try {
    const res = await fetch(
      `/api/v1/endpoints/${keyPath(routeKey.value)}/uptime-series?range=${range.value}`, { cache: 'no-store' })
    if (!res.ok) {
      uptimeSeries.value = { timestamps: [], values: [] }
      uptimeResolution.value = ''
      uptimeError.value = res.status === 404
        ? 'Gatus has no recorded sweeps under this key.'
        : 'Gatus could not read the uptime history for this range.'
      return
    }
    const data = await res.json()
    uptimeSeries.value = {
      timestamps: Array.isArray(data.timestamps) ? data.timestamps : [],
      values: Array.isArray(data.values) ? data.values : [],
    }
    uptimeResolution.value = data.resolution || ''
    uptimeError.value = ''
  } catch (e) {
    uptimeSeries.value = { timestamps: [], values: [] }
    uptimeResolution.value = ''
    uptimeError.value = 'Could not reach Gatus for the uptime history.'
  }
}

const loadHistory = async () => {
  historyLoading.value = true
  await Promise.allSettled([fetchMetricHistory(), fetchUptimeSeries()])
  historyLoading.value = false
}

watch(historyRange, () => { loadHistory() })

// --- Fetching -----------------------------------------------------------
const fetchSnapshot = async () => {
  try {
    const res = await fetch(`/api/v1/hv/${keyPath(routeKey.value)}`, { cache: 'no-store' })
    if (res.status === 404) {
      notReported.value = true
      snapshot.value = {}
      return
    }
    if (!res.ok) return
    snapshot.value = await res.json()
    notReported.value = false
  } catch (e) { /* leave the previous snapshot in place */ }
}

const fetchSnapshots = async () => {
  try {
    const res = await fetch('/api/v1/hv', { cache: 'no-store' })
    if (!res.ok) return
    snapshots.value = await res.json()
  } catch (e) { /* leave the previous snapshots in place */ }
}

const fetchResults = async () => {
  try {
    const res = await fetch(
      `/api/v1/endpoints/${keyPath(routeKey.value)}/statuses?page=1&pageSize=${MAX_BARS}`, { cache: 'no-store' })
    if (!res.ok) return
    const data = await res.json()
    results.value = Array.isArray(data.results) ? data.results : []
  } catch (e) { /* leave the previous results in place */ }
}

const tick = async () => {
  await Promise.allSettled([fetchSnapshot(), fetchSnapshots(), fetchResults()])
  loaded.value = true
}

const refreshAll = async () => {
  loading.value = true
  await Promise.allSettled([tick(), loadHistory()])
  loading.value = false
}

let poll = null
let historyPoll = null

onMounted(async () => {
  await tick()
  await loadHistory()
  poll = setInterval(tick, POLL_MS)
  historyPoll = setInterval(loadHistory, HISTORY_POLL_MS)
})

onUnmounted(() => {
  if (poll) clearInterval(poll)
  if (historyPoll) clearInterval(historyPoll)
})

// Clicking a fleet row keeps this component mounted and only swaps the param.
watch(routeKey, async () => {
  loaded.value = false
  notReported.value = false
  snapshot.value = {}
  results.value = []
  await tick()
  await loadHistory()
})
</script>

<style scoped>
.hv-panel { max-width: 1500px; margin: 0 auto; }

.eyebrow {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 10px; letter-spacing: 0.1em; text-transform: uppercase;
  color: hsl(var(--muted-foreground)); font-weight: 700;
}
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; }
.small { font-size: 0.75rem; }
.dim { color: hsl(var(--muted-foreground)); }
.strong { font-weight: 600; }
.back-link:focus-visible, .refresh:focus-visible { outline: 2px solid hsl(var(--ring)); outline-offset: 2px; border-radius: 8px; }

.sec-title { font-size: 0.8rem; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em; color: hsl(var(--muted-foreground)); margin-bottom: 0.55rem; }
.sec-dim { font-weight: 500; opacity: 0.6; letter-spacing: 0.02em; }

.live-ind { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.72rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: hsl(var(--muted-foreground)); text-transform: uppercase; letter-spacing: 0.08em; }
.live-ind .ldot { width: 7px; height: 7px; border-radius: 999px; background: hsl(var(--muted-foreground) / 0.5); }
.live-ind.on { color: hsl(var(--foreground)); }
.live-ind.on .ldot { background: #5aa06b; }

.notice { border: 1px dashed hsl(var(--border)); border-radius: 12px; padding: 1.1rem 1.25rem; }
.notice-warn { border-style: solid; border-color: rgb(224 160 88 / 0.4); background: rgb(224 160 88 / 0.07); }
.notice-paused { border-style: solid; border-color: rgb(138 143 152 / 0.45); background: hsl(var(--muted) / 0.35); }
.notice-nodata { border-style: solid; border-color: hsl(var(--foreground) / 0.35); background: hsl(var(--foreground) / 0.04); }
.notice-title { font-weight: 700; margin-bottom: 0.35rem; }
.notice-body { font-size: 0.875rem; color: hsl(var(--muted-foreground)); }
.notice code, .meter-note code, .tbl-note code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background: hsl(var(--muted) / 0.6); padding: 0.05rem 0.3rem; border-radius: 4px; font-size: 0.85em; }
.env-pre { margin-top: 0.6rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.78rem; background: hsl(var(--muted) / 0.7); border: 1px solid hsl(var(--border)); border-radius: 6px; padding: 0.55rem 0.7rem; overflow-x: auto; user-select: all; }

.partial { display: flex; align-items: flex-start; gap: 0.45rem; font-size: 0.78rem; line-height: 1.45; color: #e0a458; background: rgb(224 160 88 / 0.09); border: 1px solid rgb(224 160 88 / 0.28); border-radius: 8px; padding: 0.5rem 0.7rem; margin-bottom: 0.6rem; }
.partial svg { flex-shrink: 0; margin-top: 0.15rem; }

.status-pill { font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.08em; padding: 0.2rem 0.65rem; border-radius: 6px; color: #fff; }
.status-pill.st-up { background: var(--status-up); }
.status-pill.st-degraded { background: var(--status-degraded); }
.status-pill.st-down { background: var(--status-down); }
.status-pill.st-none { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }
.band-counts { font-size: 0.8rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); }
.band-warn { font-size: 0.75rem; color: #e0a458; }

/* --- KPI strip --- */
.kpis { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1px; background: hsl(var(--border)); border: 1px solid hsl(var(--border)); border-radius: 12px; overflow: hidden; }
@media (min-width: 640px) { .kpis { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (min-width: 1100px) { .kpis { grid-template-columns: repeat(6, minmax(0, 1fr)); } }
.kpi { background: hsl(var(--card)); padding: 0.7rem 0.9rem; min-width: 0; }
.kpi-val { font-size: 1.5rem; font-weight: 700; line-height: 1.1; margin-top: 0.1rem; font-variant-numeric: tabular-nums; }
.kpi-val.ok { color: #7bbd8a; }
.kpi-val.warn { color: #e0a458; }
.kpi-val.bad { color: #ef6b53; }
.kpi-of { font-size: 0.95rem; font-weight: 600; color: hsl(var(--muted-foreground)); }
.kpi-sub { font-size: 0.7rem; color: hsl(var(--muted-foreground)); margin-top: 0.15rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* --- meters --- */
.meter { border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--card)); padding: 0.85rem 1rem; }
.meter-head { display: flex; align-items: baseline; justify-content: space-between; margin-bottom: 0.45rem; }
.meter-title { font-size: 0.86rem; font-weight: 600; }
.meter-num { font-size: 1.1rem; font-weight: 700; }
.meter-num.ok { color: #7bbd8a; }
.meter-num.warn { color: #e0a458; }
.meter-num.bad { color: #ef6b53; }
.bar { height: 10px; border-radius: 999px; background: hsl(var(--muted) / 0.8); overflow: hidden; }
.bar.thin { height: 6px; }
.fill { height: 100%; border-radius: 999px; background: hsl(var(--muted-foreground) / 0.5); transition: width 0.3s; }
.fill.ok { background: #7bbd8a; }
.fill.warn { background: #e0a458; }
.fill.bad { background: #ef6b53; }
.meter-note { font-size: 0.73rem; color: hsl(var(--muted-foreground)); margin-top: 0.5rem; line-height: 1.45; }

/* --- tables --- */
.tbl-wrap { overflow-x: auto; border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--card)); }
.tbl { width: 100%; border-collapse: collapse; font-size: 0.8rem; }
.tbl th { text-align: left; font-size: 10px; text-transform: uppercase; letter-spacing: 0.06em; color: hsl(var(--muted-foreground)); font-weight: 700; padding: 0.5rem 0.7rem; border-bottom: 1px solid hsl(var(--border)); white-space: nowrap; }
.tbl td { padding: 0.45rem 0.7rem; border-bottom: 1px solid hsl(var(--border) / 0.5); white-space: nowrap; }
.tbl tbody tr:last-child td { border-bottom: 0; }
.tbl .num, .tbl th.num { text-align: right; font-variant-numeric: tabular-nums; }
.tbl td.num.ok { color: #7bbd8a; }
.tbl td.num.warn { color: #e0a458; }
.tbl td.num.bad { color: #ef6b53; }
.tbl .used { width: 10rem; }
.tbl tbody tr.row-bad { background: rgb(239 107 83 / 0.05); }
.tbl tbody tr.row-link { cursor: pointer; }
.tbl tbody tr.row-link:hover { background: hsl(var(--muted) / 0.45); }
.cell-meter { display: flex; align-items: center; gap: 0.5rem; }
.cell-meter .bar { flex: 1; min-width: 3rem; }
.cell-pct { width: 2.6rem; text-align: right; font-size: 0.75rem; }
.chev { color: hsl(var(--muted-foreground) / 0.5); }
.tbl-note { font-size: 0.73rem; color: hsl(var(--muted-foreground)); margin-top: 0.5rem; line-height: 1.45; }

.vm-state { font-size: 10px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.06em; padding: 0.1rem 0.4rem; border-radius: 4px; }
.vm-state.vm-running { color: #7bbd8a; background: rgb(123 189 138 / 0.14); }
.vm-state.vm-off { color: hsl(var(--muted-foreground)); background: hsl(var(--muted) / 0.6); }
.vm-state.vm-warn { color: #e0a458; background: rgb(224 160 88 / 0.14); }
.vm-state.vm-bad { color: #ef6b53; background: rgb(239 107 83 / 0.14); }

/* --- facts grid --- */
.facts { display: grid; grid-template-columns: 1fr; gap: 1px; background: hsl(var(--border)); border: 1px solid hsl(var(--border)); border-radius: 12px; overflow: hidden; }
@media (min-width: 640px) { .facts { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (min-width: 1100px) { .facts { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
.fact { background: hsl(var(--card)); padding: 0.5rem 0.85rem; display: flex; justify-content: space-between; gap: 1rem; min-width: 0; }
.fk { font-size: 0.75rem; color: hsl(var(--muted-foreground)); flex-shrink: 0; }
.fv { font-size: 0.8rem; font-weight: 600; text-align: right; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* --- stage ladder --- */
.ladder { border: 1px solid hsl(var(--border)); border-radius: 12px; background: hsl(var(--card)); padding: 0.4rem 0.9rem; }
.rung { position: relative; display: flex; gap: 0.75rem; padding: 0.7rem 0; }
.rung-mark { flex-shrink: 0; width: 1.5rem; height: 1.5rem; border-radius: 999px; display: flex; align-items: center; justify-content: center; z-index: 1; }
.rung-pass .rung-mark { background: rgb(123 189 138 / 0.18); color: #7bbd8a; }
.rung-fail .rung-mark { background: rgb(239 107 83 / 0.18); color: #ef6b53; }
.rung-skipped .rung-mark, .rung-unknown .rung-mark { background: hsl(var(--muted)); color: hsl(var(--muted-foreground)); }
.rung-line { position: absolute; left: 0.75rem; top: 2.2rem; bottom: -0.7rem; width: 1px; background: rgb(123 189 138 / 0.35); }
.rung-line.dead { background: hsl(var(--border)); }
.rung-body { min-width: 0; flex: 1; }
.rung-top { display: flex; align-items: baseline; justify-content: space-between; gap: 0.75rem; }
.rung-label { font-size: 0.86rem; font-weight: 600; }
.rung-ms { font-size: 0.75rem; color: hsl(var(--muted-foreground)); flex-shrink: 0; }
.rung-blurb { font-size: 0.78rem; color: hsl(var(--muted-foreground)); line-height: 1.45; margin-top: 0.1rem; }
.rung-ok { font-size: 0.73rem; color: #7bbd8a; margin-top: 0.25rem; }
.rung-err { margin-top: 0.35rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.75rem; color: #ef6b53; background: rgb(239 107 83 / 0.09); border-radius: 6px; padding: 0.35rem 0.55rem; }
.rung-skipped .rung-label, .rung-unknown .rung-label { color: hsl(var(--muted-foreground)); }

/* --- bars --- */
.bars-wrap { overflow-x: auto; }
.bars { display: flex; gap: 2px; min-width: 15rem; }
.stcell { flex: 1 1 0; min-width: 3px; height: 28px; border-radius: 3px; }
.stcell.empty { background: hsl(var(--muted) / 0.7); }
.axis { display: flex; justify-content: space-between; margin-top: 0.3rem; font-size: 0.68rem; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-variant-numeric: tabular-nums; color: hsl(var(--muted-foreground)); }

.hist-head { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 0.6rem; margin-bottom: 0.55rem; }
</style>
