'use strict';
// Drives the real flowreview binary in headless Chromium. Builds a temp git
// repo with changes (a few small files plus one 10k-line file), starts
// `bin/flowreview --no-open` against it, and exercises the UI FR-5/FR-6
// built: the file queue, the virtualized side-by-side diff with its anchor
// data attributes, hunk/file keyboard navigation, the session summary panel,
// and the PLAN.md section 8 performance budget -- everything the Go tests
// cannot see because it only exists in the browser.
//
//   npm install playwright-core   (or rely on ~/node_modules, see taskhound)
//   PLAYWRIGHT_BROWSERS_PATH=./pw-browsers npx playwright-core install chromium-headless-shell
//   PLAYWRIGHT_BROWSERS_PATH=./pw-browsers node tests/smoke.js
//
// It needs Playwright's own chromium: corporate-managed Chrome refuses to be
// automated ("remote debugging is disallowed by the system admin").

const { chromium } = require('playwright-core');
const { execFileSync, spawn } = require('child_process');
const { mkdtempSync, mkdirSync, writeFileSync } = require('fs');
const { tmpdir } = require('os');
const path = require('path');

const BIN = path.resolve(__dirname, '..', 'bin', 'flowreview');
const PAINT_BUDGET_MS = 1000; // PLAN.md section 8: <1s session open / file first paint
const SWITCH_BUDGET_MS = 100; // PLAN.md section 8: <100ms file switch
const LONGTASK_BUDGET_MS = 50; // PLAN.md section 8: no janky frame while scrolling
const BIG_FILE_LINES = 10000;

let failures = 0;
function check(name, cond, detail = '') {
  console.log(`${cond ? 'PASS' : 'FAIL'}  ${name}${cond ? '' : '  -- ' + detail}`);
  if (!cond) failures++;
}

function git(dir, env, ...args) {
  execFileSync('git', args, { cwd: dir, env: { ...process.env, ...env } });
}

// goFnSource builds a Go file with one function whose body statement v10 is
// tenValue: 10 for the fixture's committed revision, 100 for its
// worktree-edited one. A default 3-line-context diff only shows a handful of
// lines around v10, leaving real, unchanged body on both sides for FR-8's
// "expand enclosing function" to splice in -- unlike a.txt's one-word edit,
// which is small enough that the whole file is already the hunk. Every vN is
// summed into the return value (not just v0): the file list now also drives
// GET /api/order, /api/radius and /api/tests through a real go/packages
// Load, which type-checks the source -- an unused local is a hard error
// there, not just a vet nit, and would otherwise make Compute's whole
// package fail to build an SSA call graph (empty Callers/Callees for every
// symbol in it, radius and testreach both report nothing).
function goFnSource(tenValue) {
  const lines = ['package main', '', 'func Compute() int {'];
  for (let i = 0; i < 20; i++) lines.push(`\tv${i} := ${i === 10 ? tenValue : i}`);
  lines.push('\tsum := v0 + v1 + v2 + v3 + v4 + v5 + v6 + v7 + v8 + v9');
  lines.push('\tsum += v10 + v11 + v12 + v13 + v14 + v15 + v16 + v17 + v18 + v19');
  lines.push('\treturn sum', '}');
  return lines.join('\n') + '\n';
}

// callerSource is FR-19/FR-21's fixture: CallCompute calls Compute and never
// itself changes across the diff (an "unchanged caller" of it, in_diff true
// since this file does carry a hunk elsewhere); TestCompute (in its own
// file, never touched by the diff at all) also calls Compute, so it shows up
// as an unchanged caller with in_diff false, and makes Compute "reached by a
// test" for FR-21. unrelated is the one line this file's own hunk actually
// touches -- far enough above CallCompute (two blank lines) that the
// default 3-line-context hunk never overlaps CallCompute's own declaration
// range, so CallCompute stays correctly "unchanged" per golang's own
// hunk-range-overlap definition (see ChangedSymbols) -- and, having no
// caller of its own, doubles as FR-19's "no callers" case.
const callerSource = `package main

func unrelated() int {
	return 1
}


func CallCompute() int {
	return Compute()
}
`;

const testSource = `package main

import "testing"

func TestCompute(t *testing.T) {
	Compute()
}
`;

// buildRepo makes a repo with: a modified tracked file and a new untracked
// one (so the file queue has more than one entry and WorktreeDiff exercises
// both its tracked and untracked-synthesis paths), a 10k-line file with
// changes scattered through it for the performance budget below, a small Go
// file whose one hunk sits in the middle of a function for FR-8 (also
// FR-17/19/21's changed-function fixture), a caller/test pair for FR-19/21,
// and one very long line for the wrap toggle.
function buildRepo() {
  const dir = mkdtempSync(path.join(tmpdir(), 'flowreview-smoke-'));
  const env = {
    GIT_AUTHOR_NAME: 'Smoke', GIT_AUTHOR_EMAIL: 'smoke@example.com',
    GIT_COMMITTER_NAME: 'Smoke', GIT_COMMITTER_EMAIL: 'smoke@example.com',
  };
  git(dir, env, 'init', '-q', '-b', 'main', '.');
  git(dir, env, 'config', 'user.name', 'Smoke');
  git(dir, env, 'config', 'user.email', 'smoke@example.com');

  writeFileSync(path.join(dir, 'a.txt'), 'line one\nline two\nline three\n');
  const bigLines = [];
  for (let i = 0; i < BIG_FILE_LINES; i++) bigLines.push(`const line${i} = ${i};`);
  writeFileSync(path.join(dir, 'big.js'), bigLines.join('\n') + '\n');
  writeFileSync(path.join(dir, 'fn.go'), goFnSource(10));
  writeFileSync(path.join(dir, 'go.mod'), 'module smoke\n\ngo 1.21\n');
  writeFileSync(path.join(dir, 'caller.go'), callerSource);
  writeFileSync(path.join(dir, 'fn_test.go'), testSource);
  writeFileSync(path.join(dir, 'long.txt'), 'short line\n');
  git(dir, env, 'add', 'a.txt', 'big.js', 'fn.go', 'go.mod', 'caller.go', 'fn_test.go', 'long.txt');
  git(dir, env, '-c', 'commit.gpgsign=false', 'commit', '-q', '-m', 'init');

  writeFileSync(path.join(dir, 'a.txt'), 'line one\nline TWO\nline three\n');
  writeFileSync(path.join(dir, 'b.txt'), 'a brand new file\n');
  writeFileSync(path.join(dir, 'fn.go'), goFnSource(100));
  // unrelated's body, not CallCompute -- see callerSource's own comment.
  writeFileSync(path.join(dir, 'caller.go'), callerSource.replace('return 1', 'return 2'));
  // A 400-char line: no reasonable viewport shows all of it without wrap.
  writeFileSync(path.join(dir, 'long.txt'), 'x'.repeat(400) + '\n');
  // Scatter ~100 single-line edits through the big file -- enough hunks to
  // make virtualization matter across the whole scroll, not so many that
  // every line is a hunk (which no real diff looks like).
  for (let i = 0; i < BIG_FILE_LINES; i += 100) {
    bigLines[i] = `const line${i} = ${i} * 2; // changed`;
  }
  writeFileSync(path.join(dir, 'big.js'), bigLines.join('\n') + '\n');
  return dir;
}

// serve starts flowreview --no-open against repoDir and resolves once it has
// printed its URL (the first line on stdout, per FR-5's contract). args are
// prepended before --no-open --port 0 (e.g. ['pr', 'o/r#1'] for FR-11's PR
// scenario below); extraEnv is merged in on top of FLOWREVIEW_HOME.
function serve(repoDir, home, args = [], extraEnv = {}) {
  const proc = spawn(BIN, [...args, '--no-open', '--port', '0'], {
    cwd: repoDir,
    env: { ...process.env, FLOWREVIEW_HOME: home, ...extraEnv },
  });
  return new Promise((resolve, reject) => {
    let buf = '';
    proc.stdout.on('data', (d) => {
      buf += d;
      const m = buf.match(/https?:\/\/127\.0\.0\.1:\d+\//);
      if (m) resolve({ proc, url: m[0] });
    });
    proc.stderr.on('data', (d) => process.stderr.write(d));
    proc.on('error', reject);
    setTimeout(() => reject(new Error('flowreview did not report a URL in time')), 5000);
  });
}

// selectFileAndMeasure clicks the file-row whose path contains pathSubstr
// and returns the milliseconds from click until its rows have both arrived
// (the GET /api/file/{id}/rows response) and painted (one settled frame
// after that), for the first-paint and file-switch budgets below.
async function selectFileAndMeasure(page, pathSubstr) {
  const row = page.locator('.file-row', { hasText: pathSubstr });
  // Matched on this file's own id, not just the "/api/file/.*/rows" shape: a
  // prior selection's fetch can still be in flight when this one clicks
  // (moveSelection's own j/k checks only wait for the file-row highlight to
  // move, not for its rows fetch to finish -- see their own comments), and a
  // bare shape match can resolve this Promise.all on that stale response
  // instead of the one this click actually triggers. FR-17/19/21's
  // background analysis adds real server-side latency variance that made
  // this latent race easy to hit in practice, not just in theory.
  const fileId = await row.getAttribute('data-file');
  const t0 = Date.now();
  await Promise.all([
    page.waitForResponse((r) => r.url().includes(`/api/file/${fileId}/rows`)),
    row.click(),
  ]);
  await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  return Date.now() - t0;
}

// waitForCondition polls fn (which may itself be async, e.g. a fetch to the
// server) sequentially -- not page.waitForFunction's rAF-driven polling,
// which can fire overlapping evaluations of a slow async predicate and
// resolve on a stale one. Used below (FR-7) to wait on server-persisted
// state directly rather than guessing a fixed delay.
async function waitForCondition(fn, timeoutMs = 2000, intervalMs = 25) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await fn()) return true;
    if (Date.now() >= deadline) return false;
    await new Promise((r) => setTimeout(r, intervalMs));
  }
}

async function runWorktreeSmoke() {
  const repoDir = buildRepo();
  const home = mkdtempSync(path.join(tmpdir(), 'flowreview-home-'));
  // let, not const: the FR-7 process-restart check below replaces both with
  // a freshly spawned process/URL over the same FLOWREVIEW_HOME.
  let { proc, url } = await serve(repoDir, home);
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });

  try {
    const t0 = Date.now();
    await page.goto(url);
    await page.waitForSelector('.file-row');
    const paintMs = Date.now() - t0;
    console.log(`session open -> first paint: ${paintMs}ms (budget ${PAINT_BUDGET_MS}ms)`);
    check('performance: first paint is under budget', paintMs < PAINT_BUDGET_MS, `${paintMs}ms`);

    /* 1 -- the file queue shows every changed file */
    const paths = await page.$$eval('.file-row .path', (els) => els.map((e) => e.textContent));
    check('files: the modified file is listed', paths.includes('a.txt'), paths.join(','));
    check('files: the added file is listed', paths.includes('b.txt'), paths.join(','));
    check('files: the 10k-line file is listed', paths.includes('big.js'), paths.join(','));
    check('files: the go function file is listed', paths.includes('fn.go'), paths.join(','));

    /* 2 -- the session panel shows real progress, not a placeholder */
    const summary = await page.textContent('#session-summary');
    check('session: the summary reports hunk progress', /hunks reviewed/.test(summary), summary);

    /* 3 -- selecting a.txt renders its rows side by side, with a paired
       change row (the fixture's one-word edit collapses del+add into one
       row -- see render.pairBlock) */
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    // .bg-add on the new side only happens for an "add" or "change" row;
    // this fixture's only such row is the paired change (its one-word edit).
    const changeCell = page.locator('#diff-rows [data-side="new"].code.bg-add').first();
    await changeCell.waitFor();
    const anchor = await changeCell.evaluate((el) => ({ ...el.dataset }));
    check('rows: a changed line carries file/side/line/hunk/position',
      !!anchor.file && anchor.side === 'new' && !!anchor.line && !!anchor.hunk && !!anchor.position,
      JSON.stringify(anchor));

    /* 4 -- the DOM's anchors are not just present, they are exactly what
       the server rendered for this file: compare a few cells against
       GET /api/file/{id}/rows directly. */
    const aFileId = anchor.file;
    const apiRows = await page.evaluate((id) => fetch(`/api/file/${id}/rows`).then((r) => r.json()), aFileId);
    const changeRow = apiRows.find((r) => r.kind === 'change');
    check('rows: a change row exists in the API response', !!changeRow, JSON.stringify(apiRows));
    if (changeRow) {
      // .bg-del/.bg-add narrow to the change row's own cells: every cell in
      // the hunk shares the same data-hunk, but only the change row's old
      // side is tinted bg-del and its new side bg-add.
      const oldCell = await page.locator(`#diff-rows [data-hunk="${changeRow.hunk_id}"][data-side="old"].code.bg-del`).first().evaluate((el) => ({ ...el.dataset }));
      const newCell = await page.locator(`#diff-rows [data-hunk="${changeRow.hunk_id}"][data-side="new"].code.bg-add`).first().evaluate((el) => ({ ...el.dataset }));
      check('rows: DOM old-side anchor matches the API row exactly',
        Number(oldCell.line) === changeRow.old_no && Number(oldCell.position) === changeRow.old_position,
        `DOM=${JSON.stringify(oldCell)} API old_no/old_position=${changeRow.old_no}/${changeRow.old_position}`);
      check('rows: DOM new-side anchor matches the API row exactly',
        Number(newCell.line) === changeRow.new_no && Number(newCell.position) === changeRow.new_position,
        `DOM=${JSON.stringify(newCell)} API new_no/new_position=${changeRow.new_no}/${changeRow.new_position}`);
    }

    /* 5 -- j/k move the file selection */
    const selectedPath = () => page.locator('.file-row.selected .path').first().textContent();
    const before = await selectedPath();
    await page.keyboard.press('j');
    await page.waitForFunction((prev) => {
      const el = document.querySelector('.file-row.selected .path');
      return el && el.textContent !== prev;
    }, before, { timeout: 2000 }).catch(() => {});
    const after = await selectedPath();
    check('keyboard: j moves to the next file', after !== before, `${before} -> ${after}`);

    await page.keyboard.press('k');
    await page.waitForFunction((prev) => {
      const el = document.querySelector('.file-row.selected .path');
      return el && el.textContent === prev;
    }, before, { timeout: 2000 }).catch(() => {});
    const back = await selectedPath();
    check('keyboard: k moves back to the previous file', back === before, `${back} vs ${before}`);

    /* 6 -- n/p jump between hunks, independent of which file (big.js, with
       ~100 scattered single-line hunks, is the file that actually exercises
       this -- a.txt only has one). */
    await selectFileAndMeasure(page, 'big.js');
    const diffScrollTop = () => page.evaluate(() => document.getElementById('diff').scrollTop);
    // waitForFunction after each press (falling back to an immediate read on
    // timeout, same as before) rather than reading scrollTop right away --
    // FR-17/19/21's background analysis (go/packages Load, real CPU work)
    // now starts the moment the page loads, and can occasionally delay this
    // tab's own next animation frame just long enough to read pre-press
    // state without it.
    const beforeN = await diffScrollTop();
    await page.keyboard.press('n');
    await page.waitForFunction((prev) => document.getElementById('diff').scrollTop > prev, beforeN, { timeout: 3000 }).catch(() => {});
    const afterN1 = await diffScrollTop();
    check('keyboard: n jumps forward to a hunk', afterN1 > beforeN, `${beforeN} -> ${afterN1}`);

    await page.keyboard.press('n');
    await page.waitForFunction((prev) => document.getElementById('diff').scrollTop > prev, afterN1, { timeout: 3000 }).catch(() => {});
    const afterN2 = await diffScrollTop();
    check('keyboard: n again jumps further forward', afterN2 > afterN1, `${afterN1} -> ${afterN2}`);

    await page.keyboard.press('p');
    await page.waitForFunction((prev) => document.getElementById('diff').scrollTop === prev, afterN1, { timeout: 3000 }).catch(() => {});
    const afterP = await diffScrollTop();
    check('keyboard: p jumps back to the previous hunk', afterP === afterN1, `${afterN2} -> ${afterP}, want ${afterN1}`);

    /* 7 -- marking a hunk reviewed updates the file queue's badge */
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    const hunkId = await page.getAttribute('#diff-rows [data-hunk]', 'data-hunk');
    await page.evaluate((hid) => fetch('/api/state', {
      method: 'POST',
      body: JSON.stringify({ hunk_id: hid, reviewed: true }),
    }), hunkId);
    await page.reload();
    await page.waitForSelector('.file-row');
    const badgeCount = await page.locator('.file-row .reviewed-badge').count();
    check('review state: a fully reviewed file shows a badge after reload', badgeCount >= 1, String(badgeCount));

    /* 8 -- the API behind the UI responds */
    const health = await page.evaluate(() => fetch('/api/health').then((r) => r.json()));
    check('api: /api/health reports ok', health.ok === true, JSON.stringify(health));

    /* 9 -- performance budget: first paint of the 10k-line file */
    const bigPaintMs = await selectFileAndMeasure(page, 'big.js');
    console.log(`big.js select -> first paint: ${bigPaintMs}ms (budget ${PAINT_BUDGET_MS}ms)`);
    check('performance: 10k-line file first paint is under budget', bigPaintMs < PAINT_BUDGET_MS, `${bigPaintMs}ms`);
    await page.waitForSelector('#diff-rows [data-hunk]');

    /* 10 -- performance budget: switching between two already-opened,
       normal-sized files is near instant (the render cache hit) */
    await selectFileAndMeasure(page, 'b.txt');
    const switchMs = await selectFileAndMeasure(page, 'a.txt');
    console.log(`a.txt <-> b.txt switch: ${switchMs}ms (budget ${SWITCH_BUDGET_MS}ms)`);
    check('performance: file switch between two normal files is under budget', switchMs < SWITCH_BUDGET_MS, `${switchMs}ms`);

    /* 11 -- performance budget: scrolling the 10k-line file top to bottom
       produces no long task over the budget */
    await selectFileAndMeasure(page, 'big.js');
    await page.evaluate(() => {
      window.__longTasks = [];
      const obs = new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) window.__longTasks.push(entry.duration);
      });
      obs.observe({ entryTypes: ['longtask'] });
      window.__longTaskObserver = obs;
    });
    await page.evaluate(() => new Promise((resolve) => {
      const diff = document.getElementById('diff');
      const maxScroll = diff.scrollHeight - diff.clientHeight;
      const steps = 80;
      let i = 0;
      function step() {
        diff.scrollTop = Math.round((maxScroll * i) / steps);
        i++;
        if (i <= steps) {
          requestAnimationFrame(step);
        } else {
          setTimeout(resolve, 150); // let the last scheduled render settle
        }
      }
      requestAnimationFrame(step);
    }));
    const longTasks = await page.evaluate(() => {
      window.__longTaskObserver.disconnect();
      return window.__longTasks;
    });
    const worstLongTask = longTasks.length ? Math.max(...longTasks) : 0;
    console.log(`scrolling big.js: ${longTasks.length} long task(s), worst ${worstLongTask.toFixed(1)}ms (budget ${LONGTASK_BUDGET_MS}ms)`);
    check('performance: no long task over budget while scrolling the 10k-line file',
      longTasks.every((d) => d <= LONGTASK_BUDGET_MS), JSON.stringify(longTasks));

    /* 12 -- FR-8: expanding a hunk's enclosing function splices extra
       context rows in without touching the hunk's own row anchors, and
       collapsing restores both the original rows and the scroll position. */
    await selectFileAndMeasure(page, 'fn.go');
    await page.waitForSelector('#diff-rows [data-hunk]');
    const fnHunkId = await page.getAttribute('#diff-rows [data-hunk]', 'data-hunk');
    const changeCellSelector = `#diff-rows [data-hunk="${fnHunkId}"][data-side="new"].code.bg-add`;

    await page.evaluate(() => { document.getElementById('diff').scrollTop = 40; });
    const scrollBeforeExpand = await diffScrollTop();
    const rowCountBefore = await page.evaluate(() => state.rows.length);
    const anchorBefore = await page.locator(changeCellSelector).first().evaluate((el) => ({ ...el.dataset }));

    await page.locator(`.expand-btn[data-hunk="${fnHunkId}"]`).click();
    await page.waitForFunction((before) => state.rows.length > before, rowCountBefore, { timeout: 2000 });
    const rowCountExpanded = await page.evaluate(() => state.rows.length);
    check('expand: expanding a hunk adds context rows', rowCountExpanded > rowCountBefore,
      `${rowCountBefore} -> ${rowCountExpanded}`);

    const anchorExpanded = await page.locator(changeCellSelector).first().evaluate((el) => ({ ...el.dataset }));
    check('expand: the hunk\'s change-row anchor is unchanged after expanding',
      anchorExpanded.line === anchorBefore.line && anchorExpanded.position === anchorBefore.position &&
      anchorExpanded.hunk === anchorBefore.hunk,
      `before=${JSON.stringify(anchorBefore)} after=${JSON.stringify(anchorExpanded)}`);

    await page.locator(`.expand-btn[data-hunk="${fnHunkId}"]`).click();
    await page.waitForFunction((before) => state.rows.length === before, rowCountBefore, { timeout: 2000 });
    const scrollAfterCollapse = await diffScrollTop();
    check('expand: collapsing restores the original scroll position',
      scrollAfterCollapse === scrollBeforeExpand, `${scrollBeforeExpand} -> ${scrollAfterCollapse}`);

    const anchorCollapsed = await page.locator(changeCellSelector).first().evaluate((el) => ({ ...el.dataset }));
    check('expand: the hunk\'s change-row anchor is unchanged after collapsing',
      anchorCollapsed.line === anchorBefore.line && anchorCollapsed.position === anchorBefore.position &&
      anchorCollapsed.hunk === anchorBefore.hunk,
      `before=${JSON.stringify(anchorBefore)} collapsed=${JSON.stringify(anchorCollapsed)}`);
    /* ======================== FR-7: review queue, reviewed state, progress ======================== */

    /* 12 -- `r` toggles the current hunk (the one nearest above the top of
       the viewport) and both dims it and updates the header progress
       fraction. big.js has ~100 hunks, so marking its first one reviewed
       does not complete the file -- keeps this check independent of the
       completion/auto-advance behavior exercised in 13. */
    await selectFileAndMeasure(page, 'big.js'); // selectFile resets scrollTop to 0
    await page.waitForSelector('#diff-rows [data-hunk]');
    const countBefore12 = await page.textContent('#topbar-count');
    await page.keyboard.press('r');
    await page.waitForFunction(
      (before) => document.getElementById('topbar-count').textContent !== before,
      countBefore12, { timeout: 2000 },
    ).catch(() => {});
    const countAfter12 = await page.textContent('#topbar-count');
    check('FR-7: r toggles the current hunk and updates the header progress fraction',
      countAfter12 !== countBefore12, `${countBefore12} -> ${countAfter12}`);
    const dimmedCount = await page.locator('.hunk-header.hunk-reviewed').count();
    check('FR-7: a reviewed hunk header is visually dimmed', dimmedCount >= 1, String(dimmedCount));

    /* 13 -- `R` toggles every hunk of the whole file. b.txt is a one-hunk
       added file, so R both completes it (file-queue state flips to
       "reviewed") and triggers the completion flash + auto-advance to the
       next unreviewed file. */
    await selectFileAndMeasure(page, 'b.txt');
    await page.keyboard.press('R');
    await page.waitForFunction(() => {
      const el = document.querySelector('.file-row.selected');
      return el && el.classList.contains('reviewed');
    }, { timeout: 2000 }).catch(() => {});
    const bRowClass = await page.evaluate(() => {
      const rows = [...document.querySelectorAll('.file-row')];
      const b = rows.find((r) => r.querySelector('.path').textContent === 'b.txt');
      return b ? b.className : '';
    });
    check('FR-7: R marks every hunk of the file reviewed', /\breviewed\b/.test(bRowClass), bRowClass);

    const selectedPathBeforeAdvance = await page.locator('.file-row.selected .path').first().textContent();
    await page.waitForFunction(
      (prev) => {
        const el = document.querySelector('.file-row.selected .path');
        return el && el.textContent !== prev;
      },
      selectedPathBeforeAdvance,
      { timeout: 2000 },
    ).catch(() => {});
    const selectedPathAfterAdvance = await page.locator('.file-row.selected .path').first().textContent();
    check('FR-7: completing a file auto-advances to the next unreviewed one',
      selectedPathAfterAdvance !== selectedPathBeforeAdvance,
      `${selectedPathBeforeAdvance} -> ${selectedPathAfterAdvance}`);

    /* 14 -- `?` opens the keyboard-shortcut help overlay. */
    await page.keyboard.press('?');
    await page.waitForSelector('#help-overlay:not([hidden])');
    const helpText = await page.textContent('#help-overlay');
    check('FR-7: ? shows a help overlay listing the shortcuts',
      /hunk/.test(helpText) && /reviewed/.test(helpText), helpText);
    await page.keyboard.press('Escape');
    // Not waitForSelector: an element matching [hidden] is never "visible",
    // so that would wait for the state it is already in and time out.
    await page.waitForFunction(() => document.getElementById('help-overlay').hidden);

    /* 15 -- FR-7 accept: mark a hunk with r, reload the page AND restart the
       process (same FLOWREVIEW_HOME) -- state persists; progress counts
       in /api/session match what was marked.
       State checks below hit the server directly with node's own fetch,
       not page.evaluate: confirming persistence is exactly the server's
       job, independent of the browser, and page.waitForFunction's rAF-driven
       polling can overlap evaluations of a slow async predicate and resolve
       on a stale one (waitForCondition polls sequentially instead). */
    await selectFileAndMeasure(page, 'a.txt'); // single-hunk file, easy to reason about
    await page.waitForSelector('#diff-rows [data-hunk]');
    const aHunkId = await page.getAttribute('#diff-rows [data-hunk]', 'data-hunk');

    // Start from a known "unreviewed" state regardless of what earlier checks did.
    await fetch(`${url}api/state`, { method: 'POST', body: JSON.stringify({ hunk_id: aHunkId, reviewed: false }) });
    await waitForCondition(async () => {
      const s = await fetch(`${url}api/session`).then((r) => r.json());
      return !(s.hunk_states && s.hunk_states[aHunkId] === 'reviewed');
    });

    await page.reload();
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    await page.keyboard.press('r'); // a.txt's one hunk is always "current" at scrollTop 0
    const marked = await waitForCondition(async () => {
      const s = await fetch(`${url}api/session`).then((r) => r.json());
      return !!(s.hunk_states && s.hunk_states[aHunkId] === 'reviewed');
    });
    check('FR-7 accept: r persists the hunk as reviewed', marked, 'timed out waiting for the server to see it reviewed');

    const sessBeforeRestart = await fetch(`${url}api/session`).then((r) => r.json());
    await page.reload(); // same process, reload the page
    await page.waitForSelector('.file-row');
    const sessAfterPageReload = await fetch(`${url}api/session`).then((r) => r.json());
    check('FR-7 accept: reviewed state survives a page reload',
      sessAfterPageReload.progress.reviewed_hunks === sessBeforeRestart.progress.reviewed_hunks && sessAfterPageReload.progress.reviewed_hunks > 0,
      `${JSON.stringify(sessBeforeRestart.progress)} -> ${JSON.stringify(sessAfterPageReload.progress)}`);

    proc.kill();
    await new Promise((resolve) => proc.once('exit', resolve));
    ({ proc, url } = await serve(repoDir, home)); // same FLOWREVIEW_HOME, a brand new process
    await page.goto(url);
    await page.waitForSelector('.file-row');
    const sessAfterRestart = await fetch(`${url}api/session`).then((r) => r.json());
    check('FR-7 accept: reviewed state survives a process restart (same FLOWREVIEW_HOME)',
      sessAfterRestart.progress.reviewed_hunks === sessBeforeRestart.progress.reviewed_hunks && sessAfterRestart.progress.reviewed_hunks > 0,
      `${JSON.stringify(sessBeforeRestart.progress)} -> ${JSON.stringify(sessAfterRestart.progress)}`);

    /* ======================== FR-9: comment drafts ======================== */

    /* 16 -- inline draft: click the line-number gutter to open a compose
       card, type, save; the card renders through mdlite. a.txt's single
       hunk changes its line 2 ("line two" -> "line TWO"). */
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    await page.locator('#diff-rows [data-side="new"][data-line="2"].lineno').click();
    await page.waitForSelector('#comment-new textarea');
    await page.fill('#comment-new textarea', 'needs a **look**');
    await page.click('#comment-new [data-action="save"]');
    await page.waitForSelector('#comment-list .comment-card');
    const draftBodyHTML = await page.locator('#comment-list .comment-card .body-view').first().innerHTML();
    check('FR-9: a saved inline draft renders through mdlite',
      /<strong>look<\/strong>/.test(draftBodyHTML), draftBodyHTML);
    const draftHasMarker = await page.locator('#diff-rows [data-side="new"][data-line="2"].lineno .marker-dot.draft').count();
    check('FR-9: the line carrying a draft shows a gutter marker', draftHasMarker >= 1, String(draftHasMarker));

    /* 17 -- FR-9 accept: draft -> reload -> still there. */
    await page.reload();
    await page.waitForSelector('.file-row');
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#comment-list .comment-card');
    const afterReloadText = await page.textContent('#comment-list .comment-card .body-view');
    check('FR-9 accept: a draft survives a page reload', /look/.test(afterReloadText), afterReloadText);

    /* 18 -- FR-9 accept: stale draft flagged after a fixture refresh. Change
       the hunk the draft is anchored to (a second edit to the same line) and
       trigger POST /api/reload -- the endpoint this slice adds so a running
       server notices the working tree moved without a process restart --
       then confirm the comment shows the stale warning. */
    writeFileSync(path.join(repoDir, 'a.txt'), 'line one\nline TWO changed again\nline three\n');
    const reloadResp = await fetch(`${url}api/reload`, { method: 'POST' }).then((r) => r.json());
    check('FR-9: POST /api/reload reconciles against the changed fixture',
      reloadResp.progress.total_hunks >= 1, JSON.stringify(reloadResp.progress));
    await page.reload();
    await page.waitForSelector('.file-row');
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#comment-list .comment-card.stale');
    const staleWarning = await page.textContent('#comment-list .comment-card.stale .stale-warning');
    check('FR-9 accept: a stale draft is flagged with a warning', /changed/.test(staleWarning), staleWarning);

    /* 19 -- re-anchor: click Re-anchor, then click a line in the diff; the
       comment is re-upserted with a fresh anchor and loses its stale status
       (handleUpsertComment always resets to "draft" on any upsert). */
    await page.click('#comment-list .comment-card.stale [data-action="reanchor"]');
    await page.waitForSelector('.comment-card.reanchoring');
    await page.waitForSelector('#diff-rows [data-hunk]');
    await page.locator('#diff-rows [data-side="new"][data-line="2"].lineno').click();
    const unstaled = await waitForCondition(async () => {
      const s = await fetch(`${url}api/session`).then((r) => r.json());
      return s.comments.length === 1 && s.comments[0].status !== 'stale';
    });
    check('FR-9: re-anchoring a stale comment clears the stale flag', unstaled, 'timed out waiting for a non-stale status');

    /* 20 -- remote comments: stub GET /api/remote-comments (kept simple per
       the slice's own note -- no fake GitHub API needed) and confirm a
       non-outdated one renders inline as a gutter marker at its anchor,
       while an outdated one -- no live line left to anchor it to -- renders
       read-only in the side panel instead. */
    await page.route('**/api/remote-comments', (route) => route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify([
        { id: 1, author: 'reviewer', body: 'looks fine to me', path: 'a.txt', line: 2, side: 'new', created_at: new Date().toISOString() },
        { id: 2, author: 'reviewer', body: 'this was about an old line', path: 'a.txt', line: 1, side: 'new', created_at: new Date().toISOString(), outdated: true },
      ]),
    }));
    await page.reload();
    await page.waitForSelector('.file-row');
    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    const remoteMarker = await page.locator('#diff-rows [data-side="new"][data-line="2"].lineno .marker-dot.remote').count();
    check('FR-9: a non-outdated remote comment renders inline at its anchor', remoteMarker >= 1, String(remoteMarker));
    const outdatedCard = await page.locator('#remote-comment-list .remote-comment-card').count();
    check('FR-9: an outdated remote comment renders read-only in the side panel', outdatedCard >= 1, String(outdatedCard));

    /* ======================== FR-17: flow order ======================== */
    /* 21 -- the file list groups by flow order once GET /api/order finishes
       (a real go/packages Load, seconds not milliseconds -- wait for the
       toggle button to stop reading "computing" rather than guess a fixed
       delay), and the toolbar toggle switches it back to plain diff order. */
    await selectFileAndMeasure(page, 'a.txt');
    const orderReady = await waitForCondition(async () => {
      const text = await page.textContent('#order-toggle');
      return !!text && !/computing/i.test(text);
    }, 60000, 200);
    check('FR-17: flow order finishes computing', orderReady, 'order-toggle still reads "computing" after 60s');

    const listedPaths = () => page.$$eval('.file-row .path', (els) => els.map((e) => e.textContent));
    check('FR-17: flow order groups at least one file under a group title', (await page.locator('.file-group-title').count()) >= 1);
    const flowPaths = await listedPaths();

    await page.click('#order-toggle'); // flow -> diff
    await page.waitForFunction((prev) => {
      const paths = [...document.querySelectorAll('.file-row .path')].map((e) => e.textContent);
      return JSON.stringify(paths) !== prev;
    }, JSON.stringify(flowPaths), { timeout: 2000 }).catch(() => {});
    const diffPaths = await listedPaths();
    check('FR-17: the order toggle changes the displayed file order', JSON.stringify(flowPaths) !== JSON.stringify(diffPaths),
      `flow=${flowPaths.join(',')} diff=${diffPaths.join(',')}`);
    check('FR-17: diff order shows no group titles', (await page.locator('.file-group-title').count()) === 0);

    await page.click('#order-toggle'); // diff -> flow, for the checks below
    await page.waitForFunction(() => document.querySelectorAll('.file-group-title').length > 0, { timeout: 2000 }).catch(() => {});

    /* ======================== FR-19: callers panel ======================== */
    /* 22 -- for fn.go's one hunk (already "current" at scrollTop 0), the
       panel shows Compute's known callers: CallCompute (its file caller.go
       is in the diff, so it is clickable) and TestCompute (its file is
       untouched by the diff, so it renders as plain, non-clickable text). */
    await selectFileAndMeasure(page, 'fn.go');
    await page.waitForSelector('#diff-rows [data-hunk]');
    const callersReady = await waitForCondition(async () => {
      if (await page.getAttribute('#callers-panel', 'hidden') !== null) return false;
      return !/Computing/.test(await page.textContent('#callers-content'));
    }, 60000, 200);
    check('FR-19: the callers panel finishes computing', callersReady, 'callers-content still reads "Computing…" after 60s');

    const callersText = await page.textContent('#callers-content');
    check('FR-19: the callers panel names the changed function Compute', /Compute/.test(callersText), callersText);
    check('FR-19: the callers panel lists the in-diff caller CallCompute', /CallCompute/.test(callersText), callersText);
    check('FR-19: the callers panel lists the out-of-diff caller TestCompute', /TestCompute/.test(callersText), callersText);

    const inDiffCaller = page.locator('#callers-content .nav-entry.clickable', { hasText: 'CallCompute' });
    check('FR-19: the in-diff caller entry is clickable', (await inDiffCaller.count()) >= 1, callersText);
    const outOfDiffCallerClickable = await page.locator('#callers-content .nav-entry.clickable', { hasText: 'TestCompute' }).count();
    check('FR-19: the out-of-diff caller entry is not clickable', outOfDiffCallerClickable === 0, String(outOfDiffCallerClickable));

    await inDiffCaller.first().click();
    await page.waitForFunction(() => {
      const el = document.querySelector('.file-row.selected .path');
      return el && el.textContent === 'caller.go';
    }, { timeout: 2000 }).catch(() => {});
    const selectedAfterCallerJump = await page.locator('.file-row.selected .path').first().textContent();
    check('FR-19: clicking an in-diff caller jumps to its file', selectedAfterCallerJump === 'caller.go', selectedAfterCallerJump);

    /* 23 -- caller.go's own changed function (unrelated) has no caller at
       all -- the "no callers" case. jumpToLocation's selectFile() flips
       .selected synchronously, before its rows fetch resolves (see
       selectFile's own code), so waiting on "not Computing" alone can read
       fn.go's still-stale Compute card; wait for the new content itself. */
    await page.waitForSelector('#diff-rows [data-hunk]');
    await waitForCondition(async () => /unrelated/.test(await page.textContent('#callers-content')), 5000, 100);
    const callersTextUnrelated = await page.textContent('#callers-content');
    check('FR-19: a changed function with no callers shows "No callers."', /No callers/.test(callersTextUnrelated), callersTextUnrelated);

    /* ======================== FR-21: tests panel ======================== */
    /* 24 -- Compute is reached by TestCompute (FR-21's "reached" state) and
       carries a copyable `go test` command; unrelated is not reached by any
       test (the "unreached" state, with its warning flag). */
    await selectFileAndMeasure(page, 'fn.go');
    await page.waitForSelector('#diff-rows [data-hunk]');
    const testsReady = await waitForCondition(async () => {
      if (await page.getAttribute('#tests-panel', 'hidden') !== null) return false;
      return !/Computing/.test(await page.textContent('#tests-content'));
    }, 60000, 200);
    check('FR-21: the tests panel finishes computing', testsReady, 'tests-content still reads "Computing…" after 60s');

    const testsTextCompute = await page.textContent('#tests-content');
    check('FR-21: a reached function lists the reaching test', /Reached by/.test(testsTextCompute) && /TestCompute/.test(testsTextCompute), testsTextCompute);

    const copyBtn = page.locator('#tests-content [data-action="copy-cmd"]');
    check('FR-21: a reached function shows a copy-command button', (await copyBtn.count()) >= 1, testsTextCompute);
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
    await copyBtn.first().click();
    await page.waitForFunction(() => {
      const btn = document.querySelector('#tests-content [data-action="copy-cmd"]');
      return btn && btn.textContent === 'Copied';
    }, { timeout: 2000 }).catch(() => {});
    check('FR-21: clicking copy confirms with "Copied"', (await copyBtn.first().textContent()) === 'Copied');
    const clipboardText = await page.evaluate(() => navigator.clipboard.readText()).catch(() => '');
    check('FR-21: the clipboard holds the matching `go test` command', /^go test .*-run/.test(clipboardText), clipboardText);

    await selectFileAndMeasure(page, 'caller.go');
    await page.waitForSelector('#diff-rows [data-hunk]');
    await waitForCondition(async () => !/Computing/.test(await page.textContent('#tests-content')), 5000, 100);
    const testsTextUnrelated = await page.textContent('#tests-content');
    check('FR-21: an unreached function shows the warning flag', /no test reaches this/.test(testsTextUnrelated), testsTextUnrelated);

    /* ======================== long-line wrap toggle ======================== */
    /* 25 -- long lines are clipped by default (both sides stay visible);
       "scroll" mode shows a long line at its natural width; the toolbar
       button does the same thing the `w` key does. */
    await selectFileAndMeasure(page, 'long.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    const longCell = page.locator('#diff-rows [data-side="new"].code').first();
    await longCell.waitFor();
    const wrapOffOverflow = await longCell.evaluate((el) => el.scrollWidth - el.clientWidth);
    check('wrap: by default a long line is clipped inside its cell', wrapOffOverflow > 50, String(wrapOffOverflow));

    await page.keyboard.press('w');
    await page.waitForFunction(() => document.body.classList.contains('wrap-lines'), { timeout: 2000 });
    const wrapOnOverflow = await longCell.evaluate((el) => el.scrollWidth - el.clientWidth);
    check('wrap: the `w` key shows the long line at its natural width', wrapOnOverflow <= 1, String(wrapOnOverflow));

    await page.click('#wrap-toggle');
    await page.waitForFunction(() => !document.body.classList.contains('wrap-lines'), { timeout: 2000 });
    const wrapOffAgainOverflow = await longCell.evaluate((el) => el.scrollWidth - el.clientWidth);
    check('wrap: clicking the wrap-toggle button does the same as the `w` key', wrapOffAgainOverflow > 50, String(wrapOffAgainOverflow));

    /* ======================== every shortcut also has a button ======================== */
    /* 26 -- the pre-existing shortcut-only actions (prev/next file, prev/next
       hunk, mark hunk/file reviewed, comment, help) each get a visible,
       clickable control too; clicking it does the same thing its key does. */
    await selectFileAndMeasure(page, 'a.txt');
    const beforeTbNextFile = await page.locator('.file-row.selected .path').first().textContent();
    await page.click('#tb-next-file');
    await page.waitForFunction((prev) => {
      const el = document.querySelector('.file-row.selected .path');
      return el && el.textContent !== prev;
    }, beforeTbNextFile, { timeout: 2000 }).catch(() => {});
    const afterTbNextFile = await page.locator('.file-row.selected .path').first().textContent();
    check('toolbar: the next-file button does the same as `j`', afterTbNextFile !== beforeTbNextFile, `${beforeTbNextFile} -> ${afterTbNextFile}`);

    await page.click('#tb-prev-file');
    await page.waitForFunction((prev) => {
      const el = document.querySelector('.file-row.selected .path');
      return el && el.textContent === prev;
    }, beforeTbNextFile, { timeout: 2000 }).catch(() => {});
    const afterTbPrevFile = await page.locator('.file-row.selected .path').first().textContent();
    check('toolbar: the prev-file button does the same as `k`', afterTbPrevFile === beforeTbNextFile, `${afterTbPrevFile} vs ${beforeTbNextFile}`);

    await selectFileAndMeasure(page, 'big.js');
    const beforeTbHunk = await diffScrollTop();
    await page.click('#tb-next-hunk');
    await page.waitForFunction((prev) => document.getElementById('diff').scrollTop > prev, beforeTbHunk, { timeout: 3000 }).catch(() => {});
    const afterTbNextHunk = await diffScrollTop();
    check('toolbar: the next-hunk button does the same as `n`', afterTbNextHunk > beforeTbHunk, `${beforeTbHunk} -> ${afterTbNextHunk}`);
    await page.click('#tb-prev-hunk');
    await page.waitForFunction((prev) => document.getElementById('diff').scrollTop === prev, beforeTbHunk, { timeout: 3000 }).catch(() => {});
    const afterTbPrevHunk = await diffScrollTop();
    check('toolbar: the prev-hunk button does the same as `p`', afterTbPrevHunk === beforeTbHunk, `${afterTbNextHunk} -> ${afterTbPrevHunk}`);

    await selectFileAndMeasure(page, 'a.txt');
    await page.waitForSelector('#diff-rows [data-hunk]');
    const countBeforeToggleBtn = await page.textContent('#topbar-count');
    await page.locator('.reviewed-toggle').first().click();
    await page.waitForFunction((before) => document.getElementById('topbar-count').textContent !== before, countBeforeToggleBtn, { timeout: 2000 }).catch(() => {});
    const countAfterToggleBtn = await page.textContent('#topbar-count');
    check('hunk header: the reviewed-toggle button does the same as `r`', countAfterToggleBtn !== countBeforeToggleBtn, `${countBeforeToggleBtn} -> ${countAfterToggleBtn}`);
    await page.locator('.reviewed-toggle').first().click(); // revert, so it does not leak into checks above if this file runs again
    await page.waitForFunction((before) => document.getElementById('topbar-count').textContent === before, countBeforeToggleBtn, { timeout: 2000 }).catch(() => {});

    await selectFileAndMeasure(page, 'long.txt');
    const beforeFileReviewed = await page.evaluate(() => {
      const row = [...document.querySelectorAll('.file-row')].find((r) => r.querySelector('.path').textContent === 'long.txt');
      return row ? row.classList.contains('reviewed') : null;
    });
    await page.click('#tb-file-reviewed');
    await page.waitForFunction((before) => {
      const row = [...document.querySelectorAll('.file-row')].find((r) => r.querySelector('.path').textContent === 'long.txt');
      return row && row.classList.contains('reviewed') !== before;
    }, beforeFileReviewed, { timeout: 2000 }).catch(() => {});
    const afterFileReviewed = await page.evaluate(() => {
      const row = [...document.querySelectorAll('.file-row')].find((r) => r.querySelector('.path').textContent === 'long.txt');
      return row ? row.classList.contains('reviewed') : null;
    });
    check('toolbar: the file-reviewed button does the same as `R`', afterFileReviewed !== beforeFileReviewed, `${beforeFileReviewed} -> ${afterFileReviewed}`);

    await selectFileAndMeasure(page, 'fn.go');
    await page.waitForSelector('#diff-rows [data-hunk]');
    await page.locator('#diff-rows [data-side="new"].code[data-line]').first().click(); // focuses the line without opening compose (a code cell, not the lineno gutter)
    check('comment button prerequisite: a code-cell click focuses without opening compose', (await page.locator('#comment-new textarea').count()) === 0);
    await page.click('.comment-btn');
    await page.waitForSelector('#comment-new textarea');
    check('hunk header: the comment button does the same as `c`', (await page.locator('#comment-new textarea').count()) === 1);
    await page.click('#comment-new [data-action="cancel"]');

    await page.click('#help-button');
    await page.waitForSelector('#help-overlay:not([hidden])');
    const toolbarHelpText = await page.textContent('#help-overlay');
    check('topbar: the help button does the same as `?`', /wrap/.test(toolbarHelpText), toolbarHelpText);
    await page.keyboard.press('Escape');
    await page.waitForFunction(() => document.getElementById('help-overlay').hidden);
  } finally {
    await browser.close();
    proc.kill();
  }
}

// ======================== FR-11: GitHub host publish ========================
// A second, independent scenario: a fake GitHub HTTP server (plain Node
// http, not a browser mock -- FLOWREVIEW_GITHUB_API points flowreview's own
// github client at it) plus a local bare "origin" with refs/pull/1/head,
// exactly the fixture approach cmd/flowreview/e2e_test.go's newPRFixture
// uses for the Go-level PR test. This is the part only a real browser can
// confirm: the "Submit review" panel's Preview/Submit/confirm flow and the
// published badge it leaves on the comment card.

const http = require('http');

// buildPRFixtureRepo lays out a bare repo under ".../o/r" (so the "origin"
// URL's last two path segments are literally "o" and "r", which is what
// flowreview's clone resolution needs to recognize this as o/r without
// shelling out to `gh`) with one base commit on main and one PR commit
// pushed to refs/pull/1/head -- a one-line edit to a.txt, so the PR has
// exactly one file and one hunk to comment on.
function buildPRFixtureRepo() {
  const root = mkdtempSync(path.join(tmpdir(), 'flowreview-pr-smoke-'));
  const bareDir = path.join(root, 'o', 'r');
  mkdirSync(bareDir, { recursive: true });
  git(bareDir, {}, 'init', '-q', '--bare', '-b', 'main', '.');

  const dir = mkdtempSync(path.join(tmpdir(), 'flowreview-pr-smoke-work-'));
  const env = {
    GIT_AUTHOR_NAME: 'Smoke', GIT_AUTHOR_EMAIL: 'smoke@example.com',
    GIT_COMMITTER_NAME: 'Smoke', GIT_COMMITTER_EMAIL: 'smoke@example.com',
  };
  git(dir, env, 'init', '-q', '-b', 'main', '.');
  git(dir, env, 'config', 'user.name', 'Smoke');
  git(dir, env, 'config', 'user.email', 'smoke@example.com');
  git(dir, env, 'remote', 'add', 'origin', 'file://' + bareDir);

  writeFileSync(path.join(dir, 'a.txt'), 'line one\nline two\n');
  git(dir, env, 'add', 'a.txt');
  git(dir, { ...env, GIT_AUTHOR_DATE: '2021-01-01T00:00:00Z', GIT_COMMITTER_DATE: '2021-01-01T00:00:00Z' },
    '-c', 'commit.gpgsign=false', 'commit', '-q', '-m', 'root: add a.txt');
  git(dir, env, 'push', '-q', 'origin', 'main');
  const baseSHA = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: dir }).toString().trim();

  git(dir, env, 'checkout', '-q', '-b', 'pr-branch');
  writeFileSync(path.join(dir, 'a.txt'), 'line one\nline TWO\n');
  git(dir, env, 'add', 'a.txt');
  git(dir, { ...env, GIT_AUTHOR_DATE: '2021-01-01T00:01:00Z', GIT_COMMITTER_DATE: '2021-01-01T00:01:00Z' },
    '-c', 'commit.gpgsign=false', 'commit', '-q', '-m', 'pr: edit a.txt');
  git(dir, env, 'push', '-q', 'origin', 'pr-branch:refs/pull/1/head');
  const headSHA = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: dir }).toString().trim();
  git(dir, env, 'checkout', '-q', 'main');

  const diffText = execFileSync('git', ['diff', baseSHA, headSHA], { cwd: dir }).toString();
  return { dir, baseSHA, headSHA, diffText };
}

// startFakeGitHub answers exactly the requests flowreview's github client
// makes while opening this PR and then submitting a review for it: the PR
// resource (as JSON, or as the v3 diff when Accept asks for it), its two
// comment-list endpoints (empty -- nothing to reconcile against), and
// POST .../reviews, which always succeeds here (the 422-fallback path is
// already covered by the recorded-HTTP Go tests in internal/host/github).
function startFakeGitHub(prJSON, diffText) {
  const server = http.createServer((req, res) => {
    if (req.url === '/repos/o/r/pulls/1' && req.method === 'GET') {
      if (req.headers.accept === 'application/vnd.github.v3.diff') {
        res.writeHead(200, { 'Content-Type': 'text/plain' });
        res.end(diffText);
      } else {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify(prJSON));
      }
      return;
    }
    if ((req.url.startsWith('/repos/o/r/pulls/1/comments') || req.url.startsWith('/repos/o/r/issues/1/comments')) && req.method === 'GET') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end('[]');
      return;
    }
    if (req.url === '/repos/o/r/pulls/1/reviews' && req.method === 'POST') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ id: 555, html_url: 'https://github.example/o/r/pull/1#pullrequestreview-555' }));
      return;
    }
    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ message: 'not found in fake GitHub: ' + req.method + ' ' + req.url }));
  });
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(server)));
}

async function runPRPublishSmoke() {
  const { dir, baseSHA, headSHA, diffText } = buildPRFixtureRepo();
  const prJSON = {
    number: 1, title: 'Smoke PR title', body: '', state: 'open', merged: false, draft: false,
    html_url: 'https://github.example/o/r/pull/1',
    user: { login: 'smoke' },
    base: { ref: 'main', sha: baseSHA },
    head: { ref: 'pr-branch', sha: headSHA, repo: { clone_url: 'https://github.example/o/r.git' } },
  };
  const fakeGitHub = await startFakeGitHub(prJSON, diffText);
  const home = mkdtempSync(path.join(tmpdir(), 'flowreview-pr-home-'));
  const { proc, url } = await serve(dir, home, ['pr', 'o/r#1'], {
    FLOWREVIEW_GITHUB_API: `http://127.0.0.1:${fakeGitHub.address().port}`,
    GITHUB_TOKEN: 'smoke-test-token',
  });

  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });
  try {
    await page.goto(url);
    await page.waitForSelector('.file-row');

    /* draft -- an inline comment on a.txt's one changed line */
    await page.locator('.file-row', { hasText: 'a.txt' }).click();
    await page.waitForSelector('#diff-rows [data-hunk]');
    await page.locator('#diff-rows [data-side="new"][data-line="2"].lineno').click();
    await page.waitForSelector('#comment-new textarea');
    await page.fill('#comment-new textarea', 'needs a look before merge');
    await page.click('#comment-new [data-action="save"]');
    await page.waitForSelector('#comment-list .comment-card');

    /* preview -- the panel is visible (PR session) and reports the draft */
    const panelVisible = await page.isVisible('#publish-panel');
    check('FR-11: the submit-review panel is shown for a PR session', panelVisible);
    await page.click('#publish-preview');
    await page.waitForFunction(() => document.getElementById('publish-preflight').textContent.trim() !== '');
    const preflightText = await page.textContent('#publish-preflight');
    check('FR-11: preview reports exactly one inline comment, no general, none excluded',
      /1 inline, 0 general/.test(preflightText), preflightText);

    /* submit -- with its own confirm step before anything is sent */
    await page.fill('#publish-body', 'Looks good overall.');
    await page.click('#publish-submit');
    await page.waitForSelector('#publish-confirm:not([hidden])');
    const confirmText = await page.textContent('#publish-confirm-summary');
    check('FR-11: the confirm step summarizes what will be submitted',
      /1 inline and 0 general/.test(confirmText), confirmText);
    await page.click('#publish-confirm-yes');

    /* badge shows published */
    await page.waitForSelector('#comment-list .comment-card.published', { timeout: 5000 });
    const badgeText = await page.textContent('#comment-list .comment-card .publish-badge.published');
    check('FR-11 accept: submitting marks the draft published', /Published/.test(badgeText), badgeText);
    const stillEditable = await page.locator('#comment-list .comment-card.published [data-action="edit"]').count();
    check('FR-11: a published comment renders read-only (no Edit button)', stillEditable === 0, String(stillEditable));
  } finally {
    await browser.close();
    proc.kill();
    fakeGitHub.close();
  }
}

(async () => {
  await runWorktreeSmoke();
  await runPRPublishSmoke();
  console.log(failures ? `\n${failures} failure(s)` : '\nall checks passed');
  process.exit(failures ? 1 : 0);
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
