'use strict';
const { chromium } = require('playwright-core');

const URL = 'http://localhost:8080';

let failures = 0;
function check(name, cond, detail = '') {
  console.log(`${cond ? 'PASS' : 'FAIL'}  ${name}${cond ? '' : '  -- ' + detail}`);
  if (!cond) failures++;
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 720 } });
  await page.goto(URL);
  await page.evaluate(() => localStorage.clear());
  await page.reload();

  const shapes = () => page.evaluate(() => window.__diagme.shapes);
  const selected = () => page.evaluate(() => window.__diagme.selected);

  /* 1 — text tool: click, type, commit, survives a click elsewhere */
  await page.click('[data-tool="text"]');
  await page.mouse.click(500, 400);
  await page.waitForTimeout(50);
  await page.keyboard.type('hello world');
  await page.waitForTimeout(400);          // past the refocus-guard window
  await page.mouse.click(1000, 600);       // blur -> commit
  await page.waitForTimeout(100);
  let sh = await shapes();
  const txt = sh.find(s => s.type === 'text');
  check('text: shape created with typed content', !!txt && txt.text === 'hello world',
    JSON.stringify(sh));
  check('text: rendered in svg', await page.locator('svg text').count() > 0);

  /* 2 — markdown note: drag-create, type markdown, commit, renders */
  await page.click('[data-tool="note"]');
  await page.mouse.move(200, 150);
  await page.mouse.down();
  await page.mouse.move(520, 380, { steps: 4 });
  await page.mouse.up();
  await page.waitForTimeout(50);
  await page.keyboard.type('# Title\n- item one\n**bold** text');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(100);
  sh = await shapes();
  const note = sh.find(s => s.type === 'note');
  check('note: markdown text stored', !!note && note.text.includes('# Title'),
    JSON.stringify(note));
  check('note: h1 rendered', (await page.locator('.note-body h1').textContent().catch(() => '')) === 'Title');
  check('note: list item rendered', (await page.locator('.note-body li').first().textContent().catch(() => '')) === 'item one');

  /* 3 — marquee: two boxes, drag-select over both */
  await page.keyboard.press('Escape');
  await page.click('[data-tool="rect"]');
  await page.mouse.move(700, 120); await page.mouse.down();
  await page.mouse.move(820, 200, { steps: 3 }); await page.mouse.up();
  await page.click('[data-tool="rect"]');
  await page.mouse.move(700, 250); await page.mouse.down();
  await page.mouse.move(840, 330, { steps: 3 }); await page.mouse.up();
  await page.keyboard.press('Escape');
  await page.click('[data-tool="select"]');
  await page.mouse.move(650, 80); await page.mouse.down();
  await page.mouse.move(900, 400, { steps: 5 }); await page.mouse.up();
  await page.waitForTimeout(50);
  let selIds = await selected();
  check('marquee: selects both boxes', selIds.length === 2, `selected=${JSON.stringify(selIds)}`);

  /* 4 — group move + group delete */
  const before = (await shapes()).filter(s => selIds.includes(s.id)).map(s => s.x);
  await page.mouse.move(760, 160); await page.mouse.down();
  await page.mouse.move(860, 210, { steps: 4 }); await page.mouse.up();
  await page.waitForTimeout(50);
  const after = (await shapes()).filter(s => selIds.includes(s.id)).map(s => s.x);
  check('group move: both boxes shifted',
    after.length === 2 && after.every((x, i) => Math.abs(x - before[i] - 100) < 2),
    `before=${before} after=${after}`);
  const countBefore = (await shapes()).length;
  await page.keyboard.press('Delete');
  await page.waitForTimeout(50);
  const countAfter = (await shapes()).length;
  check('group delete: both removed', countBefore - countAfter === 2,
    `${countBefore} -> ${countAfter}`);

  /* 5 — arrow bend: create arrow, drag mid handle, bend stored */
  await page.click('[data-tool="arrow"]');
  await page.mouse.move(600, 500); await page.mouse.down();
  await page.mouse.move(900, 500, { steps: 3 }); await page.mouse.up();
  await page.waitForTimeout(50);
  await page.mouse.move(750, 500); await page.mouse.down();   // mid handle
  await page.mouse.move(750, 420, { steps: 3 }); await page.mouse.up();
  await page.waitForTimeout(50);
  const arrow = (await shapes()).find(s => s.type === 'arrow');
  check('arrow bend: mid handle sets bend point', !!arrow && arrow.mx != null && Math.abs(arrow.my - 420) < 5,
    JSON.stringify(arrow));

  await browser.close();
  console.log(failures ? `\n${failures} FAILURE(S)` : '\nALL PASS');
  process.exit(failures ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
