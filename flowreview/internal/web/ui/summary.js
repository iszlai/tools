'use strict';
// FlowReview's structured hunk summaries (FR-28 backend, FR-29 trimmed UI;
// PLAN.md §2 "Structured summaries", §5). Deliberately a separate file from
// app.js, not a module app.js imports: the two communicate through the DOM
// app.js already produces, plus two tiny hooks app.js calls out to --
// window.FlowReviewSummary.stripHTML while building a hunk-header cell, and
// .onRowsRendered after #diff-rows is redrawn -- so this file can be read,
// changed and tested on its own. Every control here is a visible button with
// a tooltip naming its keyboard shortcut, if it has one -- never a shortcut
// alone.
//
// Why a strip, not a row: app.js's virtualized diff gives every row,
// including a hunk header, a fixed ROW_HEIGHT (20px) grid cell with
// `overflow-y: hidden` (see styles.css's own comment on this), because the
// whole pane's scroll math is `row index * ROW_HEIGHT`. A multi-line card
// cannot be inserted in flow without breaking that. So: collapsed, the
// summary is a one-line strip appended inside the existing hunk-header cell
// (a truncated "what" plus a small kind badge, never taller than the row);
// expanded, it is a floating popover positioned over the diff -- like
// #help-overlay, but anchored to the strip that opened it -- not an extra
// row.
//
// Card content is intentionally read defensively: `what` is the headline,
// `kind` a small secondary badge, and every other field (inferred_why,
// impact, risk_flags, confidence, fallback) is optional and simply omitted
// when absent or empty, so a future prompt/schema revision that adds or
// drops fields needs no change here as long as `what` and `kind` keep their
// names.
//
// Fetching: app.js only ever renders rows within the viewport plus a small
// overscan (see its own ROW_HEIGHT/OVERSCAN comment), so "the hunk-header
// cells currently in #diff-rows" already *is* "hunks near the viewport" --
// onRowsRendered just reads that straight off the DOM via each cell's
// data-hunk attribute (already set by app.js's cellHTML for FR-9's anchors)
// and asks the server for whichever of those are not already known.

const SUMMARY_TOGGLE_KEY = 'flowreview.summaries.enabled';
const POLL_INTERVAL_MS = 1500;
const POLL_MAX_ATTEMPTS = 20; // ~30s; a request stuck past this just stops polling, no error shown

const cache = new Map(); // hunk_id -> model.SnippetSummary
const inFlightOrPending = new Set(); // hunk_id requested (batch or poll), no answer cached yet
const pollTimers = new Map(); // hunk_id -> setTimeout handle
let wanted = new Set(); // hunk_id currently in #diff-rows, per the last onRowsRendered
let popoverHunkId = null;
let enabled = localStorage.getItem(SUMMARY_TOGGLE_KEY) !== 'off';

function escapeHTML(s) {
  const div = document.createElement('div');
  div.textContent = s == null ? '' : String(s);
  return div.innerHTML;
}

function escapeAttr(s) {
  return String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;');
}

function cssEscape(s) {
  return window.CSS && CSS.escape ? CSS.escape(s) : String(s).replace(/["\\]/g, '\\$&');
}

function truncate(s, n) {
  if (!s) return '';
  return s.length > n ? s.slice(0, n - 1) + '…' : s;
}

async function getJSON(url, opts) {
  const res = await fetch(url, opts);
  if (!res.ok) throw new Error(res.statusText);
  return res.json();
}

async function postJSON(url, body) {
  return getJSON(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
}

// --- on/off toggle: a visible glyph button, plus 's' -----------------------
// The button is built here and slotted into the top-bar toolbar, just
// before its "?" help button, so everything about it lives in this file.

function buildToggleButton() {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.id = 'fr-summary-toggle-button';
  btn.textContent = '✦';
  btn.setAttribute('aria-label', 'Toggle hunk summaries');
  btn.addEventListener('click', toggleEnabled);
  const toolbar = document.getElementById('toolbar');
  if (toolbar) toolbar.insertBefore(btn, toolbar.lastElementChild); // before "?"
  else document.body.appendChild(btn);
  return btn;
}

// syncEnabledUI updates everything that reflects `enabled`: the body class
// styles.css hides every strip on, and the toggle button's pressed state and
// tooltip (which always names both the action and its 's' shortcut).
function syncEnabledUI() {
  document.body.classList.toggle('fr-summaries-off', !enabled);
  const btn = document.getElementById('fr-summary-toggle-button') || buildToggleButton();
  btn.setAttribute('aria-pressed', String(enabled));
  btn.classList.toggle('active', enabled);
  btn.title = enabled ? 'Hide hunk summaries (s)' : 'Show hunk summaries (s)';
}

function setEnabled(next) {
  enabled = next;
  localStorage.setItem(SUMMARY_TOGGLE_KEY, next ? 'on' : 'off');
  syncEnabledUI();
  if (enabled) onRowsRendered(); // pick up whatever is already on screen
}

function toggleEnabled() {
  setEnabled(!enabled);
}

// --- fetching & polling -------------------------------------------------

async function requestSummaries(ids) {
  if (!enabled || !ids.length) return;
  let result;
  try {
    result = await postJSON('/api/summaries', { hunk_ids: ids });
  } catch {
    return; // best effort: a failed batch just leaves those hunks without a card for now
  }
  applyResult(result);
}

function applyResult(result) {
  for (const [id, s] of Object.entries(result.ready || {})) {
    cache.set(id, s);
    inFlightOrPending.delete(id);
    clearPoll(id);
    updateStripDOM(id);
  }
  for (const id of result.pending || []) {
    if (cache.has(id)) continue;
    inFlightOrPending.add(id);
    schedulePoll(id, 0);
  }
}

function clearPoll(id) {
  const t = pollTimers.get(id);
  if (t) {
    clearTimeout(t);
    pollTimers.delete(id);
  }
}

function schedulePoll(id, attempt) {
  if (pollTimers.has(id)) return;
  const timer = setTimeout(async () => {
    pollTimers.delete(id);
    if (!enabled || !wanted.has(id) || cache.has(id)) {
      inFlightOrPending.delete(id);
      return;
    }
    let result;
    try {
      result = await getJSON(`/api/summaries?ids=${encodeURIComponent(id)}`);
    } catch {
      return;
    }
    applyResult(result);
    if (!cache.has(id) && attempt + 1 < POLL_MAX_ATTEMPTS) {
      schedulePoll(id, attempt + 1);
    } else if (!cache.has(id)) {
      inFlightOrPending.delete(id); // gave up; a later onRowsRendered can retry from scratch
    }
  }, POLL_INTERVAL_MS);
  pollTimers.set(id, timer);
}

// --- app.js hooks ---------------------------------------------------------

// onRowsRendered: see this file's header comment for why reading the DOM is
// enough to know "which hunks are near the viewport."
function onRowsRendered() {
  const ids = [];
  document.querySelectorAll('#diff-rows .hunk-header[data-hunk]').forEach((el) => {
    if (el.dataset.hunk) ids.push(el.dataset.hunk);
  });
  wanted = new Set(ids);
  if (!enabled || !ids.length) return;
  const missing = ids.filter((id) => !cache.has(id) && !inFlightOrPending.has(id));
  if (missing.length) {
    missing.forEach((id) => inFlightOrPending.add(id));
    requestSummaries(missing);
  }
}

// stripHTML is pure and synchronous -- called while app.js builds a
// hunk-header cell's HTML string, before that string is ever in the DOM, so
// it must never itself trigger a fetch (onRowsRendered does that once the
// row is actually rendered).
function stripHTML(hunkId) {
  if (!hunkId) return '';
  const s = cache.get(hunkId);
  const body = s ? readyStripHTML(hunkId, s) : loadingStripHTML();
  return `<span class="fr-summary-strip" data-hunk-strip="${escapeAttr(hunkId)}">${body}</span>`;
}

function loadingStripHTML() {
  return '<span class="fr-summary-shimmer" title="Summarizing…"></span>';
}

// kindBadgeHTML renders kind as a small, secondary badge -- never the
// headline; "" (not yet known, or a future prompt variant that dropped it)
// renders nothing rather than a placeholder.
function kindBadgeHTML(kind) {
  if (!kind) return '';
  return `<span class="fr-kind fr-kind-${escapeAttr(kind)}">${escapeHTML(kind)}</span>`;
}

// readyStripHTML leads with `what` (the headline) and treats every other
// field as optional secondary detail, omitted when empty.
function readyStripHTML(hunkId, s) {
  const risky = s.risk_flags && s.risk_flags.length ? ' fr-risky' : '';
  const fallback = s.fallback ? '<span class="fr-fallback" title="No model configured or the model failed; this is a derived summary, not AI-generated">fallback</span>' : '';
  return (
    `<button type="button" class="fr-summary-toggle${risky}" data-hunk="${escapeAttr(hunkId)}" title="Click for details">` +
    `<span class="fr-what">${escapeHTML(truncate(s.what, 90))}</span>` +
    kindBadgeHTML(s.kind) +
    fallback +
    '<span class="fr-chevron" aria-hidden="true">▾</span>' +
    '</button>'
  );
}

// updateStripDOM keeps a strip already on screen in sync once a fetch/poll
// resolves after its row first rendered with a shimmer. A no-op if the hunk
// scrolled out of the DOM in the meantime -- the next time it renders,
// stripHTML will read the now-populated cache itself.
function updateStripDOM(hunkId) {
  const el = document.querySelector(`[data-hunk-strip="${cssEscape(hunkId)}"]`);
  if (!el) return;
  const s = cache.get(hunkId);
  el.innerHTML = s ? readyStripHTML(hunkId, s) : loadingStripHTML();
}

// --- expand popover --------------------------------------------------------

function closePopover() {
  popoverHunkId = null;
  const el = document.getElementById('fr-summary-popover');
  if (el) el.remove();
}

// popoverHTML leads with `what` as the headline paragraph; kind sits in the
// header row as a small secondary badge next to confidence, and every other
// field (inferred_why, impact, risk_flags, confidence, fallback) renders only
// when present and non-empty.
function popoverHTML(s) {
  const risk = (s.risk_flags || []).map((f) => `<span class="fr-risk-flag">${escapeHTML(f)}</span>`).join('');
  const hasConfidence = typeof s.confidence === 'number';
  return `
    <div class="fr-summary-popover-card">
      <div class="fr-summary-popover-head">
        ${kindBadgeHTML(s.kind)}
        ${hasConfidence ? `<span class="fr-confidence">confidence ${Math.round(s.confidence * 100)}%</span>` : ''}
        ${s.fallback ? '<span class="fr-fallback">fallback</span>' : ''}
        <button type="button" class="fr-close" data-action="close" title="Close" aria-label="Close">×</button>
      </div>
      <p class="fr-what fr-headline">${escapeHTML(s.what || '')}</p>
      ${s.inferred_why ? `<p class="fr-why"><strong>Why:</strong> ${escapeHTML(s.inferred_why)}</p>` : ''}
      ${s.impact ? `<p class="fr-impact"><strong>Impact:</strong> ${escapeHTML(s.impact)}</p>` : ''}
      ${risk ? `<div class="fr-risk-flags">${risk}</div>` : ''}
      <div class="fr-summary-popover-actions">
        <button type="button" class="fr-regenerate" data-action="regenerate" title="Regenerate summary (asks the model again)" aria-label="Regenerate summary">↻</button>
      </div>
    </div>`;
}

function openPopover(hunkId, anchorEl) {
  closePopover();
  const s = cache.get(hunkId);
  if (!s) return;
  popoverHunkId = hunkId;
  const rect = anchorEl.getBoundingClientRect();
  const el = document.createElement('div');
  el.id = 'fr-summary-popover';
  el.style.position = 'fixed';
  el.style.left = `${Math.max(8, rect.left)}px`;
  el.style.top = `${rect.bottom + 4}px`;
  el.innerHTML = popoverHTML(s);
  document.body.appendChild(el);
}

async function regenerate(hunkId) {
  if (!hunkId) return;
  cache.delete(hunkId);
  inFlightOrPending.add(hunkId);
  updateStripDOM(hunkId);
  closePopover();
  try {
    await postJSON('/api/summaries/regenerate', { hunk_id: hunkId });
  } catch {
    inFlightOrPending.delete(hunkId);
    return;
  }
  schedulePoll(hunkId, 0);
}

document.addEventListener('click', (e) => {
  const toggle = e.target.closest('.fr-summary-toggle');
  if (toggle) {
    const hunkId = toggle.dataset.hunk;
    if (popoverHunkId === hunkId) {
      closePopover();
    } else {
      openPopover(hunkId, toggle);
    }
    return;
  }
  const action = e.target.closest('button[data-action]');
  if (action && action.closest('#fr-summary-popover')) {
    if (action.dataset.action === 'close') closePopover();
    if (action.dataset.action === 'regenerate') regenerate(popoverHunkId);
    return;
  }
  if (popoverHunkId && !e.target.closest('#fr-summary-popover')) closePopover();
});

// The diff pane scrolls internally (#diff), which would leave a fixed-
// position popover pointing at empty space; closing it on scroll matches how
// a native tooltip/popover behaves.
document.addEventListener(
  'scroll',
  (e) => {
    if (popoverHunkId && e.target && e.target.id === 'diff') closePopover();
  },
  true,
);

document.addEventListener('keydown', (e) => {
  const target = e.target;
  if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA')) return;
  const help = document.getElementById('help-overlay');
  if (help && !help.hidden) return; // app.js already owns every key while help is open

  if (e.key === 's') toggleEnabled();
  if (e.key === 'Escape' && popoverHunkId) closePopover();
});

syncEnabledUI(); // also builds the toggle button on first run

window.FlowReviewSummary = { stripHTML, onRowsRendered };
