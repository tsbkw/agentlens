import puppeteer from 'puppeteer-core';
import path from 'path';
import { fileURLToPath } from 'url';
import fs from 'fs';
import os from 'os';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

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

async function runSyntheticParityTest() {
  console.log('===============================================================');
  console.log('🧪 AgentLens Synthetic Integration & UI Parity Test');
  console.log('===============================================================');

  const chromePath = getChromePath();
  console.log(`[Setup] Browser binary: ${chromePath}`);

  const fixturePath = path.resolve(__dirname, '../fixtures/synthetic/gemini_edge_cases.jsonl');
  const goldenPath = path.resolve(__dirname, '../fixtures/synthetic/gemini_edge_cases_golden.json');
  const fixtureText = fs.readFileSync(fixturePath, 'utf8');
  const golden = JSON.parse(fs.readFileSync(goldenPath, 'utf8'));

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
  page.on('pageerror', err => uncaughtErrors.push(`[PageError] ${err.message}`));
  page.on('console', msg => {
    if (msg.type() === 'error') {
      uncaughtErrors.push(`[ConsoleError] ${msg.text()}`);
    }
  });

  await page.goto(fileUrl, { waitUntil: 'load' });

  // 1. Feed the synthetic Gemini transcript into the browser engine
  console.log('Test 1: Ingesting synthetic Gemini edge-case transcript in Web UI...');
  const parsedData = await page.evaluate((text) => {
    const virtualPath = '/home/tsbkw0/.gemini/antigravity-cli/brain/sess-synthetic/.system_generated/logs/transcript.jsonl';
    processTraceFiles([{ path: virtualPath, text }]);

    return {
      provider: currentProvider ? currentProvider.id : null,
      turns: currentTurns.map(t => ({
        index: t.index,
        prompt: t.prompt
      })),
      nodes: currentNodes.map(n => ({
        id: n.id,
        name: n.name,
        type: n.type,
        scope_name: n.scope_name || '',
        caller_scope: n.caller_scope || 'Agent',
        status: n.status,
        output: typeof n.output === 'string' ? n.output : JSON.stringify(n.output)
      })),
      anomalies: currentAnomalies.map(a => ({
        type: a.type,
        node_id: a.nodeId || a.node_id
      })),
      domStats: {
        totalNodes: document.getElementById('statTotalNodes').textContent.trim(),
        failedNodes: document.getElementById('statFailedCalls').textContent.trim(),
        anomalies: document.getElementById('statAnomalies').textContent.trim()
      }
    };
  }, fixtureText);

  // 2. Parity assertions against Golden Specification
  console.log('Test 2: Verifying dual-engine parity against golden specification...');
  if (parsedData.provider !== golden.provider) {
    throw new Error(`Provider detection mismatch: expected ${golden.provider}, got ${parsedData.provider}`);
  }
  console.log(`  ✓ Provider correctly detected as: ${parsedData.provider}`);

  if (parsedData.turns.length !== golden.turn_count) {
    throw new Error(`Turn count mismatch: expected ${golden.turn_count}, got ${parsedData.turns.length}`);
  }
  console.log(`  ✓ Turn count verified: ${parsedData.turns.length}`);

  if (parsedData.nodes.length !== golden.node_count) {
    throw new Error(`Node count mismatch: expected ${golden.node_count}, got ${parsedData.nodes.length}`);
  }
  console.log(`  ✓ Node count verified: ${parsedData.nodes.length}`);

  // Assert individual node parity
  const nodeMap = new Map();
  parsedData.nodes.forEach(n => nodeMap.set(n.id, n));

  for (const expected of golden.nodes) {
    const actual = nodeMap.get(expected.id);
    if (!actual) {
      throw new Error(`Expected node ${expected.id} not found in browser parse tree`);
    }
    if (actual.name !== expected.name) {
      throw new Error(`Node ${expected.id} name mismatch: expected ${expected.name}, got ${actual.name}`);
    }
    if (actual.type !== expected.type) {
      throw new Error(`Node ${expected.id} type mismatch: expected ${expected.type}, got ${actual.type}`);
    }
    if (actual.scope_name !== expected.scope_name) {
      throw new Error(`Node ${expected.id} scope_name mismatch: expected "${expected.scope_name}", got "${actual.scope_name}"`);
    }
    if (actual.caller_scope !== expected.caller_scope) {
      throw new Error(`Node ${expected.id} caller_scope mismatch: expected "${expected.caller_scope}", got "${actual.caller_scope}"`);
    }
    if (actual.status !== expected.status) {
      throw new Error(`Node ${expected.id} status mismatch: expected ${expected.status}, got ${actual.status}`);
    }
    if (actual.output !== expected.output) {
      throw new Error(`Node ${expected.id} output mismatch:\nExpected: ${expected.output}\nActual:   ${actual.output}`);
    }
  }
  console.log('  ✓ All 9 call nodes strictly match golden specification (scopes, types, outputs, statuses)');

  // Assert anomalies parity
  if (parsedData.anomalies.length !== golden.anomalies.length) {
    throw new Error(`Anomalies count mismatch: expected ${golden.anomalies.length}, got ${parsedData.anomalies.length}`);
  }
  for (let i = 0; i < golden.anomalies.length; i++) {
    const exp = golden.anomalies[i];
    const act = parsedData.anomalies[i];
    if (act.type !== exp.type || act.node_id !== exp.node_id) {
      throw new Error(`Anomaly[${i}] mismatch: expected ${JSON.stringify(exp)}, got ${JSON.stringify(act)}`);
    }
  }
  console.log('  ✓ All 3 anomalies strictly match golden specification (auth_expired, silent_fallback, retry_loop)');

  // 3. DOM & UI Fidelity Assertions
  console.log('\nTest 3: DOM stats & inspector drawer fidelity...');
  if (parsedData.domStats.totalNodes !== String(golden.node_count)) {
    throw new Error(`DOM statTotalNodes expected ${golden.node_count}, got ${parsedData.domStats.totalNodes}`);
  }
  if (parsedData.domStats.anomalies !== String(golden.anomalies.length)) {
    throw new Error(`DOM statAnomalies expected ${golden.anomalies.length}, got ${parsedData.domStats.anomalies}`);
  }
  console.log(`  ✓ DOM stats rendered: ${parsedData.domStats.totalNodes} nodes, ${parsedData.domStats.anomalies} anomalies`);

  // Test Drawer interaction for parallel call node output
  await page.evaluate(() => {
    openDrawerById('call-parallel-1');
  });
  await page.waitForSelector('#drawer.open', { visible: true });
  const drawerOutput1 = await page.$eval('#drawerOutput', el => el.textContent.trim());
  if (!drawerOutput1.includes('On branch main') || !drawerOutput1.includes('nothing to commit')) {
    throw new Error(`Drawer output for call-parallel-1 incorrect: ${drawerOutput1}`);
  }
  console.log('  ✓ Drawer correctly displays correlated output for parallel call 1');

  await page.evaluate(() => {
    openDrawerById('call-parallel-2');
  });
  const drawerOutput2 = await page.$eval('#drawerOutput', el => el.textContent.trim());
  if (!drawerOutput2.includes('diff --git a/test b/test')) {
    throw new Error(`Drawer output for call-parallel-2 incorrect: ${drawerOutput2}`);
  }
  console.log('  ✓ Drawer correctly displays correlated output for parallel call 2');

  // Test Drawer interaction for skill node with quoted path
  await page.evaluate(() => {
    openDrawerById('call-skill-quoted');
  });
  const drawerBadges = await page.$eval('#drawerBadges', el => el.textContent.trim());
  if (!drawerBadges.includes('Skill: quota-aware-task-runner')) {
    throw new Error(`Drawer badges expected Skill: quota-aware-task-runner, got: ${drawerBadges}`);
  }
  console.log('  ✓ Drawer badge correctly displays unquoted Skill scope: Skill: quota-aware-task-runner');

  // 4. Verify no uncaught errors or console warnings
  console.log('\nTest 4: Error checks...');
  if (uncaughtErrors.length > 0) {
    throw new Error(`Uncaught runtime errors detected:\n${uncaughtErrors.join('\n')}`);
  }
  console.log('  ✓ 0 uncaught errors or console warnings detected.');

  await browser.close();
  console.log('\n===============================================================');
  console.log('🎉 ALL SYNTHETIC INTEGRATION & PARITY TESTS PASSED!');
  console.log('===============================================================');
}

runSyntheticParityTest().catch(err => {
  console.error('\n💥 Synthetic parity test failed:\n', err.message);
  process.exit(1);
});
