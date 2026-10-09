import puppeteer from 'puppeteer-core';
import path from 'path';
import { fileURLToPath } from 'url';
import fs from 'fs';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

import os from 'os';

function getChromePath() {
  if (process.env.CHROME_BIN && fs.existsSync(process.env.CHROME_BIN)) {
    return process.env.CHROME_BIN;
  }
  const possiblePaths = [
    '/usr/bin/google-chrome',
    '/usr/bin/google-chrome-stable',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser'
  ];
  for (const p of possiblePaths) {
    if (fs.existsSync(p)) return p;
  }
  // Dynamically search ~/.cache/puppeteer
  const puppeteerCache = path.join(os.homedir(), '.cache/puppeteer/chrome');
  if (fs.existsSync(puppeteerCache)) {
    const findChrome = (dir) => {
      try {
        const entries = fs.readdirSync(dir, { withFileTypes: true });
        for (const entry of entries) {
          const full = path.join(dir, entry.name);
          if (entry.isFile() && (entry.name === 'chrome' || entry.name === 'chrome.exe')) return full;
          if (entry.isDirectory()) {
            const res = findChrome(full);
            if (res) return res;
          }
        }
      } catch (e) {}
      return null;
    };
    const found = findChrome(puppeteerCache);
    if (found) return found;
  }
  throw new Error('No Chrome/Chromium executable found for E2E tests.');
}

async function runTestSuite() {
  console.log('===============================================================');
  console.log('🚀 AgentLens Automated UI Regression Test Suite');
  console.log('===============================================================');
  const chromePath = getChromePath();
  console.log(`[Setup] Browser binary: ${chromePath}`);

  const browser = await puppeteer.launch({
    executablePath: chromePath,
    headless: 'new',
    args: ['--no-sandbox', '--disable-setuid-sandbox', '--disable-dev-shm-usage', '--window-size=1280,800']
  });

  const page = await browser.newPage();
  await page.setViewport({ width: 1280, height: 800 });

  const htmlPath = path.resolve(__dirname, '../../web/index.html');
  const fileUrl = `file://${htmlPath}`;
  console.log(`[Setup] Target URL: ${fileUrl}\n`);

  const uncaughtErrors = [];
  page.on('pageerror', err => {
    uncaughtErrors.push(`[PageError] ${err.message}`);
  });
  page.on('console', msg => {
    if (msg.type() === 'error') {
      uncaughtErrors.push(`[ConsoleError] ${msg.text()}`);
    }
  });

  await page.goto(fileUrl, { waitUntil: 'load' });

  // -------------------------------------------------------------
  // Test 1: Load Sample & Verify Initial Render
  // -------------------------------------------------------------
  console.log('Test 1: Load Sample & Verify Initial Render...');
  const loadBtn = await page.$('#loadSampleBtn');
  if (!loadBtn) throw new Error('Missing #loadSampleBtn element');
  await loadBtn.click();
  await page.waitForSelector('#graphSection', { visible: true });

  const nodeCountText = await page.$eval('#statTotalNodes', el => el.textContent.trim());
  const totalNodes = parseInt(nodeCountText, 10);
  console.log(`  ✓ Sample trace loaded successfully (${totalNodes} nodes)`);
  if (isNaN(totalNodes) || totalNodes < 10) {
    throw new Error(`Expected at least 10 nodes loaded, got: ${nodeCountText}`);
  }

  // -------------------------------------------------------------
  // Test 2: Viewport & Scroll Regression Test (Drawer Positioning)
  // -------------------------------------------------------------
  console.log('\nTest 2: Viewport & Scroll Regression Test (Drawer visibility when scrolled)...');
  const calleeRows = await page.$$('.callee-row');
  console.log(`  Found ${calleeRows.length} callee rows in Call Graph.`);
  if (calleeRows.length < 5) throw new Error('Expected at least 5 callee rows in Call Graph');

  // Pick the last row (bottom of the call graph)
  const targetRow = calleeRows[calleeRows.length - 1];
  await targetRow.evaluate(el => el.scrollIntoView({ block: 'center' }));
  
  const scrollYBeforeClick = await page.evaluate(() => window.scrollY);
  console.log(`  Scrolled window to scrollY: ${scrollYBeforeClick}px`);

  // Click the row to open inspector drawer
  await targetRow.click();
  await page.waitForSelector('#drawer.open', { visible: true });

  // Measure drawer positioning relative to active browser viewport
  const drawerGeometry = await page.evaluate(() => {
    const drawerEl = document.getElementById('drawer');
    const headerEl = drawerEl.querySelector('.drawer-header') || drawerEl;
    const rect = drawerEl.getBoundingClientRect();
    const headerRect = headerEl.getBoundingClientRect();
    const vh = window.innerHeight;

    return {
      drawer: { top: rect.top, bottom: rect.bottom, height: rect.height },
      header: { top: headerRect.top, bottom: headerRect.bottom },
      vh,
      scrollY: window.scrollY,
      scrollTop: drawerEl.scrollTop
    };
  });

  console.log(`  Drawer geometry: top=${drawerGeometry.drawer.top.toFixed(1)}px, headerTop=${drawerGeometry.header.top.toFixed(1)}px (Viewport height: ${drawerGeometry.vh}px, scrollY: ${drawerGeometry.scrollY}px)`);

  // Verification: Header must be in viewport [0, vh]
  const isHeaderVisibleInViewport = drawerGeometry.header.top >= 0 && drawerGeometry.header.top < drawerGeometry.vh;
  const isOffScreenAbove = drawerGeometry.drawer.bottom <= 0 || drawerGeometry.header.bottom < 50;

  if (!isHeaderVisibleInViewport || isOffScreenAbove) {
    const errorMsg = `❌ UI REGRESSION DETECTED: Drawer is not visible in the viewport when scrolled down! (headerTop: ${drawerGeometry.header.top.toFixed(1)}px, scrollY: ${drawerGeometry.scrollY}px). Expected drawer header top to be within [0, ${drawerGeometry.vh}px].`;
    console.error(`  ${errorMsg}`);
    await browser.close();
    throw new Error(errorMsg);
  }
  console.log('  ✓ Drawer is pinned/visible in viewport despite page scrolling down.');

  // -------------------------------------------------------------
  // Test 3: Drawer Content & Metadata Fidelity
  // -------------------------------------------------------------
  console.log('\nTest 3: Drawer Content & Metadata Fidelity...');
  const title = await page.$eval('#drawerTitle', el => el.textContent.trim());
  const badges = await page.$eval('#drawerBadges', el => el.textContent.trim());
  const args = await page.$eval('#drawerArgs', el => el.textContent.trim());
  const output = await page.$eval('#drawerOutput', el => el.textContent.trim());

  console.log(`  ✓ Title: "${title}", Badges: "${badges.replace(/\s+/g, ' ')}"`);
  console.log(`  ✓ Arguments: ${args.length} chars, Output: ${output.length} chars`);
  if (!title) throw new Error('Drawer title is empty');

  // -------------------------------------------------------------
  // Test 4: Turn Execution Trace Viewport & Interaction Test
  // -------------------------------------------------------------
  console.log('\nTest 4: Turn Execution Trace Viewport & Click Test...');
  const tabTraceBtn = await page.$('#tabTraceBtn');
  await tabTraceBtn.click();
  await page.waitForSelector('#traceViewContainer .turn-card', { visible: true });

  const traceNodes = await page.$$('#traceViewContainer .node-row');
  console.log(`  Found ${traceNodes.length} node rows in Turn Execution Trace.`);
  if (traceNodes.length < 5) throw new Error('Expected at least 5 node rows in Trace view');

  // Scroll down to the last node in Turn 3
  const lastTraceNode = traceNodes[traceNodes.length - 1];
  await lastTraceNode.evaluate(el => el.scrollIntoView({ block: 'center' }));
  const traceScrollY = await page.evaluate(() => window.scrollY);
  console.log(`  Scrolled trace view to scrollY: ${traceScrollY}px`);

  await lastTraceNode.click();
  await page.waitForSelector('#drawer.open', { visible: true });

  const traceDrawerGeometry = await page.evaluate(() => {
    const headerEl = document.querySelector('#drawer .drawer-header') || document.getElementById('drawer');
    const headerRect = headerEl.getBoundingClientRect();
    return {
      headerTop: headerRect.top,
      vh: window.innerHeight,
      scrollY: window.scrollY
    };
  });

  if (traceDrawerGeometry.headerTop < 0 || traceDrawerGeometry.headerTop >= traceDrawerGeometry.vh) {
    const errorMsg = `❌ UI REGRESSION DETECTED in Trace view: Drawer header is offscreen (${traceDrawerGeometry.headerTop.toFixed(1)}px) at scrollY: ${traceDrawerGeometry.scrollY}px`;
    console.error(`  ${errorMsg}`);
    await browser.close();
    throw new Error(errorMsg);
  }
  console.log(`  ✓ Drawer correctly positioned in Trace view (headerTop: ${traceDrawerGeometry.headerTop.toFixed(1)}px).`);

  // -------------------------------------------------------------
  // Test 5: Drawer Close & Re-open State Reset
  // -------------------------------------------------------------
  console.log('\nTest 5: Drawer Close & Reset Test...');
  const closeBtn = await page.$('#closeDrawerBtn');
  await closeBtn.click();
  const isDrawerClosed = await page.evaluate(() => {
    const d = document.getElementById('drawer');
    return !d.classList.contains('open') || window.getComputedStyle(d).display === 'none';
  });
  if (!isDrawerClosed) throw new Error('Drawer failed to close on close button click');
  console.log('  ✓ Drawer closes properly.');

  // -------------------------------------------------------------
  // Test 6: Search & Filter Integrity
  // -------------------------------------------------------------
  console.log('\nTest 6: Search & Filter Integrity...');
  const tabDepsBtn = await page.$('#tabDepsBtn');
  await tabDepsBtn.click();

  await page.select('#filterSelect', 'mcp');
  const mcpRowsCount = await page.$$eval('.callee-row', rows => rows.length);
  console.log(`  ✓ Filtered by 'mcp': ${mcpRowsCount} rows displayed`);
  if (mcpRowsCount === 0) throw new Error('Filter by MCP returned 0 rows');

  await page.type('#searchInput', 'postgres');
  const pgRowsCount = await page.$$eval('.callee-row', rows => rows.length);
  console.log(`  ✓ Filtered by query 'postgres': ${pgRowsCount} rows displayed`);

  // Reset filter
  await page.select('#filterSelect', 'all');
  await page.evaluate(() => { document.getElementById('searchInput').value = ''; });

  // -------------------------------------------------------------
  // Test 7a: Claude Code trace (auto-detected provider)
  // -------------------------------------------------------------
  console.log('\nTest 7a: Claude Code Sample (provider auto-detection)...');
  await page.click('#resetBtn');
  await page.click('#loadClaudeSampleBtn');
  await page.waitForSelector('#graphSection', { visible: true });

  const claudeStats = await page.evaluate(() => ({
    provider: currentProvider && currentProvider.id,
    nodes: document.getElementById('statTotalNodes').textContent.trim(),
    mcp: document.getElementById('statMcpCalls').textContent.trim(),
    failed: document.getElementById('statFailedCalls').textContent.trim(),
    anomalyTypes: currentAnomalies.map(a => a.type).sort(),
    turns: currentTurns.length,
    callees: [...document.querySelectorAll('.callee-row')].map(el => el.textContent.replace(/\s+/g, ' ').trim()),
    trace: document.getElementById('traceViewContainer').textContent
  }));
  console.log(`  Detected provider: ${claudeStats.provider}, nodes=${claudeStats.nodes}, mcp=${claudeStats.mcp}, failed=${claudeStats.failed}, anomalies=${claudeStats.anomalyTypes.join(',')}`);
  if (claudeStats.provider !== 'claude-code') throw new Error(`Expected claude-code provider, got ${claudeStats.provider}`);
  if (claudeStats.nodes !== '6' || claudeStats.mcp !== '2' || claudeStats.failed !== '1') {
    throw new Error(`Unexpected Claude Code stats: ${JSON.stringify(claudeStats)}`);
  }
  if (claudeStats.turns !== 2) throw new Error(`Expected 2 turns (tool results/notifications excluded), got ${claudeStats.turns}`);
  if (JSON.stringify(claudeStats.anomalyTypes) !== JSON.stringify(['auth_expired', 'silent_fallback'])) {
    throw new Error(`Expected auth_expired + silent_fallback, got ${claudeStats.anomalyTypes}`);
  }
  for (const scope of ['Skill: incident-triage', 'Subagent: Explore']) {
    if (!claudeStats.callees.some(c => c.includes(scope))) {
      throw new Error(`Expected ${scope} in the call graph, got ${claudeStats.callees}`);
    }
  }
  if (!claudeStats.trace.includes('Skill: incident-triage')) {
    throw new Error('Expected the skill scope in the turn trace');
  }
  console.log('  ✓ Claude Code trace parsed with turns, scopes, MCP failure and anomalies');

  // -------------------------------------------------------------
  // Test 7b: Trace content is rendered as text, not HTML
  // -------------------------------------------------------------
  console.log('\nTest 7b: HTML escaping of trace content...');
  await page.click('#resetBtn');
  const injected = await page.evaluate(() => {
    const lines = [
      { type: 'user', timestamp: '2026-10-07T14:00:00Z', message: { content: '<img src=x id=pwned-prompt onerror="window.__pwned=1"> run it' } },
      { type: 'assistant', timestamp: '2026-10-07T14:00:01Z', message: { content: [{ type: 'tool_use', id: 't1', name: '<b id=pwned-name>Bash</b>', input: {} }] } }
    ].map(l => JSON.stringify(l)).join('\n');
    processTraceText(lines);
    return {
      injectedElements: document.querySelectorAll('#pwned-prompt, #pwned-name').length,
      pwned: window.__pwned === 1,
      promptShown: document.getElementById('traceViewContainer').textContent.includes('<img src=x')
    };
  });
  if (injected.injectedElements > 0 || injected.pwned) throw new Error('Trace content was injected as HTML');
  if (!injected.promptShown) throw new Error('Expected the raw prompt text to be displayed');
  console.log('  ✓ Prompts and tool names are escaped');

  // -------------------------------------------------------------
  // Test 7: Console & Runtime Errors
  // -------------------------------------------------------------
  console.log('\nTest 7: Uncaught Errors Check...');
  if (uncaughtErrors.length > 0) {
    throw new Error(`Uncaught errors during execution:\n${uncaughtErrors.join('\n')}`);
  }
  console.log('  ✓ 0 uncaught errors or console errors detected.');

  await browser.close();
  console.log('\n===============================================================');
  console.log('🎉 ALL UI REGRESSION TESTS PASSED!');
  console.log('===============================================================');
}

runTestSuite().catch(err => {
  console.error('\n💥 Test suite failed:\n', err.message);
  process.exit(1);
});
