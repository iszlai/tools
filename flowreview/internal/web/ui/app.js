'use strict';
// FlowReview's UI shell: file queue, a plain unified-diff row table, and a
// session summary panel. Vanilla JS, no bundler -- FR-6 replaces renderRows
// with a virtualized, side-by-side, syntax-highlighted table; the row shape
// it will render stays `{kind, old_no, new_no, text, hunk_id, position}` per
// GET /api/file/{id}/rows, so this file is the reference for how those fields
// map onto the DOM.

const state = {
  files: [],
  selected: null,
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

function rowClass(kind) {
  switch (kind) {
    case 'add': return 'row-add';
    case 'del': return 'row-del';
    case 'header': return 'row-header';
    default: return 'row-context';
  }
}

function renderRows(rows) {
  const diff = document.getElementById('diff');
  const table = document.createElement('table');
  table.className = 'diff-table';
  const tbody = document.createElement('tbody');

  for (const r of rows) {
    const tr = document.createElement('tr');
    tr.className = rowClass(r.kind);
    tr.dataset.file = state.selected;
    tr.dataset.side = r.kind === 'del' ? 'old' : 'new';
    tr.dataset.line = String((r.kind === 'del' ? r.old_no : r.new_no) || '');
    tr.dataset.hunk = r.hunk_id || '';
    tr.dataset.position = String(r.position || '');

    if (r.kind === 'header') {
      tr.innerHTML = `<td colspan="3" class="text">${escapeHTML(r.text)}</td>`;
    } else {
      tr.innerHTML = `
        <td class="lineno">${r.old_no || ''}</td>
        <td class="lineno">${r.new_no || ''}</td>
        <td class="text">${escapeHTML(r.text)}</td>
      `;
    }
    tbody.appendChild(tr);
  }

  table.appendChild(tbody);
  diff.innerHTML = '';
  diff.appendChild(table);
}

async function selectFile(id) {
  state.selected = id;
  renderFiles();
  const rows = await getJSON(`/api/file/${encodeURIComponent(id)}/rows`);
  renderRows(rows);
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
});

(async function init() {
  await refreshSession();
  await loadFiles();
})();
