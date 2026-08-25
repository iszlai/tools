# diagme — quick local diagramming tool

## Todo

- [x] Static app skeleton (index.html, style.css, app.js) — vanilla JS + SVG, no build step
- [x] Shapes: box, arrow, straight line, freehand line, text label, markdown doc box
- [x] 6-color palette (applies to new shapes; recolors selected shape)
- [x] Zoom (wheel/pinch + buttons) and pan (two-finger scroll / space-drag / middle-drag)
- [x] Corner-resize for all shapes; arrows/lines get start/mid/end handles (mid = bend)
- [x] Text editing overlays (double-click), mini markdown renderer for doc boxes
- [x] Undo/redo + localStorage autosave (survives refresh)
- [x] Dockerfile (nginx:alpine) + docker-compose.yml + README
- [x] Verify: `node --check` passed, docker image built, container serves all files (HTTP 200)

## Review

- Stack: single-page vanilla JS (~550 lines) rendering into one SVG element; full re-render
  per interaction (fine at this scale, zero dependencies, no build step).
- Arrow/line bends: dragging the middle handle stores a pass-through point; the curve is a
  quadratic Bézier whose control point is derived so the line goes through the handle.
- Corner resize is one generic scale-from-opposite-corner routine shared by boxes, docs,
  freehand strokes (scales points) and text (scales font size).
- Markdown docs use a ~40-line renderer (h1–h3, bold, italic, inline code, code fences,
  ul/ol) inside an SVG foreignObject — no CDN, works offline.
- Beyond the ask, added two small things any drawing tool is unusable without:
  undo/redo (Cmd+Z) and localStorage autosave. Nothing else speculative.
- Round 2 (user feedback): fixed dead text tool (focus-race: editor was focused during
  pointerdown, browser's default mousedown blur committed the empty edit and deleted the
  shape — now created on pointerup + 300ms refocus guard on blur); added marquee
  multi-select (drag empty canvas; shift adds; group move/delete/recolor; resize handles
  stay single-selection only). See tasks/lessons.md.
- Verified by tests/smoke.js in headless Chromium: 9/9 assertions pass (text typing,
  markdown rendering, marquee count, group move, group delete, arrow bend).
- docker-compose now bind-mounts ./app — edits are live on browser refresh, no rebuild.
