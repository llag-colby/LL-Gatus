// Fallback labels for the monitored drive letters.
//
// The live share path, server, account and per-stage results all arrive from
// collector/smb_collector.py via /api/v1/smb/:key, so this table is only used
// before the first snapshot lands (a cold Gatus, or a collector that has never
// run). The collector's own SHARES table is the source of truth.
//
// It still has to exist because the endpoint group is deliberately a bare drive
// letter: the key is interpolated into /endpoints/{key} unencoded and
// config/key/key.go sanitize() does not strip backslashes, so a UNC path in the
// group would break the drill-down URL.
export const SMB_SHARES = Object.freeze({
  'l:_smb-shares': { drive: 'L:', server: 'rr-fs01', share: 'Company Hub' },
  'k:_smb-shares': { drive: 'K:', server: 'llfs01', share: 'exports' },
  'p:_smb-shares': { drive: 'P:', server: 'llfs01', share: 'ReportHub(Shoals)' },
})

// The card is the endpoint name "SMB Shares", so every key ends with its slug.
// Matching the suffix rather than a prefix is what distinguishes this family
// from phones_/firewall_/wireless_, whose kind sits in the GROUP half of the key.
export const SMB_KEY_SUFFIX = '_smb-shares'

export const isSmbShareKey = (key) => typeof key === 'string' && key.endsWith(SMB_KEY_SUFFIX)

export const shareFor = (key) => SMB_SHARES[key] || null

// "\\llfs01\exports", written with doubled backslashes because that is what an
// operator pastes into Explorer or `net use`.
export const uncPath = (s) => (s && s.server && s.share ? `\\\\${s.server}\\${s.share}` : '')

// Drive letter from the key alone, so a key with no catalog entry still gets a
// sensible heading instead of falling back to the raw slug.
export const driveFromKey = (key) => {
  const slug = String(key || '').slice(0, -SMB_KEY_SUFFIX.length)
  return slug ? slug.toUpperCase() : ''
}

// The collector reports one step per stage, and names the first one "auth"
// instead of "connect" when that is where it actually broke. Both map to the
// same row here: register_session does the TCP connect, the SMB negotiate and
// the NTLM bind in one call, so they are measured together and only the error
// text can separate them.
export const STAGES = Object.freeze([
  {
    id: 'connect',
    aliases: ['connect', 'auth'],
    label: 'Connect & authenticate',
    blurb: 'Reach the server on 445, negotiate SMB and bind as the service account.',
  },
  {
    id: 'mount',
    aliases: ['mount'],
    label: 'Open the share',
    blurb: "Attach to the share itself. This is what catches a share that was removed or ACL'd shut.",
  },
  {
    id: 'list',
    aliases: ['list'],
    label: 'Read the root',
    blurb: 'List the top level of the share, which proves it is actually readable.',
  },
])

// Map the reported steps onto the fixed stage ladder, so a run that died at
// stage one still shows the later stages as "not reached" rather than hiding
// them. That distinction matters: "not reached" is not "passed".
export const stageLadder = (steps) => {
  const reported = Array.isArray(steps) ? steps : []
  let failedBefore = false
  return STAGES.map((stage) => {
    const step = reported.find((s) => s && stage.aliases.includes(s.name))
    if (!step) {
      return { ...stage, state: failedBefore ? 'skipped' : 'unknown', ms: null, error: '' }
    }
    if (!step.ok) {
      failedBefore = true
      return { ...stage, state: 'fail', ms: step.ms, error: step.error || '' }
    }
    return { ...stage, state: 'pass', ms: step.ms, error: '' }
  })
}

// Prefix the collector uses for "we could not even try", no credentials, so
// nothing was attempted. Painted black rather than red, because an absent
// signal is not a reported failure. Keep in step with smb_collector.py.
export const NOT_REPORTING_RE = /^no smb reporting\b/i
export const isNotReporting = (r) =>
  !!r && !r.success && (Array.isArray(r.errors) ? r.errors : []).some((e) => NOT_REPORTING_RE.test(e))

export const fmtGB = (gb) => {
  if (gb === null || gb === undefined || Number.isNaN(gb)) return ','
  if (gb >= 1024) return `${(gb / 1024).toFixed(2)} TB`
  if (gb >= 10) return `${Math.round(gb)} GB`
  return `${gb.toFixed(1)} GB`
}

// One definition, in utils/keys.js, re-exported so existing imports keep
// working. Three copies of the same rule is three chances to fix one.
export { keyPath } from './keys'
