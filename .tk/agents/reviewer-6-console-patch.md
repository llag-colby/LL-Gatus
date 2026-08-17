# Review — LL-Telemetry operations console patch

Target: `C:\Users\colby.west\Desktop\Projects\LL-Telemetry\telemetry\dashboard\index.html`
Vendored copy: `C:\Users\colby.west\Desktop\Projects\Gatus\api\assets\telemetry-console.html` — verified **byte-identical** (`diff` clean, 678 lines both).
Baseline for comparison: `LL-Telemetry@5f42e01` (`git show HEAD:telemetry/dashboard/index.html`).

**APPROVED: no — 2 blockers, 5 suggestions.**

---

## BLOCKER 1 — `min-height:50px` on `.bar` leaks into the per-site sparkbar and inflates it from 4px to 50px

**CONFIDENCE: 95%**
**FILE:** `telemetry/dashboard/index.html`
**LINE:** 51 (`.bar`), colliding with 108 (`.site .bar`) and 391 (markup)

Two different elements share `class="bar"`:

- line 254 — `<div class="bar">` the sticky top bar
- line 391 — `<div class="bar">` the 4px runs/failures sparkbar inside every site card

The old rule used a **fixed height**, which `.site .bar` overrode cleanly:

```css
/* before */
.bar{ ... height:46px; gap:14px; padding:0 16px; ... }
.site .bar{height:4px;background:var(--bg);border-radius:2px;overflow:hidden;display:flex}
```

`height:46px` vs `height:4px` — same property, higher specificity wins, sparkbar renders 4px.

The patch changed the top bar to `min-height`:

```css
/* after — line 51-54 */
.bar{
  position:sticky;top:0;z-index:30;min-height:50px;display:flex;align-items:center;
  gap:10px;flex-wrap:wrap;padding:8px 16px;background:var(--bg);
  border-bottom:1px solid var(--line);
}
```

`.site .bar` (line 108, unchanged) still only declares `height:4px`. **`min-height` is a different property and is not overridden by `height`** — used height is `clamp(min-height, height, max-height)` = `max(50px, 4px)` = **50px**. `box-sizing:border-box` is global (line 31), so every site card's sparkbar now renders as a 50px-tall block with `padding:8px 16px` and a `border-bottom`, instead of a 4px hairline. The site board — the "a site that is failing reads instantly" component in DESIGN.md — is wrecked.

Compounding, from the same leak:
- `flex-wrap:wrap` is new, so the two `<i>` fills (percentage widths summing to ~100% of a box narrowed by 32px of padding, separated by a 10px gap) can now **wrap onto a second line** instead of overflowing.
- `align-items:center` overrides the default `stretch`, so `.site .bar i{height:100%}` (line 109) resolves against the 34px content box rather than filling — the fills float as a centered 34px band.
- `position:sticky;top:0;z-index:30` also leaks (pre-existing, but invisible at 4px and very visible at 50px).

**FIX:** stop sharing the class name. Rename the site sparkbar to something unambiguous — in `renderSites` line 391 emit `<div class="sbar">`, and rename the CSS at lines 108–109 to `.site .sbar` / `.site .sbar i`. That kills the whole family of leaks (position, z-index, padding, gap, wrap) permanently rather than playing whack-a-mole. If you want the minimal patch instead, add `min-height:0;padding:0;gap:0;position:static;flex-wrap:nowrap;border-bottom:none;align-items:stretch` to `.site .bar` — but the rename is the correct fix.

---

## BLOCKER 2 — `renderRibbon(RIB)` throws when `RIB` is still `null` (brushing before first load, or with the API down)

**CONFIDENCE: 85%**
**FILE:** `telemetry/dashboard/index.html`
**LINE:** 401–402, called unguarded from 432, 439, 469, 480

`renderRibbon` dereferences its argument immediately:

```js
function renderRibbon(tl){
  RIB=tl;const svg=document.getElementById('ribbon');
  const W=1000,H=78,pad=1;const s=tl.series||[];   // TypeError if tl===null
```

`RIB` starts `null` (line 400) and is only assigned inside `renderRibbon` itself, i.e. only after `refresh()` successfully resolves `/timeline` (line 601–602). All four new-code call sites pass `RIB` unguarded:

- 432 `schedule()` rAF callback
- 439 `endDrag`
- 469 Escape handler
- 480 `BRUSH_NOTE.clear.onclick`

The `<svg class="ribbon">` is 78px tall and in the DOM from first paint, so it is interactive before any data arrives. Two reachable paths:

1. **Click or drag the ribbon during initial load** — `pointerdown` → `pointerup` → `endDrag` → `renderRibbon(null)` → `TypeError: Cannot read properties of null (reading 'series')`.
2. **API is down.** `refresh()` catches and calls `showErr` (line 607), so `RIB` stays `null` *permanently* while the console sits there displaying an error. Every subsequent ribbon interaction throws.

Note `ribbonSpan()` (line 424) *is* null-guarded (`RIB?RIB.series:[]`), which is what lets `pointermove`/`timeForFrac` run far enough to set `S.brush` and schedule a frame that then dies. Consequence beyond the console error: `endDrag` sets `activePointer=null` (line 436) *before* the throw, so the pointer state does recover — but `applyRiver()` on line 442 is never reached, so on the failure path the river is not re-filtered on release. Same at line 469 for Escape.

**FIX:** guard once at the top of the function rather than at four call sites:

```js
function renderRibbon(tl){
  if(!tl)return;
  RIB=tl;...
```

---

## SUGGESTION 1 — a second pointerdown mid-drag silently wipes the brush the first pointer just drew

**CONFIDENCE: 85%**
**LINE:** 444–449

`pointerdown` has no guard against an already-active pointer:

```js
svg.addEventListener('pointerdown',e=>{
  if(e.pointerType==='mouse'&&e.button!==0)return;
  activePointer=e.pointerId;startX=xf(e);moved=false;
  ...
```

Two-finger sequence on the ribbon (`touch-action:none` on line 115 means the ribbon swallows touch, so this is easy to hit): finger A drags out a brush; finger B touches down → `activePointer` is reassigned to B, `startX` is clobbered, **`moved` resets to `false`**. Finger B lifts without moving → `endDrag` matches B → `if(!moved)S.brush=null` (line 438) **clears the brush A just drew**. Finger A's subsequent moves are ignored (`e.pointerId!==activePointer`) and its `pointerup` early-returns.

State does not get permanently stuck (implicit pointer capture release on `pointerup` covers the orphaned capture for A), so this is data-loss-free but user-visibly wrong: an accidental second touch or palm contact discards the selection.

**FIX:** `if(activePointer!==null)return;` as the first line of the `pointerdown` handler — first pointer wins, later ones are ignored until release.

---

## SUGGESTION 2 — `startX` is stored as a fraction, so a live-mode refresh mid-drag slides the brush start

**CONFIDENCE: 80%**
**LINE:** 446 (`startX=xf(e)`), 456, 426

`xf()` returns a 0..1 fraction of the SVG width, and `timeForFrac` (line 426) resolves it against `ribbonSpan()`, which reads the *current* `RIB`. In live mode a `setInterval` fires `refresh()` every 5s (line 618), which calls `renderRibbon(tl)` and advances `RIB.series` — so the span shifts under an in-progress drag and the anchor fraction now maps to a different absolute time than when the user pressed down. The brush's left edge creeps.

The same 5s tick also runs `loadRiver()` → `applyRiver()` mid-drag, partially undoing the "rebuild the river once, on release" win the patch was written for (comment at 440–441).

**FIX:** resolve the anchor to an absolute timestamp at pointerdown — `startT=timeForFrac(xf(e))` — and in `pointermove` build the brush from `startT` and `timeForFrac(at)`. Optionally also skip the live refresh while `activePointer!==null`.

---

## SUGGESTION 3 — the new window-level Escape handler makes Escape mean three things at once

**CONFIDENCE: 85%**
**LINE:** 464–470, interacting with 622 and 632

Escape now has three independent listeners, all of which fire on a single press:

- line 622 — search input: clears the query and calls `refresh()`
- line 632 — document: closes the run drawer and the keys drawer
- line 464 — **new** window handler: clears the brush and calls `applyRiver()`

So with a brush active, pressing Escape to close the detail drawer *also* silently discards the time brush; pressing Escape to clear the search box does too. The user gets a side effect they did not ask for, and the brush is not recoverable.

The guard at 466 (`if(activePointer===null&&!S.brush)return;`) correctly makes the handler inert when there's nothing to cancel, but does nothing to arbitrate against the other two consumers.

**FIX:** scope the handler to cancelling an **in-flight drag only**, which is what the change description says it is for:

```js
window.addEventListener('keydown',e=>{
  if(e.key!=='Escape'||activePointer===null)return;
  e.stopPropagation();
  activePointer=null;moved=false;S.brush=null;
  if(frame){cancelAnimationFrame(frame);frame=0;}
  renderRibbon(RIB);updateBrushNote();applyRiver();
});
```

Clearing a *committed* brush already has a dedicated affordance — the `clear` button in the brush note (line 478).

Also note the handler sets `activePointer=null` without `svg.releasePointerCapture(...)`. Harmless in practice (implicit release fires at `pointerup`), but inconsistent with `endDrag` and worth mirroring for clarity.

---

## SUGGESTION 4 — `.seg button` at `--fg-mute` on `--bg` drops the range selector below WCAG AA

**CONFIDENCE: 85%**
**LINE:** 67

The time-range buttons are 11px mono. Using the DESIGN.md token values (near-neutral, chroma 0.012, so `Y ≈ L³` is a good approximation):

| pairing | tokens | ratio |
|---|---|---|
| **before**: `.seg button` `--fg-dim` on `.bar` `--panel` | 0.74 on 0.20 | **7.9 : 1** |
| **after**: `.seg button` `--fg-mute` on `.bar` `--bg` | 0.56 on 0.16 | **4.2 : 1** |

4.2:1 is under the 4.5:1 AA floor for normal-size text. The double move — text down one step *and* background down one step — is what crosses the line. DESIGN.md also scopes `--fg-mute` to "tertiary, axis labels"; the range selector is the console's primary navigation, not tertiary chrome.

For contrast, the other two recolors in this patch are fine:
- `.brand b` → `--fg-dim` on `--bg` (line 56) = **8.4 : 1**. Passes comfortably. No action needed.
- `.brand .tag`, `.search .k`, `.micro` at `--fg-mute` on `--bg` = 4.2:1, but these are genuinely tertiary and match the token's documented role. Acceptable.

**FIX:** `.seg button{...;color:var(--fg-dim)}` (8.4:1 on the new `--bg` bar) and promote the hover on line 68 to `color:var(--fg)`, keeping the resting/hover/pressed states distinct.

Pre-existing, not introduced here, but adjacent: `.brush-note button` (line 120) is `--fg-mute` on `.phd`'s `--raise` = **3.5 : 1** at 10.5px. Worth fixing while you're in the file.

---

## SUGGESTION 5 — full-bleed `.wrap`: brush stroke scales non-uniformly, and the stat strip stops being a strip

**CONFIDENCE: 80%**
**LINE:** 82, affecting 420 and 88–89

Removing `max-width:1720px;margin:0 auto` is safe for the ribbon's *bars* — `preserveAspectRatio="none"` on a `0 0 1000 78` viewBox is exactly the right call for a full-width timeline, and DESIGN.md explicitly wants it full-width. Two consequences are worth handling:

1. **Brush selection rect stroke (line 420).** `stroke-width="1"` is in user units and `preserveAspectRatio="none"` scales x and y independently. At 1720px the x-scale was 1.72; full-bleed on a 3440px ultrawide it's 3.44, so the selection box renders with ~3.4px vertical edges and ~1px horizontal edges — a visibly lopsided rectangle. **Fix:** add `vector-effect="non-scaling-stroke"` to that rect.

2. **Stat cells.** `.band` (line 86) gives the stats panel `minmax(320px,1.15fr)` of an unbounded row and `.stats` (line 88) is `repeat(2,1fr)`, so on a 3440px viewport each of the 6 stat cells is ~600px wide around a 22px number. DESIGN.md: "Stat cell. Small (not hero) ... Grouped in a dense strip." **Fix:** `.stats{grid-template-columns:repeat(auto-fit,minmax(160px,1fr))}` so the strip densifies with available width instead of stretching. Same class of issue, lower priority: `.hbar` (line 133) pins `.lbl` at 120px while the track grows unbounded, so script names truncate next to a 1000px+ bar.

---

## Checked and clear

- **CSP.** No violations introduced. Verified against the vendored copy: zero inline `on*=` handler attributes in markup, zero `eval`/`new Function`/`javascript:`, zero external URLs, exactly one `<script>` block, exactly one `/api/v1` occurrence. All handlers are JS property assignments (`el.onclick=`), including the new `BRUSH_NOTE.clear.onclick` at line 480 — fine under a hash-only `script-src`. Inline `style="..."` attributes are covered by `style-src 'unsafe-inline'` (via the `style-src-attr` fallback), as the comment in `api/telemetry.go` already documents.
- **CSP hash staleness.** Not a risk: `buildTelemetryCSP` / `inlineHash` in `C:\Users\colby.west\Desktop\Projects\Gatus\api\telemetry.go` (lines 289–338) compute the SHA-256 at `init()` from the embedded bytes *after* the `/api/v1` rewrite, and panic if the script-block or API-base count ever drifts from 1. Rewriting the script cannot desync the header.
- **`BRUSH_NOTE` cache invalidation.** Safe. `#brushnote` is written in exactly one place (line 478) and nothing else touches it — grep for `brushnote` returns only the static markup at 282 and `updateBrushNote`. Its parent `.phd` (line 282) is static markup; `renderRibbon` only writes `#ribsub`, `#riblegend` and `#ribbon`, which are siblings, never the container. The cached nodes cannot be orphaned.
- **`hidden` on `#brushclear`.** Works — neither `button{}` (line 42) nor `.brush-note button` (line 120) declares `display`, so the UA `[hidden]{display:none}` rule is not overridden.
- **Stale rAF after teardown.** No path found. `frame` is cleared *first* inside the callback (line 432), so a throw inside `renderRibbon` cannot wedge scheduling; both `endDrag` (437) and the Escape handler (468) `cancelAnimationFrame` before repainting synchronously. `schedule()` is only reachable from `pointermove`, which is itself gated on a matching `activePointer`.
- **Never-removed `window` `pointerup` / `keydown` listeners.** Not a leak. This is a single-page, never-unmounted document; both are registered exactly once from the IIFE at line 427 and hold only closure state. `endDrag` is idempotent — the svg listener (459) runs first under pointer capture and nulls `activePointer`, then the bubbled window listener (463) early-returns at line 434. No double-apply.
- **Sticky bar covering content.** No. A `position:sticky` element still occupies flow space, so a wrapped, taller `.bar` pushes `.wrap` down rather than overlapping it. `.scrim` at `z-index:40` (line 193) still correctly outranks the bar's `z-index:30`.
- **Other rules assuming a 46px bar.** Only one `46` remains in the file — `.dhd{height:46px}` at line 200, the drawer header, unrelated to the top bar. `.rv thead th{position:sticky;top:0}` (line 157) sticks inside `.river`'s own `overflow:auto` box, not the viewport, so it never depended on the bar height.
- **`.search` flex sizing at narrow widths.** Sound. `flex:1 1 220px;min-width:180px;max-width:340px` with `flex-wrap:wrap` on the parent means the search wraps to its own line rather than being crushed, and `.grow{flex:1}` (basis 0) still right-aligns `.health` on whatever line it lands on. Cosmetic nit only: the `width:260px` on line 72 is now dead — `flex-basis:220px` wins on the main axis.

---

## Summary

```
BLOCKERS: 2
  1. .bar min-height:50px leaks into .site .bar — sparkbar 4px -> 50px    line 51/108/391
  2. renderRibbon(RIB) throws on null RIB before first load / API down    line 401, 432/439/469/480

SUGGESTIONS: 5
  1. second pointerdown mid-drag wipes the brush                          line 444
  2. fractional startX slides under live refresh                          line 446
  3. window Escape handler overloads Escape three ways                    line 464
  4. .seg button 4.2:1 contrast, below AA                                 line 67
  5. full-bleed: non-uniform brush stroke + stretched stat strip          line 82/420/88

APPROVED: no
```

Both blockers are in code paths the patch touched. Blocker 1 is the higher-impact one — it is a pure cascade accident from swapping `height` for `min-height` on a class name that is shared with an unrelated component, and it degrades the site board on every page load, not just under an edge case. Fix it by renaming the site sparkbar's class, not by piling overrides onto `.site .bar`.

Remember to re-vendor into `C:\Users\colby.west\Desktop\Projects\Gatus\api\assets\telemetry-console.html` after any fix — the two files must stay byte-identical, and `api/telemetry.go` recomputes the CSP hash from the embedded copy at `init()`.
