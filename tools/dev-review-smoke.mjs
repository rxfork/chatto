// SPDX-FileCopyrightText: 2026 ChattoCorp GmbH
// SPDX-License-Identifier: AGPL-3.0-or-later

// Check the real review server behind local and simulated Codespaces forwarding.
// Each run owns an empty data directory and stops its server before completion.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import net from 'node:net';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
await mkdir(join(root, '.context'), { recursive: true });
const interrupted = new AbortController();
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.once(signal, () => interrupted.abort());
}

// Reserve the HTTP and embedded NATS ports together, then release for startup.
async function availablePort() {
  for (;;) {
    const http = net.createServer();
    await new Promise((resolve) => http.listen(0, '127.0.0.1', resolve));
    const port = http.address().port;
    const nats = net.createServer();
    try {
      await new Promise((resolve, reject) => {
        nats.once('error', reject);
        nats.listen(port + 4, '127.0.0.1', resolve);
      });
      return port;
    } catch (error) {
      if (error.code !== 'EADDRINUSE' && error.code !== 'ERR_SOCKET_BAD_PORT') throw error;
    } finally {
      await new Promise((resolve) => http.close(resolve));
      if (nats.listening) await new Promise((resolve) => nats.close(resolve));
    }
  }
}

for (const forwarding of [
  { name: 'local', codespace: '', domain: '' },
  { name: 'Codespaces', codespace: 'review-smoke', domain: '' },
  { name: 'custom forwarding domain', codespace: 'review-smoke', domain: 'forward.example' }
]) {
  interrupted.signal.throwIfAborted();
  const port = await availablePort();
  const data = await mkdtemp(join(root, '.context/review-smoke-'));
  const origin = forwarding.codespace
    ? `https://${forwarding.codespace}-${port}.${forwarding.domain || 'app.github.dev'}`
    : `http://localhost:${port}`;
  const backend = `http://127.0.0.1:${port}`;
  const server = spawn('bash', ['tools/dev-review.sh'], {
    cwd: root,
    env: {
      ...process.env,
      CHATTO_DEV_PORT_BASE: String(port),
      CHATTO_DEV_DATA_ROOT: data,
      CODESPACE_NAME: forwarding.codespace,
      GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN: forwarding.domain
    },
    // Server logs can contain local account identifiers; do not publish them.
    stdio: 'ignore'
  });
  const exited = once(server, 'exit');
  try {
    const deadline = Date.now() + 30_000;
    for (;;) {
      interrupted.signal.throwIfAborted();
      assert.equal(server.exitCode, null, 'review server exited during startup');
      try {
        const response = await fetch(`${backend}/readyz`, { signal: AbortSignal.timeout(1000) });
        if (response.ok) break;
      } catch {
        // Wait for the listener and bootstrap to become ready.
      }
      assert.ok(Date.now() < deadline, 'review server did not become ready');
      await delay(100, undefined, { signal: interrupted.signal });
    }
    const metadata = await fetch(`${backend}/.well-known/oauth-authorization-server`);
    assert.equal((await metadata.json()).issuer, origin);
    const frontend = await fetch(backend);
    assert.equal(frontend.status, 200);
    assert.match(await frontend.text(), /<!doctype html>/i);
    const login = await fetch(`${backend}/auth/browser/login`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Chatto-Authentication-Mode': 'cookie',
        Origin: origin,
        Host: new URL(origin).host,
        'X-Forwarded-Proto': forwarding.codespace ? 'https' : 'http'
      },
      body: JSON.stringify({ login: 'alice', password: 'foobar123' })
    });
    assert.equal(login.status, 200, 'bootstrap password login failed');
    const cookies = login.headers.getSetCookie();
    assert.ok(
      cookies.some((cookie) => /HttpOnly/i.test(cookie)),
      'missing session cookie'
    );
    if (forwarding.codespace) {
      assert.ok(cookies.some((cookie) => /HttpOnly/i.test(cookie) && /; Secure/i.test(cookie)));
    }
    assert.ok(!Object.hasOwn(await login.json(), 'token'), 'browser login issued a bearer token');
    console.log(`Review startup, frontend, origin, and browser login passed: ${forwarding.name}`);
  } finally {
    server.kill('SIGTERM');
    const stopped = await Promise.race([exited.then(() => true), delay(5000).then(() => false)]);
    if (!stopped) {
      server.kill('SIGKILL');
      await exited;
    }
    await rm(data, { recursive: true, force: true });
    assert.equal(stopped, true, 'review server did not stop on SIGTERM');
  }
}
