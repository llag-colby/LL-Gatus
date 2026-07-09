// Subtle, friendly alert chimes generated with the Web Audio API (no files).
// Sine tones at low volume: a gentle rise for recovery, a soft fall for down,
// a small dip for degraded.

let ctx = null

function getCtx() {
  if (!ctx) {
    const AC = window.AudioContext || window.webkitAudioContext
    if (!AC) return null
    ctx = new AC()
  }
  return ctx
}

// Browsers block audio until a user gesture — call this from a click/keypress.
export function unlockAudio() {
  const c = getCtx()
  if (c && c.state === 'suspended') c.resume()
}

function tone(freqs, { duration = 0.16, peak = 0.05, gap = 0.11, type = 'sine' } = {}) {
  const c = getCtx()
  if (!c) return
  if (c.state === 'suspended') c.resume()
  const start = c.currentTime + 0.01
  freqs.forEach((f, i) => {
    const osc = c.createOscillator()
    const gain = c.createGain()
    osc.type = type
    osc.frequency.value = f
    const t = start + i * gap
    gain.gain.setValueAtTime(0.0001, t)
    gain.gain.exponentialRampToValueAtTime(peak, t + 0.02)
    gain.gain.exponentialRampToValueAtTime(0.0001, t + duration)
    osc.connect(gain)
    gain.connect(c.destination)
    osc.start(t)
    osc.stop(t + duration + 0.03)
  })
}

// Recovered / back up — gentle two-note rise (D5 -> G5).
export const playUp = () => tone([587.33, 783.99])
// Degraded — small downward dip (C5 -> B4).
export const playDegraded = () => tone([523.25, 493.88], { gap: 0.1 })
// Down — soft descending fall (C5 -> G4).
export const playDown = () => tone([523.25, 392.0], { gap: 0.13, duration: 0.2 })
