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
  selectedPath: null, // FR-9: path of the selected file, for comment anchors and remote-comment matching
  rows: [], // render.Row from GET /api/file/{id}/rows, for the selected file
  hunkRows: [], // indices into `rows` where kind === "hunk", for n/p

  // FR-7
  hunkStates: {}, // hunk_id -> model.HunkStatus, from the last GET/POST /api/session
  comments: [], // this session's own comment drafts, from the same response (FR-9 also reads this)

  // FR-9
  remoteComments: [], // GET /api/remote-comments, fetched once at init
  focusedAnchor: null, // the last code-cell click, target of the `c` shortcut
  composing: null, // {id: string|null, anchor: Anchor|null, body: string} -- the one open compose card, or null
  reanchorCommentId: null, // set while picking a new line for a stale comment
};

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

function renderFiles() {
  const el = document.getElementById('files');
  el.innerHTML = '';
  for (const f of state.files) {
    const row = document.createElement('div');
    row.className = `file-row ${fileState(f)}` + (f.id === state.selected ? ' selected' : '');
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

function rowCellsHTML(fileId, row, isCurrent) {
  const reviewed = state.hunkStates[row.hunk_id] === 'reviewed';
  const dim = reviewed ? ' hunk-reviewed' : '';
  if (row.kind === 'hunk') {
    const cls = 'cell hunk-header' + dim + (isCurrent ? ' current' : '');
    const check = reviewed ? ' <span class="reviewed-check">✓ reviewed</span>' : '';
    return cellHTML(cls, fileId, '', '', row.hunk_id, '', (row.old_html || '') + check);
  }
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

// ensureDiffDOM builds #diff's virtualization scaffolding once; selectFile
// just updates the spacer height and re-renders the visible window on every
// later call.
function ensureDiffDOM() {
  const diff = document.getElementById('diff');
  if (!diff.dataset.virtualized) {
    diff.dataset.virtualized = '1';
    diff.innerHTML = '<div id="diff-spacer"></div><div id="diff-rows"></div>';
    diff.addEventListener('scroll', onDiffScroll, { passive: true });
    diff.addEventListener('click', onDiffClick); // FR-9
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
  const f = state.files.find((x) => x.id === id);
  state.selectedPath = f ? f.path : null; // FR-9: comment anchors key off the path, not the file id
  state.focusedAnchor = null; // FR-9: a focused line belongs to the file it was focused in
  renderFiles();
  const token = ++selectToken;
  const rows = await getJSON(`/api/file/${encodeURIComponent(id)}/rows`);
  if (token !== selectToken) return; // a newer selectFile call has since started

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

// ======================== FR-7: review queue, reviewed state, progress ========================

// applySessionView is the one place a SessionView response (from GET
// /api/session or any POST that returns one) gets applied to `state` and
// redrawn -- the header progress bar, the right-pane summary, the comment
// list, and the hunk-state overlay on whichever rows are currently on
// screen.
function applySessionView(sess) {
  state.hunkStates = sess.hunk_states || {};
  state.comments = sess.comments || [];
  renderSessionSummary(sess);
  renderComments();
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
async function toggleCurrentHunk() {
  const idx = currentHunkIndex();
  if (idx < 0) return;
  const hunkId = state.rows[state.hunkRows[idx]].hunk_id;
  const reviewed = state.hunkStates[hunkId] !== 'reviewed';
  const sess = await postJSON('/api/state', { hunk_id: hunkId, reviewed });
  applySessionView(sess);
  await loadFiles();
  renderVisibleRows();
  await maybeCelebrateCompletion();
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

function moveSelection(delta) {
  if (state.files.length === 0) return;
  const idx = state.files.findIndex((f) => f.id === state.selected);
  const next = Math.min(Math.max((idx < 0 ? 0 : idx) + delta, 0), state.files.length - 1);
  selectFile(state.files[next].id);
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

function commentCardHTML(c) {
  const stale = c.status === 'stale';
  const reanchoring = state.reanchorCommentId === c.id;
  const composingHere = !!(state.composing && state.composing.id === c.id);
  const body = composingHere ? composeFormHTML(c.id, state.composing.body) : `<div class="body-view">${md(c.body)}</div>`;
  return `
    <div class="comment-card${stale ? ' stale' : ''}${reanchoring ? ' reanchoring' : ''}" id="comment-${escapeAttr(c.id)}">
      <div class="meta"><span>${commentMeta(c)}</span>${c.publishable === false ? '<span title="not on a diff line; cannot be published">local only</span>' : ''}</div>
      ${stale ? `<div class="stale-warning">This line changed since you drafted this comment.${reanchoring ? ' Click a line in the diff to re-anchor.' : ''}</div>` : ''}
      ${body}
      <div class="actions">
        ${composingHere ? '' : `<button type="button" data-action="edit" data-id="${escapeAttr(c.id)}">Edit</button>`}
        ${stale && c.anchor && !reanchoring ? `<button type="button" data-action="reanchor" data-id="${escapeAttr(c.id)}">Re-anchor</button>` : ''}
        ${reanchoring ? '<button type="button" data-action="cancel-reanchor">Cancel re-anchor</button>' : ''}
        <button type="button" data-action="delete" data-id="${escapeAttr(c.id)}">Delete</button>
      </div>
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
  if (e.key === 'r') toggleCurrentHunk();
  if (e.key === 'R') toggleCurrentFile();
  if (e.key === 'c') openComposeForFocused();
});

(async function init() {
  document.getElementById('help-button').addEventListener('click', toggleHelp);
  document.getElementById('help-overlay').addEventListener('click', (e) => {
    if (e.target.id === 'help-overlay') toggleHelp(); // clicked the backdrop, not the card
  });
  document.getElementById('add-general-comment').addEventListener('click', startGeneralComment);
  document.getElementById('comments-panel').addEventListener('click', onCommentsPanelClick);
  document.getElementById('comments-panel').addEventListener('input', onCommentsPanelInput);

  await refreshSession();
  await loadFiles();
  await loadRemoteComments();
})();
