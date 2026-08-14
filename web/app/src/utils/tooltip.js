/**
 * Global hover tooltips for anything carrying [data-tooltip].
 *
 * This replaces the old CSS ::after pseudo-element tooltip, which had two
 * problems that are unfixable in pure CSS:
 *
 *   1. Blurry text. Centering with `left: 50%; transform: translateX(-50%)`
 *      lands the bubble on a half pixel whenever its width is odd, and the
 *      scale() transition kept it on a composited layer — so the text rendered
 *      soft. Here the bubble is a single fixed-position node placed at ROUNDED
 *      integer coordinates, with no transform left on it once it settles.
 *   2. Clipping. A pseudo-element lives inside its parent's clipping and
 *      stacking context, so tooltips on cards near the screen edge (or inside
 *      anything with overflow hidden) got cut off. A fixed node parented to the
 *      body escapes all of that, and is clamped to the viewport besides.
 *
 * Usage: data-tooltip="Some text" (+ optional data-tip-pos="bottom" to prefer
 * below the element). Placement flips automatically when there's no room.
 */

const GAP = 9          // distance between the element and the bubble
const MARGIN = 8       // minimum distance from the viewport edge
const MAX_WIDTH = 320  // wrap beyond this

let tip = null
let arrow = null
let current = null     // element the tooltip is currently describing

// In full-screen mode only the fullscreen element's subtree is rendered, so the
// bubble has to live inside it or it would silently vanish on the wall display.
const host = () => document.fullscreenElement || document.body

const build = () => {
  if (tip) return
  tip = document.createElement('div')
  tip.className = 'gt-tip'
  tip.setAttribute('role', 'tooltip')
  arrow = document.createElement('span')
  arrow.className = 'gt-tip__arrow'
  tip.appendChild(arrow)
  tip.appendChild(document.createTextNode(''))
}

const setText = (text) => {
  // Last child is the text node (the arrow is first).
  tip.lastChild.nodeValue = text
}

const place = (el) => {
  const maxWidth = Math.min(MAX_WIDTH, window.innerWidth - MARGIN * 2)
  tip.style.maxWidth = `${maxWidth}px`
  // Try a single line first; only wrap when it genuinely doesn't fit, so short
  // labels stay on one line instead of being chopped off mid-word.
  tip.style.whiteSpace = 'nowrap'
  if (tip.scrollWidth > tip.clientWidth) tip.style.whiteSpace = 'normal'

  const target = el.getBoundingClientRect()
  const bubble = tip.getBoundingClientRect()
  // Preferred side, flipped if it would run off the top/bottom.
  let below = el.getAttribute('data-tip-pos') === 'bottom'
  if (below && target.bottom + GAP + bubble.height > window.innerHeight - MARGIN) {
    below = target.top - GAP - bubble.height < MARGIN
  } else if (!below && target.top - GAP - bubble.height < MARGIN) {
    below = target.bottom + GAP + bubble.height <= window.innerHeight - MARGIN
  }

  let top = below ? target.bottom + GAP : target.top - GAP - bubble.height
  let left = target.left + target.width / 2 - bubble.width / 2
  left = Math.max(MARGIN, Math.min(left, window.innerWidth - bubble.width - MARGIN))
  top = Math.max(MARGIN, Math.min(top, window.innerHeight - bubble.height - MARGIN))
  tip.style.left = `${Math.round(left)}px`
  tip.style.top = `${Math.round(top)}px`

  // Keep the arrow pointing at the element even after the bubble was clamped.
  const center = target.left + target.width / 2 - Math.round(left)
  arrow.style.left = `${Math.round(Math.max(12, Math.min(center, bubble.width - 12)))}px`
  tip.classList.toggle('gt-tip--below', below)
}

const show = (el) => {
  const text = el.getAttribute('data-tooltip')
  if (!text) return hide()
  build()
  if (tip.parentNode !== host()) host().appendChild(tip)
  current = el
  setText(text)
  // Position while still invisible (a hidden element still has a box to
  // measure), otherwise it flashes at wherever the previous one sat.
  place(el)
  tip.classList.add('gt-tip--visible')
}

const hide = () => {
  current = null
  if (tip) tip.classList.remove('gt-tip--visible')
}

// The label can change while hovered (e.g. Mute ⇄ Enable sound alerts), so
// re-read it after a click instead of leaving a stale bubble on screen.
const refresh = () => {
  if (!current) return
  if (!current.isConnected) return hide()
  const text = current.getAttribute('data-tooltip')
  if (!text) return hide()
  setText(text)
  place(current)
}

const onPointerOver = (e) => {
  // Touch devices get no hover tooltips (they'd stick after a tap).
  if (!window.matchMedia('(hover: hover)').matches) return
  const el = e.target.closest ? e.target.closest('[data-tooltip]:not([data-tooltip=""])') : null
  if (el === current) return
  el ? show(el) : hide()
}

const onFocusIn = (e) => {
  const el = e.target.closest ? e.target.closest('[data-tooltip]:not([data-tooltip=""])') : null
  if (el && el !== current) show(el)
}

const onClick = () => setTimeout(refresh, 0)
const onScroll = () => hide()

/** Installs the global tooltip handlers. Safe to call once, from App.vue. */
export function installTooltips() {
  document.addEventListener('mouseover', onPointerOver, true)
  document.addEventListener('mouseleave', hide)
  document.addEventListener('focusin', onFocusIn, true)
  document.addEventListener('focusout', hide, true)
  document.addEventListener('click', onClick, true)
  // Anything that moves the anchor invalidates the position; cheapest correct
  // answer is to hide rather than chase it.
  window.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', onScroll)
  document.addEventListener('fullscreenchange', hide)
  window.addEventListener('keydown', (e) => { if (e.key === 'Escape') hide() })
}

export function hideTooltip() {
  hide()
}
