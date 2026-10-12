// Browser engine parity for the Claude Code regression fixture: the page must derive the
// same turns, calls, links and anomalies from the fixture's files as the Go pipeline
// recorded in tests/fixtures/synthetic/claude_code_regression_golden.json.
import puppeteer from 'puppeteer-core';
import path from 'path';
import { fileURLToPath } from 'url';
import fs from 'fs';
import os from 'os';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

function getChromePath() {
  if (process.env.CHROME_BIN && fs.existsSync(process.env.CHROME_BIN)) return process.env.CHROME_BIN;
  for (const p of ['/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium', '/usr/bin/chromium-browser']) {
    if (fs.existsSync(p)) return p;
  }
  const cache = path.join(os.homedir(), '.cache/puppeteer/chrome');
  const find = (dir) => {
    try {
      for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name);
        if (entry.isFile() && (entry.name === 'chrome' || entry.name === 'chrome.exe')) return full;
        if (entry.isDirectory()) { const r = find(full); if (r) return r; }
      }
    } catch (e) {}
    return null;
  };
  const found = fs.existsSync(cache) ? find(cache) : null;
  if (found) return found;
  throw new Error('No Chrome/Chromium executable found for E2E tests.');
}

// Every file of the fixture, with the paths a dropped folder would produce
function fixtureFiles(root, stem) {
  const files = [{ path: `/${stem}.jsonl`, text: fs.readFileSync(path.join(root, `${stem}.jsonl`), 'utf8') }];
  const walk = (dir, rel) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      const relPath = `${rel}/${entry.name}`;
      if (entry.isDirectory()) walk(full, relPath);
      else files.push({ path: relPath, text: fs.readFileSync(full, 'utf8') });
    }
  };
  walk(path.join(root, stem), `/${stem}`);
  return files;
}

function sortAnomalies(list) {
  return list.slice().sort((a, b) => (a.node_id + '|' + a.type + '|' + a.related_node_id).localeCompare(b.node_id + '|' + b.type + '|' + b.related_node_id));
}

function expectEqual(label, actual, expected) {
  const a = JSON.stringify(actual), e = JSON.stringify(expected);
  if (a !== e) throw new Error(`${label} differs from the golden file:\n  golden: ${e}\n  browser: ${a}`);
}

async function run() {
  console.log('===============================================================');
  console.log('🧪 AgentLens Claude Code Fixture: Browser / Go Parity Test');
  console.log('===============================================================');
  const chromePath = getChromePath();
  const root = path.resolve(__dirname, '../fixtures/synthetic');
  const stem = 'claude_code_regression';
  const golden = JSON.parse(fs.readFileSync(path.join(root, `${stem}_golden.json`), 'utf8'));
  const files = fixtureFiles(root, stem);
  console.log(`[Setup] ${files.length} fixture files, ${golden.nodes.length} golden calls`);

  const browser = await puppeteer.launch({
    executablePath: chromePath,
    headless: 'new',
    args: ['--no-sandbox', '--disable-setuid-sandbox', '--disable-dev-shm-usage', '--window-size=1280,800']
  });
  const page = await browser.newPage();
  await page.setViewport({ width: 1280, height: 800 });
  const errors = [];
  page.on('pageerror', err => errors.push(`[PageError] ${err.message}`));
  page.on('console', msg => { if (msg.type() === 'error') errors.push(`[ConsoleError] ${msg.text()}`); });
  await page.goto(`file://${path.resolve(__dirname, '../../web/index.html')}`, { waitUntil: 'load' });

  console.log('\nTest 1: Loading the fixture as a dropped folder...');
  const parsed = await page.evaluate((files) => {
    processTraceFiles(files, 'fixture');
    return {
      provider: currentProvider ? currentProvider.id : null,
      subagents: currentSubagentTraces.slice().sort(),
      turns: currentTurns.map(t => ({ index: t.index, prompt: t.prompt })),
      nodes: currentNodes.map(n => ({
        id: n.id, name: n.name, type: n.type, mcp_server: n.mcp_server || '', status: n.status,
        turn_index: n.turn_index, caller_scope: n.caller_scope || 'Agent', scope_name: n.scope_name || '',
        parent_id: n.parent_id || '', parent_link: n.parent_link || '', auxiliary: !!n.auxiliary,
        duration_ms: n.duration_ms || 0
      })),
      anomalies: currentAnomalies.map(a => ({
        type: a.type, node_id: a.nodeId, related_node_id: a.relatedNodeId || '', confidence: a.confidence || '', title: a.title
      })),
      stats: {
        nodes: document.getElementById('statTotalNodes').textContent.trim(),
        anomalies: document.getElementById('statAnomalies').textContent.trim()
      }
    };
  }, files);
  if (parsed.provider !== golden.provider) throw new Error(`Provider: expected ${golden.provider}, got ${parsed.provider}`);
  const expectedSubagents = files.map(f => f.path).filter(p => /\/subagents\/.*\.jsonl$/.test(p)).sort();
  expectEqual('Stitched subagent transcripts', parsed.subagents, expectedSubagents);
  console.log(`  ✓ Provider ${parsed.provider}, ${parsed.subagents.length} subagent transcripts stitched, stray files ignored`);

  console.log('\nTest 2: Turns, calls and links match the Go pipeline...');
  expectEqual('Turns', parsed.turns, golden.turns.map(t => ({ index: t.index, prompt: t.prompt })));
  if (parsed.nodes.length !== golden.nodes.length) {
    const ids = new Set(golden.nodes.map(n => n.id));
    const extra = parsed.nodes.filter(n => !ids.has(n.id)).map(n => n.id);
    const seen = new Set(parsed.nodes.map(n => n.id));
    const missing = golden.nodes.filter(n => !seen.has(n.id)).map(n => n.id);
    throw new Error(`Call count: expected ${golden.nodes.length}, got ${parsed.nodes.length} (extra: ${extra}; missing: ${missing})`);
  }
  const byId = new Map(parsed.nodes.map(n => [n.id, n]));
  for (const want of golden.nodes) {
    const got = byId.get(want.id);
    if (!got) throw new Error(`Call ${want.id} missing in the browser`);
    const pick = (n) => ({
      id: n.id, name: n.name, type: n.type, mcp_server: n.mcp_server || '', status: n.status, turn_index: n.turn_index,
      caller_scope: n.caller_scope, scope_name: n.scope_name || '', parent_id: n.parent_id || '', parent_link: n.parent_link || '',
      auxiliary: !!n.auxiliary, duration_ms: n.duration_ms || 0
    });
    expectEqual(`Call ${want.id}`, pick(got), pick(want));
  }
  console.log(`  ✓ ${golden.turns.length} turns and ${golden.nodes.length} calls match (status, scope, parent link, duration)`);

  console.log('\nTest 3: Anomalies match the Go detector...');
  const wantAnomalies = sortAnomalies(golden.anomalies.map(a => ({ type: a.type, node_id: a.node_id, related_node_id: a.related_node_id || '', confidence: a.confidence || '', title: a.title })));
  expectEqual('Anomalies', sortAnomalies(parsed.anomalies), wantAnomalies);
  if (parsed.stats.nodes !== String(golden.nodes.length) || parsed.stats.anomalies !== String(golden.anomalies.length)) {
    throw new Error(`DOM stats: expected ${golden.nodes.length} calls / ${golden.anomalies.length} anomalies, got ${JSON.stringify(parsed.stats)}`);
  }
  console.log(`  ✓ ${golden.anomalies.length} anomalies match (type, call, related call, confidence, title)`);

  console.log('\nTest 4: Error checks...');
  if (errors.length) throw new Error(`Uncaught errors:\n${errors.join('\n')}`);
  console.log('  ✓ 0 uncaught errors or console errors detected.');

  await browser.close();
  console.log('\n===============================================================');
  console.log('🎉 CLAUDE CODE FIXTURE PARITY TESTS PASSED!');
  console.log('===============================================================');
}

run().catch(err => {
  console.error('\n💥 Claude Code fixture parity test failed:\n', err.message);
  process.exit(1);
});
