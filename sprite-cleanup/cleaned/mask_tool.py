#!/usr/bin/env -S uv run --quiet --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pillow", "numpy", "scipy"]
# ///
"""Paint-over masks: override the automatic cut where it guesses wrong.

    ./mask_tool.py init          # templates at 1x
    ./mask_tool.py init 4        # templates at 4x — easier to paint
    ./mask_tool.py clear duelist # throw one template away

Then open cleaned/masks/<name>_paint.png in any editor and paint:

    pure GREEN  #00FF00  keep this, whatever the algorithm decided
    pure RED    #FF0000  drop this

Leave everything else untouched. Use a hard-edged pencil at 100% opacity with
anti-aliasing off — half-transparent brush strokes blend into the artwork and
won't be recognised. Green wins where the two overlap.

Save, then re-run ./cleanup.py. Templates are picked up automatically, and
any integer zoom factor is detected from the file's size.
"""
import os
import sys
import numpy as np
from PIL import Image

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.dirname(HERE)        # the folder holding the original PNGs
OUT = HERE                         # this script's own folder
MASKS = os.path.join(OUT, 'masks')

SHEETS = {'archer': 'image.png', 'knight': 'image (1).png',
          'duelist': 'image (2).png', 'bastion': 'image (3).png'}

README = """Paint-over masks
================

Open <name>_paint.png, paint on top of the artwork, save, then re-run
cleanup.py.

    pure GREEN  #00FF00   force KEEP  (overrides any removal)
    pure RED    #FF0000   force DROP  (erases, even if the art is there)

Anything you don't paint is left to the automatic pass. Green beats red.

Use a hard pencil, 100% opacity, anti-aliasing OFF. Soft or semi-transparent
strokes blend with the sepia underneath and will be ignored.

Templates may be at a zoom factor (see scale in the filename note below); the
cleanup script works out the factor from the image size, so don't resize them.

Delete a template to go back to fully automatic for that sheet.
"""


def init(scale=1):
    os.makedirs(MASKS, exist_ok=True)
    for label, fname in SHEETS.items():
        img = Image.open(os.path.join(SRC, fname)).convert('RGB')
        if scale != 1:
            img = img.resize((img.width * scale, img.height * scale), Image.NEAREST)
        path = os.path.join(MASKS, f'{label}_paint.png')
        if os.path.exists(path):
            print(f'skip   {path} (already exists)')
            continue
        img.save(path)
        print(f'wrote  {path}  {img.width}x{img.height}  ({scale}x)')
    with open(os.path.join(MASKS, 'README.txt'), 'w') as fh:
        fh.write(README)


def clear(label):
    path = os.path.join(MASKS, f'{label}_paint.png')
    if os.path.exists(path):
        os.remove(path)
        print('removed', path)
    else:
        print('nothing at', path)


def read_paint(label, shape):
    """-> (force_keep, force_drop) boolean masks at sheet resolution."""
    path = os.path.join(MASKS, f'{label}_paint.png')
    if not os.path.exists(path):
        return None, None
    p = np.asarray(Image.open(path).convert('RGB')).astype(np.int16)
    h, w = shape
    ph, pw = p.shape[:2]
    if pw % w or ph % h or (pw // w) != (ph // h):
        raise SystemExit(f'{path}: size {pw}x{ph} is not an integer multiple '
                         f'of the sheet ({w}x{h}) — do not resize templates')
    s = pw // w
    r, g, b = p[..., 0], p[..., 1], p[..., 2]
    keep = (g >= 170) & (r <= 100) & (b <= 100)
    drop = (r >= 170) & (g <= 100) & (b <= 100)
    if s > 1:      # any painted subpixel marks the whole sheet pixel
        keep = keep.reshape(h, s, w, s).any(axis=(1, 3))
        drop = drop.reshape(h, s, w, s).any(axis=(1, 3))
    return keep, drop


def apply_paint(a, label):
    keep, drop = read_paint(label, a.shape)
    if keep is None:
        return a, 0, 0
    a = a.copy()
    a[drop] = 0.0
    a[keep] = 1.0        # applied second: green wins on overlap
    return a, int(keep.sum()), int(drop.sum())


if __name__ == '__main__':
    cmd = sys.argv[1] if len(sys.argv) > 1 else 'init'
    if cmd == 'init':
        init(int(sys.argv[2]) if len(sys.argv) > 2 else 1)
    elif cmd == 'clear':
        clear(sys.argv[2])
    else:
        raise SystemExit(__doc__)
