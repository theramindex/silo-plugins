# Plugin web UI verification — 2026-11-13

Result: **Needs work**. This review used Modern Web Guidance for accessibility,
dialogs, forms, HTML, layout, responsive behavior and performance. It identified
11 confirmed findings; it did not apply the repairs listed below.

## Scope

- App Links 0.1.5: listing, admin editor, embedded-app shell and missing-page
  recovery, rendered from its Go HTML strings with synthetic fixture values.
- Dispatcharr 0.12.0: Home, channels, guide, search, settings, Sports, programme
  details, admin and connection editor.
- Xtream 0.7.0: equivalent destinations, source editor, player and Sports companion.
- HDHomeRun 0.5.0: channels, guide and endpoint administration through local
  development emulators.
- RomM 0.7.2: library, game details, account dialog and emulator/save browser checks.
- The shared media client was reviewed as a dependency. Its source matched the
  v0.1.16 module used by the three Live TV plugins.

API-only endpoints, host-owned provider/settings forms, native Kodi UI,
documentation placeholders, external embedded apps and vendored emulator/
decoder implementations were excluded.

## Confirmed findings

1. **P1 — Dispatcharr and Xtream source dialogs:** custom source editors trap
   Tab but leave background controls focusable. Use native modal semantics or
   make appropriate outside siblings inert; preserve dirty-form handling.
2. **P1 — Dispatcharr compact admin navigation:** end-aligned overflowing tabs
   place early tabs outside the left edge at phone/tablet widths. Use safe/start
   alignment and an accessible scroll region or compact navigation pattern.
3. **P1 — App Links enlarged-text admin layout:** at 320px and 200% text, row
   and input content extends beyond the viewport while root clipping conceals
   the overflow. Permit controls and grid children to shrink/wrap.
4. **P1 — HDHomeRun enlarged-text navigation:** the same viewport/text-size case
   makes the shared navigation overflow. Wrap it or provide intentional,
   keyboard-reachable scrolling.
5. **P1 — Contrast:** App Links' light text on its fallback pink primary surface
   measures about 3.35:1, and Live TV programme badges about 3.52:1. Small
   guide-freshness text also fails 4.5:1 in the checked light palettes. Correct
   semantic foreground/background pairs across default and custom themes.
6. **P1 — Dispatcharr source-table semantics:** rows declared with ARIA table
   roles lack the required columnheader/cell structure. Prefer native table
   markup or complete the ARIA structure.
7. **P2 — Xtream narrow Sports toolbar:** the companion delay controls overflow
   at 320px. Dispatcharr has a separately prepared intrinsic-wrapping repair;
   the corresponding Xtream repair remains outstanding.
8. **P2 — RomM Menu naming:** the visible Menu label is absent from the button's
   accessible name. Preserve the visible label in the name for voice control.
9. **P2 — RomM active filters:** an aria-label on an unroled generic div does not
   provide valid named-group semantics.
10. **P2 — Dispatcharr search categories:** at 320px with 200% text, a category
    tile extends beyond the viewport despite no document overflow. Use
    shrinkable grid tracks or an intentionally scrollable layout.
11. **P2 — Dynamic viewport minima:** Xtream's player and App Links' embedded
    shell combine dynamic height with a legacy viewport minimum. Where the
    latter is larger, it defeats dynamic sizing. This was confirmed from the
    CSS constraints; actual mobile browser chrome requires a device check.

## Triage and retained patterns

Metadata-heavy card-name warnings need manual naming/voice-control review;
several include decorative initials, counts or punctuation. A small Xtream
checkbox also needs its enclosing label hit area checked before repair.
App Links' fallback does not react to a light-theme slug, but actual host theme
injection needs separate verification. Eager Xtream decoder downloads are a
profiling candidate, not a measured LCP/INP finding.

Transient contrast warnings during transitions and unsettled viewport geometry
were dropped after settled checks. Composite focus styling was inspected before
classifying outline suppression. Programme overlays in both Live TV plugins
correctly make the background inert; RomM's native account modal also blocks
background focus and supports Escape. Those working patterns were retained.

## Validation

The local review exercised 31 rendered states, 67 axe checks and 155 viewport/
text-size cases. There were no page JavaScript errors. Existing checks passed:
175 JavaScript tests across App Links, Dispatcharr, Xtream and shared media;
29 maintained JavaScript syntax checks; RomM's library browser fixture; both
Sports companion fixtures; and Xtream's desktop player layout check.

RomM's full player smoke passed on a fresh isolated fixture host, including
approval/cancel/decline, emulator boot, save/resume and failed-sync retry. Its
first shared-fixture attempt timed out at retry and was not classified as a
confirmed product defect. An additional Xtream 320px companion reproduction
failed as described in finding 7; its existing browser fixture checks 390px.

This is local source/Chromium verification, not full WCAG certification or
proof of Safari/Firefox, assistive technology, physical gamepads, real mobile
browser chrome, custom production themes, upstream providers or live streaming.
