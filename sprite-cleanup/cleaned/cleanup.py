#!/usr/bin/env -S uv run --quiet --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["pillow", "numpy", "scipy"]
# ///
"""Clean the parchment background off the sprite sheets.

  1. paper model  - polynomial seed, then the real paper interpolated across
                    the subject; leaves ~1-2 levels of residual on plain
                    background, where a bicubic alone leaves +-30
  2. frame rules  - faint vertical divider stripes folded into the paper model,
                    so art crossing a stripe still cuts out correctly
  3. dark key     - softly ramped "darker than paper" -> clean AA edges
  4. furniture    - drawn ground line, cast shadow, caption text
  5. scenery      - fence posts / log piles in the idle cell, separated from
                    the figure by a 1px erosion (they only touch via AA)
  6. light key    - lit skin, which has no drawn contour and so is invisible to
                    the dark key
  7. solidify     - figure interiors made opaque; genuine see-through gaps
                    (between the legs, inside a bow) carved back out by tone
  8. unmix        - paper colour divided out of partial-alpha pixels: no halo
  9. paint        - manual keep/drop overrides, see mask_tool.py
"""
import os
import numpy as np
from PIL import Image
from scipy import ndimage

from mask_tool import apply_paint

LUMA = np.array([0.299, 0.587, 0.114], np.float32)
HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.dirname(HERE)        # the folder holding the original PNGs
OUT = HERE                         # this script's own folder


def disk(r):
    y, x = np.mgrid[-r:r + 1, -r:r + 1]
    return x * x + y * y <= r * r


def mad(v):
    return 1.4826 * np.median(np.abs(v - np.median(v))) + 1e-6


# --------------------------------------------------------------- paper model

def fit_poly(img, deg=3, iters=6):
    h, w, _ = img.shape
    yy, xx = np.mgrid[0:h, 0:w].astype(np.float32)
    xn, yn = xx / (w - 1) * 2 - 1, yy / (h - 1) * 2 - 1
    A = np.stack([((xn ** i) * (yn ** j)).ravel()
                  for i in range(deg + 1) for j in range(deg + 1 - i)], 1)
    L = (img @ LUMA).ravel()
    seed = L >= np.percentile(L, 55)

    out = np.empty_like(img)
    for c in range(3):
        b = img[:, :, c].ravel()
        k = seed.copy()
        for _ in range(iters):
            coef, *_ = np.linalg.lstsq(A[k], b[k], rcond=None)
            r = b - A @ coef
            nk = np.abs(r) < max(2.5 * mad(r[k]), 4.0)
            if nk.sum() < 0.05 * b.size:
                break
            k = nk
        out[:, :, c] = (A @ coef).reshape(h, w)
    return out


def inpaint_smooth(img, mask, sigmas=(6, 12, 24, 48, 96, 192)):
    """Smoothly interpolate the paper across the subject.

    Coarse-to-fine normalised convolution: every scale gives an estimate, and
    finer (more accurate) scales are blended in wherever enough background
    pixels actually support them.
    """
    m = mask.astype(np.float32)
    est = None
    for s in sorted(sigmas, reverse=True):
        num = ndimage.gaussian_filter(img * m[..., None], (s, s, 0))
        den = ndimage.gaussian_filter(m, s)[..., None]
        cur = num / np.maximum(den, 1e-6)
        if est is None:
            est = cur
        else:
            w = np.clip(den / 0.05, 0, 1)
            est = est * (1 - w) + cur * w
    return est


def fit_paper(img, rounds=2):
    """Paper model: global polynomial seed, then refined by interpolating the
    real paper across the subject. A bicubic alone leaves +-30 of error on the
    parchment's blotches, which would force a threshold high enough to eat
    faint linework."""
    paper = fit_poly(img)
    big = max(img.shape[:2])
    sigmas = tuple(s for s in (6, 12, 24, 48, 96, 192, 384) if s <= big)
    for _ in range(rounds):
        d = (paper @ LUMA) - (img @ LUMA)
        sigma = mad(d[np.abs(d) < 20])
        fg = ndimage.binary_dilation(np.abs(d) > max(2.5 * sigma, 5.0), disk(4))
        if fg.mean() > 0.9:
            break
        paper = inpaint_smooth(img, ~fg, sigmas)
    return paper


def soft_alpha(img, paper):
    """Alpha from how far a pixel's tone sits below the paper.

    One-sided on purpose. Pale skin is lighter than the parchment, so this key
    leaves faces hollow — that is repaired by solidify() further down, not by
    also keying on "lighter than paper", which drags in parchment grain and
    leaves faces with partial alpha that unmix() blows out to white.
    """
    d = (paper @ LUMA) - (img @ LUMA)
    sigma = mad(d[np.abs(d) < 20])
    t0 = max(2.5 * sigma, 10.0)
    return np.clip((d - t0) / 22.0, 0, 1).astype(np.float32), t0, d


def add_light(a, d, t_light=20.0, min_area=30):
    """Recover lit skin, which the dark key cannot see.

    Faces are drawn with no contour on the lit side — the parchment itself is
    the edge — so the nose, mouth and chin get bitten off. Skin runs ~20+
    lighter than the paper model while bare paper runs ~3, so a hard threshold
    separates them; hard rather than ramped because a partial alpha here is
    exactly what unmix() amplifies into white blocks.

    Only regions touching existing ink are admitted, which keeps stray bright
    parchment grain out. Runs after the furniture passes so it cannot resurrect
    caption glyphs by welding them onto a figure.
    """
    ink = a > 0.45
    cand = ((-d) > t_light) & ~ink
    lab, n = ndimage.label(cand, structure=np.ones((3, 3), bool))
    if n == 0:
        return a
    areas = np.bincount(lab.ravel(), minlength=n + 1)
    touching = np.unique(lab[ndimage.binary_dilation(ink, disk(1)) & cand])
    ids = [i for i in touching if i and areas[i] >= min_area]
    if not ids:
        return a
    return np.maximum(a, np.isin(lab, ids).astype(np.float32))


def solidify(a, d, reach=7, paper_tol=10.0, min_open=25):
    """Make each figure's interior fully opaque, keeping real gaps open.

    Close the outline and fill it, which captures faces (lighter than paper, so
    the key misses them) along with everything else enclosed. Then carve back
    out only the regions that are genuinely parchment showing through — between
    the legs, inside the curve of a bow — identified by tone: paper averages ~0
    against the paper model, whereas pale skin sits 17 to 60 above it.

    Opaque interiors also keep unmix() honest: it only ever rescales pixels at
    the soft outer edge, where the paper really is mixed in.
    """
    solid = ndimage.binary_fill_holes(
        ndimage.binary_closing(a > 0.45, disk(reach)))
    interior = solid & (a <= 0.45)
    lab, n = ndimage.label(interior)
    if n:
        stay_open = np.zeros(n + 1, bool)
        for i, sl in enumerate(ndimage.find_objects(lab), start=1):
            m = lab[sl] == i
            if m.sum() >= min_open and abs(float(d[sl][m].mean())) <= paper_tol:
                stay_open[i] = True
        solid &= ~stay_open[lab]
    return np.maximum(a, solid.astype(np.float32))


# ------------------------------------------------- faint frame-divider stripes

def flatten_columns(img, paper, ink_cut=45.0, min_frac=0.40, max_shift=40.0):
    """Absorb faint vertical frame rules into the paper model.

    The sheets are divided by soft tan stripes ~15px wide. Detecting them by
    position was unreliable, so instead every column gets its own residual
    offset, measured over that column's non-ink rows. A stripe reads as a
    uniform positive offset and cancels out; ordinary columns measure ~0.

    Folding stripes into the paper (rather than deleting the columns) means a
    sword or cloak crossing a stripe is still cut out correctly, since ink is
    far darker than the stripe.
    """
    h, w, _ = img.shape
    d = (paper @ LUMA) - (img @ LUMA)
    # cap ink so it can't drag the median, then take a tall per-column running
    # median: robust to the linework crossing a stripe, and unlike a whole
    # column median it follows how the stripe fades top to bottom
    capped = np.clip(d, -20.0, 20.0)
    win = max(31, (h // 6) | 1)
    shift = ndimage.median_filter(capped, size=(win, 1), mode='nearest')
    shift = np.clip(shift, 0.0, max_shift).astype(np.float32)
    return paper - shift[..., None], int((shift.max(axis=0) > 3).sum())


# ------------------------------------------------------- background furniture

def strip_ground(ink, min_len=18, max_thick=6):
    """Long, thin, horizontal ink: the drawn ground line."""
    flat = ink & ~ndimage.binary_opening(ink, np.ones((max_thick + 1, 1), bool))
    lab, n = ndimage.label(flat, structure=np.ones((3, 3), bool))
    if n == 0:
        return np.zeros_like(ink)
    kill = np.zeros(n + 1, bool)
    for i, sl in enumerate(ndimage.find_objects(lab), start=1):
        hh, ww = sl[0].stop - sl[0].start, sl[1].stop - sl[1].start
        if ww >= min_len and hh <= max_thick and ww >= 3 * hh:
            kill[i] = True
    return kill[lab]


def find_band(ink, lo_frac=0.55, keep=0.6, thick=26):
    """Rows holding the drawn ground: flat ink at the figures' baseline."""
    h = ink.shape[0]
    flat = ink & ~ndimage.binary_opening(ink, np.ones((thick, 1), bool))
    prof = flat.sum(axis=1).astype(np.float32)
    lo = int(h * lo_frac)
    seg = prof[lo:]
    if not seg.size or seg.max() <= 0:
        return None
    hot = seg > keep * seg.max()
    pk = int(seg.argmax())
    y0 = y1 = pk
    while y0 - 1 >= 0 and hot[y0 - 1]:
        y0 -= 1
    while y1 + 1 < hot.size and hot[y1 + 1]:
        y1 += 1
    return lo + y0, lo + y1 + 1


def core_columns(ink, x0, x1, slack=45):
    """Column span of the figure in a cell, found by how high its ink reaches.

    A figure's head sits far above any scenery, so growing outwards from the
    highest column while the ink stays near that height traces the figure and
    stops at fence posts and log piles.
    """
    sub = ink[:, x0:x1]
    present = sub.any(axis=0)
    if not present.any():
        return None
    tops = np.where(present, sub.argmax(axis=0), 1 << 20)
    c = int(np.argmin(tops))
    lim = tops[c] + slack
    l = r = c
    while l - 1 >= 0 and tops[l - 1] <= lim:
        l -= 1
    while r + 1 < tops.size and tops[r + 1] <= lim:
        r += 1
    return x0 + l, x0 + r + 1


def trim_ground(a, band, edges, thick=19, min_w=45, flat_ratio=3.0):
    """Clear the ground line and its cast shadow either side of each figure.

    Two jobs at once: the sheet reads much cleaner, and — more importantly —
    this band is what physically joins the fence posts and log piles to the
    figure, so severing it lets scenery removal work by component at all.
    """
    if band is None:
        return
    y0, y1 = band
    ink = a > 0.45
    flat = ink & ~ndimage.binary_opening(ink, np.ones((thick, 1), bool))

    # wide, flat blobs sitting on the baseline: ground line and cast shadow
    lab, n = ndimage.label(flat, structure=np.ones((3, 3), bool))
    if n:
        kill = np.zeros(n + 1, bool)
        for i, sl in enumerate(ndimage.find_objects(lab), start=1):
            hh, ww = sl[0].stop - sl[0].start, sl[1].stop - sl[1].start
            spans_band = sl[0].start < y1 and sl[0].stop > y0
            if spans_band and ww >= min_w and ww >= flat_ratio * hh:
                kill[i] = True
        a[kill[lab]] = 0.0

    # whatever flat ink is left in the band beside the figure is background
    ink = a > 0.45
    flat = ink & ~ndimage.binary_opening(ink, np.ones((thick, 1), bool))
    for i in range(len(edges) - 1):
        cc = core_columns(ink, edges[i], edges[i + 1])
        if cc is None:
            continue
        cl, cr = cc
        for xa, xb in ((edges[i], cl), (cr, edges[i + 1])):
            if xb > xa:
                sub = a[y0:y1, xa:xb]
                sub[flat[y0:y1, xa:xb]] = 0.0


def strip_captions(ink, band=(0.72, 0.96), max_ink=0.08, max_h=40, max_area=2500):
    """Caption text ('idle', 'attack', 'bastion EMP') below the artwork.

    The cut row is the emptiest row in the lower band rather than a fixed
    fraction, because the caption sits at very different heights in a 205px
    sheet and a 1024px tower. Whole glyphs are then removed by component, so
    tall ascenders crossing the cut don't leave crumbs behind.
    """
    h = ink.shape[0]
    cov = ink.mean(axis=1)
    lo, hi = int(h * band[0]), int(h * band[1])
    y = lo + int(np.argmin(cov[lo:hi]))

    below = ink[y:].sum()
    if below == 0 or below > max_ink * max(ink.sum(), 1):
        return np.zeros_like(ink)

    lab, n = ndimage.label(ink, structure=np.ones((3, 3), bool))
    if n == 0:
        return np.zeros_like(ink)
    areas = np.bincount(lab.ravel(), minlength=n + 1)
    boxes = ndimage.find_objects(lab)
    kill = np.zeros(n + 1, bool)
    for i in np.unique(lab[y:]):
        if i == 0:
            continue
        sl = boxes[i - 1]
        if (sl[0].stop - sl[0].start) <= max_h and areas[i] <= max_area:
            kill[i] = True
    return kill[lab]


def drop_scenery(a, x0, x1, band, margin=8):
    """Keep only the figure in one cell, dropping fence posts and log piles.

    Scenery can't be told apart by area (a log pile outweighs a character) and
    plain connectivity fails too, because the drawn ground welds everything in
    the cell into a single blob. So the baseline rows are cut first, which
    separates the props, the figure is picked out as the component reaching
    highest, and then only the baseline rows under the figure are restored so
    it keeps its feet.
    """
    ink = a[:, x0:x1] > 0.45
    if not ink.any():
        return
    # the props only touch the figure through 1-2px of anti-aliasing, so a
    # single-pixel erosion separates them while thin gear (bows, blades)
    # survives as a 1px core and is restored by the dilation further down
    cut = ndimage.binary_erosion(ink, disk(1))
    if band is not None:
        cut[band[0]:band[1], :] = False
    if not cut.any():
        return

    lab, n = ndimage.label(cut, structure=np.ones((3, 3), bool))
    if n == 0:
        return
    ys, xs = np.nonzero(cut)
    fid = lab[ys[np.argmin(ys)], xs[np.argmin(ys)]]   # component reaching highest
    boxes = ndimage.find_objects(lab)
    fy, fx = boxes[fid - 1]

    keep = lab == fid
    for i in range(1, n + 1):
        if i == fid:
            continue
        sy, sx = boxes[i - 1]
        if (sx.start >= fx.start - margin and sx.stop <= fx.stop + margin
                and sy.start >= fy.start - margin and sy.stop <= fy.stop + margin):
            keep |= lab == i

    if band is not None:
        # give the figure its feet back: regrow into the baseline rows, but
        # only within the figure's own columns so props stay severed
        lo, hi = max(fx.start - 2, 0), min(fx.stop + 2, ink.shape[1])
        region = np.zeros_like(ink)
        region[band[0]:band[1], lo:hi] = ink[band[0]:band[1], lo:hi]
        for _ in range(band[1] - band[0] + 2):
            grown = ndimage.binary_dilation(keep, disk(1)) & (region | keep)
            if (grown == keep).all():
                break
            keep = grown

    # undo the erosion: grow back onto the real silhouette, which restores
    # anti-aliased edges and thin gear without re-admitting the props
    keep = ndimage.binary_dilation(keep, disk(2)) & ink

    cell = a[:, x0:x1]
    a[:, x0:x1] = np.where(keep, cell, 0)


def drop_orphans(a, x0, x1, max_area=120, margin=6):
    """Small stray marks outside the figure: ground-line stubs, speed ticks.

    Size-capped so genuine detached props (the knight's dropped sword and
    helmet in the fall pose) are kept.
    """
    cell = a[:, x0:x1]
    lab, n = ndimage.label(cell > 0.45, structure=np.ones((3, 3), bool))
    if n == 0:
        return
    boxes = ndimage.find_objects(lab)
    areas = np.bincount(lab.ravel(), minlength=n + 1)
    main = int(np.argmax([sl[0].stop - sl[0].start for sl in boxes])) + 1
    my, mx = boxes[main - 1]
    kill = np.zeros(n + 1, bool)
    for i in range(1, n + 1):
        if i == main or areas[i] > max_area:
            continue
        sy, sx = boxes[i - 1]
        inside = (sx.start >= mx.start - margin and sx.stop <= mx.stop + margin
                  and sy.start >= my.start - margin and sy.stop <= my.stop + margin)
        if not inside:
            kill[i] = True
    a[:, x0:x1] = np.where(kill[lab], 0, cell)


def despeckle(a, min_area=20):
    lab, n = ndimage.label(a > 0.25, structure=np.ones((3, 3), bool))
    if n:
        areas = np.bincount(lab.ravel(), minlength=n + 1)
        a[np.isin(lab, np.flatnonzero(areas < min_area))] = 0.0


def unmix(img, paper, a):
    a3 = a[..., None]
    rgb = (img - (1.0 - a3) * paper) / np.maximum(a3, 0.06)
    return np.clip(np.where(a3 > 0.02, rgb, img), 0, 255)


def to_rgba(rgb, a):
    out = np.zeros(rgb.shape[:2] + (4,), np.uint8)
    out[..., :3] = np.rint(rgb).astype(np.uint8)
    out[..., 3] = np.rint(np.clip(a, 0, 1) * 255).astype(np.uint8)
    return out


def checker(rgba, size=8):
    h, w = rgba.shape[:2]
    y, x = np.mgrid[0:h, 0:w]
    bg = np.dstack([np.where(((x // size + y // size) % 2) == 0, 214, 170)
                    .astype(np.float32)] * 3)
    al = rgba[..., 3:4].astype(np.float32) / 255.0
    return np.rint(rgba[..., :3] * al + bg * (1 - al)).astype(np.uint8)


def trim(rgba, pad=0):
    ys, xs = np.nonzero(rgba[..., 3] > 2)
    if not ys.size:
        return rgba
    y0, y1 = max(ys.min() - pad, 0), min(ys.max() + 1 + pad, rgba.shape[0])
    x0, x1 = max(xs.min() - pad, 0), min(xs.max() + 1 + pad, rgba.shape[1])
    return rgba[y0:y1, x0:x1]


# ------------------------------------------------------------------ top level

def process(name, n_frames, scenery_cells=(), label=None):
    img = np.asarray(Image.open(os.path.join(SRC, name)).convert('RGB')).astype(np.float32)
    h, w, _ = img.shape

    paper = fit_paper(img)
    paper, n_shift = flatten_columns(img, paper)
    a, t0, d = soft_alpha(img, paper)

    edges = [round(i * w / n_frames) for i in range(n_frames + 1)]
    a[strip_ground(a > 0.45)] = 0.0
    band = find_band(a > 0.45)
    trim_ground(a, band, edges)
    a[strip_captions(a > 0.45)] = 0.0
    for i in scenery_cells:
        # small overhang: the log pile spills a few px past the cell boundary
        drop_scenery(a, edges[i], min(edges[i + 1] + 18, w), band)
    for i in range(n_frames):
        drop_orphans(a, edges[i], edges[i + 1])
    despeckle(a)
    # both run last: the structural passes above need the raw, un-filled outline
    a = add_light(a, d)
    a = solidify(a, d)

    painted = ''
    if label:
        a, n_keep, n_drop = apply_paint(a, label)
        if n_keep or n_drop:
            painted = f'  paint +{n_keep}/-{n_drop}'

    rgba = to_rgba(unmix(img, paper, a), a)
    print(f'{name:16s} t0={t0:4.1f} shifted_cols={n_shift:4d} '
          f'opaque={100 * (a > 0.9).mean():4.1f}%{painted}')
    return rgba, edges


if __name__ == '__main__':
    os.makedirs(OUT, exist_ok=True)
    os.makedirs(os.path.join(OUT, 'frames'), exist_ok=True)
    POSES = ['1_idle', '2_stride_a', '3_stride_b', '4_attack', '5_fall']
    jobs = [('image.png', 'archer', 5, (0,)),
            ('image (1).png', 'knight', 5, (0,)),
            ('image (2).png', 'duelist', 5, (0,)),
            ('image (3).png', 'bastion', 1, ())]

    for fname, label, nf, cells in jobs:
        rgba, edges = process(fname, nf, cells, label)
        Image.fromarray(rgba).save(f'{OUT}/{label}_sheet.png')
        Image.fromarray(checker(rgba)).save(f'{OUT}/_preview_{label}.png')
        cw = max(edges[i + 1] - edges[i] for i in range(nf))
        for i in range(nf):
            cell = rgba[:, edges[i]:edges[i + 1]]
            if cell.shape[1] < cw:      # keep every cell the same size
                pad = np.zeros((cell.shape[0], cw - cell.shape[1], 4), np.uint8)
                cell = np.concatenate([cell, pad], axis=1)
            name = f'{label}_{POSES[i]}' if nf == 5 else label
            Image.fromarray(cell).save(f'{OUT}/frames/{name}.png')
    print('written to', OUT)
