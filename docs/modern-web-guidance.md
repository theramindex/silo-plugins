# Modern Web Guidance

Use [Modern Web Guidance](https://github.com/GoogleChrome/modern-web-guidance)
when changing plugin HTML, CSS or browser JavaScript. Keep the plugin's Silo
semantic colors, typography, readability preferences and input behavior as the
product contract.

## Workflow

1. Search for one concrete capability before implementing it:
   `pnpm dlx modern-web-guidance@latest search "responsive container layout"`.
2. Retrieve matching guide IDs from the search result:
   `pnpm dlx modern-web-guidance@latest retrieve "css-layout,responsive-design"`.
   Use `npx -y` instead of `pnpm dlx` when pnpm is unavailable.
3. Check the advice against the real component and supported browsers. Prefer
   Baseline Widely Available features. Newer features need feature detection
   and a usable fallback. Confirm questionable compatibility data against
   primary browser documentation.
4. Reproduce the issue and make a bounded change. Preserve DOM/focus order;
   measure before and after performance changes.
5. Verify desktop and phone layouts, enlarged text, keyboard controls, native
   form behavior, error recovery and the repository's relevant checks. Include
   supported Safari/embedded-browser behavior when the feature affects it.

The upstream repository offers an agent skill and an installer. The CLI also
works without installing an agent plugin. An explicitly requested broad legacy
modernization can use `legacy-scan <path> --json`; its deterministic rules cover
only a subset of the guidance. Review candidates, valid fallbacks and vendor
code instead of treating scanner output as a complete audit.

## Verification standards

- Inspect visible controls as well as document width. Root clipping can conceal
  overflowing forms, and intentional shelves/tables may scroll independently.
- Test 200% text size. A single viewport-width breakpoint cannot account for
  text growth or a narrow component inside a desktop page.
- Use native modal dialogs or complete focus isolation for custom dialogs.
  Check background focus, Escape, safe dismissal and return to the invoker.
- Keep a control's visible label in its accessible name. Associate supporting
  metadata with the appropriate description rather than guessing from an axe
  finding alone.
- Verify foreground/background pairs across themes, including accent badges.
- Measure performance before introducing scheduling, containment or caching.
- Distinguish source/fixture checks from live upstream, assistive-technology,
  physical controller and cross-browser verification.

[The initial verification findings](web-ui-verification-2026-11-13.md) cover
App Links, Dispatcharr, Xtream, HDHomeRun, RomM and their shared media client.
