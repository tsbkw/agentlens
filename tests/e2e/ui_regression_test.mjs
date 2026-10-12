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
    trace: document.getElementById('traceViewContainer').textContent,
    subagentCalls: currentNodes.filter(n => n.caller_scope === 'Subagent: Explore').map(n => `${n.name}<${n.parent_id}`),
    sourceInfo: document.getElementById('sourceInfo').textContent
  }));
  console.log(`  Detected provider: ${claudeStats.provider}, nodes=${claudeStats.nodes}, mcp=${claudeStats.mcp}, failed=${claudeStats.failed}, anomalies=${claudeStats.anomalyTypes.join(',')}`);
  if (claudeStats.provider !== 'claude-code') throw new Error(`Expected claude-code provider, got ${claudeStats.provider}`);
  // 6 calls in the main transcript + 2 in the stitched subagent transcript
  if (claudeStats.nodes !== '8' || claudeStats.mcp !== '2' || claudeStats.failed !== '1') {
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
  if (JSON.stringify(claudeStats.subagentCalls) !== JSON.stringify(['Bash<toolu_agent_01', 'Read<toolu_agent_01'])) {
    throw new Error(`Expected the subagent's Bash and Read nested under its Agent call, got ${claudeStats.subagentCalls}`);
  }
  if (!claudeStats.sourceInfo.includes('Claude Code') || !claudeStats.sourceInfo.includes('1 subagent transcript')) {
    throw new Error(`Unexpected source info: ${claudeStats.sourceInfo}`);
  }
  console.log('  ✓ Claude Code trace parsed with turns, scopes, subagent transcript, MCP failure and anomalies');

  // -------------------------------------------------------------
  // Test 7c: Subagent stitching from picked files and dropped folders
  // -------------------------------------------------------------
  console.log('\nTest 7c: Subagent transcripts from picked files and dropped folders...');
  const traces = path.join(__dirname, '..', '..', 'examples', 'traces');
  const subDir = path.join(traces, 'sample_claude_code_session', 'subagents');

  // File picker without folder information: the session file plus the subagent files
  await page.click('#resetBtn');
  const input = await page.$('#fileInput');
  await input.uploadFile(
    path.join(subDir, 'agent-a1b2c3d4e5f60718.jsonl'),
    path.join(traces, 'sample_claude_code_session.jsonl'),
    path.join(subDir, 'agent-a1b2c3d4e5f60718.meta.json')
  );
  await page.waitForFunction(() => document.getElementById('graphSection').style.display !== 'none' && currentNodes.length > 0);
  const picked = await page.evaluate(() => ({
    nodes: currentNodes.length,
    stitched: currentSubagentTraces.length,
    subagentParents: currentNodes.filter(n => n.caller_scope === 'Subagent: Explore').map(n => n.parent_id)
  }));
  if (picked.nodes !== 8 || picked.stitched !== 1 || picked.subagentParents.some(id => id !== 'toolu_agent_01')) {
    throw new Error(`Picked files were not stitched: ${JSON.stringify(picked)}`);
  }

  // Dropped session file + its folder, with a nested subagent spawned by the first one
  await page.click('#resetBtn');
  const files = {
    '/sess.jsonl': fs.readFileSync(path.join(traces, 'sample_claude_code_session.jsonl'), 'utf8'),
    '/sess/subagents/agent-a1b2c3d4e5f60718.jsonl': fs.readFileSync(path.join(subDir, 'agent-a1b2c3d4e5f60718.jsonl'), 'utf8')
      + JSON.stringify({ type: 'assistant', timestamp: '2026-10-07T14:00:20Z', message: { content: [{ type: 'tool_use', id: 'toolu_nested', name: 'Agent', input: { subagent_type: 'reviewer' } }] } }) + '\n',
    '/sess/subagents/agent-a1b2c3d4e5f60718.meta.json': fs.readFileSync(path.join(subDir, 'agent-a1b2c3d4e5f60718.meta.json'), 'utf8'),
    '/sess/subagents/agent-nested.jsonl': JSON.stringify({ type: 'assistant', timestamp: '2026-10-07T14:00:21Z', message: { content: [{ type: 'tool_use', id: 'toolu_nested_grep', name: 'Grep', input: {} }] } }) + '\n',
    '/sess/subagents/agent-nested.meta.json': JSON.stringify({ toolUseId: 'toolu_nested' }),
    '/other-session/subagents/agent-x.jsonl': '{}\n'
  };
  const dropped = await page.evaluate(async (files) => {
    // Minimal FileSystemEntry tree, as produced by dropping a file and a folder
    const tree = {};
    for (const [p, text] of Object.entries(files)) {
      let dir = tree;
      const parts = p.slice(1).split('/');
      parts.slice(0, -1).forEach(seg => { dir = dir[seg] = dir[seg] || {}; });
      dir[parts[parts.length - 1]] = new File([text], parts[parts.length - 1]);
    }
    const toEntry = (name, node, parent) => {
      const fullPath = `${parent}/${name}`;
      if (node instanceof File) return { isFile: true, isDirectory: false, fullPath, file: (ok) => ok(node) };
      const children = Object.entries(node).map(([n, v]) => toEntry(n, v, fullPath));
      return {
        isFile: false, isDirectory: true, fullPath,
        createReader: () => { let done = false; return { readEntries: (ok) => { ok(done ? [] : children); done = true; } }; }
      };
    };
    const items = Object.entries(tree).map(([n, v]) => ({ webkitGetAsEntry: () => toEntry(n, v, '') }));
    const event = new Event('drop', { bubbles: true, cancelable: true });
    event.dataTransfer = { items, files: [] };
    document.getElementById('dropzone').dispatchEvent(event);
    for (let i = 0; i < 50 && currentSubagentTraces.length < 2; i++) await new Promise(r => setTimeout(r, 20));
    return {
      stitched: currentSubagentTraces.slice().sort(),
      nested: currentNodes.filter(n => n.name === 'Grep').map(n => `${n.caller_scope}<${n.parent_id}`),
      anomalies: currentAnomalies.map(a => a.type).sort()
    };
  }, files);
  if (JSON.stringify(dropped.stitched) !== JSON.stringify(['/sess/subagents/agent-a1b2c3d4e5f60718.jsonl', '/sess/subagents/agent-nested.jsonl'])) {
    throw new Error(`Expected both subagent transcripts of the dropped session, got ${dropped.stitched}`);
  }
  if (JSON.stringify(dropped.nested) !== JSON.stringify(['Subagent: reviewer<toolu_nested'])) {
    throw new Error(`Expected the nested subagent call under its spawning call, got ${dropped.nested}`);
  }
  if (JSON.stringify(dropped.anomalies) !== JSON.stringify(['auth_expired', 'silent_fallback'])) {
    throw new Error(`Subagent calls must not create cross-actor anomalies, got ${dropped.anomalies}`);
  }
  console.log('  ✓ Subagent transcripts stitched from picked files and from a dropped folder (nested included)');

  // -------------------------------------------------------------
  // Test 7d: Parent link provenance (sidecar / result / inferred)
  // -------------------------------------------------------------
  console.log('\nTest 7d: Subagent parent links and inferred placement...');
  await page.click('#resetBtn');
  const linked = await page.evaluate(() => {
    const j = (...objs) => objs.map(o => JSON.stringify(o)).join('\n');
    processTraceFiles([
      { path: '/s.jsonl', text: j(
        { type: 'user', message: { content: 'first' }, timestamp: '2026-10-07T10:00:00Z' },
        { type: 'assistant', message: { content: [{ type: 'tool_use', id: 'call_agent', name: 'Agent', input: { subagent_type: 'Explore' } }] }, timestamp: '2026-10-07T10:00:01Z' },
        { type: 'assistant', message: { content: [{ type: 'tool_use', id: 'call_skill', name: 'Skill', input: { skill: 'web-research' } }] }, timestamp: '2026-10-07T10:00:10Z' },
        { type: 'user', message: { content: [{ type: 'tool_result', tool_use_id: 'call_skill', content: 'launched' }] }, toolUseResult: { status: 'forked', agentId: 'agentF' }, timestamp: '2026-10-07T10:00:11Z' },
        { type: 'user', message: { content: 'second' }, timestamp: '2026-10-07T11:00:00Z' }
      ) },
      { path: '/s/subagents/agent-agentA.meta.json', text: JSON.stringify({ agentType: 'fork', toolUseId: 'call_agent' }) },
      { path: '/s/subagents/agent-agentA.jsonl', text: j(
        { agentId: 'agentA', type: 'assistant', message: { content: [{ type: 'tool_use', id: 'call_agent', name: 'Agent', input: { subagent_type: 'Explore' } }] }, timestamp: '2026-10-07T10:00:01Z' },
        { agentId: 'agentA', type: 'assistant', message: { content: [{ type: 'tool_use', id: 'a_read', name: 'Read', input: {} }] }, timestamp: '2026-10-07T10:00:02Z' }
      ) },
      { path: '/s/subagents/agent-agentF.meta.json', text: JSON.stringify({ agentType: 'general-purpose', name: 'web-research' }) },
      { path: '/s/subagents/agent-agentF.jsonl', text: j({ agentId: 'agentF', type: 'assistant', message: { content: [{ type: 'tool_use', id: 'f_nav', name: 'Bash', input: {} }] }, timestamp: '2026-10-07T10:00:12Z' }) },
      { path: '/s/subagents/agent-agentU.meta.json', text: JSON.stringify({ agentType: 'general-purpose', name: 'background-check' }) },
      { path: '/s/subagents/agent-agentU.jsonl', text: j({ agentId: 'agentU', type: 'assistant', message: { content: [{ type: 'tool_use', id: 'u_grep', name: 'Grep', input: {} }] }, timestamp: '2026-10-07T11:00:05Z' }) }
    ]);
    const pick = id => { const n = currentNodes.find(x => x.id === id); return n && `${n.parent_id}|${n.parent_link || ''}|${n.turn_index}|${n.caller_scope}`; };
    return {
      count: currentNodes.length,
      agentCalls: currentNodes.filter(n => n.id === 'call_agent').length,
      a_read: pick('a_read'), f_nav: pick('f_nav'),
      placeholder: pick('inferred-subagent-agentU'), u_grep: pick('u_grep'),
      inferredBadges: document.querySelectorAll('#depsViewContainer .badge-warning, #traceViewContainer .badge-warning').length
    };
  });
  const expectLinks = {
    a_read: 'call_agent|sidecar|1|Subagent: Explore',
    f_nav: 'call_skill|result|1|Skill: web-research',
    placeholder: '|inferred|2|Agent',
    u_grep: 'inferred-subagent-agentU|inferred|2|Subagent: background-check'
  };
  for (const [k, v] of Object.entries(expectLinks)) {
    if (linked[k] !== v) throw new Error(`${k}: expected ${v}, got ${linked[k]}`);
  }
  if (linked.agentCalls !== 1 || linked.count !== 6) throw new Error(`Replayed spawning call must be counted once: ${JSON.stringify(linked)}`);
  if (linked.inferredBadges < 2) throw new Error(`Expected inferred badges in the dependency and trace views, got ${linked.inferredBadges}`);
  console.log('  ✓ sidecar / result / inferred parent links match the Go collector, replayed calls are deduplicated');

  // -------------------------------------------------------------
  // Test 7e: Denied and bookkeeping calls in attempt sequences
  // (same transcript as TestDeniedAndAuxiliaryCallsInAttemptSequences)
  // -------------------------------------------------------------
  console.log('\nTest 7e: Denied and auxiliary calls...');
  await page.click('#resetBtn');
  const seqLines = [
      "{\"type\":\"user\",\"message\":{\"content\":\"go\"},\"timestamp\":\"2026-10-07T10:00:00Z\"}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"id\":\"m1\",\"name\":\"mcp__github__get_issue\",\"input\":{}}]},\"timestamp\":\"2026-10-07T10:00:01Z\"}",
      "{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"m1\",\"content\":\"Authentication Failed: Bad credentials\",\"is_error\":true}]},\"toolUseResult\":\"Error\",\"timestamp\":\"2026-10-07T10:00:02Z\"}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"id\":\"ts\",\"name\":\"ToolSearch\",\"input\":{\"query\":\"github\"}}]},\"timestamp\":\"2026-10-07T10:00:03Z\"}",
      "{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"ts\",\"content\":\"No such tool available\",\"is_error\":true}]},\"toolUseResult\":\"Error\",\"timestamp\":\"2026-10-07T10:00:04Z\"}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"id\":\"b1\",\"name\":\"Bash\",\"input\":{\"command\":\"gh issue view 1\"}}]},\"timestamp\":\"2026-10-07T10:00:05Z\"}",
      "{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"b1\",\"content\":\"ok\"}]},\"toolUseResult\":{\"stdout\":\"ok\"},\"timestamp\":\"2026-10-07T10:00:06Z\"}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"id\":\"e1\",\"name\":\"mcp__notion__update_page\",\"input\":{}}]},\"timestamp\":\"2026-10-07T10:00:07Z\"}",
      "{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"e1\",\"content\":\"The user doesn't want to proceed with this tool use. The tool use was rejected.\",\"is_error\":true}]},\"toolUseResult\":\"Error\",\"timestamp\":\"2026-10-07T10:00:08Z\"}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"id\":\"g1\",\"name\":\"Grep\",\"input\":{}}]},\"timestamp\":\"2026-10-07T10:00:09Z\"}",
      "{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"g1\",\"content\":\"match\"}]},\"toolUseResult\":{\"stdout\":\"match\"},\"timestamp\":\"2026-10-07T10:00:10Z\"}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"id\":\"r1\",\"name\":\"Read\",\"input\":{}}]},\"timestamp\":\"2026-10-07T10:00:11Z\"}",
      "{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"r1\",\"content\":\"File does not exist.\",\"is_error\":true}]},\"toolUseResult\":\"Error\",\"timestamp\":\"2026-10-07T10:00:12Z\"}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"id\":\"td\",\"name\":\"TaskUpdate\",\"input\":{}}]},\"timestamp\":\"2026-10-07T10:00:13Z\"}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"id\":\"r2\",\"name\":\"Read\",\"input\":{}}]},\"timestamp\":\"2026-10-07T10:00:14Z\"}",
      "{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"r2\",\"content\":\"File does not exist.\",\"is_error\":true}]},\"toolUseResult\":\"Error\",\"timestamp\":\"2026-10-07T10:00:15Z\"}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"id\":\"d1\",\"name\":\"Bash\",\"input\":{\"command\":\"rm -rf build\"}}]},\"timestamp\":\"2026-10-07T10:00:16Z\"}",
      "{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"d1\",\"content\":\"Permission for this action was denied by the Claude Code auto mode classifier.\",\"is_error\":true}]},\"toolUseResult\":\"Error\",\"timestamp\":\"2026-10-07T10:00:17Z\"}",
      "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"tool_use\",\"id\":\"r3\",\"name\":\"Read\",\"input\":{}}]},\"timestamp\":\"2026-10-07T10:00:18Z\"}",
      "{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"r3\",\"content\":\"File does not exist.\",\"is_error\":true}]},\"toolUseResult\":\"Error\",\"timestamp\":\"2026-10-07T10:00:19Z\"}"
  ];
  const seqResult = await page.evaluate((lines) => {
    processTraceText(lines.join('\n'));
    const by = id => currentNodes.find(n => n.id === id);
    return {
      statuses: ['e1', 'd1', 'm1'].map(id => by(id).status),
      auxiliary: ['ts', 'td', 'b1'].map(id => !!by(id).auxiliary),
      anomalies: currentAnomalies.filter(a => a.type !== 'auth_expired')
        .map(a => a.type === 'silent_fallback' ? `fallback:${a.relatedNodeId}->${a.nodeId}` : `loop:${a.title}`)
    };
  }, seqLines);
  if (JSON.stringify(seqResult.statuses) !== JSON.stringify(['denied', 'denied', 'failed'])) throw new Error(`Unexpected statuses ${seqResult.statuses}`);
  if (JSON.stringify(seqResult.auxiliary) !== JSON.stringify([true, true, false])) throw new Error(`Unexpected auxiliary flags ${seqResult.auxiliary}`);
  if (JSON.stringify(seqResult.anomalies) !== JSON.stringify(['fallback:m1->b1', 'loop:Retry Loop / Thrashing: Read failed 3 times'])) {
    throw new Error(`Unexpected anomalies ${seqResult.anomalies}`);
  }
  console.log('  ✓ Refusals are denied, bookkeeping calls are skipped, fallbacks and loops match the Go detector');

  // -------------------------------------------------------------
  // Test 7f: Fallback confidence from the substitutes catalog and shared identifiers
  // -------------------------------------------------------------
  console.log('\nTest 7f: Fallback confidence...');
  await page.click('#resetBtn');
  const conf = await page.evaluate(() => {
    const call = (id, name, input, ts) => ({ type: 'assistant', message: { content: [{ type: 'tool_use', id, name, input }] }, timestamp: ts });
    const fail = (id, ts) => ({ type: 'user', message: { content: [{ type: 'tool_result', tool_use_id: id, content: 'Error: 502 Bad Gateway', is_error: true }] }, toolUseResult: 'Error', timestamp: ts });
    const ok = (id, ts) => ({ type: 'user', message: { content: [{ type: 'tool_result', tool_use_id: id, content: 'ok' }] }, toolUseResult: { stdout: 'ok' }, timestamp: ts });
    processTraceText([
      { type: 'user', message: { content: 'go' }, timestamp: '2026-10-07T10:00:00Z' },
      call('a', 'mcp__github__get_issue', { owner: 'acme', repo: 'widgets', issue_number: 42 }, '2026-10-07T10:00:01Z'), fail('a', '2026-10-07T10:00:02Z'),
      call('b', 'Bash', { command: 'gh issue view 42 -R acme/widgets' }, '2026-10-07T10:00:03Z'), ok('b', '2026-10-07T10:00:04Z'),
      call('c', 'mcp__warehouse__describe_table', { dataset: 'sales_eu', table: 'orders_2026' }, '2026-10-07T10:00:05Z'), fail('c', '2026-10-07T10:00:06Z'),
      call('d', 'Bash', { command: 'dbcli describe sales_eu.orders_2026' }, '2026-10-07T10:00:07Z'), ok('d', '2026-10-07T10:00:08Z'),
      call('e', 'mcp__warehouse__list_jobs', { project: 'nightly' }, '2026-10-07T10:00:09Z'), fail('e', '2026-10-07T10:00:10Z'),
      call('f', 'Bash', { command: 'go test ./...' }, '2026-10-07T10:00:11Z'), ok('f', '2026-10-07T10:00:12Z')
    ].map(l => JSON.stringify(l)).join('\n'));
    const fb = currentAnomalies.filter(a => a.type === 'silent_fallback').map(a => `${a.relatedNodeId}->${a.nodeId}:${a.confidence}`);
    const lowRow = [...document.querySelectorAll('.fallback-alert-row')].some(r => r.textContent.includes('Possible Fallback (low confidence)'));
    return { fb, lowRow };
  });
  if (JSON.stringify(conf.fb) !== JSON.stringify(['a->b:high', 'c->d:high', 'e->f:low'])) throw new Error(`Unexpected fallback confidence ${conf.fb}`);
  if (!conf.lowRow) throw new Error('Expected the low-confidence fallback to be labelled in the dependency view');
  console.log('  ✓ catalog CLI and shared identifiers give high confidence, an unrelated next call stays low');

  // -------------------------------------------------------------
  // Test 7g: Parallel sibling calls, pre-turn calls, object error messages (parity with Go)
  // -------------------------------------------------------------
  console.log('\nTest 7g: Sibling calls, pre-turn calls and object errors...');
  await page.click('#resetBtn');
  const parity = await page.evaluate(() => {
    const call = (id, name, input, ts) => ({ type: 'assistant', message: { content: [{ type: 'tool_use', id, name, input }] }, timestamp: ts });
    const fail = (id, ts, content = 'Error: 502 Bad Gateway') => ({ type: 'user', message: { content: [{ type: 'tool_result', tool_use_id: id, content, is_error: true }] }, toolUseResult: 'Error', timestamp: ts });
    const ok = (id, ts) => ({ type: 'user', message: { content: [{ type: 'tool_result', tool_use_id: id, content: 'ok' }] }, toolUseResult: { stdout: 'ok' }, timestamp: ts });
    processTraceText([
      // A call before the first prompt: it keeps its own turn and never reacts to turn 1
      call('pre', 'mcp__github__get_issue', { owner: 'acme', repo: 'widgets' }, '2026-10-07T09:59:00Z'), fail('pre', '2026-10-07T09:59:01Z'),
      { type: 'user', message: { content: 'go' }, timestamp: '2026-10-07T10:00:00Z' },
      // Two tool uses in one model response share a timestamp: the sibling is not a fallback
      { type: 'assistant', timestamp: '2026-10-07T10:00:01Z', message: { content: [
        { type: 'tool_use', id: 'a', name: 'mcp__github__get_issue', input: { owner: 'acme', repo: 'widgets', issue_number: 42 } },
        { type: 'tool_use', id: 'sib', name: 'Bash', input: { command: 'gh issue view 42 -R acme/widgets' } }
      ] } },
      fail('a', '2026-10-07T10:00:03Z'), ok('sib', '2026-10-07T10:00:03Z'),
      // Issued after the failure was known: this one is the fallback
      call('b', 'Bash', { command: 'gh issue view 42 -R acme/widgets' }, '2026-10-07T10:00:04Z'), ok('b', '2026-10-07T10:00:05Z')
    ].map(l => JSON.stringify(l)).join('\n'));
    return {
      fallbacks: currentAnomalies.filter(a => a.type === 'silent_fallback').map(a => `${a.relatedNodeId}->${a.nodeId}`),
      turns: currentTurns.map(t => `${t.index}:${t.nodes.map(n => n.id).join(',')}`),
      titles: currentAnomalies.map(a => a.title)
    };
  });
  if (JSON.stringify(parity.fallbacks) !== JSON.stringify(['a->b'])) throw new Error(`Unexpected fallbacks ${parity.fallbacks}`);
  if (JSON.stringify(parity.turns) !== JSON.stringify(['0:pre', '1:a,sib,b'])) throw new Error(`Unexpected turns ${parity.turns}`);
  if (!parity.titles.includes('Silent Fallback Detected: mcp__github__get_issue → Bash')) throw new Error(`Unexpected titles ${parity.titles}`);
  const objErr = await page.evaluate(() => {
    processTraceText([
      { event_type: 'tool_call', id: 'c1', name: 'mcp_github_x', mcp_server: 'github', params: { q: 'a' }, status: 'error', error: { code: 401 }, timestamp: '2026-10-07T10:00:00Z' }
    ].map(l => JSON.stringify(l)).join('\n'));
    return currentAnomalies.map(a => a.type);
  });
  if (JSON.stringify(objErr) !== JSON.stringify(['auth_expired'])) throw new Error(`Expected an auth anomaly from an object error, got ${objErr}`);
  console.log('  ✓ Siblings are not fallbacks, pre-turn calls get turn 0, object errors are scanned');

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
  // Test 8: Local sessions dashboard (agentlens ui API, mocked)
  // -------------------------------------------------------------
  console.log('\nTest 8: Local sessions dashboard...');
  const hiddenOnFile = await page.$eval('#localSection', el => getComputedStyle(el).display === 'none');
  if (!hiddenOnFile) throw new Error('Expected the local sessions section to stay hidden without the API');

  const tracesDir = path.resolve(__dirname, '../../examples/traces');
  const sessionFiles = [
    'sample_claude_code_session.jsonl',
    'sample_claude_code_session/subagents/agent-a1b2c3d4e5f60718.jsonl',
    'sample_claude_code_session/subagents/agent-a1b2c3d4e5f60718.meta.json'
  ].map(rel => ({ path: '/' + rel, text: fs.readFileSync(path.join(tracesDir, rel), 'utf8') }));
  const counts = { calls: 7, failed: 1, denied: 0, auth_expired: 1, silent_fallbacks: 1, possible_fallbacks: 0, retry_loops: 0 };
  const reportJSON = {
    window: { since: '2026-10-01T00:00:00+09:00', until: '0001-01-01T00:00:00Z' },
    sessions: 1,
    total: counts,
    mcp_servers: [{ key: 'github', ...counts, calls: 1 }, { key: '<b id=pwned-server>slack</b>', calls: 1 }],
    tools: [],
    sessions_detail: [{ key: 'sample_claude_code_session', project: '-home-dev-backend-api', provider: 'claude-code', last_call: '2026-10-07T14:01:09.900Z', ...counts }]
  };
  const requests = [];
  const local = await browser.newPage();
  local.on('pageerror', err => uncaughtErrors.push(`[PageError] ${err.message}`));
  await local.setRequestInterception(true);
  local.on('request', req => {
    const url = new URL(req.url());
    if (url.origin !== 'http://localhost:8000') return req.abort();
    requests.push(url.pathname + url.search);
    if (url.pathname === '/api/report') {
      return req.respond({ contentType: 'application/json', body: JSON.stringify({ ...reportJSON, window: { ...reportJSON.window, since: url.searchParams.get('since') === '30d' ? '2026-09-11T00:00:00+09:00' : reportJSON.window.since } }) });
    }
    if (url.pathname === '/api/sessions/sample_claude_code_session/files') {
      return req.respond({ contentType: 'application/json', body: JSON.stringify(sessionFiles) });
    }
    return req.respond({ contentType: 'text/html; charset=utf-8', body: fs.readFileSync(htmlPath, 'utf8') });
  });
  await local.goto('http://localhost:8000/');
  await local.waitForSelector('#reportSessions tr.session-row', { timeout: 5000 });
  const dash = await local.evaluate(() => ({
    sessions: document.querySelectorAll('#reportSessions tr.session-row').length,
    servers: document.querySelectorAll('#reportServers tbody tr').length,
    stats: document.getElementById('reportStats').textContent.replace(/\s+/g, ' '),
    injected: document.querySelectorAll('#pwned-server').length,
    status: document.getElementById('reportStatus').textContent
  }));
  if (dash.sessions !== 1 || dash.servers !== 2 || !dash.stats.includes('Auth Expired1') || dash.injected > 0 || !dash.status.includes('→ now')) {
    throw new Error(`Unexpected dashboard ${JSON.stringify(dash)}`);
  }
  if (!requests.includes('/api/report?since=7d')) throw new Error(`Expected the last 7 days by default, got ${requests}`);

  await local.select('#reportPeriod', '30d');
  await local.type('#reportProject', 'backend');
  await local.click('#reportForm button[type=submit]');
  await local.waitForFunction(() => document.getElementById('reportStatus').textContent.includes('click a session'));
  if (!requests.includes('/api/report?since=30d&project=backend')) throw new Error(`Expected the filtered report request, got ${requests}`);

  await local.click('#reportSessions tr.session-row');
  await local.waitForFunction(() => document.getElementById('sourceInfo').textContent !== '');
  const opened = await local.evaluate(() => ({
    source: document.getElementById('sourceInfo').textContent,
    calls: document.getElementById('statTotalNodes').textContent
  }));
  if (!opened.source.startsWith('Session sample_claude_code_session · -home-dev-backend-api · Format: Claude Code') ||
      !opened.source.includes('1 subagent transcript') || opened.calls !== '8') {
    throw new Error(`Unexpected session view ${JSON.stringify(opened)}`);
  }
  await local.click('#resetBtn');
  const back = await local.$eval('#localSection', el => getComputedStyle(el).display);
  if (back !== 'flex') throw new Error('Expected the dashboard after going back');
  await local.close();
  console.log('  ✓ Report renders escaped, filters reach the API, a session opens with its subagent');

  // -------------------------------------------------------------
  // Test 9: Viewer behaviour (filters, selection, state reset, alerts, unknown format, phone width)
  // -------------------------------------------------------------
  console.log('\nTest 9: Viewer behaviour...');
  await page.click('#resetBtn');
  const unknown = await page.evaluate(() => {
    processTraceFiles([{ path: '/notes.txt', text: '{"foo":1}\n[1,2,3]\nnot json' }]);
    return { error: document.getElementById('dropError').textContent, graph: document.getElementById('graphSection').style.display };
  });
  if (!unknown.error.includes('No tool calls or prompts recognised') || unknown.graph !== 'none') throw new Error(`Unknown format not reported: ${JSON.stringify(unknown)}`);

  await page.click('#loadClaudeSampleBtn');
  await page.waitForSelector('.callee-row');
  const behaviour = await page.evaluate(() => {
    const out = {};
    out.denied = document.getElementById('statDeniedCalls').textContent;
    // Filters apply to the trace view and 'anomalies' means nodes with anomalies
    filterSelect.value = 'anomalies'; refreshViews();
    out.anomalyRows = [...document.querySelectorAll('#traceViewContainer .node-row .node-title')].map(e => e.textContent.trim());
    out.anomalyDeps = [...document.querySelectorAll('.callee-row .callee-name')].map(e => e.textContent.trim().split(/\s+/)[0]);
    filterSelect.value = 'all'; searchInput.value = 'deploy-prod'; refreshViews();
    out.searchRows = document.querySelectorAll('#traceViewContainer .node-row').length;
    // Clicking a row highlights it
    document.querySelector('#traceViewContainer .node-row').click();
    out.selected = document.querySelectorAll('.node-row.selected').length;
    out.drawerOpen = drawer.classList.contains('open');
    return out;
  });
  if (behaviour.denied !== '0') throw new Error(`Expected a Denied stat, got ${behaviour.denied}`);
  if (!behaviour.anomalyRows.includes('mcp__github__create_issue') || !behaviour.anomalyRows.includes('Bash') || behaviour.anomalyRows.includes('Read')) throw new Error(`Unexpected anomaly rows ${behaviour.anomalyRows}`);
  if (!behaviour.anomalyDeps.includes('mcp__github__create_issue')) throw new Error(`Unexpected anomaly deps ${behaviour.anomalyDeps}`);
  if (behaviour.searchRows < 1) throw new Error('Search must apply to the trace view');
  if (behaviour.selected !== 1 || !behaviour.drawerOpen) throw new Error(`Expected one highlighted row and an open drawer, got ${JSON.stringify(behaviour)}`);
  await page.keyboard.press('Escape');
  if (await page.$eval('#drawer', el => el.classList.contains('open'))) throw new Error('Escape must close the drawer');

  // A new session starts with a clean view, and repeated findings are grouped and capped
  const reset = await page.evaluate(() => {
    const lines = [{ type: 'user', message: { content: 'go' }, timestamp: '2026-10-07T10:00:00Z' }];
    for (let i = 0; i < 8; i++) {
      for (let k = 0; k < 3; k++) {
        const id = `c${i}-${k}`;
        lines.push({ type: 'assistant', timestamp: `2026-10-07T10:0${i}:${String(k * 10).padStart(2, '0')}Z`, message: { content: [{ type: 'tool_use', id, name: `tool_${i}`, input: {} }] } });
        lines.push({ type: 'user', timestamp: `2026-10-07T10:0${i}:${String(k * 10 + 5).padStart(2, '0')}Z`, message: { content: [{ type: 'tool_result', tool_use_id: id, content: 'boom', is_error: true }] } });
      }
    }
    processTraceText(lines.map(l => JSON.stringify(l)).join('\n'));
    return {
      search: searchInput.value, filter: filterSelect.value, drawer: drawer.classList.contains('open'),
      cards: document.querySelectorAll('.alert-card').length, more: !!document.getElementById('alertsMoreBtn'),
      anomalies: currentAnomalies.length
    };
  });
  if (reset.search !== '' || reset.filter !== 'all' || reset.drawer) throw new Error(`State not reset: ${JSON.stringify(reset)}`);
  if (reset.anomalies !== 8 || reset.cards !== 5 || !reset.more) throw new Error(`Expected 5 of 8 alert cards and a show-all button, got ${JSON.stringify(reset)}`);
  await page.click('#alertsMoreBtn');
  if (await page.$$eval('.alert-card', els => els.length) !== 8) throw new Error('Show all must render every alert');

  // Phone width: no horizontal page scroll, drawer fits
  await page.setViewport({ width: 390, height: 844 });
  await page.click('#loadClaudeSampleBtn');
  await page.waitForSelector('.callee-row');
  await page.evaluate(() => document.querySelector('#traceViewContainer .node-row').click());
  const phone = await page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, drawerLeft: document.getElementById('drawer').getBoundingClientRect().left }));
  if (phone.scrollWidth > 390) throw new Error(`Horizontal page scroll at 390px: ${phone.scrollWidth}px`);
  if (phone.drawerLeft < 0) throw new Error(`Drawer overflows the viewport at 390px: left ${phone.drawerLeft}`);
  await page.keyboard.press('Escape');
  await page.setViewport({ width: 1280, height: 800 });
  console.log('  ✓ Unknown files are reported, filters reach the trace view, rows highlight, state resets, alerts group, phone width fits');

  // -------------------------------------------------------------
  // Test 10: Client-Side Privacy Tooltip & Info Icon
  // -------------------------------------------------------------
  console.log('\nTest 10: Client-Side Privacy Tooltip & Info Icon...');
  const privacyBadge = await page.$('.privacy-badge');
  if (!privacyBadge) throw new Error('Missing .privacy-badge element');
  const tooltipText = await page.$eval('.privacy-tooltip', el => el.textContent.trim());
  if (!tooltipText.includes('local browser sandbox') || !tooltipText.includes('never uploaded')) {
    throw new Error(`Unexpected privacy tooltip text: ${tooltipText}`);
  }
  console.log('  ✓ Client-side privacy badge, info icon, and explanation tooltip verified');

  // -------------------------------------------------------------
  // Test 11: Failure Details Inspection in Call Graph & Invocation Chips
  // -------------------------------------------------------------
  console.log('\nTest 11: Failure Details Inspection in Call Graph & Invocation Chips...');
  await page.click('#resetBtn');
  await page.click('#loadSampleBtn');
  await page.waitForSelector('.callee-row');

  // Find the row for mcp_postgres_query (which has 1 success and 3 failures)
  const pgDepInfo = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('.callee-row')];
    const pgRow = rows.find(r => r.querySelector('.callee-name')?.textContent.includes('mcp_postgres_query'));
    if (!pgRow) return null;
    return {
      text: pgRow.innerText,
      countsText: pgRow.querySelector('.callee-counts')?.textContent
    };
  });
  if (!pgDepInfo) throw new Error('mcp_postgres_query row not found in Call Graph');
  if (!pgDepInfo.countsText.includes('✗ 3')) throw new Error(`Expected ✗ 3 failure count in pg row, got: ${pgDepInfo.countsText}`);

  // Click the row directly - it must default to the failed invocation!
  await page.evaluate(() => {
    const rows = [...document.querySelectorAll('.callee-row')];
    const pgRow = rows.find(r => r.querySelector('.callee-name')?.textContent.includes('mcp_postgres_query'));
    pgRow.click();
  });

  const drawerState1 = await page.evaluate(() => {
    return {
      isOpen: document.getElementById('drawer').classList.contains('open'),
      title: document.getElementById('drawerTitle').textContent,
      badges: document.getElementById('drawerBadges').textContent,
      output: document.getElementById('drawerOutput').textContent,
      invocationsCount: document.getElementById('drawerInvocationsCount').textContent,
      chips: [...document.querySelectorAll('.invocation-chip')].map(c => ({ text: c.textContent, cls: c.className }))
    };
  });

  if (!drawerState1.isOpen) throw new Error('Drawer did not open when clicking row');
  if (!drawerState1.badges.includes('failed')) {
    throw new Error(`Expected drawer to show failed status invocation when clicking row with failures, got: ${drawerState1.badges}`);
  }
  if (!drawerState1.output.includes('Connection timeout after 30000ms')) {
    throw new Error(`Expected drawer output to contain database timeout failure, got: ${drawerState1.output}`);
  }
  if (drawerState1.invocationsCount !== '3') {
    throw new Error(`Expected 3 invocations in switcher, got: ${drawerState1.invocationsCount}`);
  }
  console.log('  ✓ Clicking row with failures correctly opened failed invocation');

  // Click chip #2 to switch to the second retry
  await page.evaluate(() => {
    const chips = document.querySelectorAll('.invocation-chip');
    chips[1].click();
  });

  const drawerState2 = await page.evaluate(() => {
    return {
      badges: document.getElementById('drawerBadges').textContent,
      meta: document.getElementById('drawerMeta').textContent
    };
  });
  if (!drawerState2.meta.includes('call-pg-retry-2')) {
    throw new Error(`Expected drawer to switch to call-pg-retry-2 upon clicking chip #2, got: ${drawerState2.meta}`);
  }
  console.log('  ✓ Clicking invocation chip #2 switched to call-pg-retry-2');

  // Click the "View in Trace" button
  await page.click('#drawerFilterTraceBtn');
  const traceState = await page.evaluate(() => {
    return {
      view: currentView,
      searchVal: document.getElementById('searchInput').value,
      visibleRows: document.querySelectorAll('#traceViewContainer .node-row').length
    };
  });
  if (traceState.view !== 'trace' || traceState.searchVal !== 'mcp_postgres_query') {
    throw new Error(`View in Trace did not switch tab or filter search: ${JSON.stringify(traceState)}`);
  }
  console.log('  ✓ "View in Trace" button successfully switched to trace view filtered to the tool');

  // Test mixed success + failure invocation chips on a single tool
  await page.evaluate(() => {
    const mixedTrace = [
      { step_index: 0, source: 'USER_EXPLICIT', type: 'USER_INPUT', content: 'test mixed' },
      { step_index: 1, source: 'MODEL', type: 'PLANNER_RESPONSE', tool_calls: [{ id: 'call-mix-1', name: 'test_tool', args: { num: 1 } }] },
      { step_index: 2, source: 'SYSTEM', type: 'GENERIC', status: 'DONE', content: 'success output' },
      { step_index: 3, source: 'MODEL', type: 'PLANNER_RESPONSE', tool_calls: [{ id: 'call-mix-2', name: 'test_tool', args: { num: 2 } }] },
      { step_index: 4, source: 'SYSTEM', type: 'GENERIC', status: 'ERROR', content: 'failed output: error 500' }
    ].map(JSON.stringify).join('\n');
    processTraceText(mixedTrace);
  });
  await page.waitForSelector('.callee-row');

  // Verify clicking the row defaults to failed invocation (#2)
  await page.evaluate(() => {
    document.querySelector('.callee-row').click();
  });
  const mixedState1 = await page.evaluate(() => ({
    badges: document.getElementById('drawerBadges').textContent,
    chips: [...document.querySelectorAll('.invocation-chip')].map(c => c.textContent.trim())
  }));
  if (!mixedState1.badges.includes('failed')) {
    throw new Error(`Expected mixed tool to default to failed invocation, got: ${mixedState1.badges}`);
  }
  // Click chip #1 (success)
  await page.evaluate(() => {
    document.querySelectorAll('.invocation-chip')[0].click();
  });
  const mixedState2 = await page.evaluate(() => ({
    badges: document.getElementById('drawerBadges').textContent
  }));
  if (!mixedState2.badges.includes('success')) {
    throw new Error(`Expected mixed tool to switch to success invocation, got: ${mixedState2.badges}`);
  }
  console.log('  ✓ Mixed tool row defaulted to failure and allowed switching to success invocation');

  // -------------------------------------------------------------
  // Test 12: Browser Back/Forward Navigation & Deep Linking
  // -------------------------------------------------------------
  console.log('\nTest 12: Browser Back/Forward Navigation & Deep Linking...');
  // Deep linking test: navigate directly to URL with session and call
  const deepLinkUrl = `${fileUrl}#session=sample-gemini&call=call-gh-issue`;
  const page2 = await browser.newPage();
  await page2.setViewport({ width: 1280, height: 800 });
  await page2.goto(deepLinkUrl, { waitUntil: 'load' });

  await page2.waitForSelector('#drawer.open');
  const deepLinkDrawer = await page2.evaluate(() => ({
    title: document.getElementById('drawerTitle').textContent,
    badges: document.getElementById('drawerBadges').textContent,
    output: document.getElementById('drawerOutput').textContent,
    graphVisible: document.getElementById('graphSection').style.display !== 'none'
  }));

  if (!deepLinkDrawer.graphVisible) throw new Error('Graph section should be visible after deep linking');
  if (deepLinkDrawer.title !== 'mcp_github_create_issue') throw new Error(`Expected drawer title mcp_github_create_issue, got: ${deepLinkDrawer.title}`);
  if (!deepLinkDrawer.badges.includes('failed')) throw new Error(`Expected failed badge on deep-linked call, got: ${deepLinkDrawer.badges}`);
  console.log('  ✓ Deep-linked directly via URL to session and specific call');
  await page2.close();

  // Test full browser Back/Forward navigation stack
  const page3 = await browser.newPage();
  await page3.setViewport({ width: 1280, height: 800 });
  await page3.goto(fileUrl, { waitUntil: 'load' });

  // 1. Initial state: dashboard
  const initDropzone = await page3.$eval('#dropzoneSection', el => getComputedStyle(el).display);
  if (initDropzone === 'none') throw new Error('Expected dropzone on initial load');

  // 2. Click load sample trace -> session view
  await page3.click('#loadSampleBtn');
  await page3.waitForSelector('.callee-row');
  const sessionUrl = await page3.evaluate(() => window.location.href);
  if (!sessionUrl.includes('session=')) throw new Error(`Expected URL to include session, got: ${sessionUrl}`);

  // 3. Click a callee row -> drawer open
  await page3.evaluate(() => {
    document.querySelector('.callee-row').click();
  });
  await page3.waitForSelector('#drawer.open');
  const callUrl = await page3.evaluate(() => window.location.href);
  if (!callUrl.includes('call=')) throw new Error(`Expected URL to include call, got: ${callUrl}`);

  // 4. Browser Back: closes drawer, keeps session view
  await page3.goBack();
  await new Promise(r => setTimeout(r, 200));
  const backState1 = await page3.evaluate(() => ({
    drawerOpen: document.getElementById('drawer').classList.contains('open'),
    graphVisible: document.getElementById('graphSection').style.display !== 'none'
  }));
  if (backState1.drawerOpen) throw new Error('First browser back must close the drawer');
  if (!backState1.graphVisible) throw new Error('First browser back must keep the session graph view open');
  console.log('  ✓ Browser Back closed the drawer while keeping session view');

  // 5. Browser Back again: returns to dashboard
  await page3.goBack();
  await new Promise(r => setTimeout(r, 200));
  const backState2 = await page3.evaluate(() => ({
    dropzoneVisible: document.getElementById('dropzoneSection').style.display !== 'none',
    graphVisible: document.getElementById('graphSection').style.display !== 'none'
  }));
  if (!backState2.dropzoneVisible || backState2.graphVisible) {
    throw new Error('Second browser back must return to the dashboard / dropzone');
  }
  console.log('  ✓ Second browser Back returned to dashboard / session list');

  // 6. Browser Forward: restores session view
  await page3.goForward();
  await new Promise(r => setTimeout(r, 200));
  const forwardState1 = await page3.evaluate(() => ({
    graphVisible: document.getElementById('graphSection').style.display !== 'none',
    drawerOpen: document.getElementById('drawer').classList.contains('open')
  }));
  if (!forwardState1.graphVisible) throw new Error('Browser Forward must restore session graph');
  console.log('  ✓ Browser Forward navigated back to session view');

  // 7. Browser Forward again: reopens drawer on call
  await page3.goForward();
  await new Promise(r => setTimeout(r, 200));
  const forwardState2 = await page3.evaluate(() => ({
    drawerOpen: document.getElementById('drawer').classList.contains('open')
  }));
  if (!forwardState2.drawerOpen) {
    throw new Error('Second browser Forward must reopen the inspector drawer');
  }
  console.log('  ✓ Second browser Forward restored inspector drawer');
  await page3.close();

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
