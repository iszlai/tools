'use strict';
// FlowReview's UI shell: file queue, a virtualized side-by-side diff table,
// and a session summary panel. Vanilla JS, no bundler.
//
// The diff pane never puts all of a file's rows in the DOM -- for a 10k-line
// file that would be the whole performance budget gone before a single
// scroll. Instead #diff holds two children: #diff-spacer, an empty div whose
// height is rows.length * ROW_HEIGHT (so the browser gives #diff a correctly
// sized scrollbar), and #diff-rows, a CSS grid holding only the rows
// currently in or near the viewport, shifted into place with `transform:
// translateY(...)`. Every row's four cells (old line number, old code, new
// line number, new code) are grid items of #diff-rows itself -- the row div
// is `display: contents`, so it contributes no box of its own and every
// row's cells share one set of column tracks, which keeps columns aligned
// however small a slice of the file happens to be rendered.

const ROW_HEIGHT = 20; // px; must match .cell's height in styles.css
const OVERSCAN = 15; // extra rows rendered above/below the viewport

const state = {
  files: [],
  selected: null,
  rows: [], // render.Row from GET /api/file/{id}/rows, for the selected file
  hunkRows: [], // indices into `rows` where kind === "hunk", for n/p
};

async function getJSON(url, opts) {
  const res = await fetch(url, opts);
  let body = null;
  try {
    body = await res.json();
  } catch {
    // no body, or not JSON -- fall through to the status-based error below
  }
  if (!res.ok) {
    throw new Error((body && body.error) || res.statusText);
  }
  return body;
}

function statusLetter(status) {
  switch (status) {
    case 'added': return 'A';
    case 'modified': return 'M';
    case 'deleted': return 'D';
    case 'renamed': return 'R';
    case 'copied': return 'C';
    default: return '?';
  }
}

function escapeHTML(s) {
  const div = document.createElement('div');
  div.textContent = s;
  return div.innerHTML;
}

// escapeAttr is escapeHTML plus quotes, for values template strings below
// drop straight into a double-quoted HTML attribute.
function escapeAttr(s) {
  return String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;');
}

function renderFiles() {
  const el = document.getElementById('files');
  el.innerHTML = '';
  for (const f of state.files) {
    const row = document.createElement('div');
    row.className = 'file-row' + (f.id === state.selected ? ' selected' : '');
    row.dataset.file = f.id;
    row.tabIndex = 0;
    const fullyReviewed = f.hunks > 0 && f.reviewed >= f.hunks;
    row.innerHTML = `
      <span class="status">${statusLetter(f.status)}</span>
      <span class="path" title="${escapeHTML(f.path)}">${escapeHTML(f.path)}</span>
      <span class="stats">+${f.additions}/-${f.deletions}</span>
      ${fullyReviewed ? '<span class="reviewed-badge" title="fully reviewed"></span>' : ''}
    `;
    row.addEventListener('click', () => selectFile(f.id));
    el.appendChild(row);
  }
}

// sideBgClass picks the add/del tint for one side of one row: a "change"
// row tints its old side as deleted and its new side as added; an unpaired
// "add"/"del" row tints only the side it has content on, and marks the
// other side as empty (no line there) rather than neutral.
function sideBgClass(kind, side) {
  if (side === 'old') {
    if (kind === 'del' || kind === 'change') return 'bg-del';
    if (kind === 'add') return 'bg-empty';
    return '';
  }
  if (kind === 'add' || kind === 'change') return 'bg-add';
  if (kind === 'del') return 'bg-empty';
  return '';
}

// cellHTML builds one anchorable cell: a line-number cell or a code cell.
// Every cell -- including ones with nothing on that side -- carries all five
// data attributes FR-9 anchors comments against, even when a value is empty
// because the row has no line there.
function cellHTML(cls, fileId, side, lineNo, hunkID, position, inner) {
  return `<div class="${cls}" data-file="${escapeAttr(fileId)}" data-side="${side}" ` +
    `data-line="${lineNo || ''}" data-hunk="${escapeAttr(hunkID || '')}" data-position="${position || ''}">${inner}</div>`;
}

function rowCellsHTML(fileId, row) {
  if (row.kind === 'hunk') {
    return cellHTML('cell hunk-header', fileId, '', '', row.hunk_id, '', row.old_html || '');
  }
  const oldBg = sideBgClass(row.kind, 'old');
  const newBg = sideBgClass(row.kind, 'new');
  return (
    cellHTML(`cell lineno ${oldBg}`, fileId, 'old', row.old_no, row.hunk_id, row.old_position, row.old_no || '') +
    cellHTML(`cell code chroma ${oldBg}`, fileId, 'old', row.old_no, row.hunk_id, row.old_position, row.old_html || '') +
    cellHTML(`cell lineno ${newBg}`, fileId, 'new', row.new_no, row.hunk_id, row.new_position, row.new_no || '') +
    cellHTML(`cell code chroma ${newBg}`, fileId, 'new', row.new_no, row.hunk_id, row.new_position, row.new_html || '')
  );
}

// ensureDiffDOM builds #diff's virtualization scaffolding once; selectFile
// just updates the spacer height and re-renders the visible window on every
// later call.
function ensureDiffDOM() {
  const diff = document.getElementById('diff');
  if (!diff.dataset.virtualized) {
    diff.dataset.virtualized = '1';
    diff.innerHTML = '<div id="diff-spacer"></div><div id="diff-rows"></div>';
    diff.addEventListener('scroll', onDiffScroll, { passive: true });
  }
  return diff;
}

// onDiffScroll coalesces scroll events onto one requestAnimationFrame, so a
// fast scroll gesture never queues up more re-renders than frames to show
// them in -- the thing that keeps scrolling under the 50ms-long-task budget.
let scrollRAF = null;
function onDiffScroll() {
  if (scrollRAF !== null) return;
  scrollRAF = requestAnimationFrame(() => {
    scrollRAF = null;
    renderVisibleRows();
  });
}

function renderVisibleRows() {
  const diff = document.getElementById('diff');
  const rowsEl = document.getElementById('diff-rows');
  const rows = state.rows;
  if (!rows.length) {
    rowsEl.innerHTML = '';
    return;
  }
  const scrollTop = diff.scrollTop;
  const viewportHeight = diff.clientHeight;
  const first = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN);
  const last = Math.min(rows.length, Math.ceil((scrollTop + viewportHeight) / ROW_HEIGHT) + OVERSCAN);

  let html = '';
  for (let i = first; i < last; i++) {
    html += `<div class="diff-row">${rowCellsHTML(state.selected, rows[i])}</div>`;
  }
  rowsEl.innerHTML = html;
  rowsEl.style.transform = `translateY(${first * ROW_HEIGHT}px)`;
}

// jumpHunk moves to the next/previous hunk relative to whatever is
// currently scrolled to the top of the viewport -- not a separately tracked
// cursor -- so it does the right thing even after the user scrolls by hand.
function jumpHunk(delta) {
  const hunks = state.hunkRows;
  if (!hunks.length) return;
  const diff = document.getElementById('diff');
  const topRow = Math.floor(diff.scrollTop / ROW_HEIGHT);
  let cur = -1;
  for (let i = 0; i < hunks.length; i++) {
    if (hunks[i] <= topRow) cur = i;
  }
  const next = Math.min(Math.max(cur + delta, 0), hunks.length - 1);
  diff.scrollTop = hunks[next] * ROW_HEIGHT;
  renderVisibleRows();
}

async function selectFile(id) {
  state.selected = id;
  renderFiles();
  const rows = await getJSON(`/api/file/${encodeURIComponent(id)}/rows`);
  state.rows = rows;
  state.hunkRows = [];
  for (let i = 0; i < rows.length; i++) {
    if (rows[i].kind === 'hunk') state.hunkRows.push(i);
  }

  const diff = ensureDiffDOM();
  diff.scrollTop = 0;
  document.getElementById('diff-spacer').style.height = `${rows.length * ROW_HEIGHT}px`;
  renderVisibleRows();
}

async function refreshSession() {
  const sess = await getJSON('/api/session');
  const el = document.getElementById('session-summary');
  const p = sess.progress;
  const pct = p.total_hunks ? Math.round((p.reviewed_hunks / p.total_hunks) * 100) : 0;
  el.innerHTML = `
    <div class="title">${escapeHTML(sess.title)}</div>
    <div class="progress-bar"><div style="width:${pct}%"></div></div>
    <div>${p.reviewed_hunks}/${p.total_hunks} hunks reviewed</div>
    <div>${p.reviewed_files}/${p.total_files} files reviewed</div>
  `;
}

async function loadFiles() {
  state.files = await getJSON('/api/files');
  renderFiles();
  if (state.files.length > 0 && state.selected === null) {
    await selectFile(state.files[0].id);
  }
}

function moveSelection(delta) {
  if (state.files.length === 0) return;
  const idx = state.files.findIndex((f) => f.id === state.selected);
  const next = Math.min(Math.max((idx < 0 ? 0 : idx) + delta, 0), state.files.length - 1);
  selectFile(state.files[next].id);
}

document.addEventListener('keydown', (e) => {
  const target = e.target;
  if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA')) return;
  if (e.key === 'j') moveSelection(1);
  if (e.key === 'k') moveSelection(-1);
  if (e.key === 'n') jumpHunk(1);
  if (e.key === 'p') jumpHunk(-1);
});

(async function init() {
  await refreshSession();
  await loadFiles();
})();
