# diagme

Tiny local Excalidraw-style diagramming aid. Vanilla JS + SVG, no build step, served by nginx.

## Run

```bash
docker compose up -d --build
```

Open http://localhost:8080 (change the port in `docker-compose.yml` if 8080 is taken).

Without Docker: open `app/index.html` directly, or `python3 -m http.server -d app 8080`.

## Features

| Tool | Key | Notes |
|---|---|---|
| Select / move | `V` | drag a shape to move it; corner handles resize; drag empty canvas to marquee-select everything in the area (shift-click or shift-drag adds to the selection); a multi-selection moves, deletes, and recolors together |
| Box | `R` | drag to size, click for default |
| Arrow | `A` | selected: drag start/end handles; drag the middle handle to bend |
| Line | `L` | same handles as arrow, no head |
| Freehand | `P` | stays active for multiple strokes |
| Text | `T` | click to place, type; double-click to re-edit; corner resize scales font |
| Text doc | `D` | markdown box (`#` headings, `**bold**`, `` `code` ``, lists, code fences) |

Markdown rendering is `mdlite`: `app/md.js` is a copy of `../mdlite/md.js`.
Edit it there and run `make -C ../mdlite install` — `make -C ../mdlite check`
fails if the copy has drifted. It also handles `- [ ]` task lists.

- 6-color palette — sets color for new shapes; recolors the selected shape.
- Zoom: `⌘/ctrl + scroll` or pinch, plus the `− / %` `/ +` buttons (click `%` to reset).
- Pan: two-finger scroll, `space + drag`, or middle-mouse drag.
- `Delete` removes selection, `⌘/ctrl+Z` undo, `shift` redo.
- Autosaves to the browser's localStorage — survives refresh and container restarts.
- `app/` is bind-mounted into the container — edit a file, refresh the browser, no rebuild.

## Smoke test

`tests/smoke.js` drives the running app in headless Chromium (text, markdown note, marquee,
group move/delete, arrow bend):

```bash
npm install playwright-core
PLAYWRIGHT_BROWSERS_PATH=./pw-browsers npx playwright-core install chromium-headless-shell
PLAYWRIGHT_BROWSERS_PATH=./pw-browsers node tests/smoke.js
```

Note: corporate-managed Chrome can't be automated (admin policy blocks remote debugging) —
use Playwright's chromium as above.
