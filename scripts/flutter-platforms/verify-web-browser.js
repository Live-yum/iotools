// Run only as an ordinary repository CI test; this is not a cloud-browser bypass.
const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const { mkdtemp, mkdir, writeFile, rm } = require('node:fs/promises');
const { tmpdir } = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { chromium } = require('playwright');

(async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'iotools-web-browser-'));
  const output = path.resolve('platform-evidence/web-browser');
  await mkdir(output, { recursive: true });
  const received = [];
  const echo = http.createServer(async (request, response) => {
    let body = ''; for await (const chunk of request) body += chunk;
    received.push(body);
    response.writeHead(200, { 'Content-Type': 'text/plain; charset=utf-8' });
    response.end('浏览器真实响应😀');
  });
  await new Promise(resolve => echo.listen(0, '127.0.0.1', resolve));
  await writeFile(path.join(root, 'iotools.yaml'), `version: 1\nrequests:\n  - id: browser-exact\n    name: 浏览器精确写入\n    protocol: http\n    action: POST\n    endpoint: http://127.0.0.1:${echo.address().port}/echo\n    timeout: 5s\n    params:\n      json:\n        number: 18446744073709551615\n        string: "18446744073709551615"\n`);
  const gateway = spawn(path.resolve(process.argv[2]), ['-data', root], { stdio: ['ignore', 'pipe', 'pipe'] });
  let logs = '', browser, page;
  gateway.stdout.on('data', b => { logs += b; });
  gateway.stderr.on('data', b => { logs += b; });
  try {
    const deadline = Date.now() + 15000;
    while (!/http:\/\/127\.0\.0\.1:\d+/.test(logs)) {
      if (gateway.exitCode !== null || Date.now() > deadline) throw Error(`Gateway did not start: ${logs}`);
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    const origin = logs.match(/http:\/\/127\.0\.0\.1:\d+/)[0];
    browser = await chromium.launch({ channel: 'chrome', headless: true, chromiumSandbox: true });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const external = [], errors = [];
    await context.route('**/*', route => {
      const url = new URL(route.request().url());
      if (!['data:', 'blob:'].includes(url.protocol) && url.origin !== origin) {
        external.push(url.origin); return route.abort();
      }
      return route.continue();
    });
    page = await context.newPage();
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(origin);
    // Flutter intentionally places this accessible control at (-1,-1).
    // A focused keyboard activation is the supported assistive-technology path.
    await page.locator('flt-semantics-placeholder').press('Enter', { timeout: 30000 });
    // Flutter merges this visible card's text into its accessible group name.
    // Role locators still require real visibility/actionability and use a click.
    const requestCard = () => page.getByRole('group', { name: /HTTP\s+浏览器精确写入\s+POST/ });
    await requestCard().waitFor({ state: 'visible', timeout: 30000 });
    await page.screenshot({ path: path.join(output, '00-config-card.png'), fullPage: true });
    await requestCard().click();
    await page.getByRole('button', { name: '执行', exact: true }).click();
    await page.getByText('确认执行写操作', { exact: true }).waitFor();
    const review = await page.locator('body').evaluate(body => [
      body.innerText,
      ...Array.from(body.querySelectorAll('[aria-label]'), element => element.getAttribute('aria-label')),
      ...Array.from(body.querySelectorAll('input, textarea'), element => element.value),
    ].join('\n'));
    assert(review.includes('"number":18446744073709551615'));
    assert(review.includes('"string":"18446744073709551615"'));
    assert.equal(received.length, 0);
    await page.screenshot({ path: path.join(output, '01-exact-review.png'), fullPage: true });
    await page.getByRole('button', { name: '取消', exact: true }).click();
    assert.equal(received.length, 0);
    await page.getByRole('button', { name: '执行', exact: true }).click();
    await page.getByRole('button', { name: '确认执行', exact: true }).click();
    await page.getByText('已完成', { exact: true }).first().waitFor({ timeout: 15000 });
    assert.equal(received.length, 1);
    assert(received[0].includes('"number":18446744073709551615'));
    assert(received[0].includes('"string":"18446744073709551615"'));
    await page.getByText('浏览器真实响应😀', { exact: false })
      .or(page.getByLabel('浏览器真实响应😀', { exact: false })).first().waitFor();
    await page.screenshot({ path: path.join(output, '02-real-response.png'), fullPage: true });
    await page.reload();
    // Flutter intentionally places this accessible control at (-1,-1).
    // A focused keyboard activation is the supported assistive-technology path.
    await page.locator('flt-semantics-placeholder').press('Enter', { timeout: 30000 });
    await requestCard().waitFor({ state: 'visible', timeout: 30000 });
    assert.equal(received.length, 1, 'reload must not replay a confirmed write');
    assert.deepEqual(errors, []);
    assert.deepEqual(external, [], 'all UI, fonts and CanvasKit resources must be local');
    await writeFile(path.join(output, 'result.json'), JSON.stringify({ status: 'passed', browser: await browser.version(), exact_http_writes: 1, cancelled_writes: 0, reload_replays: 0, external_requests: external, page_errors: errors }, null, 2));
    console.log('PASS actual Chrome UI + local Go gateway, exact HTTP write, cancel, reload/no replay, offline resources');
  } catch (error) {
    if (page) {
      await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true }).catch(() => {});
      await writeFile(path.join(output, 'failure-dom.txt'), await page.content().catch(() => 'unavailable'));
    }
    throw error;
  } finally {
    if (browser) await browser.close();
    gateway.kill('SIGINT');
    await Promise.race([new Promise(resolve => gateway.once('exit', resolve)), new Promise(resolve => setTimeout(resolve, 5000))]);
    if (gateway.exitCode === null) gateway.kill('SIGTERM');
    await new Promise(resolve => echo.close(resolve));
    await writeFile(path.join(output, 'gateway.log'), logs);
    await rm(root, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
