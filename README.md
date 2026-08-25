# tools

Small, self-contained tools. Each one stands on its own — no shared install, no build step.

## `cutout.html` — interactive sprite cutout

Single-file browser tool for knocking a background off a sprite sheet by hand.
No dependencies, no server: open the file in a browser.

```
open cutout.html
```

Drop a sprite sheet onto the page, then use **Brush** / **Bucket** to erase
background and **Keep** to paint areas back in. **Show mask** toggles the
alpha overlay, **Undo** steps back, **Save progress** keeps your work across
reloads, and **Download PNG** exports the cut result.

Use this when the automatic pass below gets something wrong and you just want
to fix it directly.

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
