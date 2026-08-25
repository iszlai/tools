'use strict';

/* ============================== state ============================== */

const COLORS = ['#1e1e1e', '#e03131', '#1971c2', '#2f9e44', '#f08c00', '#9c36b5'];
const FONT = 'ui-sans-serif, system-ui, sans-serif';

const svg = document.getElementById('canvas');
const scene = document.getElementById('scene');
const overlay = document.getElementById('overlay');

let shapes = [];          // scene model — the only persisted drawing state
let uid = 1;
let selectedIds = [];     // multi-select
let editingId = null;     // shape currently open in a textarea
let editing = null;       // {s, ta, before, fresh, openedAt}
let tool = 'select';
let color = COLORS[0];
let zoom = 1, panX = 0, panY = 0;
let drag = null;          // active pointer gesture
let spaceDown = false;
let undoStack = [], redoStack = [];

/* ============================ persistence =========================== */

function persist() {
  localStorage.setItem('diagme', JSON.stringify({ shapes, uid, zoom, panX, panY }));
}

function loadState() {
  try {
    const d = JSON.parse(localStorage.getItem('diagme'));
    if (d && Array.isArray(d.shapes)) {
      shapes = d.shapes; uid = d.uid || 1;
      zoom = d.zoom || 1; panX = d.panX || 0; panY = d.panY || 0;
    }
  } catch (e) { /* corrupt saved state — start fresh */ }
}

/* =============================== undo =============================== */

function checkpoint() {
  undoStack.push(JSON.stringify(shapes));
  if (undoStack.length > 200) undoStack.shift();
  redoStack = [];
}

// drop the last checkpoint if the gesture turned out to be a no-op (plain click)
function dropNoopCheckpoint() {
  if (undoStack.length && undoStack[undoStack.length - 1] === JSON.stringify(shapes)) undoStack.pop();
}

function undo() {
  if (!undoStack.length) return;
  redoStack.push(JSON.stringify(shapes));
  shapes = JSON.parse(undoStack.pop());
  selectedIds = []; persist(); render();
}

function redo() {
  if (!redoStack.length) return;
  undoStack.push(JSON.stringify(shapes));
  shapes = JSON.parse(redoStack.pop());
  selectedIds = []; persist(); render();
}

/* ============================= helpers ============================== */

function esc(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function screenPt(e) {
  const r = svg.getBoundingClientRect();
  return [e.clientX - r.left, e.clientY - r.top];
}

function worldPt(e) {
  const [sx, sy] = screenPt(e);
  return [(sx - panX) / zoom, (sy - panY) / zoom];
}

function worldToScreen(x, y) {
  return [x * zoom + panX, y * zoom + panY];
}

function selectedShapes() {
  return selectedIds.map(id => shapes.find(s => s.id === id)).filter(Boolean);
}

function singleSelected() {
  return selectedIds.length === 1 ? shapes.find(s => s.id === selectedIds[0]) || null : null;
}

function shapeBounds(s) {
  switch (s.type) {
    case 'rect':
    case 'note':
      return { x: s.x, y: s.y, w: s.w, h: s.h };
    case 'text': {
      const lines = (s.text || '').split('\n');
      const w = Math.max(30, ...lines.map(l => l.length * s.size * 0.58));
      return { x: s.x, y: s.y, w, h: Math.max(1, lines.length) * s.size * 1.3 };
    }
    case 'draw': {
      const xs = s.points.map(p => p[0]), ys = s.points.map(p => p[1]);
      const x = Math.min(...xs), y = Math.min(...ys);
      return { x, y, w: Math.max(...xs) - x, h: Math.max(...ys) - y };
    }
    case 'line':
    case 'arrow': {
      const xs = [s.x1, s.x2], ys = [s.y1, s.y2];
      if (s.mx != null) { xs.push(s.mx); ys.push(s.my); }
      const x = Math.min(...xs), y = Math.min(...ys);
      return { x, y, w: Math.max(...xs) - x, h: Math.max(...ys) - y };
    }
  }
}

function rectsIntersect(a, b) {
  return a.x <= b.x + b.w && a.x + a.w >= b.x && a.y <= b.y + b.h && a.y + a.h >= b.y;
}

/* ========================= mini markdown ============================ */

function md(src) {
  const inline = t => esc(t)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\*([^*]+)\*/g, '<em>$1</em>');

  let out = '', list = null, inCode = false, codeBuf = [];
  const closeList = () => { if (list) { out += `</${list}>`; list = null; } };

  for (const line of src.split('\n')) {
    if (line.trim().startsWith('```')) {
      if (inCode) { out += '<pre>' + esc(codeBuf.join('\n')) + '</pre>'; codeBuf = []; inCode = false; }
      else { closeList(); inCode = true; }
      continue;
    }
    if (inCode) { codeBuf.push(line); continue; }

    const h = line.match(/^(#{1,3})\s+(.*)/);
    if (h) { closeList(); const n = h[1].length; out += `<h${n}>${inline(h[2])}</h${n}>`; continue; }

    const ul = line.match(/^\s*[-*]\s+(.*)/);
    if (ul) { if (list !== 'ul') { closeList(); out += '<ul>'; list = 'ul'; } out += `<li>${inline(ul[1])}</li>`; continue; }

    const ol = line.match(/^\s*\d+[.)]\s+(.*)/);
    if (ol) { if (list !== 'ol') { closeList(); out += '<ol>'; list = 'ol'; } out += `<li>${inline(ol[1])}</li>`; continue; }

    closeList();
    if (line.trim() === '') continue;
    out += `<p>${inline(line)}</p>`;
  }
  if (inCode) out += '<pre>' + esc(codeBuf.join('\n')) + '</pre>';
  closeList();
  return out;
}

/* ============================ rendering ============================= */

function lineGeom(s) {
  const hasBend = s.mx != null;
  const cx = hasBend ? 2 * s.mx - (s.x1 + s.x2) / 2 : null;
  const cy = hasBend ? 2 * s.my - (s.y1 + s.y2) / 2 : null;
  const d = hasBend
    ? `M ${s.x1} ${s.y1} Q ${cx} ${cy} ${s.x2} ${s.y2}`
    : `M ${s.x1} ${s.y1} L ${s.x2} ${s.y2}`;
  return { d, cx, cy, hasBend };
}

function arrowHead(x, y, fromX, fromY, stroke) {
  const a = Math.atan2(y - fromY, x - fromX);
  const L = 12, w = Math.PI / 7;
  const p1x = x - L * Math.cos(a - w), p1y = y - L * Math.sin(a - w);
  const p2x = x - L * Math.cos(a + w), p2y = y - L * Math.sin(a + w);
  return `<path d="M ${p1x} ${p1y} L ${x} ${y} L ${p2x} ${p2y}" fill="none" stroke="${stroke}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>`;
}

function drawPath(points) {
  if (points.length < 3) {
    const a = points[0], b = points[points.length - 1] || points[0];
    return `M ${a[0]} ${a[1]} L ${b[0]} ${b[1]}`;
  }
  let d = `M ${points[0][0]} ${points[0][1]}`;
  for (let i = 1; i < points.length - 1; i++) {
    const mx = (points[i][0] + points[i + 1][0]) / 2;
    const my = (points[i][1] + points[i + 1][1]) / 2;
    d += ` Q ${points[i][0]} ${points[i][1]} ${mx} ${my}`;
  }
  return d;
}

function renderShape(s) {
  switch (s.type) {
    case 'rect':
      return `<g data-id="${s.id}">
        <rect x="${s.x}" y="${s.y}" width="${s.w}" height="${s.h}" rx="8"
          fill="none" pointer-events="all" stroke="${s.color}" stroke-width="2"/></g>`;

    case 'line':
    case 'arrow': {
      const g = lineGeom(s);
      let head = '';
      if (s.type === 'arrow') {
        const fx = g.hasBend ? g.cx : s.x1, fy = g.hasBend ? g.cy : s.y1;
        head = arrowHead(s.x2, s.y2, fx, fy, s.color);
      }
      return `<g data-id="${s.id}">
        <path d="${g.d}" fill="none" stroke="transparent" stroke-width="14" pointer-events="stroke"/>
        <path d="${g.d}" fill="none" stroke="${s.color}" stroke-width="2" stroke-linecap="round"/>${head}</g>`;
    }

    case 'draw': {
      const d = drawPath(s.points);
      return `<g data-id="${s.id}">
        <path d="${d}" fill="none" stroke="transparent" stroke-width="14" pointer-events="stroke"/>
        <path d="${d}" fill="none" stroke="${s.color}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></g>`;
    }

    case 'text': {
      const b = shapeBounds(s);
      const hit = `<rect x="${b.x}" y="${b.y}" width="${b.w}" height="${b.h}" fill="none" pointer-events="all"/>`;
      if (s.id === editingId) return `<g data-id="${s.id}">${hit}</g>`;
      const lines = (s.text || '').split('\n');
      const spans = lines.map((ln, i) =>
        `<tspan x="${s.x}" dy="${i ? s.size * 1.3 : s.size}">${esc(ln) || '&#160;'}</tspan>`).join('');
      return `<g data-id="${s.id}">${hit}
        <text x="${s.x}" y="${s.y}" fill="${s.color}" font-size="${s.size}" font-family="${FONT}">${spans}</text></g>`;
    }

    case 'note': {
      const body = (s.text || '').trim()
        ? md(s.text)
        : '<p class="placeholder">double-click to write markdown&#8230;</p>';
      return `<g data-id="${s.id}">
        <rect x="${s.x}" y="${s.y}" width="${s.w}" height="${s.h}" rx="8"
          fill="#ffffff" stroke="${s.color}" stroke-width="2"/>
        <foreignObject x="${s.x + 10}" y="${s.y + 8}" width="${Math.max(1, s.w - 20)}" height="${Math.max(1, s.h - 16)}" pointer-events="none">
          <div xmlns="http://www.w3.org/1999/xhtml" class="note-body">${body}</div>
        </foreignObject></g>`;
    }
  }
  return '';
}

function renderSelection() {
  if (!selectedIds.length) return '';
  const sw = 1.5 / zoom;

  const single = singleSelected();
  if (single && (single.type === 'line' || single.type === 'arrow')) {
    const s = single;
    const r = 6 / zoom;
    const mx = s.mx != null ? s.mx : (s.x1 + s.x2) / 2;
    const my = s.my != null ? s.my : (s.y1 + s.y2) / 2;
    const dot = (x, y, h) =>
      `<circle data-handle="${h}" cx="${x}" cy="${y}" r="${r}" fill="#fff" stroke="#4c6ef5" stroke-width="${sw}" style="cursor:move"/>`;
    return dot(s.x1, s.y1, 'p1') + dot(mx, my, 'mid') + dot(s.x2, s.y2, 'p2');
  }

  const bs = selectedShapes().map(shapeBounds);
  if (!bs.length) return '';
  const p = 6 / zoom;
  const x = Math.min(...bs.map(b => b.x)) - p;
  const y = Math.min(...bs.map(b => b.y)) - p;
  const w = Math.max(...bs.map(b => b.x + b.w)) + p - x;
  const h = Math.max(...bs.map(b => b.y + b.h)) + p - y;
  const box = `<rect x="${x}" y="${y}" width="${w}" height="${h}" fill="none"
    stroke="#4c6ef5" stroke-width="${sw}" stroke-dasharray="${4 / zoom} ${3 / zoom}"/>`;

  if (!single) return box; // multi-select: move/delete/recolor, no resize handles

  const hs = 9 / zoom;
  const corners = [
    ['nw', x, y, 'nwse-resize'], ['ne', x + w, y, 'nesw-resize'],
    ['sw', x, y + h, 'nesw-resize'], ['se', x + w, y + h, 'nwse-resize'],
  ];
  const handles = corners.map(([hn, hx, hy, cur]) =>
    `<rect data-handle="${hn}" x="${hx - hs / 2}" y="${hy - hs / 2}" width="${hs}" height="${hs}"
      fill="#fff" stroke="#4c6ef5" stroke-width="${sw}" style="cursor:${cur}"/>`).join('');
  return box + handles;
}

function renderMarquee() {
  if (!drag || drag.mode !== 'marquee') return '';
  const x = Math.min(drag.ax, drag.bx), y = Math.min(drag.ay, drag.by);
  const w = Math.abs(drag.bx - drag.ax), h = Math.abs(drag.by - drag.ay);
  return `<rect x="${x}" y="${y}" width="${w}" height="${h}"
    fill="#4c6ef5" fill-opacity="0.07" stroke="#4c6ef5" stroke-width="${1 / zoom}"/>`;
}

function render() {
  scene.setAttribute('transform', `translate(${panX} ${panY}) scale(${zoom})`);
  svg.style.backgroundSize = `${24 * zoom}px ${24 * zoom}px`;
  svg.style.backgroundPosition = `${panX}px ${panY}px`;
  scene.innerHTML = shapes.map(renderShape).join('') + renderSelection() + renderMarquee();
  document.getElementById('zlabel').textContent = Math.round(zoom * 100) + '%';
}

/* ========================= geometry edits =========================== */

function translateShape(s, orig, dx, dy) {
  switch (s.type) {
    case 'rect': case 'note': case 'text':
      s.x = orig.x + dx; s.y = orig.y + dy; break;
    case 'line': case 'arrow':
      s.x1 = orig.x1 + dx; s.y1 = orig.y1 + dy;
      s.x2 = orig.x2 + dx; s.y2 = orig.y2 + dy;
      if (orig.mx != null) { s.mx = orig.mx + dx; s.my = orig.my + dy; }
      break;
    case 'draw':
      s.points = orig.points.map(([px, py]) => [px + dx, py + dy]); break;
  }
}

function cornerResize(s, orig, handle, wx, wy) {
  const b = shapeBounds(orig);
  const cx = handle.includes('w') ? b.x : b.x + b.w;   // dragged corner
  const cy = handle.includes('n') ? b.y : b.y + b.h;
  const fx = handle.includes('w') ? b.x + b.w : b.x;   // fixed (opposite) corner
  const fy = handle.includes('n') ? b.y + b.h : b.y;
  const clamp = v => Math.max(0.05, v);
  const sx = clamp((wx - fx) / ((cx - fx) || 1));
  const sy = clamp((wy - fy) / ((cy - fy) || 1));

  switch (s.type) {
    case 'rect': case 'note':
      s.x = fx + (b.x - fx) * sx; s.y = fy + (b.y - fy) * sy;
      s.w = Math.max(16, b.w * sx); s.h = Math.max(16, b.h * sy);
      break;
    case 'draw':
      s.points = orig.points.map(([px, py]) => [fx + (px - fx) * sx, fy + (py - fy) * sy]);
      break;
    case 'text':
      s.x = fx + (orig.x - fx) * sx; s.y = fy + (orig.y - fy) * sy;
      s.size = Math.max(8, orig.size * sy);
      break;
  }
}

/* ========================== text editing =========================== */

function startEdit(s, fresh = false) {
  if (editing) commitEdit();
  editingId = s.id;
  render();

  const ta = document.createElement('textarea');
  ta.value = s.text || '';

  if (s.type === 'note') {
    ta.className = 'edit-note';
    const [sx, sy] = worldToScreen(s.x, s.y);
    ta.style.left = sx + 'px';
    ta.style.top = sy + 'px';
    ta.style.width = s.w * zoom + 'px';
    ta.style.height = s.h * zoom + 'px';
    ta.style.padding = `${8 * zoom}px ${10 * zoom}px`;
    ta.style.fontSize = 13 * zoom + 'px';
    ta.placeholder = '# Markdown\n- lists\n**bold** `code`';
  } else {
    ta.className = 'edit-text';
    const [sx, sy] = worldToScreen(s.x, s.y);
    ta.style.left = sx - 2 + 'px';
    ta.style.top = sy - 2 + 'px';
    ta.style.fontSize = s.size * zoom + 'px';
    ta.style.color = s.color;
    const fit = () => {
      ta.style.width = '0'; ta.style.height = '0';
      ta.style.width = Math.max(120, ta.scrollWidth + 20) + 'px';
      ta.style.height = ta.scrollHeight + 'px';
    };
    ta.addEventListener('input', fit);
    requestAnimationFrame(fit);
  }

  overlay.appendChild(ta);
  editing = { s, ta, before: JSON.stringify(shapes), fresh, openedAt: performance.now() };
  ta.focus();

  // The browser's default mousedown handling can blur a textarea focused during the
  // same click sequence that opened it. Re-focus instead of committing in that window.
  ta.addEventListener('blur', () => {
    if (editing && editing.ta === ta && performance.now() - editing.openedAt < 300) {
      requestAnimationFrame(() => { if (editing && editing.ta === ta) ta.focus(); });
      return;
    }
    commitEdit();
  });
  ta.addEventListener('keydown', ev => {
    ev.stopPropagation();
    if (ev.key === 'Escape') { ev.preventDefault(); commitEdit(); }
  });
}

function commitEdit() {
  if (!editing) return;
  const { s, ta, before, fresh } = editing;
  editing = null; editingId = null;
  const txt = ta.value;
  ta.remove();

  if (txt !== s.text) {
    // fresh shapes already have a creation checkpoint covering this edit
    if (!fresh) { undoStack.push(before); redoStack = []; }
    s.text = txt;
  }
  if (s.type === 'text' && !txt.trim()) {
    shapes = shapes.filter(x => x !== s);
    selectedIds = selectedIds.filter(id => id !== s.id);
    dropNoopCheckpoint();
  }
  persist(); render();
}

/* ========================= pointer gestures ========================= */

svg.addEventListener('pointerdown', e => {
  if (editing) return; // blur handler commits; next click acts normally

  if (e.button === 1 || spaceDown) {
    e.preventDefault();
    drag = { mode: 'pan', sx: e.clientX, sy: e.clientY, px: panX, py: panY };
    svg.classList.add('panning');
    svg.setPointerCapture(e.pointerId);
    return;
  }
  if (e.button !== 0) return;

  const [wx, wy] = worldPt(e);

  if (tool === 'select') {
    const hEl = e.target.closest('[data-handle]');
    const single = singleSelected();
    if (hEl && single) {
      checkpoint();
      drag = { mode: 'handle', h: hEl.dataset.handle, s: single, orig: JSON.parse(JSON.stringify(single)) };
    } else {
      const el = e.target.closest('[data-id]');
      if (el) {
        const id = +el.dataset.id;
        if (e.shiftKey) {
          selectedIds = selectedIds.includes(id)
            ? selectedIds.filter(x => x !== id)
            : [...selectedIds, id];
        } else if (!selectedIds.includes(id)) {
          selectedIds = [id];
        }
        if (selectedIds.includes(id)) {
          checkpoint();
          const origs = {};
          for (const s of selectedShapes()) origs[s.id] = JSON.parse(JSON.stringify(s));
          drag = { mode: 'move', origs, wx, wy };
        }
      } else {
        // empty canvas: marquee selection (shift keeps the existing selection)
        drag = { mode: 'marquee', ax: wx, ay: wy, bx: wx, by: wy, keep: e.shiftKey ? [...selectedIds] : [] };
        if (!e.shiftKey) selectedIds = [];
      }
    }
  } else if (tool === 'rect' || tool === 'note') {
    checkpoint();
    const s = { id: uid++, type: tool, x: wx, y: wy, w: 0, h: 0, color };
    if (tool === 'note') s.text = '';
    shapes.push(s); selectedIds = [s.id];
    drag = { mode: 'create', ax: wx, ay: wy, s };
  } else if (tool === 'arrow' || tool === 'line') {
    checkpoint();
    const s = { id: uid++, type: tool, x1: wx, y1: wy, x2: wx, y2: wy, mx: null, my: null, color };
    shapes.push(s); selectedIds = [s.id];
    drag = { mode: 'createLine', s };
  } else if (tool === 'draw') {
    checkpoint();
    const s = { id: uid++, type: 'draw', points: [[wx, wy]], color };
    shapes.push(s);
    drag = { mode: 'draw', s };
  } else if (tool === 'text') {
    // create on pointerup — creating + focusing here loses focus to the browser's
    // default mousedown handling
    drag = { mode: 'placeText', wx, wy };
  }

  svg.setPointerCapture(e.pointerId);
  render();
});

svg.addEventListener('pointermove', e => {
  if (!drag) return;
  if (drag.mode === 'pan') {
    panX = drag.px + (e.clientX - drag.sx);
    panY = drag.py + (e.clientY - drag.sy);
    render();
    return;
  }
  const [wx, wy] = worldPt(e);
  const s = drag.s;

  switch (drag.mode) {
    case 'create':
      s.x = Math.min(drag.ax, wx); s.y = Math.min(drag.ay, wy);
      s.w = Math.abs(wx - drag.ax); s.h = Math.abs(wy - drag.ay);
      break;
    case 'createLine':
      s.x2 = wx; s.y2 = wy;
      break;
    case 'draw':
      s.points.push([wx, wy]);
      break;
    case 'move': {
      const dx = wx - drag.wx, dy = wy - drag.wy;
      for (const sel of selectedShapes()) {
        const orig = drag.origs[sel.id];
        if (orig) translateShape(sel, orig, dx, dy);
      }
      break;
    }
    case 'handle':
      if (drag.h === 'p1') { s.x1 = wx; s.y1 = wy; }
      else if (drag.h === 'p2') { s.x2 = wx; s.y2 = wy; }
      else if (drag.h === 'mid') { s.mx = wx; s.my = wy; }
      else cornerResize(s, drag.orig, drag.h, wx, wy);
      break;
    case 'marquee': {
      drag.bx = wx; drag.by = wy;
      const box = {
        x: Math.min(drag.ax, drag.bx), y: Math.min(drag.ay, drag.by),
        w: Math.abs(drag.bx - drag.ax), h: Math.abs(drag.by - drag.ay),
      };
      const hit = shapes.filter(sh => rectsIntersect(shapeBounds(sh), box)).map(sh => sh.id);
      selectedIds = [...new Set([...drag.keep, ...hit])];
      break;
    }
  }
  render();
});

svg.addEventListener('pointerup', () => {
  if (!drag) return;
  const d = drag;
  drag = null;
  svg.classList.remove('panning');
  if (d.mode === 'pan') { persist(); return; }
  if (d.mode === 'marquee') { render(); return; }

  if (d.mode === 'placeText') {
    checkpoint();
    const s = { id: uid++, type: 'text', x: d.wx, y: d.wy, text: '', size: 20, color };
    shapes.push(s); selectedIds = [s.id];
    setTool('select');
    startEdit(s, true);
    return;
  }

  if (d.mode === 'create') {
    const s = d.s;
    if (s.w < 8 && s.h < 8) { // plain click — default size
      s.w = s.type === 'note' ? 280 : 140;
      s.h = s.type === 'note' ? 170 : 90;
    }
    setTool('select');
    if (s.type === 'note') { render(); startEdit(s, true); }
  } else if (d.mode === 'createLine') {
    const s = d.s;
    if (Math.hypot(s.x2 - s.x1, s.y2 - s.y1) < 4) {
      shapes = shapes.filter(x => x !== s);
      selectedIds = [];
    } else {
      setTool('select');
    }
  } else if (d.mode === 'draw') {
    if (d.s.points.length < 2) shapes = shapes.filter(x => x !== d.s);
    // freehand stays active for multiple strokes
  }

  dropNoopCheckpoint();
  persist(); render();
});

svg.addEventListener('dblclick', e => {
  const el = e.target.closest('[data-id]');
  if (!el) return;
  const s = shapes.find(x => x.id === +el.dataset.id);
  if (s && (s.type === 'text' || s.type === 'note')) {
    selectedIds = [s.id];
    startEdit(s);
  }
});

/* =========================== zoom & pan ============================= */

function setZoom(nz, sx, sy) {
  nz = Math.min(5, Math.max(0.1, nz));
  panX = sx - (sx - panX) * (nz / zoom);
  panY = sy - (sy - panY) * (nz / zoom);
  zoom = nz;
  render(); persist();
}

svg.addEventListener('wheel', e => {
  e.preventDefault();
  if (e.ctrlKey || e.metaKey) {
    const [sx, sy] = screenPt(e);
    setZoom(zoom * Math.exp(-e.deltaY * 0.01), sx, sy);
  } else {
    panX -= e.deltaX; panY -= e.deltaY;
    render(); persist();
  }
}, { passive: false });

// Safari trackpad pinch
let gestureStartZoom = 1;
svg.addEventListener('gesturestart', e => { e.preventDefault(); gestureStartZoom = zoom; });
svg.addEventListener('gesturechange', e => {
  e.preventDefault();
  const r = svg.getBoundingClientRect();
  setZoom(gestureStartZoom * e.scale, e.clientX - r.left, e.clientY - r.top);
});
svg.addEventListener('gestureend', e => e.preventDefault());

document.getElementById('zin').onclick = () =>
  setZoom(zoom * 1.2, svg.clientWidth / 2, svg.clientHeight / 2);
document.getElementById('zout').onclick = () =>
  setZoom(zoom / 1.2, svg.clientWidth / 2, svg.clientHeight / 2);
document.getElementById('zlabel').onclick = () =>
  setZoom(1, svg.clientWidth / 2, svg.clientHeight / 2);
document.getElementById('zundo').onclick = undo;
document.getElementById('zredo').onclick = redo;

document.getElementById('clear').onclick = () => {
  if (!shapes.length) return;
  if (!confirm('Clear the whole canvas?')) return;
  checkpoint();
  shapes = []; selectedIds = [];
  persist(); render();
};

/* ============================ keyboard ============================== */

const KEY_TOOLS = {
  v: 'select', 1: 'select', r: 'rect', b: 'rect', 2: 'rect',
  a: 'arrow', 3: 'arrow', l: 'line', 4: 'line', p: 'draw', 5: 'draw',
  t: 'text', 6: 'text', d: 'note', 7: 'note',
};

window.addEventListener('keydown', e => {
  if (editing || e.target.tagName === 'TEXTAREA' || e.target.tagName === 'INPUT') return;

  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'z') {
    e.preventDefault();
    e.shiftKey ? redo() : undo();
    return;
  }
  if (e.metaKey || e.ctrlKey || e.altKey) return;

  if (e.key === ' ') { spaceDown = true; svg.classList.add('panning'); e.preventDefault(); return; }
  if (e.key === 'Delete' || e.key === 'Backspace') {
    if (selectedIds.length) {
      checkpoint();
      shapes = shapes.filter(s => !selectedIds.includes(s.id));
      selectedIds = [];
      persist(); render();
    }
    return;
  }
  if (e.key === 'Escape') { selectedIds = []; render(); return; }

  const t = KEY_TOOLS[e.key.toLowerCase()];
  if (t) setTool(t);
});

window.addEventListener('keyup', e => {
  if (e.key === ' ') { spaceDown = false; if (!drag) svg.classList.remove('panning'); }
});

/* ============================== toolbar ============================= */

function setTool(t) {
  tool = t;
  svg.dataset.tool = t;
  document.querySelectorAll('#toolbar [data-tool]').forEach(b =>
    b.classList.toggle('active', b.dataset.tool === t));
}

document.querySelectorAll('#toolbar [data-tool]').forEach(b =>
  b.addEventListener('click', () => setTool(b.dataset.tool)));

const paletteEl = document.getElementById('palette');
COLORS.forEach(c => {
  const b = document.createElement('button');
  b.className = 'swatch';
  b.style.background = c;
  b.title = c;
  b.addEventListener('click', () => {
    color = c;
    document.querySelectorAll('.swatch').forEach(x => x.classList.toggle('active', x === b));
    const sel = selectedShapes().filter(s => s.color !== c);
    if (sel.length) {
      checkpoint();
      sel.forEach(s => { s.color = c; });
      persist(); render();
    }
  });
  paletteEl.appendChild(b);
});

/* ================================ init ============================== */

loadState();
setTool('select');
document.querySelector('.swatch').classList.add('active');
render();

// test/debug hook — not used by the app itself
window.__diagme = {
  get shapes() { return shapes; },
  get selected() { return selectedIds; },
};
