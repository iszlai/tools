# Lessons

## 2026-08-25 — focus() during pointerdown loses to the browser's default mousedown blur

**What happened:** Text tool created a textarea and focused it inside the `pointerdown`
handler. The browser's default mousedown handling then moved focus to `<body>`, the blur
handler committed the (empty) edit and deleted the shape — text tool looked completely dead.

**Rules:**
1. Never `focus()` an editor from inside `pointerdown`/`mousedown`. Create + focus it on
   `pointerup` (after the focus-stealing default has already run), and add a short
   refocus-guard window on blur for the remaining races.
2. Interactive UI (focus, blur, event ordering, pointer capture) cannot be verified by
   `node --check` or code reading — run a real-browser smoke test (`tests/smoke.js`)
   before calling UI work done.
3. This Mac's corporate Chrome blocks automation ("DevTools remote debugging is disallowed
   by the system admin"). Use Playwright's own chromium (`PLAYWRIGHT_BROWSERS_PATH=<dir>
   npx playwright-core install chromium-headless-shell`); it must run outside the sandbox
   (Mach port registration is denied inside).
