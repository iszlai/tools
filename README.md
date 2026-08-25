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
