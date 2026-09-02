# tools

Small, self-contained tools. Each one stands on its own — no shared install, no build step.

## `cutout.html` — interactive sprite cutout

Single-file browser tool. No dependencies, no server, no build: open the file.

```
open cutout.html
```

Drop (or paste) an image and it cuts the background straight away — it runs the
same background model as the pipeline below: the parchment is estimated and
interpolated across the subject, then keyed both darker and lighter than that
estimate so unoutlined skin survives, and the paper colour is divided back out
of the soft edges so nothing keeps a halo. **Tolerance** re-keys live.

What it deliberately leaves alone is the furniture — captions, the drawn ground
line, fence posts and log piles. Those were the fiddliest heuristics in the
pipeline and they are quicker to handle by hand:

| Tool | Key | Does | Good for |
|---|---|---|---|
| **Brush** | `b` | paints pixel by pixel | corrections |
| **Bucket** | `f` | fills one connected region, drag to sweep | a fence post, a caption word, a hole in a face |
| **Box** | `x` | drag a rectangle | the bulk — a caption strip, a log pile, a whole fence |

**Erase** (`e`) and **Keep** (`k`) set what the tool does; hold `alt` to invert
mid-action. **Show mask** tints your overrides, `⌘Z` undoes, **Download PNG**
exports.

### Leak guard

On these sheets the artist drew a horizon line running *behind* each character,
so fence, figure and log pile are all one connected region — a naive bucket fill
on the fence takes the character with it. **Leak guard** (default 1) erodes
before filling to break hairline joins, and a fill that would swallow more than
20% of the visible art is refused with a note rather than silently wrecking the
frame. Where the join is the character's own body rather than a thin bridge, no
guard can help: use **Box** there.

### Taking a break

**Save progress** (`⌘S`) downloads `<name>-progress.json` — the source image,
your mask and your settings. Drop that file back on the page to carry on exactly
where you left off. Work is also auto-saved after every stroke, so if you just
close the tab a **Resume** button appears next time. Auto-save uses browser
storage and skips images over ~4 MB (it says so); the button always works.

## `diagme/` — quick local diagramming

Excalidraw-shaped sketchpad for the diagrams you draw once and throw away. Vanilla
JS drawing into a single SVG element: no dependencies, no build step — nginx just
serves the folder.

```bash
cd diagme
docker compose up -d --build
```

Open http://localhost:8080 (change the port in `docker-compose.yml` if 8080 is
taken). `app/` is bind-mounted into the container, so edit a file, refresh the
browser, done — no rebuild. Without Docker, `app/index.html` opens straight from
disk.

| Tool | Key | Does |
|---|---|---|
| **Select / move** | `v` | drag a shape to move it, corner handles resize; drag empty canvas to marquee-select, `shift` adds to the selection |
| **Box** | `r` | drag to size, click for a default |
| **Arrow** | `a` | once selected, drag the start/end handles — or the middle one to bend it |
| **Line** | `l` | same handles, no head |
| **Freehand** | `p` | stays active across strokes |
| **Text** | `t` | click and type, double-click to re-edit, corner resize scales the font |
| **Text doc** | `d` | markdown box — `#` headings, `**bold**`, `` `code` ``, lists, fences |

A multi-selection moves, deletes and recolours as one. The 6-colour palette sets the
colour for new shapes and recolours whatever is selected. `⌘/ctrl + scroll` or pinch
zooms (click `%` to reset), two-finger scroll / `space + drag` / middle-drag pans,
`delete` clears the selection, `⌘Z` undoes and `⌘⇧Z` redoes. Everything autosaves to
localStorage, so a refresh or a container restart loses nothing.

Bends are the only real geometry in it: dragging an arrow's middle handle stores a
pass-through point, and the curve is a quadratic Bézier whose control point is solved
so the line goes *through* that handle rather than near it. Markdown docs render via a
~40-line renderer inside an SVG `foreignObject` — no CDN, works offline.

### Smoke test

Focus, blur and event ordering can't be verified by reading the code, so
`tests/smoke.js` drives the running app in headless Chromium and asserts on text
entry, markdown rendering, marquee count, group move, group delete and arrow bend.

```bash
cd diagme
npm install playwright-core
PLAYWRIGHT_BROWSERS_PATH=./pw-browsers npx playwright-core install chromium-headless-shell
PLAYWRIGHT_BROWSERS_PATH=./pw-browsers node tests/smoke.js
```

It needs Playwright's own chromium — corporate-managed Chrome refuses to be automated
("remote debugging is disallowed by the system admin"). `tasks/lessons.md` has the
write-up of the focus race that made the test necessary.

## `sprite-cleanup/` — automatic parchment removal

Python pipeline that removes the parchment background from sepia sprite sheets
and slices them into individual frames.

```
sprite-cleanup/
├── image.png, image (1).png, image (2).png, image (3).png   # source sheets
└── cleaned/
    ├── cleanup.py        # the pipeline
    ├── mask_tool.py      # manual keep/drop overrides
    ├── masks/            # paint-over templates
    ├── frames/           # sliced output frames
    └── *_sheet.png, _preview_*.png
```

The scripts resolve paths relative to themselves: `cleanup.py` reads the source
sheets from its **parent** folder and writes output into its **own** folder.
Keep that layout intact or edit `SRC` / `OUT` at the top of each script.

### Run it

Both scripts are [uv](https://docs.astral.sh/uv/) inline-metadata scripts —
dependencies (pillow, numpy, scipy) are declared in the file and fetched on
first run.

```bash
cd sprite-cleanup/cleaned
./cleanup.py
```

### Fixing a bad cut

Where the automatic pass guesses wrong, override it with a paint mask:

```bash
./mask_tool.py init 4        # templates at 4x zoom — easier to paint
```

Open `masks/<name>_paint.png` in any editor and paint with a **hard pencil,
100% opacity, anti-aliasing off**:

| Colour | Hex | Meaning |
|---|---|---|
| pure green | `#00FF00` | force **keep** |
| pure red | `#FF0000` | force **drop** |

Green beats red. Anything unpainted is left to the automatic pass. Save, then
re-run `./cleanup.py`. Delete a template to go back to fully automatic for that
sheet. See `masks/README.txt` for the full notes.

### How the pipeline works

`cleanup.py` runs nine stages, in order:

1. **paper model** — polynomial seed, then the real paper interpolated across the subject
2. **frame rules** — faint vertical divider stripes folded into the paper model
3. **dark key** — softly ramped "darker than paper" for clean anti-aliased edges
4. **furniture** — drawn ground line, cast shadow, caption text
5. **scenery** — fence posts / log piles separated from the figure by a 1px erosion
6. **light key** — lit skin, which has no drawn contour and so is invisible to the dark key
7. **solidify** — figure interiors made opaque, genuine see-through gaps carved back out
8. **unmix** — paper colour divided out of partial-alpha pixels, so no halo
9. **paint** — manual keep/drop overrides from `mask_tool.py`

## `taskhound/` — issues in a file you can commit

A CLI (`th`) over a single `.taskhound.yaml` at the root of a repo, plus a
kanban board on localhost that can do everything the CLI can. Built for working
a plan with agents: every issue declares what blocks it, so `th next` answers
"what can I start right now" and `th dependents` answers "what does finishing
this unlock".

```bash
cd taskhound && ./install.sh    # ~/.local/bin/th + the agent skill
cd ~/your-repo && th init
th add "Extract the rate limiter"
th add "Rate-limit the public API" --blocked-by TH-1
th next
th ui --open
```

Only `blocked_by` is stored; `blocks` is derived on read, so the two directions
can never disagree, and cycles are refused at write time. There is no `blocked`
status — an issue is blocked when a blocker of it isn't `done`, which is
computed rather than remembered.

Several agents and a human can drive one board at once: every write is a
read-modify-write under an exclusive `flock` on a sidecar lock file, landing via
temp-file-and-rename so a reader never sees half a file. The test suite runs 24
concurrent `th add` processes against one board and asserts nothing is lost.

`install.sh` also drops a skill in `~/.claude/skills/taskhound/`, so Claude Code
picks the tool up on its own; `th agent-guide` prints the same document for
anything else. `taskhound/README.md` has the full command table, the HTTP API,
and a recipe for migrating an existing `tasks/todo.md` onto a board.

Go, one vendored dependency — `make build` and `make test` need no network.
