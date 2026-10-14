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
// translateY(...)`. Every row's cells are grid items of #diff-rows itself --
// the row div is `display: contents`, so it contributes no box of its own
// and every row's cells share one set of column tracks, which keeps columns
// aligned however small a slice of the file happens to be rendered.
//
// Side-by-side vs. unified (layout toggle, 'u'): state.sbsRows is always the
// canonical side-by-side rows the server rendered (what used to be the only
// `rows`); state.rows is whichever of those -- or, in unified mode,
// deriveUnifiedRows's reshaping of them -- is actually on screen right now,
// with state.hunkRows indexing into *that*. Everything that scrolls, jumps
// hunks, or expands/collapses (currentHunkIndex, jumpHunk, applyRowSplice,
// etc.) reads/writes state.rows/hunkRows and so works unchanged in either
// layout; only rowCellsHTML's two renderers (sbsRowCellsHTML,
// unifiedRowCellsHTML) and deriveUnifiedRows itself know the difference. See
// rebuildActiveRows, just below expandedHunks.

const ROW_HEIGHT = 20; // px; must match .cell's height in styles.css
const OVERSCAN = 15; // extra rows rendered above/below the viewport

const state = {
  files: [],
  selected: null,
  selectedPath: null, // FR-9: path of the selected file, for comment anchors and remote-comment matching
  sbsRows: [], // render.Row from GET /api/file/{id}/rows, for the selected file -- always side-by-side shape
  rows: [], // the rows actually rendered right now: sbsRows itself, or deriveUnifiedRows(sbsRows) in unified layout -- see rebuildActiveRows
  hunkRows: [], // indices into `rows` where kind === "hunk", for n/p

  // FR-7
  hunkStates: {}, // hunk_id -> model.HunkStatus, from the last GET/POST /api/session
  comments: [], // this session's own comment drafts, from the same response (FR-9 also reads this)

  // FR-9
  remoteComments: [], // GET /api/remote-comments, fetched once at init
  focusedAnchor: null, // the last code-cell click, target of the `c` shortcut
  composing: null, // {id: string|null, anchor: Anchor|null, body: string} -- the one open compose card, or null
  reanchorCommentId: null, // set while picking a new line for a stale comment

  // FR-11
  source: null, // SessionView.source, so the publish panel knows this is a PR session
  publish: {
    plan: null, // last PublishPreflight response (inline/general/excluded/head_sha), or null
    confirming: false, // true once Submit has been clicked, before the user confirms
  },

  // FR-17: flow order toggle. orderMode persists in localStorage; 'flow'
  // prefers the grouped order once GET /api/order resolves, falling back to
  // plain diff order (state.files' own order) while it is still pending --
  // 'diff' always shows plain diff order, even after flow order is ready.
  orderMode: localStorage.getItem('flowreview.orderMode') === 'diff' ? 'diff' : 'flow',
  order: null, // last successful GET /api/order?kind=flow response (order.Order), or null before it resolves
  displayOrder: [], // flattened file ids in the order renderFiles last drew -- what moveSelection (j/k) walks

  // FR-19/FR-21: callers + tests sidebar, for whichever changed function(s)
  // the current hunk (currentHunkIndex) overlaps -- see symbolsForCurrentHunk.
  radiusPending: true,
  radius: null, // GET /api/radius's report, once ready
  testsPending: true,
  testsReport: null, // GET /api/tests's report, once ready -- also the only
  // place a changed symbol's line range is exposed over the API (radius's
  // own report carries bare symbol ID strings only), so this doubles as the
  // join key source for the callers panel too.

  // FR-wrap: long-line wrap toggle ('w'), persisted in localStorage; default on.
  // Off by default: "on" lays every code column out at full width behind
  // one horizontal scrollbar (true soft wrap would break the fixed row
  // height virtualization relies on), which pushes the new side off screen.
  wrap: localStorage.getItem('flowreview.wrap') === '1',

  // Diff layout toggle ('u'): 'sbs' (default) is the original two-column
  // table; 'unified' derives single-column git-diff-style rows from it (see
  // deriveUnifiedRows). Persisted in localStorage like every other toggle here.
  layout: localStorage.getItem('flowreview.diffLayout') === 'unified' ? 'unified' : 'sbs',
};

// panes: collapsible/resizable file list and sidebar widths, and the diff's
// old/new column split -- all persisted in localStorage, all applied through
// applyPaneLayout/applyDiffSplit (see the "collapsible + resizable panes"
// section below). Kept out of `state` itself: nothing here is part of a
// file's or session's data, it is pure UI chrome that outlives file
// selection, session reload, everything.
const PANES_MIN_W = 160;
const PANES_MAX_W = 480;
const PANES_DEFAULT_FILES_W = 230; // matches the pre-JS grid-template-columns in styles.css
const PANES_DEFAULT_SIDE_W = 240;
const DIFF_FRAC_MIN = 0.15;
const DIFF_FRAC_MAX = 0.85;
const DIFF_FRAC_DEFAULT = 0.5;

function loadPx(key, fallback) {
  const n = Number(localStorage.getItem(key));
  return Number.isFinite(n) && n >= PANES_MIN_W && n <= PANES_MAX_W ? n : fallback;
}

function loadFrac(key, fallback) {
  const n = Number(localStorage.getItem(key));
  return Number.isFinite(n) && n >= DIFF_FRAC_MIN && n <= DIFF_FRAC_MAX ? n : fallback;
}

const panes = {
  filesWidth: loadPx('flowreview.filesWidth', PANES_DEFAULT_FILES_W),
  sideWidth: loadPx('flowreview.sideWidth', PANES_DEFAULT_SIDE_W),
  filesCollapsed: localStorage.getItem('flowreview.filesCollapsed') === '1',
  sideCollapsed: localStorage.getItem('flowreview.sideCollapsed') === '1',
  diffOldFrac: loadFrac('flowreview.diffOldFrac', DIFF_FRAC_DEFAULT),
};

// ======================== collapsible + resizable panes ========================
// #app's grid-template-columns is owned entirely from here once init() runs
// applyPaneLayout once: files-resizer/side-resizer are permanent 6px tracks
// (see styles.css) that keep each pane's collapse/expand button reachable
// even at 0 width. The diff's own old/new split is a CSS custom property
// (--diff-old-frac, a plain 0..1 number) that #diff-rows' grid-template and
// #diff-split-resizer's own position both read, so dragging never has to
// keep two things in sync by hand.

// makeDraggable wires one pointer-drag gesture on handleEl: on pointerdown
// (primary button, not on an embedded button -- see files-toggle/side-toggle,
// which must get a plain click instead), startDrag(e) runs once and either
// returns an onMove(e) callback (drag begins: pointer capture, a `dragging`
// class for styles.css's hover-brightened line, cleanup on pointerup) or
// null (nothing to drag right now, e.g. a collapsed pane -- see its callers).
function makeDraggable(handleEl, startDrag) {
  handleEl.addEventListener('pointerdown', (e) => {
    if (e.button !== 0 || e.target.closest('button')) return;
    const onMove = startDrag(e);
    if (!onMove) return;
    e.preventDefault();
    handleEl.classList.add('dragging');
    handleEl.setPointerCapture(e.pointerId);
    const onUp = () => {
      handleEl.classList.remove('dragging');
      handleEl.releasePointerCapture(e.pointerId);
      handleEl.removeEventListener('pointermove', onMove);
      handleEl.removeEventListener('pointerup', onUp);
    };
    handleEl.addEventListener('pointermove', onMove);
    handleEl.addEventListener('pointerup', onUp);
  });
}

// applyPaneLayout redraws everything panes.* controls: the grid itself, each
// pane's visibility, and its toggle button's glyph/tooltip (always both
// state and the key it mirrors, per this file's own convention).
function applyPaneLayout() {
  const filesW = panes.filesCollapsed ? '0px' : `${panes.filesWidth}px`;
  const sideW = panes.sideCollapsed ? '0px' : `${panes.sideWidth}px`;
  document.getElementById('app').style.gridTemplateColumns = `${filesW} 6px 1fr 6px ${sideW}`;
  document.getElementById('files').style.display = panes.filesCollapsed ? 'none' : '';
  document.getElementById('side').style.display = panes.sideCollapsed ? 'none' : '';

  const filesBtn = document.getElementById('files-toggle');
  filesBtn.textContent = panes.filesCollapsed ? '»' : '«';
  filesBtn.title = panes.filesCollapsed ? 'Expand file list ([)' : 'Collapse file list ([)';

  const sideBtn = document.getElementById('side-toggle');
  sideBtn.textContent = panes.sideCollapsed ? '«' : '»';
  sideBtn.title = panes.sideCollapsed ? 'Expand sidebar (])' : 'Collapse sidebar (])';
}

function toggleFilesCollapsed() {
  panes.filesCollapsed = !panes.filesCollapsed;
  localStorage.setItem('flowreview.filesCollapsed', panes.filesCollapsed ? '1' : '0');
  applyPaneLayout();
}

function toggleSideCollapsed() {
  panes.sideCollapsed = !panes.sideCollapsed;
  localStorage.setItem('flowreview.sideCollapsed', panes.sideCollapsed ? '1' : '0');
  applyPaneLayout();
}

function setupFilesResizer() {
  const handle = document.getElementById('files-resizer');
  makeDraggable(handle, () => {
    if (panes.filesCollapsed) return null; // the button expands it; nothing to drag yet
    const appLeft = document.getElementById('app').getBoundingClientRect().left;
    return (e) => {
      panes.filesWidth = Math.min(PANES_MAX_W, Math.max(PANES_MIN_W, e.clientX - appLeft));
      localStorage.setItem('flowreview.filesWidth', String(panes.filesWidth));
      applyPaneLayout();
    };
  });
  handle.addEventListener('dblclick', () => {
    panes.filesWidth = PANES_DEFAULT_FILES_W;
    localStorage.setItem('flowreview.filesWidth', String(panes.filesWidth));
    applyPaneLayout();
  });
}

function setupSideResizer() {
  const handle = document.getElementById('side-resizer');
  makeDraggable(handle, () => {
    if (panes.sideCollapsed) return null;
    const appRight = document.getElementById('app').getBoundingClientRect().right;
    return (e) => {
      panes.sideWidth = Math.min(PANES_MAX_W, Math.max(PANES_MIN_W, appRight - e.clientX));
      localStorage.setItem('flowreview.sideWidth', String(panes.sideWidth));
      applyPaneLayout();
    };
  });
  handle.addEventListener('dblclick', () => {
    panes.sideWidth = PANES_DEFAULT_SIDE_W;
    localStorage.setItem('flowreview.sideWidth', String(panes.sideWidth));
    applyPaneLayout();
  });
}

// applyDiffSplit pushes panes.diffOldFrac onto the document itself (not
// #diff, which ensureDiffDOM may not have built yet the first time this
// runs) so both #diff-rows' grid-template-columns and
// #diff-split-resizer's own position (styles.css) stay in lockstep without
// either ever recomputing the other.
function applyDiffSplit() {
  document.documentElement.style.setProperty('--diff-old-frac', String(panes.diffOldFrac));
}

// setupDiffSplitResizer wires dragging for the one old/new split handle,
// created once by ensureDiffDOM alongside #diff-spacer/#diff-rows (see
// below) -- #diff's content, unlike #files/#side, is otherwise rebuilt
// wholesale by renderVisibleRows, so this hookup happens where that handle
// is actually created, not in index.html.
function setupDiffSplitResizer(handle) {
  makeDraggable(handle, () => {
    const rowsEl = document.getElementById('diff-rows');
    const rect = rowsEl.getBoundingClientRect();
    // Read the lineno column's already-resolved px width straight off the
    // live grid rather than re-deriving it from --lineno-w's em value --
    // one fewer unit conversion to get wrong.
    const linenoPx = parseFloat(getComputedStyle(rowsEl).gridTemplateColumns.split(' ')[0]) || 0;
    const innerWidth = rect.width - 2 * linenoPx;
    if (innerWidth <= 0) return null;
    return (e) => {
      const oldPx = Math.min(innerWidth, Math.max(0, e.clientX - rect.left - linenoPx));
      panes.diffOldFrac = Math.min(DIFF_FRAC_MAX, Math.max(DIFF_FRAC_MIN, oldPx / innerWidth));
      localStorage.setItem('flowreview.diffOldFrac', String(panes.diffOldFrac));
      applyDiffSplit();
    };
  });
  handle.addEventListener('dblclick', () => {
    panes.diffOldFrac = DIFF_FRAC_DEFAULT;
    localStorage.setItem('flowreview.diffOldFrac', String(panes.diffOldFrac));
    applyDiffSplit();
  });
}

// expandedHunks tracks FR-8's "expand enclosing function": hunk_id -> the
// exact rows and scroll position to restore on collapse. See the dedicated
// section below selectFile.
const expandedHunks = new Map();

// selectToken guards against a slow fetch from an earlier selectFile call
// landing after a newer one: each call takes the next token and, after its
// fetch resolves, applies the response only if it is still the latest call
// in flight. Without this, fast j/k navigation can let an older file's rows
// (e.g. a large file that took longer to fetch) overwrite a file selected
// after it.
let selectToken = 0;

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

// postJSON/deleteJSON (FR-7/FR-9) are getJSON with the request method and
// JSON body filled in, for the handful of POST/DELETE endpoints the UI
// calls: /api/state, /api/comments, /api/comments/{id}, /api/reload.
async function postJSON(url, body) {
  return getJSON(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
}

async function deleteJSON(url) {
  return getJSON(url, { method: 'DELETE' });
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

// fileState (FR-7) derives the queue's unreviewed/in-progress/reviewed
// state from the counts FileView already carries -- no extra endpoint
// needed. A file with no hunks (e.g. a pure rename) has nothing to review,
// so it counts as reviewed rather than stuck unreviewed forever.
function fileState(f) {
  if (f.hunks === 0 || f.reviewed >= f.hunks) return 'reviewed';
  if (f.reviewed > 0) return 'in-progress';
  return 'unreviewed';
}

// ======================== FR-17: flow order ========================
// GET /api/order?kind=flow can take real time to compute on a large repo
// (see order.go's own doc comment); loadOrder starts it once at init and
// polls with backoff until it resolves, independent of which order the
// toggle currently shows -- so switching to "flow" after the fact needs no
// extra round trip, and the file list just shows plain diff order (see
// flowDisplayGroups) until it does.

// pollUntilReady calls fetchFn repeatedly until it resolves to a body that is
// not {pending: true}, backing off between tries (poll.go-style background
// analysis can take up to a couple of minutes on a large repo) so a slow
// first run never hammers the server. Returns null, and stops polling, on a
// hard failure -- the caller just keeps showing whatever it had before.
async function pollUntilReady(fetchFn, { initialDelay = 300, maxDelay = 5000 } = {}) {
  let delay = initialDelay;
  for (;;) {
    let body;
    try {
      body = await fetchFn();
    } catch {
      return null;
    }
    if (!body || !body.pending) return body;
    await new Promise((r) => setTimeout(r, delay));
    delay = Math.min(delay * 2, maxDelay);
  }
}

async function loadOrder() {
  const order = await pollUntilReady(() => getJSON('/api/order?kind=flow'));
  if (order) state.order = order;
  renderFiles();
}

// flowDisplayGroups turns state.order into exactly what renderFiles draws:
// every order.FileEntry resolved against state.files (Order itself carries
// no status/stats -- those come from FileView), skipping an entry whose file
// id state.files no longer has rather than crashing on it (a reload racing
// the order fetch). Returns null when flow order should not be shown: the
// toggle is set to "diff", or nothing has arrived from loadOrder yet.
function flowDisplayGroups() {
  if (state.orderMode !== 'flow' || !state.order) return null;
  const byId = new Map(state.files.map((f) => [f.id, f]));
  const resolve = (entries) => (entries || [])
    .map((e) => ({ why: e.why, file: byId.get(e.file_id) }))
    .filter((x) => !!x.file);
  return {
    groups: (state.order.groups || []).map((g) => ({ title: g.title, files: resolve(g.files) })),
    unclassified: resolve(state.order.unclassified),
  };
}

function renderOrderToggle() {
  const btn = document.getElementById('order-toggle');
  const flowReady = !!(state.order && state.order.groups && state.order.groups.length);
  if (state.orderMode === 'flow') {
    btn.textContent = flowReady ? 'Flow order ▾' : 'Flow order (computing…)';
    btn.title = 'Showing flow order -- click to show plain diff order';
  } else {
    btn.textContent = 'Diff order ▾';
    btn.title = 'Showing diff order -- click to show flow order';
  }
}

function toggleOrderMode() {
  state.orderMode = state.orderMode === 'flow' ? 'diff' : 'flow';
  localStorage.setItem('flowreview.orderMode', state.orderMode);
  renderFiles();
}

function fileRow(f, why) {
  const row = document.createElement('div');
  row.className = `file-row ${fileState(f)}` + (f.id === state.selected ? ' selected' : '');
  row.dataset.file = f.id;
  row.tabIndex = 0;
  if (why) row.title = why; // FR-17: the flow reason this file is in its group, as a plain tooltip
  const fullyReviewed = f.hunks > 0 && f.reviewed >= f.hunks;
  row.innerHTML = `
    <span class="status">${statusLetter(f.status)}</span>
    <span class="path" title="${escapeHTML(f.path)}">${escapeHTML(f.path)}</span>
    <span class="stats">+${f.additions}/-${f.deletions}</span>
    ${fullyReviewed ? '<span class="reviewed-badge" title="fully reviewed"></span>' : ''}
  `;
  row.addEventListener('click', () => selectFile(f.id));
  return row;
}

function groupTitle(text) {
  const el = document.createElement('div');
  el.className = 'file-group-title';
  el.textContent = text;
  return el;
}

// renderFiles draws #files in whichever order is currently displayed (flow
// groups, once loadOrder has something to show and the toggle has not been
// switched to "diff"; plain diff order otherwise) and records that order in
// state.displayOrder, which moveSelection (j/k) walks instead of state.files'
// own fixed order.
function renderFiles() {
  const el = document.getElementById('files');
  el.innerHTML = '';
  state.displayOrder = [];

  const append = (f, why) => {
    el.appendChild(fileRow(f, why));
    state.displayOrder.push(f.id);
  };

  const flow = flowDisplayGroups();
  if (flow) {
    for (const g of flow.groups) {
      if (!g.files.length) continue;
      el.appendChild(groupTitle(g.title));
      for (const { file, why } of g.files) append(file, why);
    }
    if (flow.unclassified.length) {
      if (flow.groups.some((g) => g.files.length)) el.appendChild(groupTitle('Other changes'));
      for (const { file, why } of flow.unclassified) append(file, why);
    }
  } else {
    for (const f of state.files) append(f);
  }

  renderOrderToggle();
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

// lineGutterInner (FR-9) is a line-number cell's content: the number itself,
// prefixed with small fixed-size marker dots when a comment is anchored at
// this exact (selected file, side, line) -- one for a reviewer's own draft,
// one for a non-outdated remote comment. Dots, not glyphs, so a marked line
// never grows past the fixed ROW_HEIGHT the virtualized grid assumes.
function lineGutterInner(lineNo, side) {
  if (!lineNo) return '';
  let markers = '';
  if (hasDraftAt(side, lineNo)) markers += '<span class="marker-dot draft" title="You have a comment on this line"></span>';
  const remote = remoteCommentAt(side, lineNo);
  if (remote) markers += `<span class="marker-dot remote" title="${escapeAttr(remote.author + ': ' + remote.body.slice(0, 120))}"></span>`;
  return markers + lineNo;
}

// rowCellsHTML builds one row's cells. isCurrent (FR-7) is whether this is
// the hunk header row `r`/n/p would act on right now; hunk review status
// (also FR-7) comes from state.hunkStates, looked up once per row here
// rather than baked into render.Row -- that cache is keyed on diff content
// only (see render.Cache) and review state changes far more often than the
// diff does.
// focusedClass (FR-9) outlines every cell of the side/line `c`'s shortcut
// would act on, recomputed on every render rather than toggled by a
// one-off DOM query -- renderVisibleRows replaces #diff-rows' innerHTML
// wholesale on each scroll frame, so anything not baked in here would
// disappear on the next one.
function focusedClass(side, lineNo) {
  const a = state.focusedAnchor;
  return a && lineNo && a.side === side && a.line === lineNo ? ' focused-line' : '';
}

// summaryStripHTML (FR-29) hands off to summary.js, a separate file loaded
// after this one: window.FlowReviewSummary is undefined only if that script
// failed to load, in which case a hunk header just renders without a card.
function summaryStripHTML(hunkId) {
  return window.FlowReviewSummary ? window.FlowReviewSummary.stripHTML(hunkId) : '';
}

// hunkHeaderHTML renders a hunk header row's single, full-width cell
// (`grid-column: 1 / -1` in styles.css, so it ignores column count --
// identical markup whether the grid underneath has side-by-side's four
// columns or unified's three). Shared by both layouts' row renderers below
// so the header, its controls and its FR-29 summary strip never drift
// between them.
function hunkHeaderHTML(fileId, row, isCurrent) {
  const reviewed = state.hunkStates[row.hunk_id] === 'reviewed';
  const dim = reviewed ? ' hunk-reviewed' : '';
  const cls = 'cell hunk-header' + dim + (isCurrent ? ' current' : '');
  const controls = reviewToggleButtonHTML(row.hunk_id, reviewed) + commentButtonHTML() + expandButtonHTML(row.hunk_id) + summaryStripHTML(row.hunk_id);
  return cellHTML(cls, fileId, '', '', row.hunk_id, '', (row.old_html || '') + controls);
}

// sbsRowCellsHTML is the original side-by-side row renderer: four cells
// (old lineno, old code, new lineno, new code) per row, matching
// render.Row's own shape one for one.
function sbsRowCellsHTML(fileId, row, isCurrent) {
  if (row.kind === 'hunk') return hunkHeaderHTML(fileId, row, isCurrent);
  const reviewed = state.hunkStates[row.hunk_id] === 'reviewed';
  const dim = reviewed ? ' hunk-reviewed' : '';
  const oldBg = sideBgClass(row.kind, 'old');
  const newBg = sideBgClass(row.kind, 'new');
  const oldFocus = focusedClass('old', row.old_no);
  const newFocus = focusedClass('new', row.new_no);
  return (
    cellHTML(`cell lineno ${oldBg}${dim}${oldFocus}`, fileId, 'old', row.old_no, row.hunk_id, row.old_position, lineGutterInner(row.old_no, 'old')) +
    cellHTML(`cell code chroma ${oldBg}${dim}${oldFocus}`, fileId, 'old', row.old_no, row.hunk_id, row.old_position, row.old_html || '') +
    cellHTML(`cell lineno ${newBg}${dim}${newFocus}`, fileId, 'new', row.new_no, row.hunk_id, row.new_position, lineGutterInner(row.new_no, 'new')) +
    cellHTML(`cell code chroma ${newBg}${dim}${newFocus}`, fileId, 'new', row.new_no, row.hunk_id, row.new_position, row.new_html || '')
  );
}

// unifiedLineBg mirrors sideBgClass for unified's two lineno gutters: a
// context row's line numbers stay neutral; an add/del row tints its own
// number bg-add/bg-del and the *other* gutter (which has no line to show)
// bg-empty, same "no line here" distinction sideBgClass draws.
function unifiedLineBg(kind, isOldGutter) {
  if (kind === 'add') return isOldGutter ? 'bg-empty' : 'bg-add';
  if (kind === 'del') return isOldGutter ? 'bg-del' : 'bg-empty';
  return '';
}

// unifiedRowCellsHTML renders one deriveUnifiedRows row: three cells (old
// lineno, new lineno, one code column) instead of sbsRowCellsHTML's four.
// The single code cell's own anchor (data-side/data-line/data-position, via
// cellHTML) is what a click or the `c` shortcut acts on: old for a deleted
// line, new for an added or context line, matching the side a published
// comment would need in each case. The two lineno gutters keep their own,
// independent anchors exactly as side-by-side always has -- clicking the old
// line number of a context row still opens a comment anchored old, clicking
// its new number still opens one anchored new.
function unifiedRowCellsHTML(fileId, row, isCurrent) {
  if (row.kind === 'hunk') return hunkHeaderHTML(fileId, row, isCurrent);
  const reviewed = state.hunkStates[row.hunk_id] === 'reviewed';
  const dim = reviewed ? ' hunk-reviewed' : '';
  const oldGutterBg = unifiedLineBg(row.kind, true);
  const newGutterBg = unifiedLineBg(row.kind, false);
  const oldFocus = focusedClass('old', row.old_no);
  const newFocus = focusedClass('new', row.new_no);

  const codeSide = row.kind === 'del' ? 'old' : 'new';
  const codeLine = row.kind === 'del' ? row.old_no : row.new_no;
  const codePos = row.kind === 'del' ? row.old_position : row.new_position;
  const codeHTML = row.kind === 'del' ? (row.old_html || '') : (row.new_html || '');
  const codeBg = row.kind === 'add' ? 'bg-add' : row.kind === 'del' ? 'bg-del' : '';
  const codeFocus = focusedClass(codeSide, codeLine);

  return (
    cellHTML(`cell lineno ${oldGutterBg}${dim}${oldFocus}`, fileId, 'old', row.old_no, row.hunk_id, row.old_position, lineGutterInner(row.old_no, 'old')) +
    cellHTML(`cell lineno ${newGutterBg}${dim}${newFocus}`, fileId, 'new', row.new_no, row.hunk_id, row.new_position, lineGutterInner(row.new_no, 'new')) +
    cellHTML(`cell code chroma ${codeBg}${dim}${codeFocus}`, fileId, codeSide, codeLine, row.hunk_id, codePos, codeHTML)
  );
}

function rowCellsHTML(fileId, row, isCurrent) {
  return state.layout === 'unified' ? unifiedRowCellsHTML(fileId, row, isCurrent) : sbsRowCellsHTML(fileId, row, isCurrent);
}

// deriveUnifiedRows reshapes the canonical side-by-side rows into unified,
// git-diff-style ones: a hunk header or context row passes through
// unchanged (a context row already carries both line numbers; its single
// code cell is built from new_html, see unifiedRowCellsHTML), an unpaired
// add/del row passes through unchanged too, and a "change" row (render.go's
// own del+add pairing for a same-position edit) splits back into its
// deletion followed by its addition -- removed above added, like `git diff`
// -- as two separate unified rows. Every row this returns still occupies
// exactly one ROW_HEIGHT slot, so virtualization's math never has to change.
function deriveUnifiedRows(sbsRows) {
  const out = [];
  for (const row of sbsRows) {
    if (row.kind === 'change') {
      out.push({ kind: 'del', hunk_id: row.hunk_id, old_no: row.old_no, old_html: row.old_html, old_position: row.old_position });
      out.push({ kind: 'add', hunk_id: row.hunk_id, new_no: row.new_no, new_html: row.new_html, new_position: row.new_position });
    } else {
      out.push(row);
    }
  }
  return out;
}

// rebuildActiveRows recomputes state.rows/hunkRows (and the spacer height)
// from state.sbsRows for whichever layout is current, then repaints. Called
// after every change to sbsRows (selectFile's fetch, applyRowSplice's
// expand/collapse) and after the layout toggle itself -- the one place both
// kinds of change funnel through, so they can never go out of sync.
function rebuildActiveRows() {
  state.rows = state.layout === 'unified' ? deriveUnifiedRows(state.sbsRows) : state.sbsRows;
  state.hunkRows = [];
  for (let i = 0; i < state.rows.length; i++) {
    if (state.rows[i].kind === 'hunk') state.hunkRows.push(i);
  }
  const spacer = document.getElementById('diff-spacer');
  if (spacer) spacer.style.height = `${state.rows.length * ROW_HEIGHT}px`;
  renderVisibleRows();
}

// ensureDiffDOM builds #diff's virtualization scaffolding once; selectFile
// just updates the spacer height and re-renders the visible window on every
// later call.
function ensureDiffDOM() {
  const diff = document.getElementById('diff');
  if (!diff.dataset.virtualized) {
    diff.dataset.virtualized = '1';
    // diff-split-resizer: the old/new column drag handle (hidden in unified
    // or wrap-lines mode via styles.css) -- built here, not in index.html,
    // since this innerHTML assignment would otherwise wipe it out.
    diff.innerHTML = '<div id="diff-spacer"></div><div id="diff-rows"></div>' +
      '<div id="diff-split-resizer" title="Drag to resize old/new columns (double-click to reset)"></div>';
    diff.addEventListener('scroll', onDiffScroll, { passive: true });
    // Delegated, not bound per-row: renderVisibleRows replaces #diff-rows's
    // content wholesale on every scroll, which would otherwise tear down a
    // per-button listener as soon as it scrolled out of view once.
    diff.addEventListener('click', onDiffClick);
    setupDiffSplitResizer(document.getElementById('diff-split-resizer'));
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

// currentHunkIndex (FR-7) is "the hunk header row nearest above the top of
// the viewport" PLAN.md defines as the current hunk: what `r` toggles and
// what gets the highlight outline. Unlike jumpHunk's own cursor below, this
// always resolves to a real hunk (0 when the viewport is scrolled above
// every hunk) since there is always a current hunk once a file has any.
function currentHunkIndex() {
  const hunks = state.hunkRows;
  if (!hunks.length) return -1;
  const diff = document.getElementById('diff');
  const topRow = Math.floor(diff.scrollTop / ROW_HEIGHT);
  let cur = 0;
  for (let i = 0; i < hunks.length; i++) {
    if (hunks[i] <= topRow) cur = i;
  }
  return cur;
}

function renderVisibleRows() {
  const diff = document.getElementById('diff');
  const rowsEl = document.getElementById('diff-rows');
  // #diff-rows only exists once ensureDiffDOM has run (selectFile's first
  // call); applySessionView (FR-7) calls this eagerly from init, before any
  // file is selected, so this can legitimately run too early.
  if (!rowsEl) return;
  const rows = state.rows;
  if (!rows.length) {
    rowsEl.innerHTML = '';
    if (window.FlowReviewSummary) window.FlowReviewSummary.onRowsRendered(); // FR-29: nothing visible now either
    updateSidebarPanelsIfNeeded(null);
    return;
  }
  const scrollTop = diff.scrollTop;
  const viewportHeight = diff.clientHeight;
  const first = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN);
  const last = Math.min(rows.length, Math.ceil((scrollTop + viewportHeight) / ROW_HEIGHT) + OVERSCAN);

  const curIdx = currentHunkIndex();
  const curRowIdx = curIdx >= 0 ? state.hunkRows[curIdx] : -1;

  let html = '';
  for (let i = first; i < last; i++) {
    html += `<div class="diff-row">${rowCellsHTML(state.selected, rows[i], i === curRowIdx)}</div>`;
  }
  rowsEl.innerHTML = html;
  rowsEl.style.transform = `translateY(${first * ROW_HEIGHT}px)`;
  if (window.FlowReviewSummary) window.FlowReviewSummary.onRowsRendered(); // FR-29: request summaries for what's now on screen
  updateSidebarPanelsIfNeeded(curRowIdx >= 0 ? rows[curRowIdx].hunk_id : null);
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

// --- FR-8: enclosing-function expansion -------------------------------
// Expanding a hunk fetches GET /api/file/{id}/expand, which returns the
// hunk's own rows (identical to what GET .../rows already served -- same
// HunkID, positions and HTML) with render.ContextRows context spliced in
// before and after, and replaces the hunk's row range in state.sbsRows with
// that response (rebuildActiveRows re-derives state.rows from it right
// after, whichever layout is current). Collapsing puts back the exact rows
// that were there before expanding, which is why hunkRowRange re-derives the
// range from hunk_id on every call rather than reusing a stored index: once
// other hunks above it have themselves been expanded or collapsed, a stored
// index would point at the wrong row.

// expandButtonHTML renders the control on a hunk header row's cell. Its
// label reflects expandedHunks so a collapsed hunk always reads "expand"
// and an expanded one "collapse", even though renderVisibleRows rebuilds
// this markup from scratch on every scroll.
function expandButtonHTML(hunkId) {
  const expanded = expandedHunks.has(hunkId);
  return ` <button type="button" class="expand-btn" data-hunk="${escapeAttr(hunkId)}" ` +
    `title="expand enclosing function (e)">${expanded ? '▾ collapse' : '▸ expand'}</button>`;
}

// onExpandClick handles the expand/collapse button on hunk headers; it is
// consulted first by onDiffClick, the pane's single delegated handler.
function onExpandClick(e) {
  const btn = e.target.closest('.expand-btn');
  if (!btn) return false;
  e.stopPropagation();
  toggleExpand(btn.dataset.hunk);
  return true;
}

// reviewToggleButtonHTML/commentButtonHTML render a hunk header's other two
// row-level controls -- every keyboard shortcut has a clickable equivalent,
// not just expand (see this file's header comment in index.html). The
// reviewed toggle doubles as the status indicator the old passive "✓
// reviewed" label used to be: active when reviewed, clickable to flip either
// way.
function reviewToggleButtonHTML(hunkId, reviewed) {
  const label = reviewed ? 'mark unreviewed' : 'mark reviewed';
  return ` <button type="button" class="hunk-btn reviewed-toggle${reviewed ? ' active' : ''}" ` +
    `data-hunk="${escapeAttr(hunkId)}" title="${label} (r)">✓</button>`;
}

function commentButtonHTML() {
  return ' <button type="button" class="hunk-btn comment-btn" title="comment on the focused line (c)">💬</button>';
}

// onHunkButtonClick handles the reviewed-toggle and comment buttons above;
// consulted alongside onExpandClick by onDiffClick.
function onHunkButtonClick(e) {
  const toggleBtn = e.target.closest('.reviewed-toggle');
  if (toggleBtn) {
    e.stopPropagation();
    const hunkId = toggleBtn.dataset.hunk;
    setHunkReviewed(hunkId, state.hunkStates[hunkId] !== 'reviewed');
    return true;
  }
  const commentBtn = e.target.closest('.comment-btn');
  if (commentBtn) {
    e.stopPropagation();
    openComposeForFocused();
    return true;
  }
  return false;
}

// hunkRowRange finds the contiguous run of state.sbsRows carrying hunk_id
// === hunkId -- the half-open range applyRowSplice replaces to expand or
// collapse it. Always against the canonical side-by-side rows, never
// state.rows: in unified layout state.rows no longer has a stable 1:1
// correspondence with hunk_id runs (a "change" row there became two rows,
// del then add -- see deriveUnifiedRows), but the server's expand/collapse
// data is still side-by-side shaped, so splicing has to happen against the
// array that shape actually matches; rebuildActiveRows re-derives state.rows
// afterward either way. This works unchanged in both expansion states:
// collapsed, it is just the hunk's header + body rows; expanded, the context
// rows ContextRows spliced in before and after carry the same hunk_id (see
// expand.go's handleExpandHunk), so the run also covers them -- in
// particular, the prepended "before" rows, which come *ahead of* the header
// row itself, so scanning forward from the header alone would miss them.
// Returns null when hunkId is not currently in state.sbsRows at all.
function hunkRowRange(hunkId) {
  const start = state.sbsRows.findIndex((r) => r.hunk_id === hunkId);
  if (start === -1) return null;
  let end = start + 1;
  while (end < state.sbsRows.length && state.sbsRows[end].hunk_id === hunkId) end++;
  return [start, end];
}

// applyRowSplice replaces state.sbsRows[start:end] with newRows (always
// side-by-side shaped, straight off the server), then hands off to
// rebuildActiveRows to re-derive state.rows/hunkRows for whichever layout is
// current, resize the spacer, and repaint.
function applyRowSplice(start, end, newRows) {
  state.sbsRows.splice(start, end - start, ...newRows);
  rebuildActiveRows();
}

async function expandHunk(hunkId) {
  if (!hunkId || expandedHunks.has(hunkId)) return;
  const range = hunkRowRange(hunkId);
  if (!range) return;
  const [start, end] = range;
  const diff = document.getElementById('diff');
  const scrollTop = diff.scrollTop;
  const originalRows = state.sbsRows.slice(start, end);

  let expandedRows;
  try {
    expandedRows = await getJSON(`/api/file/${encodeURIComponent(state.selected)}/expand?hunk=${encodeURIComponent(hunkId)}`);
  } catch {
    return; // no enclosing function found, or the request failed -- stay collapsed
  }
  if (expandedHunks.has(hunkId)) return; // a second 'e'/click landed while the fetch was in flight

  expandedHunks.set(hunkId, { originalRows, scrollTop });
  applyRowSplice(start, end, expandedRows);
}

function collapseHunk(hunkId) {
  const saved = expandedHunks.get(hunkId);
  if (!saved) return;
  const range = hunkRowRange(hunkId);
  if (!range) return;
  expandedHunks.delete(hunkId);
  applyRowSplice(range[0], range[1], saved.originalRows);
  document.getElementById('diff').scrollTop = saved.scrollTop;
}

function toggleExpand(hunkId) {
  if (!hunkId) return;
  if (expandedHunks.has(hunkId)) collapseHunk(hunkId);
  else expandHunk(hunkId);
}

// currentHunkId is jumpHunk's "which hunk is at/above the viewport's top"
// logic, reused for the 'e' shortcut's "current hunk" -- kept separate
// rather than factored out of jumpHunk, which FR-6 already owns.
function currentHunkId() {
  const hunks = state.hunkRows;
  if (!hunks.length) return null;
  const diff = document.getElementById('diff');
  const topRow = Math.floor(diff.scrollTop / ROW_HEIGHT);
  let cur = 0;
  for (let i = 0; i < hunks.length; i++) {
    if (hunks[i] <= topRow) cur = i;
  }
  return state.rows[hunks[cur]].hunk_id;
}

async function selectFile(id) {
  state.selected = id;
  const f = state.files.find((x) => x.id === id);
  state.selectedPath = f ? f.path : null; // FR-9: comment anchors key off the path, not the file id
  state.focusedAnchor = null; // FR-9: a focused line belongs to the file it was focused in
  renderFiles();
  const token = ++selectToken;
  const rows = await getJSON(`/api/file/${encodeURIComponent(id)}/rows`);
  if (token !== selectToken) return; // a newer selectFile call has since started

  state.sbsRows = rows;
  expandedHunks.clear(); // a freshly fetched file has nothing expanded yet

  const diff = ensureDiffDOM();
  diff.scrollTop = 0;
  rebuildActiveRows();
}

// ======================== FR-7: review queue, reviewed state, progress ========================

// applySessionView is the one place a SessionView response (from GET
// /api/session or any POST that returns one) gets applied to `state` and
// redrawn -- the header progress bar, the right-pane summary, the comment
// list, and the hunk-state overlay on whichever rows are currently on
// screen.
function applySessionView(sess) {
  state.hunkStates = sess.hunk_states || {};
  state.comments = sess.comments || [];
  state.source = sess.source;
  renderSessionSummary(sess);
  renderComments();
  renderPublishPanel();
  renderVisibleRows();
}

async function refreshSession() {
  const sess = await getJSON('/api/session');
  applySessionView(sess);
  return sess;
}

function describeSource(source) {
  switch (source.kind) {
    case 'worktree': return 'Worktree';
    case 'range': return `Range ${source.ref || ''}`.trim();
    case 'commit': return `Commit ${source.ref || ''}`.trim();
    case 'patch': return 'Patch';
    case 'pr': return source.pr ? `PR #${source.pr.number} ${source.pr.owner}/${source.pr.repo}` : 'Pull request';
    default: return source.kind || '';
  }
}

// renderSessionSummary fills in both progress displays: the thin header bar
// (title + bar + fraction) and the right-pane panel, which adds per-type
// counts, the comment count and the source/head -- everything PLAN.md's
// "session summary panel" asks for.
function renderSessionSummary(sess) {
  const p = sess.progress;
  const pct = p.total_hunks ? Math.round((p.reviewed_hunks / p.total_hunks) * 100) : 0;

  document.getElementById('topbar-title').textContent = sess.title;
  document.querySelector('#topbar-progress > div').style.width = `${pct}%`;
  document.getElementById('topbar-count').textContent = `${p.reviewed_hunks}/${p.total_hunks} hunks`;

  const head = sess.source.head_sha ? sess.source.head_sha.slice(0, 8) : '';
  const el = document.getElementById('session-summary');
  el.innerHTML = `
    <div class="title">${escapeHTML(sess.title)}</div>
    <div class="progress-bar"><div style="width:${pct}%"></div></div>
    <div>${p.reviewed_files}/${p.total_files} files reviewed</div>
    <div>${p.reviewed_hunks}/${p.total_hunks} hunks reviewed</div>
    <div>${state.comments.length} comment${state.comments.length === 1 ? '' : 's'}</div>
    <div>${escapeHTML(describeSource(sess.source))}${head ? ' @ ' + escapeHTML(head) : ''}</div>
  `;
}

// toggleCurrentHunk is `r`: flip the reviewed state of the current hunk (see
// currentHunkIndex's doc comment for what "current" means).
// setHunkReviewed is the POST /api/state round trip both toggleCurrentHunk
// (the `r` key, which resolves "current" first) and the per-row
// reviewed-toggle button (which already has hunkId from its own row) end up
// calling.
async function setHunkReviewed(hunkId, reviewed) {
  const sess = await postJSON('/api/state', { hunk_id: hunkId, reviewed });
  applySessionView(sess);
  await loadFiles();
  renderVisibleRows();
  await maybeCelebrateCompletion();
}

async function toggleCurrentHunk() {
  const idx = currentHunkIndex();
  if (idx < 0) return;
  const hunkId = state.rows[state.hunkRows[idx]].hunk_id;
  await setHunkReviewed(hunkId, state.hunkStates[hunkId] !== 'reviewed');
}

// toggleCurrentFile is `R`: if any hunk of the selected file is still
// unreviewed, mark every hunk reviewed; if the file is already fully
// reviewed, clear all of them. One /api/state call per hunk -- there is no
// batch endpoint, and a file's hunk count is small enough that this is not
// worth adding one for.
async function toggleCurrentFile() {
  const f = state.files.find((x) => x.id === state.selected);
  if (!f || f.hunks === 0) return;
  const hunkIds = [...new Set(state.rows.filter((r) => r.hunk_id).map((r) => r.hunk_id))];
  const makeReviewed = f.reviewed < f.hunks;
  let sess = null;
  for (const id of hunkIds) {
    sess = await postJSON('/api/state', { hunk_id: id, reviewed: makeReviewed });
  }
  if (sess) applySessionView(sess);
  await loadFiles();
  renderVisibleRows();
  await maybeCelebrateCompletion();
}

// maybeCelebrateCompletion flashes the file row the moment it becomes fully
// reviewed and, after a short delay, auto-advances to the next unreviewed
// file. lastCompletionFile makes this edge-triggered: re-toggling an
// already-complete file's last hunk back and forth doesn't replay the
// flash/auto-advance every time, only on the transition into "complete".
let lastCompletionFile = null;
async function maybeCelebrateCompletion() {
  const f = state.files.find((x) => x.id === state.selected);
  if (!f) return;
  const complete = f.hunks > 0 && f.reviewed >= f.hunks;
  if (!complete) {
    if (lastCompletionFile === f.id) lastCompletionFile = null;
    return;
  }
  if (lastCompletionFile === f.id) return;
  lastCompletionFile = f.id;

  const row = document.querySelector(`.file-row[data-file="${CSS.escape(f.id)}"]`);
  if (row) {
    row.classList.add('just-completed');
    setTimeout(() => row.classList.remove('just-completed'), 900);
  }

  const next = state.files.find((x) => x.id !== f.id && fileState(x) !== 'reviewed');
  if (next) {
    setTimeout(() => selectFile(next.id), 700); // let the flash read before jumping away
  }
}

// The key list is intentionally one array, not two: `?` should show every
// shortcut flowreview has, FR-7's and FR-9's alike.
const HELP_KEYS = [
  ['j / k', 'previous / next file'],
  ['n / p', 'previous / next hunk'],
  ['r', 'toggle reviewed on the current hunk'],
  ['R', 'toggle reviewed on the whole file'],
  ['c', 'comment on the focused line'],
  ['s', 'toggle hunk summaries on/off'],
  ['w', 'toggle line wrap'],
  ['u', 'toggle unified / side-by-side diff layout'],
  ['[', 'collapse/expand the file list'],
  [']', 'collapse/expand the sidebar'],
  ['?', 'toggle this help'],
];

function renderHelp() {
  const el = document.getElementById('help-overlay');
  const rows = HELP_KEYS.map(([k, d]) => `<dt>${escapeHTML(k)}</dt><dd>${escapeHTML(d)}</dd>`).join('');
  el.innerHTML = `<div class="help-card"><h3>Keyboard shortcuts</h3><dl>${rows}</dl></div>`;
}

function toggleHelp() {
  const el = document.getElementById('help-overlay');
  if (el.hidden) {
    renderHelp();
    el.hidden = false;
  } else {
    el.hidden = true;
  }
}

async function loadFiles() {
  state.files = await getJSON('/api/files');
  renderFiles();
  if (state.files.length > 0 && state.selected === null) {
    await selectFile(state.files[0].id);
  }
}

// moveSelection is j/k: step through state.displayOrder, the order
// renderFiles actually drew (flow groups or plain diff order, whichever is
// currently shown -- see flowDisplayGroups), not state.files' own fixed one.
function moveSelection(delta) {
  if (state.displayOrder.length === 0) return;
  const idx = state.displayOrder.indexOf(state.selected);
  const next = Math.min(Math.max((idx < 0 ? 0 : idx) + delta, 0), state.displayOrder.length - 1);
  selectFile(state.displayOrder[next]);
}

// ======================== FR-9: comment drafts ========================
// Comments render in the side panel, not as inline variable-height diff
// rows: #diff-rows assumes every row is exactly ROW_HEIGHT (see the file's
// header comment and renderVisibleRows), and the virtualization math --
// spacer height, scrollTop-to-row-index, overscan -- is built entirely on
// that assumption. Threading a reflow-on-demand row through that would
// either break the 0-long-task scrolling budget FR-6 measured, or need a
// second, much more complex virtualized layout pass. The side panel keeps
// drafts editable and exactly anchored without touching that code at all.
// The diff itself still gets exact, non-resizing feedback for both a
// reviewer's own draft and a host's remote comment: a small marker dot in
// the line-number gutter (lineGutterInner, above) and a highlighted outline
// on the focused line (focusedClass, above).

async function loadRemoteComments() {
  state.remoteComments = await getJSON('/api/remote-comments');
  renderComments();
  renderVisibleRows();
}

function hasDraftAt(side, line) {
  return state.comments.some((c) => c.anchor && c.anchor.path === state.selectedPath && c.anchor.side === side && c.anchor.line === line);
}

// remoteCommentAt only matches non-outdated remote comments: an outdated one
// means the line it was posted on has since moved, so there is no line left
// to anchor it to inline -- those render exclusively in the side panel (see
// renderComments).
function remoteCommentAt(side, line) {
  return state.remoteComments.find((c) => !c.outdated && c.path === state.selectedPath && c.side === side && c.line === line);
}

function anchorFromDataset(ds) {
  if (!ds.line || !ds.side) return null;
  return {
    path: state.selectedPath,
    side: ds.side,
    line: Number(ds.line),
    hunk_id: ds.hunk || '',
    position: ds.position ? Number(ds.position) : 0,
  };
}

function sameAnchor(a, b) {
  return !!a && !!b && a.path === b.path && a.side === b.side && a.line === b.line;
}

function existingDraftAt(anchor) {
  return state.comments.find((c) => sameAnchor(c.anchor, anchor));
}

// onDiffClick is the diff pane's one click handler (delegated, bound once in
// ensureDiffDOM): while re-anchoring a stale comment any line click re-homes
// it; otherwise clicking a line-number gutter cell opens (or re-opens, if one
// is already there) a compose card for that exact anchor, and clicking a
// code cell just focuses the line for the `c` shortcut.
function onDiffClick(e) {
  if (onExpandClick(e)) return;
  if (onHunkButtonClick(e)) return;
  const cell = e.target.closest('.cell');
  if (!cell || !cell.dataset.line) return;
  const anchor = anchorFromDataset(cell.dataset);
  if (!anchor) return;

  if (state.reanchorCommentId) {
    reanchorComment(state.reanchorCommentId, anchor);
    return;
  }

  state.focusedAnchor = anchor;
  renderVisibleRows();
  if (cell.classList.contains('lineno')) {
    openCompose(existingDraftAt(anchor) || { anchor });
  }
}

// openComposeForFocused is `c`: comment on the line focusedAnchor last
// recorded. A no-op if nothing is focused yet -- there is no line cursor to
// fall back to, only click-to-focus.
function openComposeForFocused() {
  if (!state.focusedAnchor) return;
  openCompose(existingDraftAt(state.focusedAnchor) || { anchor: state.focusedAnchor });
}

function startGeneralComment() {
  openCompose({ anchor: null });
}

// openCompose shows one editable card -- new (no id) or an existing comment
// -- in the side panel and focuses its textarea. Only one compose card is
// open at a time.
function openCompose(c) {
  state.composing = { id: c.id || null, anchor: c.anchor || null, body: c.body || '' };
  renderComments();
  const el = document.getElementById(c.id ? `comment-${c.id}` : 'comment-new');
  if (el) {
    el.scrollIntoView({ block: 'nearest' });
    const ta = el.querySelector('textarea');
    if (ta) ta.focus();
  }
}

function cancelCompose() {
  state.composing = null;
  renderComments();
}

async function saveCompose(id, anchor, body) {
  if (!body.trim()) return;
  await postJSON('/api/comments', { id: id || undefined, anchor, body });
  state.composing = null;
  await refreshSession();
}

async function removeComment(id) {
  await deleteJSON(`/api/comments/${encodeURIComponent(id)}`);
  if (state.composing && state.composing.id === id) state.composing = null;
  await refreshSession();
}

// beginReanchor switches to the comment's own file (if it isn't already
// open) so the next line the user clicks there re-homes it -- "choose a new
// line in the same file" per PLAN.md.
function beginReanchor(id) {
  const c = state.comments.find((x) => x.id === id);
  if (!c || !c.anchor) return;
  state.reanchorCommentId = id;
  const f = state.files.find((x) => x.path === c.anchor.path);
  if (f && f.id !== state.selected) {
    selectFile(f.id).then(renderComments);
  } else {
    renderComments();
  }
}

// reanchorComment re-upserts the comment with its body unchanged and a fresh
// anchor; the server always sets status back to "draft" on any upsert (see
// handleUpsertComment), which is exactly right here -- the whole point of
// re-anchoring is to clear "stale".
async function reanchorComment(id, anchor) {
  const c = state.comments.find((x) => x.id === id);
  state.reanchorCommentId = null;
  if (!c) return;
  await postJSON('/api/comments', { id, anchor, body: c.body });
  await refreshSession();
}

function cancelReanchor() {
  state.reanchorCommentId = null;
  renderComments();
}

function commentMeta(c) {
  return c.anchor ? `${escapeHTML(c.anchor.path)}:${c.anchor.line} (${escapeHTML(c.anchor.side)})` : 'General';
}

function composeFormHTML(id, body) {
  const rendered = body.trim() ? md(body) : '<em>preview</em>';
  return `
    <textarea data-draft-body>${escapeHTML(body)}</textarea>
    <div class="preview">${rendered}</div>
    <div class="actions">
      <button type="button" data-action="save" data-id="${id ? escapeAttr(id) : ''}">Save</button>
      <button type="button" data-action="cancel">Cancel</button>
    </div>`;
}

// FR-11: publishStatusHTML renders the small badge a draft carries once a
// publish attempt has recorded an outcome on it -- a link to the comment on
// the host when it published, or the error plus a one-click Retry (which
// resubmits just this draft, see retryDraft) when it failed. Both read
// straight off the model.CommentDraft fields handleUpsertComment never
// touches and Publish's applyPublishResults is the only thing that sets.
function publishStatusHTML(c) {
  if (c.status === 'published') {
    return `<div class="publish-badge published">Published ✓${c.remote_url ? ` <a href="${escapeAttr(c.remote_url)}" target="_blank" rel="noopener">view</a>` : ''}</div>`;
  }
  if (c.status === 'failed') {
    return `<div class="publish-badge failed">Failed to publish: ${escapeHTML(c.error || '')} <button type="button" data-action="retry" data-id="${escapeAttr(c.id)}">Retry</button></div>`;
  }
  return '';
}

function commentCardHTML(c) {
  const stale = c.status === 'stale';
  const published = c.status === 'published';
  const reanchoring = state.reanchorCommentId === c.id;
  const composingHere = !!(state.composing && state.composing.id === c.id);
  // Published drafts render read-only -- re-opening a compose card on
  // something already sent to the host would invite an edit nothing resends.
  const body = composingHere && !published ? composeFormHTML(c.id, state.composing.body) : `<div class="body-view">${md(c.body)}</div>`;
  return `
    <div class="comment-card${stale ? ' stale' : ''}${published ? ' published' : ''}${reanchoring ? ' reanchoring' : ''}" id="comment-${escapeAttr(c.id)}">
      <div class="meta"><span>${commentMeta(c)}</span>${c.publishable === false ? '<span title="not on a diff line; cannot be published">local only</span>' : ''}</div>
      ${stale ? `<div class="stale-warning">This line changed since you drafted this comment.${reanchoring ? ' Click a line in the diff to re-anchor.' : ''}</div>` : ''}
      ${publishStatusHTML(c)}
      ${body}
      ${published ? '' : `
      <div class="actions">
        ${composingHere ? '' : `<button type="button" data-action="edit" data-id="${escapeAttr(c.id)}">Edit</button>`}
        ${stale && c.anchor && !reanchoring ? `<button type="button" data-action="reanchor" data-id="${escapeAttr(c.id)}">Re-anchor</button>` : ''}
        ${reanchoring ? '<button type="button" data-action="cancel-reanchor">Cancel re-anchor</button>' : ''}
        <button type="button" data-action="delete" data-id="${escapeAttr(c.id)}">Delete</button>
      </div>`}
    </div>`;
}

function remoteCardHTML(c) {
  return `
    <div class="remote-comment-card outdated">
      <div class="meta"><span>${escapeHTML(c.author)}</span><span>${escapeHTML(c.path || '')}${c.line ? ':' + c.line : ''}</span></div>
      <div class="body-view">${md(c.body)}</div>
    </div>`;
}

function renderComments() {
  const list = document.getElementById('comment-list');
  const cards = state.comments.map(commentCardHTML);
  if (state.composing && !state.composing.id) {
    cards.push(`
      <div class="comment-card" id="comment-new">
        <div class="meta">${state.composing.anchor ? commentMeta({ anchor: state.composing.anchor }) : 'General'}</div>
        ${composeFormHTML(null, state.composing.body)}
      </div>`);
  }
  list.innerHTML = cards.join('') || '<p class="placeholder">No comments yet.</p>';

  const remoteList = document.getElementById('remote-comment-list');
  const outdated = state.remoteComments.filter((c) => c.outdated);
  remoteList.innerHTML = outdated.length ? outdated.map(remoteCardHTML).join('') : '<p class="placeholder">None.</p>';
}

// onCommentsPanelClick delegates every button in the side panel's comments
// section (cards are rebuilt wholesale on each render, so listeners are
// bound once on the container rather than per card).
function onCommentsPanelClick(e) {
  const btn = e.target.closest('button[data-action]');
  if (!btn) return;
  const { action, id } = btn.dataset;
  if (action === 'edit') {
    const c = state.comments.find((x) => x.id === id);
    if (c) openCompose(c);
  } else if (action === 'delete') {
    removeComment(id);
  } else if (action === 'reanchor') {
    beginReanchor(id);
  } else if (action === 'cancel-reanchor') {
    cancelReanchor();
  } else if (action === 'cancel') {
    cancelCompose();
  } else if (action === 'save') {
    const card = btn.closest('.comment-card');
    const ta = card.querySelector('textarea[data-draft-body]');
    saveCompose(id || null, state.composing.anchor, ta.value);
  } else if (action === 'retry') {
    retryDraft(id);
  }
}

// onCommentsPanelInput updates the live markdown preview and keeps the
// in-progress body in `state` as the user types, without a full re-render on
// every keystroke (which would steal focus out of the textarea).
function onCommentsPanelInput(e) {
  if (!e.target.matches('textarea[data-draft-body]')) return;
  if (state.composing) state.composing.body = e.target.value;
  const preview = e.target.nextElementSibling;
  if (preview && preview.classList.contains('preview')) {
    preview.innerHTML = e.target.value.trim() ? md(e.target.value) : '<em>preview</em>';
  }
}

// ======================== FR-11: GitHub host publish ========================
// "Submit review" lives in its own panel below the comment list, visible only
// for a PR session (state.source.kind === 'pr' -- set by applySessionView).
// Preview and Submit both call POST /api/publish/preflight, which re-fetches
// the PR's current diff and classifies every eligible draft (inline, general,
// or excluded as stale); Submit additionally shows a confirm step built from
// that same plan before actually calling POST /api/publish. Per-draft
// outcomes come back on the comments themselves (status/error/remote_url,
// see publishStatusHTML) through the refreshSession() that follows.

function renderPublishPanel() {
  const panel = document.getElementById('publish-panel');
  const isPR = !!(state.source && state.source.kind === 'pr');
  panel.hidden = !isPR;
  if (!isPR) return;

  const pf = document.getElementById('publish-preflight');
  const plan = state.publish.plan;
  pf.innerHTML = plan
    ? `<p>${plan.inline.length} inline, ${plan.general.length} general${plan.excluded.length ? `, ${plan.excluded.length} excluded` : ''}</p>` +
      (plan.excluded.length ? `<ul>${plan.excluded.map((x) => `<li>${escapeHTML(x.reason)}</li>`).join('')}</ul>` : '')
    : '';

  const confirm = document.getElementById('publish-confirm');
  confirm.hidden = !state.publish.confirming;
  if (state.publish.confirming && plan) {
    const event = document.getElementById('publish-event').value;
    document.getElementById('publish-confirm-summary').textContent =
      `Submit ${plan.inline.length} inline and ${plan.general.length} general comment(s) as ${event.toLowerCase()}?`;
  }
}

async function previewPublish() {
  state.publish.plan = await postJSON('/api/publish/preflight', { draft_ids: [] });
  renderPublishPanel();
}

async function startSubmitConfirm() {
  state.publish.plan = await postJSON('/api/publish/preflight', { draft_ids: [] });
  state.publish.confirming = true;
  renderPublishPanel();
}

function cancelSubmitConfirm() {
  state.publish.confirming = false;
  renderPublishPanel();
}

async function confirmSubmit() {
  const event = document.getElementById('publish-event').value;
  const body = document.getElementById('publish-body').value;
  await postJSON('/api/publish', { event, body, draft_ids: [] });
  state.publish.confirming = false;
  state.publish.plan = null;
  await refreshSession();
}

// retryDraft re-submits exactly one previously-failed draft: the server
// recognizes a request scoped to a single already-failed, anchored draft as a
// retry and publishes it alone (no new review object), see
// review.Workspace.Publish's isRetry.
async function retryDraft(id) {
  await postJSON('/api/publish', { event: 'COMMENT', body: '', draft_ids: [id] });
  await refreshSession();
}

// ======================== FR-19/FR-21: callers + tests sidebar ========================
// For the hunk currentHunkIndex (FR-7) calls "current", the right sidebar
// shows FR-19's blast radius (unchanged callers, call sites flagged in_diff,
// implementers) and FR-21's test reach (which tests reach it, or a warning
// that none do, plus a copyable `go test` command) for whichever changed
// function(s) that hunk's lines overlap.
//
// The join from "a hunk" to "a changed function" needs a line range to match
// against; GET /api/radius's own report carries bare symbol ID strings only
// (see radius.FuncRadius's doc comment -- an interface method has no
// model.CodeSymbol of its own, so radius never had one to give out), but
// GET /api/tests's report carries model.CodeSymbol (path + line range) for
// every changed func/method the diff touches, because FR-21 needs it to
// report "this changed function" in the first place. So symbolsForCurrentHunk
// below reads ranges from the tests report and, once it has a symbol ID,
// looks radius up by that same ID (both come from golang.SymbolID, so the
// strings agree). The accepted gap: a changed interface's own method-level
// radius entries (interfaceRadius in radius.go) have no CodeSymbol and so are
// never matched here -- only a changed func, method or (indirectly, via its
// concrete methods) a changed type show up in this sidebar.

// currentHunkRange is the current hunk's new-side line range, read back out
// of its own already-rendered header row ("@@ -o,n +s,l @@ section", see
// render.hunkRow) rather than asked of the server a second time -- render.Row
// carries no NewStart/NewLines field of its own. end mirrors
// radius.rangeInHunks' "anchor at NewStart" rule for a deletion-only hunk
// (NewLines == 0), so a symbol flagged in_diff there and a symbol matched
// here agree on what "inside this hunk" means.
function currentHunkRange() {
  const idx = currentHunkIndex();
  if (idx < 0) return null;
  const row = state.rows[state.hunkRows[idx]];
  if (!row) return null;
  const m = /\+(\d+),(\d+)/.exec(row.new_html || row.old_html || '');
  if (!m) return null;
  const start = Number(m[1]);
  const lines = Number(m[2]);
  return { path: state.selectedPath, start, end: lines > 0 ? start + lines - 1 : start };
}

function rangesOverlap(aStart, aEnd, bStart, bEnd) {
  return aStart <= bEnd && bStart <= aEnd;
}

// symbolsForCurrentHunk is this file's half of the hunk-to-function join
// described above: every changed CodeSymbol (from the tests report) whose
// declaration range overlaps the current hunk's new-side range.
function symbolsForCurrentHunk() {
  const range = currentHunkRange();
  if (!range || !state.testsReport || !state.testsReport.functions) return [];
  return state.testsReport.functions
    .map((fr) => fr.symbol)
    .filter((sym) => sym && sym.path === range.path && rangesOverlap(sym.range.start_line, sym.range.end_line, range.start, range.end));
}

function radiusForSymbol(id) {
  if (!state.radius || !state.radius.functions) return null;
  return state.radius.functions.find((fr) => fr.symbol === id) || null;
}

// shortSymbolName turns a golang.SymbolID ("pkgpath.Name" or
// "pkgpath.(Recv).Name") into just the part a reader recognizes at a glance:
// "Name" or "Recv.Name".
function shortSymbolName(id) {
  const m = /\.\(([^)]+)\)\.([^.]+)$/.exec(id);
  if (m) return `${m[1]}.${m[2]}`;
  const parts = id.split('.');
  return parts[parts.length - 1] || id;
}

function confBadgeHTML(conf) {
  return conf ? `<span class="badge conf-${escapeAttr(conf)}">${escapeHTML(conf)}</span>` : '';
}

// navEntryHTML renders one caller/call-site/implementer row. inDiff is each
// entry's own server-computed flag (Caller.InDiff is file-level, Site.InDiff
// is the exact line, Impl.InDiff is declaration-level -- see radius.go's doc
// comments on each); only an inDiff entry with a known loc is clickable, per
// FR-19's "a read-only file view is out of scope" note -- the rest render
// their path:line as plain text.
function navEntryHTML(label, loc, conf, inDiff) {
  const locText = loc ? `${loc.path}:${loc.line}` : '—';
  const clickable = !!(inDiff && loc);
  const attrs = clickable ? ` data-action="jump" data-path="${escapeAttr(loc.path)}" data-line="${loc.line}"` : '';
  return `<div class="nav-entry${clickable ? ' clickable' : ''}"${attrs}>` +
    `<span class="nav-name" title="${escapeAttr(label)}">${escapeHTML(label)}</span>` +
    `<span class="nav-loc">${escapeHTML(locText)}</span>${confBadgeHTML(conf)}</div>`;
}

// funcRadiusHTML renders one changed function's whole FR-19 card: its name,
// a badge when its signature changed, then unchanged callers, call sites
// (only present at all when the signature changed) and implementers --
// radius.FuncRadius's own three lists, each already empty when there is
// nothing to show rather than this having to guess.
function funcRadiusHTML(sym, fr) {
  const name = shortSymbolName(sym.id);
  const sigBadge = fr && fr.signature_changed ? '<span class="badge sig-changed">signature changed</span>' : '';
  const callers = (fr && fr.unchanged_callers) || [];
  const sites = (fr && fr.call_sites) || [];
  const impls = (fr && fr.implementers) || [];

  const callersHTML = callers.length
    ? callers.map((c) => navEntryHTML(shortSymbolName(c.symbol), c.site, c.confidence, c.in_diff)).join('')
    : '<p class="placeholder">No callers.</p>';
  const sitesHTML = sites.length
    ? `<h4>Call sites</h4>${sites.map((s) => navEntryHTML(shortSymbolName(s.caller), s.location, s.confidence, s.in_diff)).join('')}`
    : '';
  const implsHTML = impls.length
    ? `<h4>Implementers</h4>${impls.map((i) => navEntryHTML(shortSymbolName(i.symbol), i.location, i.confidence, i.in_diff)).join('')}`
    : '';

  return `<div class="func-radius"><div class="func-radius-title">${escapeHTML(name)}${sigBadge}</div>${callersHTML}${sitesHTML}${implsHTML}</div>`;
}

// renderCallersPanel hides the whole section when radius has nothing to say
// about this diff at all (non-Go source, or a patch with no resolvable old
// revision -- handleRadius answers an empty report in both cases rather than
// ever leaving the UI pending forever); otherwise it shows "computing…"
// until both radius and the tests report (the join's symbol-range source)
// are ready, then one card per function the current hunk overlaps, or a
// placeholder when none does.
function renderCallersPanel() {
  const panel = document.getElementById('callers-panel');
  const radiusFns = (state.radius && state.radius.functions) || [];
  if (!state.radiusPending && !radiusFns.length) {
    panel.hidden = true;
    return;
  }
  panel.hidden = false;
  const content = document.getElementById('callers-content');
  if (state.radiusPending || state.testsPending) {
    content.innerHTML = '<p class="placeholder">Computing…</p>';
    return;
  }
  const syms = symbolsForCurrentHunk();
  content.innerHTML = syms.length
    ? syms.map((sym) => funcRadiusHTML(sym, radiusForSymbol(sym.id))).join('')
    : '<p class="placeholder">No changed function here.</p>';
}

// commandForReach is the one PackageCommand (if any) whose Tests include a
// test that reaches fr -- there is at most one in practice (By's tests are
// usually declared in the same package as each other), but this does not
// assume it.
function commandForReach(fr, commands) {
  if (!fr.by || !fr.by.length || !commands) return null;
  const byNames = new Set(fr.by.map(shortSymbolName));
  return commands.find((c) => c.tests.some((t) => byNames.has(t))) || null;
}

// renderTestsPanel mirrors renderCallersPanel's hide/pending rules, reusing
// the same symbolsForCurrentHunk join -- just against the tests report's own
// per-function Reached/By instead of radius's callers.
function renderTestsPanel() {
  const panel = document.getElementById('tests-panel');
  const report = state.testsReport;
  if (!state.testsPending && (!report || !report.functions || !report.functions.length)) {
    panel.hidden = true;
    return;
  }
  panel.hidden = false;
  const content = document.getElementById('tests-content');
  if (state.testsPending) {
    content.innerHTML = '<p class="placeholder">Computing…</p>';
    return;
  }
  const syms = symbolsForCurrentHunk();
  if (!syms.length) {
    content.innerHTML = '<p class="placeholder">No changed function here.</p>';
    return;
  }
  content.innerHTML = syms.map((sym) => {
    const fr = report.functions.find((x) => x.symbol.id === sym.id);
    const name = shortSymbolName(sym.id);
    if (!fr || !fr.reached) {
      return `<div class="func-tests"><div class="func-radius-title">${escapeHTML(name)}</div>` +
        '<p class="no-test-warning">⚠ no test reaches this</p></div>';
    }
    const by = fr.by.map(shortSymbolName).join(', ');
    const cmd = commandForReach(fr, report.commands);
    const cmdHTML = cmd
      ? `<div class="test-command"><code>${escapeHTML(cmd.command)}</code><button type="button" data-action="copy-cmd" data-cmd="${escapeAttr(cmd.command)}">Copy</button></div>`
      : '';
    return `<div class="func-tests"><div class="func-radius-title">${escapeHTML(name)}</div>` +
      `<p>Reached by: ${escapeHTML(by)}</p>${cmdHTML}</div>`;
  }).join('');
}

// lastSidebarKey guards updateSidebarPanelsIfNeeded: renderVisibleRows calls
// it on every scroll frame (it is the one place "the current hunk" gets
// recomputed, see its own call site), but the callers/tests panels only need
// to redraw when the current hunk -- or the selected file -- actually
// changed, not on every frame of a scroll gesture that is still inside it.
let lastSidebarKey;
function updateSidebarPanelsIfNeeded(hunkId) {
  const key = state.selected + '|' + (hunkId || '');
  if (key === lastSidebarKey) return;
  lastSidebarKey = key;
  renderCallersPanel();
  renderTestsPanel();
}

// jumpToLocation (FR-19/FR-21's "clicking an in-diff entry") selects path's
// file if it is not already open, then scrolls to and focuses the row at
// line, when that line happens to already be part of the diff this file
// renders. It usually is for a call site (Site.InDiff means its exact line
// sits in a hunk) and often is not for an unchanged caller or an
// implementer (their own file is in the diff, but the specific line this
// entry names need not be near any hunk at all) -- when the row cannot be
// found, this still opens the file, just without a line to land on, rather
// than pretend to a precision a read-only file view would be needed for.
async function jumpToLocation(path, line) {
  const f = state.files.find((x) => x.path === path);
  if (!f) return;
  if (state.selected !== f.id) await selectFile(f.id);
  const rowIdx = state.rows.findIndex((r) => r.new_no === line);
  if (rowIdx === -1) return;
  document.getElementById('diff').scrollTop = rowIdx * ROW_HEIGHT;
  state.focusedAnchor = { path, side: 'new', line };
  renderVisibleRows();
}

async function copyTestCommand(cmd, btn) {
  try {
    await navigator.clipboard.writeText(cmd);
  } catch {
    return; // clipboard API unavailable (e.g. an insecure context) -- nothing more to do
  }
  const prev = btn.textContent;
  btn.textContent = 'Copied';
  setTimeout(() => { btn.textContent = prev; }, 1200);
}

// onFlowPanelClick delegates both panels' clicks (bound once on each
// container in init(), since funcRadiusHTML/renderTestsPanel rebuild their
// content wholesale on every redraw).
function onFlowPanelClick(e) {
  const jumpEl = e.target.closest('[data-action="jump"]');
  if (jumpEl) {
    jumpToLocation(jumpEl.dataset.path, Number(jumpEl.dataset.line));
    return;
  }
  const copyBtn = e.target.closest('[data-action="copy-cmd"]');
  if (copyBtn) copyTestCommand(copyBtn.dataset.cmd, copyBtn);
}

async function loadRadius() {
  const body = await pollUntilReady(() => getJSON('/api/radius'));
  state.radiusPending = false;
  state.radius = (body && body.report) || { functions: [] };
  forceSidebarRedraw();
}

async function loadTests() {
  const body = await pollUntilReady(() => getJSON('/api/tests'));
  state.testsPending = false;
  state.testsReport = body || { functions: [] };
  forceSidebarRedraw();
}

// forceSidebarRedraw forces a redraw once loadRadius/loadTests
// resolve, bypassing updateSidebarPanelsIfNeeded's dedup key -- the current
// hunk has not changed, but pending just flipped to false, which is exactly
// the thing that needs to show up.
function forceSidebarRedraw() {
  lastSidebarKey = undefined;
  renderCallersPanel();
  renderTestsPanel();
}

// ======================== long-line wrap toggle ('w') ========================
function applyWrapMode() {
  document.body.classList.toggle('wrap-lines', state.wrap);
  const btn = document.getElementById('wrap-toggle');
  btn.textContent = state.wrap ? '↔ Long lines: scroll' : '↔ Long lines: clip';
  btn.title = state.wrap
    ? 'Long lines: full width, one horizontal scrollbar for the diff (w)'
    : 'Long lines: clipped at the column edge, both sides visible (w)';
}

function toggleWrap() {
  state.wrap = !state.wrap;
  localStorage.setItem('flowreview.wrap', state.wrap ? '1' : '0');
  applyWrapMode();
}

// ======================== diff layout toggle: side-by-side / unified ('u') ========================
// Switching layout only ever calls rebuildActiveRows -- it never needs to
// touch expandedHunks: expand/collapse (above) always read and write
// state.sbsRows, never state.rows, so an already-expanded hunk stays
// expanded (and still collapses correctly later) no matter how many times
// the layout flips in between.
function applyDiffLayoutUI() {
  document.body.classList.toggle('diff-unified', state.layout === 'unified');
  const btn = document.getElementById('layout-toggle');
  btn.textContent = state.layout === 'unified' ? '☰ Unified' : '▥ Side-by-side';
  btn.title = state.layout === 'unified'
    ? 'Diff layout: unified -- click to switch to side-by-side (u)'
    : 'Diff layout: side-by-side -- click to switch to unified (u)';
}

function setDiffLayout(layout) {
  if (state.layout === layout) return;
  state.layout = layout;
  localStorage.setItem('flowreview.diffLayout', layout);
  applyDiffLayoutUI();
  rebuildActiveRows();
}

function toggleDiffLayout() {
  setDiffLayout(state.layout === 'unified' ? 'sbs' : 'unified');
}

document.addEventListener('keydown', (e) => {
  const target = e.target;
  if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA')) return;

  const help = document.getElementById('help-overlay');
  if (!help.hidden) {
    if (e.key === 'Escape' || e.key === '?') toggleHelp();
    return; // swallow every other key while the help overlay is open
  }

  if (e.key === '?') { toggleHelp(); return; }
  if (e.key === 'j') moveSelection(1);
  if (e.key === 'k') moveSelection(-1);
  if (e.key === 'n') jumpHunk(1);
  if (e.key === 'p') jumpHunk(-1);
  if (e.key === 'e') toggleExpand(currentHunkId());
  if (e.key === 'r') toggleCurrentHunk();
  if (e.key === 'R') toggleCurrentFile();
  if (e.key === 'c') openComposeForFocused();
  if (e.key === 'w') toggleWrap();
  if (e.key === 'u') toggleDiffLayout();
  if (e.key === '[') toggleFilesCollapsed();
  if (e.key === ']') toggleSideCollapsed();
});

(async function init() {
  document.getElementById('help-button').addEventListener('click', toggleHelp);
  document.getElementById('help-overlay').addEventListener('click', (e) => {
    if (e.target.id === 'help-overlay') toggleHelp(); // clicked the backdrop, not the card
  });
  document.getElementById('add-general-comment').addEventListener('click', startGeneralComment);
  document.getElementById('comments-panel').addEventListener('click', onCommentsPanelClick);
  document.getElementById('comments-panel').addEventListener('input', onCommentsPanelInput);
  document.getElementById('publish-preview').addEventListener('click', previewPublish);
  document.getElementById('publish-submit').addEventListener('click', startSubmitConfirm);
  document.getElementById('publish-confirm-yes').addEventListener('click', confirmSubmit);
  document.getElementById('publish-confirm-no').addEventListener('click', cancelSubmitConfirm);

  // Topbar toolbar: one button per global shortcut (see index.html's own
  // comment on #toolbar).
  document.getElementById('tb-prev-file').addEventListener('click', () => moveSelection(-1));
  document.getElementById('tb-next-file').addEventListener('click', () => moveSelection(1));
  document.getElementById('tb-prev-hunk').addEventListener('click', () => jumpHunk(-1));
  document.getElementById('tb-next-hunk').addEventListener('click', () => jumpHunk(1));
  document.getElementById('tb-file-reviewed').addEventListener('click', toggleCurrentFile);
  document.getElementById('order-toggle').addEventListener('click', toggleOrderMode);
  document.getElementById('wrap-toggle').addEventListener('click', toggleWrap);
  document.getElementById('layout-toggle').addEventListener('click', toggleDiffLayout);

  // FR-19/FR-21 sidebar panels: one click handler per panel, bound once
  // (funcRadiusHTML/renderTestsPanel replace each panel's content wholesale
  // on every redraw, same reasoning as onCommentsPanelClick above).
  document.getElementById('callers-panel').addEventListener('click', onFlowPanelClick);
  document.getElementById('tests-panel').addEventListener('click', onFlowPanelClick);

  // Collapsible/resizable panes and the diff layout toggle: see their own
  // "collapsible + resizable panes" / "diff layout toggle" sections above.
  document.getElementById('files-toggle').addEventListener('click', toggleFilesCollapsed);
  document.getElementById('side-toggle').addEventListener('click', toggleSideCollapsed);
  setupFilesResizer();
  setupSideResizer();
  applyPaneLayout();
  applyDiffSplit();
  applyDiffLayoutUI();
  applyWrapMode();

  await refreshSession();
  await loadFiles();
  await loadRemoteComments();

  // FR-17/FR-19/FR-21: all three run in the background on the server and
  // answer {"pending": true} until ready (see each endpoint's own doc
  // comment) -- loadOrder/loadRadius/loadTests poll independently of the
  // file list and of each other, so none of them block initial render.
  // Started from requestIdleCallback, not immediately: each is a real
  // go/packages Load server-side (CPU, not just a network wait), and kicking
  // off all three the instant the page loads can compete with the browser's
  // own first-paint and scroll work for the CPU it is still busy with right
  // then. A 2s timeout is the fallback for a browser without
  // requestIdleCallback (Safari) -- not a deadline on the loads themselves,
  // which keep polling however long they need to afterward.
  const startBackgroundAnalysis = () => { loadOrder(); loadRadius(); loadTests(); };
  if (window.requestIdleCallback) requestIdleCallback(startBackgroundAnalysis, { timeout: 2000 });
  else setTimeout(startBackgroundAnalysis, 0);
})();
