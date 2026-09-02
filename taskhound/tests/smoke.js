'use strict';
// Drives the real board in headless Chromium. Everything here is something the
// Go tests cannot see: which column a card lands in, whether a drag actually
// moves it, whether the drawer writes back, whether the poll notices the CLI.
//
//   npm install playwright-core
//   PLAYWRIGHT_BROWSERS_PATH=./pw-browsers npx playwright-core install chromium-headless-shell
//   PLAYWRIGHT_BROWSERS_PATH=./pw-browsers node tests/smoke.js
//
// It needs Playwright's own chromium: corporate-managed Chrome refuses to be
// automated ("remote debugging is disallowed by the system admin").

const { chromium } = require('playwright-core');
const { execFileSync, spawn } = require('child_process');
const { mkdtempSync } = require('fs');
const { tmpdir } = require('os');
const path = require('path');

const TH = path.resolve(__dirname, '..', 'bin', 'th');
const board = mkdtempSync(path.join(tmpdir(), 'th-smoke-'));
const th = (...args) => execFileSync(TH, args, { cwd: board, encoding: 'utf8' }).trim();

let failures = 0;
function check(name, cond, detail = '') {
  console.log(`${cond ? 'PASS' : 'FAIL'}  ${name}${cond ? '' : '  -- ' + detail}`);
  if (!cond) failures++;
}

// One issue per column, so every branch of the layout is exercised at once.
function seed() {
  th('init');
  const blocker = th('add', 'The blocker');                       // ready
  th('add', 'Waiting on the blocker', '--blocked-by', blocker);   // blocked
  const doing = th('add', 'Underway');
  th('update', doing, '--status', 'doing');                       // doing
  const done = th('add', 'Finished');
  th('update', done, '--status', 'done');                         // done
  th('add', 'Urgent', '--priority', 'must');                      // ready, must
  return { blocker, doing, done };
}

function serve() {
  const proc = spawn(TH, ['ui', '--port', '0'], { cwd: board });
  return new Promise((resolve, reject) => {
    let buf = '';
    proc.stdout.on('data', d => {
      buf += d;
      const m = buf.match(/http:\/\/127\.0\.0\.1:\d+/);
      if (m) resolve({ proc, url: m[0] });
    });
    proc.on('error', reject);
    setTimeout(() => reject(new Error('th ui did not report a URL')), 5000);
  });
}

const cardIn = (page, col, id) => page.locator(`.col[data-col="${col}"] .card[data-id="${id}"]`);
const statusOf = id => JSON.parse(th('show', id, '--json')).status;

(async () => {
  const ids = seed();
  const { proc, url } = await serve();
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1400, height: 900 } });

  try {
    await page.goto(url);
    await page.waitForSelector('.card');

    /* 1 — every column holds the card it should */
    check('columns: blocked holds the blocked issue', await cardIn(page, 'blocked', 'TH-2').count() === 1);
    check('columns: ready holds the unblocked issue', await cardIn(page, 'ready', 'TH-1').count() === 1);
    check('columns: doing holds work in flight', await cardIn(page, 'doing', 'TH-3').count() === 1);
    check('columns: done holds finished work', await cardIn(page, 'done', 'TH-4').count() === 1);
    check('card: a blocked card names its blocker',
      (await cardIn(page, 'blocked', 'TH-2').textContent()).includes('TH-1'));
    check('card: a non-default priority is shown',
      (await cardIn(page, 'ready', 'TH-5').textContent()).toLowerCase().includes('must'));
    check('card: a normal priority is not',
      !(await cardIn(page, 'ready', 'TH-1').textContent()).toLowerCase().includes('normal'));

    /* 2 — dragging a card moves it, and the move is written to the file */
    await page.dragAndDrop('.card[data-id="TH-1"]', '.col[data-col="doing"]');
    await page.waitForSelector('.col[data-col="doing"] .card[data-id="TH-1"]');
    check('drag: the card moved column', await cardIn(page, 'doing', 'TH-1').count() === 1);
    check('drag: the status was written to the board', statusOf('TH-1') === 'doing', statusOf('TH-1'));

    // ...and finishing the blocker releases what it was blocking.
    await page.dragAndDrop('.card[data-id="TH-1"]', '.col[data-col="done"]');
    await page.waitForSelector('.col[data-col="ready"] .card[data-id="TH-2"]');
    check('drag: closing a blocker frees the issue it blocked',
      await cardIn(page, 'ready', 'TH-2').count() === 1);

    /* 3 — the drawer writes title, description and blockers back */
    await page.click('.card[data-id="TH-3"]');
    await page.waitForSelector('#drawer.open');
    await page.fill('#d-title', 'Renamed in the drawer');
    await page.click('#edit-desc');
    await page.fill('#d-desc', '## Heading\n\n- [x] ticked\n- [ ] unticked');
    await page.fill('#d-blocked', 'TH-2');
    await page.click('#save');
    await page.waitForSelector('#drawer', { state: 'hidden' });

    const edited = JSON.parse(th('show', 'TH-3', '--json'));
    check('drawer: title saved', edited.title === 'Renamed in the drawer', edited.title);
    check('drawer: description saved', edited.description.includes('## Heading'), edited.description);
    check('drawer: blocker saved', edited.blocked_by.join() === 'TH-2', edited.blocked_by.join());

    /* 4 — the description renders as markdown, and the pencil shows the source */
    await page.click('.card[data-id="TH-3"]');
    await page.waitForSelector('#drawer.open');
    check('markdown: heading rendered', await page.locator('#d-desc-view h2').count() === 1);
    check('markdown: ticked box rendered',
      await page.locator('#d-desc-view li.task.done input:checked').count() === 1);
    check('markdown: unticked box rendered',
      await page.locator('#d-desc-view li.task:not(.done) input').count() === 1);
    check('markdown: source hidden until asked for', await page.locator('#d-desc').isHidden());
    await page.click('#edit-desc');
    check('markdown: pencil reveals the plain text', await page.locator('#d-desc').isVisible());
    check('markdown: the plain text is the source', (await page.inputValue('#d-desc')).includes('- [x] ticked'));

    /* 5 — a comment lands on the issue and shows on the card */
    await page.fill('#d-comment', 'left from the board');
    await page.click('#add-comment');
    await page.waitForFunction(() =>
      document.querySelectorAll('#comments .comment').length > 0);
    check('comment: written to the board',
      JSON.parse(th('show', 'TH-3', '--json')).comments.some(c => c.body === 'left from the board'));
    await page.click('#close');
    check('drawer: the close button closes it', await page.locator('#drawer.open').count() === 0);
    check('comment: the card shows a comment count',
      (await page.locator('.card[data-id="TH-3"]').textContent()).includes('1'));

    /* 6 — the poll notices an issue filed by the CLI while the page is open */
    const late = th('add', 'Filed from the CLI mid-session', '--priority', 'must');
    await page.waitForSelector(`.card[data-id="${late}"]`, { timeout: 6000 });
    check('poll: a CLI-filed issue appears without a reload',
      await cardIn(page, 'ready', late).count() === 1);

    /* 7 — a new issue can be filed from the board */
    await page.click('#new');
    await page.waitForSelector('#drawer.open');
    await page.fill('#d-title', 'Filed from the board');
    await page.click('#save');
    await page.waitForSelector('#drawer', { state: 'hidden' });
    check('new: the issue reached the board file',
      th('list').includes('Filed from the board'));

    /* 8 — the API refuses what the CLI refuses */
    await page.click(`.card[data-id="${late}"]`);
    await page.waitForSelector('#drawer.open');
    const titleBefore = JSON.parse(th('show', late, '--json')).title;
    await page.fill('#d-title', '');
    await page.click('#save');
    await page.waitForFunction(() => document.getElementById('err').textContent.trim().length > 0,
      null, { timeout: 5000 }).catch(() => {});
    check('validation: an empty title is refused, with a reason shown',
      (await page.locator('#err').textContent()).trim().length > 0);
    check('validation: the refused write did not reach the board',
      JSON.parse(th('show', late, '--json')).title === titleBefore);
    check('validation: the drawer stays open so the edit is not lost',
      await page.locator('#drawer.open').count() === 1);
  } finally {
    await browser.close();
    proc.kill();
  }

  console.log(failures ? `\n${failures} failure(s)` : '\nall checks passed');
  process.exit(failures ? 1 : 0);
})().catch(err => {
  console.error(err);
  process.exit(1);
});
