// Fallback labels for the monitored Hyper-V hosts.
//
// Everything live (CPU, memory, volumes, guests, OS build) arrives from
// collector/hv_collector.py via /api/v1/hv/:key. This table is only used before
// the first snapshot lands, so a cold Gatus still shows a named host and its
// expected hardware rather than a bare slug. The collector's HOSTS list is the
// source of truth.
export const HYPERVISORS = Object.freeze({
  'cu-hv01_hypervisors': { host: 'CU-HV01', site: 'Cullman', model: 'OptiPlex 7070', ramGiB: 15.79 },
  'fl-hv01_hypervisors': { host: 'FL-HV01', site: 'Florence', model: 'PowerEdge R620', ramGiB: 31.94 },
  'hoov-hv01_hypervisors': { host: 'HOOV-HV01', site: 'Hoover', model: 'PowerEdge R730xd', ramGiB: 255.91 },
  'hoov-hv02_hypervisors': { host: 'HOOV-HV02', site: 'Hoover', model: 'PowerEdge T620', ramGiB: 223.94 },
  'ms-hv01_hypervisors': { host: 'MS-HV01', site: 'Muscle Shoals', model: 'PowerEdge R730xd', ramGiB: 335.9 },
  'ms-hv02_hypervisors': { host: 'MS-HV02', site: 'Muscle Shoals', model: 'PowerEdge T630', ramGiB: 255.91 },
  'ms-hv03_hypervisors': { host: 'MS-HV03', site: 'Muscle Shoals', model: 'PowerEdge R750', ramGiB: 511.46 },
  'ms-hv04_hypervisors': { host: 'MS-HV04', site: 'Muscle Shoals', model: 'PowerEdge R740xd', ramGiB: 511.38 },
  'ms-hv06_hypervisors': { host: 'MS-HV06', site: 'Muscle Shoals', model: 'PowerEdge R750', ramGiB: 511.46 },
  'ona-hv1_hypervisors': { host: 'ONA-HV1', site: 'Ivory Tower', model: 'PowerEdge R750', ramGiB: 511.46 },
  'ona-hv2_hypervisors': { host: 'ONA-HV2', site: 'Ivory Tower', model: 'PowerEdge R750', ramGiB: 511.46 },
})

export const HV_KEY_SUFFIX = '_hypervisors'

export const isHypervisorKey = (key) => typeof key === 'string' && key.endsWith(HV_KEY_SUFFIX)

export const hostFor = (key) => HYPERVISORS[key] || null

export const hostFromKey = (key) => {
  const slug = String(key || '').slice(0, -HV_KEY_SUFFIX.length)
  return slug ? slug.toUpperCase() : ''
}

// The collector's three stages, in the order it runs them. Liveness is its own
// probe so a host with WinRM disabled still reads as alive.
export const HV_STAGES = Object.freeze([
  {
    id: 'reachable',
    label: 'Reachable',
    blurb: 'A plain TCP connect, tried against each known address in turn. This is the liveness check and it does not depend on WinRM.',
  },
  {
    id: 'winrm',
    label: 'WinRM session',
    blurb: 'Connect and authenticate on 5985. If this is the only failure the host is still up, so the row reads degraded rather than down.',
  },
  {
    id: 'collect',
    label: 'Inventory',
    blurb: 'Run the CIM and Hyper-V queries and parse the result.',
  },
])

export const hvStageLadder = (steps) => {
  const reported = Array.isArray(steps) ? steps : []
  let failedBefore = false
  return HV_STAGES.map((stage) => {
    const step = reported.find((s) => s && s.name === stage.id)
    if (!step) {
      return { ...stage, state: failedBefore ? 'skipped' : 'unknown', ms: null, error: '', detail: '' }
    }
    if (!step.ok) {
      failedBefore = true
      return { ...stage, state: 'fail', ms: step.ms, error: step.error || '', detail: step.detail || '' }
    }
    return { ...stage, state: 'pass', ms: step.ms, error: '', detail: step.detail || '' }
  })
}

// Same "could not even try" convention as the other collectors: painted black,
// because not configured is not an outage.
export const HV_NOT_REPORTING_RE = /^no hv reporting\b/i
export const hvIsNotReporting = (r) =>
  !!r && (Array.isArray(r.errors) ? r.errors : []).some((e) => HV_NOT_REPORTING_RE.test(e))

// Guest states that need no attention. Anything else is surfaced by name.
export const VM_OK_STATES = Object.freeze(['Running', 'Off'])

export const vmStateClass = (state) => {
  if (state === 'Running') return 'vm-running'
  if (state === 'Off') return 'vm-off'
  return 'vm-bad'
}

// Thresholds mirrored from the collector's defaults purely for labelling. The
// collector decides status; this only tells the reader where the line sits.
export const HV_WARN = Object.freeze({ cpu: 90, mem: 92, disk: 90 })

export const pctClass = (pct, warn) => {
  if (pct === null || pct === undefined) return ''
  if (pct >= warn) return 'bad'
  if (pct >= warn - 15) return 'warn'
  return 'ok'
}

export const fmtGB = (gb) => {
  if (gb === null || gb === undefined || Number.isNaN(gb)) return '-'
  if (gb >= 1024) return `${(gb / 1024).toFixed(2)} TB`
  if (gb >= 10) return `${Math.round(gb)} GB`
  return `${Number(gb).toFixed(1)} GB`
}

export const fmtUptime = (hours) => {
  if (hours === null || hours === undefined) return '-'
  const h = Number(hours)
  if (Number.isNaN(h)) return '-'
  if (h < 1) return `${Math.round(h * 60)} min`
  if (h < 48) return `${h.toFixed(1)} h`
  return `${Math.floor(h / 24)} d ${Math.round(h % 24)} h`
}

// Keys here carry no colon, unlike the SMB ones, but the routes that validate a
// key against config.yaml still read the raw path param. Encoding everything
// except the colon keeps one habit across both pages.
export const keyPath = (key) => encodeURIComponent(String(key || '')).replace(/%3A/gi, ':')
