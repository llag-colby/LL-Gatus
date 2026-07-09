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

function tone(freqs, { duration = 0.22, peak = 0.22, gap = 0.14, type = 'triangle' } = {}) {
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
    osc.stop(t + duration + 0.04)
  })
}

// Recovered / back up — clear rising 3-note arpeggio (C5 E5 G5).
export const playUp = () => tone([523.25, 659.25, 783.99], { gap: 0.13, duration: 0.2, peak: 0.2 })
// Degraded — noticeable two-note dip (G5 -> D#5).
export const playDegraded = () => tone([783.99, 622.25], { gap: 0.16, duration: 0.26, peak: 0.22 })
// Down — attention-grabbing alternating alert (E5 <-> G#4, twice).
export const playDown = () => tone([659.25, 415.3, 659.25, 415.3], { gap: 0.17, duration: 0.22, peak: 0.26 })
