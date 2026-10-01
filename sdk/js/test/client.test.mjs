import assert from 'node:assert/strict';
import { after, before, beforeEach, describe, test } from 'node:test';

import {
  Alror,
  AlrorError,
  ConflictError,
  ForbiddenError,
  NetworkError,
  NotFoundError,
  ServerError,
  TimeoutError,
  UnauthorizedError,
  ValidationError,
  VERSION,
  bakeMs,
  bakeFromMs,
  isTerminal,
} from '@alror/sdk';
import { GOOD_KEY, READ_KEY, sampleDeployment, startFakeServer } from './fake-server.mjs';

let srv;
let alror;
const client = (o = {}) => new Alror({ server: srv.url, apiKey: GOOD_KEY, retryBackoffMs: 1, ...o });

before(async () => {
  srv = await startFakeServer();
});
after(async () => {
  await srv.close();
});
beforeEach(() => {
  srv.requests.length = 0;
  srv.faults.length = 0;
  srv.state.deployments.clear();
  srv.state.events.clear();
  srv.state.jobs.length = 0;
  alror = client();
});

describe('auth and headers', () => {
  test('sends the API key, Accept and User-Agent', async () => {
    const me = await alror.whoami();
    assert.equal(me.org.slug, 'acme');
    assert.equal(me.actor.type, 'api_key');
    assert.ok(me.scopes.includes('deploy:write'));
    const h = srv.requests[0].headers;
    assert.equal(h.authorization, `Bearer ${GOOD_KEY}`);
    assert.equal(h.accept, 'application/json');
    assert.equal(h['user-agent'], `alror-sdk-js/${VERSION}`);
  });

  test('custom user agent and extra headers', async () => {
    await client({ userAgent: 'my-ci/1.0', headers: { 'X-Trace': 'abc' } }).whoami();
    assert.equal(srv.requests[0].headers['user-agent'], 'my-ci/1.0');
    assert.equal(srv.requests[0].headers['x-trace'], 'abc');
  });

  test('accepts a server URL with /api/v1 and trailing slashes', async () => {
    const c = new Alror({ server: `${srv.url}/api/v1/`, apiKey: GOOD_KEY });
    assert.equal(c.server, srv.url);
    await c.whoami();
    assert.equal(srv.requests[0].path, '/whoami');
  });

  test('no key -> UnauthorizedError', async () => {
    const err = await new Alror({ server: srv.url }).whoami().catch((e) => e);
    assert.ok(err instanceof UnauthorizedError);
    assert.ok(err instanceof AlrorError);
    assert.equal(err.code, 'unauthorized');
    assert.equal(err.status, 401);
    assert.equal(err.serverCode, 'unauthorized');
    assert.match(err.message, /GET \/whoami: missing or invalid API key/);
    assert.equal(srv.requests[0].headers.authorization, undefined);
  });

  test('read-only key writing -> ForbiddenError, not retried', async () => {
    const err = await client({ apiKey: READ_KEY })
      .enqueueDeploy({ service: 'checkout-api', image: 'x' })
      .catch((e) => e);
    assert.ok(err instanceof ForbiddenError);
    assert.equal(err.code, 'forbidden');
    assert.equal(err.status, 403);
    assert.equal(srv.requests.length, 1);
  });

  test('fromEnv reads ALROR_SERVER and ALROR_API_KEY', async () => {
    const c = Alror.fromEnv({ ALROR_SERVER: srv.url, ALROR_API_KEY: GOOD_KEY });
    assert.equal((await c.whoami()).org.id, 'org_1');
    assert.throws(() => Alror.fromEnv({}), /ALROR_SERVER/);
  });

  test('constructor validates options', () => {
    assert.throws(() => new Alror({}), /server/);
  });

  test('uses an injected fetch', async () => {
    const seen = [];
    const c = new Alror({
      server: 'https://alror.test',
      apiKey: 'k',
      fetch: async (url, init) => {
        seen.push({ url, init });
        return new Response(JSON.stringify({ org: { id: 'o', slug: 's', name: 'n' }, actor: { type: 'user', id: 'u', label: 'l' }, scopes: [] }), { status: 200 });
      },
    });
    const me = await c.whoami();
    assert.equal(me.actor.type, 'user');
    assert.equal(seen[0].url, 'https://alror.test/api/v1/whoami');
    assert.equal(seen[0].init.headers.Authorization, 'Bearer k');
  });
});

describe('deployments and events', () => {
  test('upsert, get by prefix, update, list with filters', async () => {
    const d1 = sampleDeployment('dep_20261002T093000_aaaaaa');
    const d2 = sampleDeployment('dep_20261002T094500_bbbbbb', { status: 'promoted', created_at: '2026-10-02T09:45:00Z' });
    const saved1 = await alror.upsertDeployment({ ...d1, updated_at: '2000-01-01T00:00:00Z' });
    // The server owns updated_at, keeps created_at and fills environment/source.
    assert.equal(saved1.created_at, d1.created_at);
    assert.notEqual(saved1.updated_at, '2000-01-01T00:00:00Z');
    assert.equal(saved1.environment, 'production');
    assert.equal(saved1.source, 'cli');
    await alror.upsertDeployment(d2);
    assert.equal(srv.requests[0].method, 'POST');
    assert.deepEqual(srv.requests[0].body, { ...d1, updated_at: '2000-01-01T00:00:00Z' });
    assert.equal(srv.requests[0].headers['content-type'], 'application/json');

    const got = await alror.getDeployment('dep_20261002T0930');
    assert.equal(got.id, d1.id);
    assert.equal(got.risk.ai_authored, true);
    assert.equal(got.plan.steps[0].bake, 120_000_000_000);
    assert.equal(bakeMs(got.plan.steps[0]), 120_000);

    const updated = await alror.updateDeployment({ ...d1, status: 'rolled_back', reason: 'error rate +40%' });
    assert.equal(updated.status, 'rolled_back');
    assert.equal(srv.requests.at(-1).method, 'PUT');
    assert.equal(srv.requests.at(-1).path, `/deployments/${d1.id}`);

    const all = await alror.listDeployments();
    assert.deepEqual(all.map((d) => d.id), [d2.id, d1.id], 'newest first');
    const filtered = await alror.listDeployments({ limit: 10, status: 'promoted', service: 'checkout-api' });
    assert.deepEqual(filtered.map((d) => d.id), [d2.id]);
    assert.deepEqual(srv.requests.at(-1).query, { limit: '10', status: 'promoted', service: 'checkout-api' });
  });

  test('unknown service -> ValidationError with serverCode unknown_service', async () => {
    const err = await alror.upsertDeployment(sampleDeployment('dep_x', { service: 'nope' })).catch((e) => e);
    assert.ok(err instanceof ValidationError);
    assert.equal(err.code, 'validation');
    assert.equal(err.status, 422);
    assert.equal(err.serverCode, 'unknown_service');
  });

  test('ambiguous prefix -> ConflictError; missing -> NotFoundError', async () => {
    await alror.upsertDeployment(sampleDeployment('dep_1_aaa'));
    await alror.upsertDeployment(sampleDeployment('dep_1_bbb'));
    const amb = await alror.getDeployment('dep_1').catch((e) => e);
    assert.ok(amb instanceof ConflictError);
    assert.equal(amb.code, 'conflict');
    assert.equal(amb.serverCode, 'ambiguous');
    const nf = await alror.getDeployment('dep_9').catch((e) => e);
    assert.ok(nf instanceof NotFoundError);
    assert.equal(nf.code, 'not_found');
    assert.equal(nf.status, 404);
    assert.equal(srv.requests.filter((r) => r.path === '/deployments/dep_9').length, 1, '4xx is not retried');
  });

  test('ids are URL-encoded', async () => {
    await alror.getDeployment('a/b c').catch(() => {});
    assert.equal(srv.requests[0].path, '/deployments/a%2Fb%20c');
  });

  test('append and list events with verdicts', async () => {
    const d = sampleDeployment();
    await alror.upsertDeployment(d);
    assert.deepEqual(await alror.listEvents(d.id), []);
    const ev = {
      at: '2026-10-02T09:32:00Z',
      kind: 'verdict',
      message: 'step 1 failed',
      weight: 5,
      verdict: { pass: false, summary: 'error rate regressed', results: [{ metric: 'error_rate', canary: 0.04, baseline: 0.01, delta: 3, p_value: 0.001, pass: false, reason: '+300%' }] },
    };
    assert.deepEqual(await alror.appendEvent(d.id, ev), ev);
    const events = await alror.listEvents(d.id);
    assert.deepEqual(events, [ev]);
    assert.equal(events[0].verdict.results[0].p_value, 0.001);
  });

  test('recentRollbacks sends RFC 3339 (server updated_at decides)', async () => {
    await alror.upsertDeployment(sampleDeployment('dep_r1', { status: 'rolled_back' }));
    const hourAgo = new Date(Date.now() - 3600_000);
    hourAgo.setUTCMilliseconds(0);
    assert.deepEqual(await alror.recentRollbacks(hourAgo), { 'checkout-api': 1 });
    assert.equal(srv.requests.at(-1).query.since, hourAgo.toISOString().replace('.000Z', 'Z'));
    await alror.recentRollbacks(new Date('2026-10-01T00:00:00.123Z'));
    assert.equal(srv.requests.at(-1).query.since, '2026-10-01T00:00:00.123Z');
    assert.deepEqual(await alror.recentRollbacks(new Date(Date.now() + 3600_000).toISOString()), {});
  });

  test('listDeployments: environment and before filters, limit bounds', async () => {
    for (let i = 0; i < 5; i++) {
      await alror.upsertDeployment(
        sampleDeployment(`dep_2026100${i}T000000_aaaaaa`, { created_at: `2026-10-0${i + 1}T00:00:00Z`, environment: i % 2 ? 'staging' : 'production' }),
      );
    }
    const staging = await alror.listDeployments({ environment: 'staging' });
    assert.deepEqual(staging.map((d) => d.environment), ['staging', 'staging']);
    const older = await alror.listDeployments({ before: 'dep_20261002T000000_aaaaaa' });
    assert.deepEqual(older.map((d) => d.id), ['dep_20261001T000000_aaaaaa', 'dep_20261000T000000_aaaaaa']);
    const byTime = await alror.listDeployments({ before: new Date('2026-10-02T00:00:00Z') });
    assert.equal(srv.requests.at(-1).query.before, '2026-10-02T00:00:00Z');
    assert.equal(byTime.length, 1);
    const bad = await alror.listDeployments({ before: 'dep_unknown' }).catch((e) => e);
    assert.ok(bad instanceof ValidationError);
    await assert.rejects(alror.listDeployments({ limit: 1001 }), RangeError);
    await assert.rejects(alror.listDeployments({ limit: 0 }), RangeError);
    assert.equal((await alror.listDeployments({ limit: 1000 })).length, 5);
  });

  test('iterDeployments pages with the last id as before until a short page', async () => {
    const n = 1203;
    for (let i = 0; i < n; i++) {
      const id = `dep_${String(i).padStart(5, '0')}`;
      // Same created_at for many rows: paging must still be exact (id desc tiebreak).
      srv.state.deployments.set(id, sampleDeployment(id, { created_at: `2026-10-0${1 + (i % 3)}T00:00:00Z` }));
    }
    srv.state.listCalls.length = 0;
    const seen = [];
    for await (const d of alror.iterDeployments({ service: 'checkout-api' })) seen.push(d.id);
    assert.equal(seen.length, n);
    assert.equal(new Set(seen).size, n, 'no duplicates');
    const calls = srv.state.listCalls;
    assert.equal(calls.length, 3); // 500 + 500 + 203
    assert.ok(calls.every((c) => c.limit === '500' && c.service === 'checkout-api'));
    assert.equal(calls[0].before, undefined);
    assert.equal(calls[1].before, seen[499]);
    assert.equal(calls[2].before, seen[999]);

    // An exact multiple of the page size needs one extra (empty) page.
    srv.state.listCalls.length = 0;
    let count = 0;
    for await (const _ of alror.iterDeployments({ pageSize: 401 })) count++;
    assert.equal(count, n);
    assert.equal(srv.state.listCalls.length, 4); // 401 * 3 = 1203, then an empty page
    // Breaking out early stops fetching.
    srv.state.listCalls.length = 0;
    for await (const _ of alror.iterDeployments({ pageSize: 10 })) break;
    assert.equal(srv.state.listCalls.length, 1);
  });

  test('config round trip', async () => {
    const cfg = await alror.getConfig();
    assert.equal(cfg.project, 'shop');
    assert.equal(cfg.policy.max_regression.error_rate, 0.25);
    cfg.services.push({ name: 'search', paths: ['services/search/'], target: 'kubernetes', cluster: 'prod', namespace: 'search', critical: false });
    const saved = await alror.putConfig(cfg);
    assert.equal(saved.services.length, 2);
    assert.equal(srv.requests.at(-1).method, 'PUT');
    const ro = await client({ apiKey: READ_KEY }).putConfig(cfg).catch((e) => e);
    assert.ok(ro instanceof ForbiddenError);
  });
});

describe('jobs', () => {
  test('enqueueDeploy maps riskOverride to risk_override and omits unset fields', async () => {
    const job = await alror.enqueueDeploy({ service: 'checkout-api', image: 'img:v2', ref: 'abc123', environment: 'production', riskOverride: 80 });
    assert.equal(job.kind, 'deploy');
    assert.equal(job.status, 'queued');
    assert.deepEqual(srv.requests[0].body, {
      kind: 'deploy',
      payload: { service: 'checkout-api', image: 'img:v2', ref: 'abc123', environment: 'production', risk_override: 80 },
    });
    await alror.enqueueDeploy({ service: 'checkout-api', image: 'img:v3', shadow: true });
    assert.deepEqual(srv.requests[1].body.payload, { service: 'checkout-api', image: 'img:v3', shadow: true });
  });

  test('enqueueRollback', async () => {
    const job = await alror.enqueueRollback('dep_1', 'pager fired');
    assert.equal(job.kind, 'rollback');
    assert.deepEqual(job.payload, { deployment_id: 'dep_1', reason: 'pager fired' });
  });

  test('claim -> finish, listJobs by status', async () => {
    const queued = await alror.enqueueDeploy({ service: 'checkout-api', image: 'img:v2' });
    const claimed = await alror.claimJob('runner-1');
    assert.equal(claimed.id, queued.id);
    assert.equal(claimed.status, 'claimed');
    assert.equal(claimed.claimed_by, 'runner-1');
    assert.deepEqual(srv.requests.at(-1).body, { worker: 'runner-1' });
    const done = await alror.finishJob(claimed.id, { status: 'done', deploymentId: 'dep_7' });
    assert.equal(done.status, 'done');
    assert.equal(done.deployment_id, 'dep_7');
    assert.deepEqual(srv.requests.at(-1).body, { status: 'done', deployment_id: 'dep_7' });
    assert.deepEqual((await alror.listJobs({ status: 'done' })).map((j) => j.id), [queued.id]);
    assert.deepEqual(await alror.listJobs({ status: 'queued' }), []);
    assert.equal((await alror.listJobs()).length, 1);
  });

  test('riskOverride accepts a level, a number or a numeric string, and is sent as-is', async () => {
    for (const v of ['low', 'medium', 'high', 0, 100, 42.5, '73']) {
      await alror.enqueueDeploy({ service: 'checkout-api', image: 'img', riskOverride: v });
      assert.deepEqual(srv.requests.at(-1).body.payload.risk_override, v);
    }
    for (const v of ['extreme', 101, -1, '1e2', 'abc', NaN]) {
      await assert.rejects(alror.enqueueDeploy({ service: 'checkout-api', image: 'img', riskOverride: v }), TypeError, String(v));
    }
    const n = srv.requests.length;
    assert.equal(srv.requests.filter((r) => r.method === 'POST').length, n, 'invalid overrides never reach the server');
    assert.equal(n, 7);
  });

  test('heartbeat extends the lease; 409 -> ConflictError code lease_lost, never retried', async () => {
    const queued = await alror.enqueueDeploy({ service: 'checkout-api', image: 'img' });
    const claimed = await alror.claimJob('runner-1');
    assert.equal(claimed.attempts, 1);
    assert.ok(claimed.heartbeat_at);
    const before = srv.requests.length;
    const hb = await alror.heartbeatJob(queued.id, 'runner-1');
    assert.equal(hb.id, queued.id);
    assert.equal(hb.status, 'claimed');
    const req = srv.requests.at(-1);
    assert.equal(req.method, 'POST');
    assert.equal(req.path, `/jobs/${queued.id}/heartbeat`);
    assert.deepEqual(req.body, { worker: 'runner-1' });
    assert.equal(srv.requests.length, before + 1);

    // Another runner now owns the job: our heartbeat lost the lease.
    srv.state.jobs[0].claimed_by = 'runner-2';
    const err = await alror.heartbeatJob(queued.id, 'runner-1').catch((e) => e);
    assert.ok(err instanceof ConflictError);
    assert.ok(err instanceof AlrorError);
    assert.equal(err.code, 'lease_lost');
    assert.equal(err.status, 409);
    assert.equal(err.serverCode, 'lease_lost');
    assert.equal(srv.requests.filter((r) => r.path.endsWith('/heartbeat')).length, 2, 'not retried');

    // finish from the runner that lost the lease is also lease_lost
    const fin = await alror.finishJob(queued.id, { status: 'done', runner: 'runner-1' }).catch((e) => e);
    assert.ok(fin instanceof ConflictError);
    assert.equal(fin.code, 'lease_lost');
    assert.equal(srv.requests.at(-1).body.worker, 'runner-1');
  });

  test('heartbeat is not retried on 5xx or timeouts either', async () => {
    srv.fault({ path: '/jobs/job_x/heartbeat', status: 503, times: 5 });
    const err = await alror.heartbeatJob('job_x', 'r').catch((e) => e);
    assert.ok(err instanceof ServerError);
    assert.equal(srv.requests.length, 1);
    const nf = await alror.heartbeatJob('job_missing', 'r').catch((e) => e);
    assert.ok(nf instanceof NotFoundError);
  });

  test('claim long-poll: 204 resolves null', async () => {
    const t0 = Date.now();
    assert.equal(await alror.claimJob('runner-1'), null);
    assert.ok(Date.now() - t0 >= 100, 'waited for the long-poll window');
  });

  test('claim long-poll wakes up when a job is enqueued', async () => {
    const pending = alror.claimJob('w');
    await new Promise((r) => setTimeout(r, 30));
    const job = await alror.enqueueDeploy({ service: 'checkout-api', image: 'img' });
    const claimed = await pending;
    assert.equal(claimed.id, job.id);
  });

  test('claim is never retried', async () => {
    srv.fault({ path: '/jobs/claim', status: 503 });
    const err = await alror.claimJob('w').catch((e) => e);
    assert.ok(err instanceof ServerError);
    assert.equal(srv.requests.length, 1);
  });

  test('claim uses claimTimeoutMs, not timeoutMs', async () => {
    const c = client({ timeoutMs: 50, claimTimeoutMs: 1000 });
    assert.equal(await c.claimJob('w'), null); // server holds ~150 ms
    const c2 = client({ claimTimeoutMs: 50 });
    const err = await c2.claimJob('w').catch((e) => e);
    assert.ok(err instanceof TimeoutError);
  });

  test('server-side validation', async () => {
    const err = await alror.enqueueDeploy({ service: '', image: '' }).catch((e) => e);
    assert.ok(err instanceof ValidationError);
    assert.equal(err.status, 422);
  });
});

describe('retries and timeouts', () => {
  test('GET retries 5xx with backoff and then succeeds', async () => {
    srv.fault({ path: '/whoami', status: 503, times: 2 });
    const me = await alror.whoami();
    assert.equal(me.org.id, 'org_1');
    assert.equal(srv.requests.length, 3);
  });

  test('GET retries a dropped connection', async () => {
    srv.fault({ path: '/config', destroy: true });
    const cfg = await alror.getConfig();
    assert.equal(cfg.project, 'shop');
    assert.equal(srv.requests.length, 2);
  });

  test('GET gives up after `retries` and throws the last error', async () => {
    srv.fault({ path: '/whoami', status: 500, times: 10, message: 'db down' });
    const err = await client({ retries: 1 }).whoami().catch((e) => e);
    assert.ok(err instanceof ServerError);
    assert.equal(err.code, 'server');
    assert.equal(err.status, 500);
    assert.match(err.message, /db down/);
    assert.equal(err.retryable, true);
    assert.equal(srv.requests.length, 2);
  });

  test('non-GET requests are not retried', async () => {
    srv.fault({ method: 'POST', path: '/jobs', status: 502, times: 5 });
    const err = await alror.enqueueDeploy({ service: 'checkout-api', image: 'x' }).catch((e) => e);
    assert.ok(err instanceof ServerError);
    assert.equal(err.status, 502);
    assert.equal(srv.requests.length, 1);
  });

  test('non-JSON error bodies still map to typed errors', async () => {
    srv.fault({ path: '/whoami', status: 404, raw: 'page not found' });
    const err = await alror.whoami().catch((e) => e);
    assert.ok(err instanceof NotFoundError);
    assert.match(err.message, /page not found/);
    assert.equal(err.serverCode, undefined);
  });

  test('timeout -> TimeoutError (a NetworkError), retried for GET', async () => {
    srv.fault({ path: '/whoami', status: 200, delayMs: 300, times: 5 });
    const c = client({ timeoutMs: 50, retries: 1 });
    const t0 = Date.now();
    const err = await c.whoami().catch((e) => e);
    assert.ok(err instanceof TimeoutError);
    assert.ok(err instanceof NetworkError);
    assert.equal(err.code, 'timeout');
    assert.equal(err.status, 0);
    assert.ok(Date.now() - t0 < 1000);
    assert.equal(srv.requests.length, 2);
  });

  test('per-call timeoutMs overrides the client default', async () => {
    srv.fault({ method: 'POST', path: '/jobs', status: 200, delayMs: 300 });
    const err = await alror.enqueueDeploy({ service: 'checkout-api', image: 'x' }, { timeoutMs: 40 }).catch((e) => e);
    assert.ok(err instanceof TimeoutError);
    assert.equal(srv.requests.length, 1);
  });

  test('connection refused -> NetworkError', async () => {
    const c = new Alror({ server: 'http://127.0.0.1:9', apiKey: GOOD_KEY, retries: 1, retryBackoffMs: 1 });
    const err = await c.whoami().catch((e) => e);
    assert.ok(err instanceof NetworkError);
    assert.ok(!(err instanceof TimeoutError));
    assert.equal(err.code, 'network');
  });

  test('caller abort rejects with AbortError and stops retrying', async () => {
    srv.fault({ path: '/whoami', status: 200, delayMs: 500 });
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 30);
    const err = await alror.whoami({ signal: ac.signal }).catch((e) => e);
    assert.equal(err.name, 'AbortError');
    assert.ok(!(err instanceof AlrorError));
    assert.equal(srv.requests.length, 1);
    const pre = new AbortController();
    pre.abort();
    await assert.rejects(alror.whoami({ signal: pre.signal }), { name: 'AbortError' });
  });

  test('invalid JSON in a 200 -> ServerError', async () => {
    srv.fault({ path: '/config', status: 200, raw: '{not json' });
    const err = await client({ retries: 0 }).getConfig().catch((e) => e);
    assert.ok(err instanceof ServerError);
    assert.match(err.message, /invalid JSON/);
  });
});

describe('stream (SSE)', () => {
  test('receives typed events with the Authorization header, stops on abort', async () => {
    const ac = new AbortController();
    const events = [];
    let opened;
    const opened$ = new Promise((r) => (opened = r));
    const done = alror.stream((ev) => {
      events.push(ev);
      if (events.length === 2) ac.abort();
    }, { signal: ac.signal, onOpen: () => opened() });
    await opened$;
    const streamReq = srv.requests.find((r) => r.path === '/stream');
    assert.equal(streamReq.headers.authorization, `Bearer ${GOOD_KEY}`);
    assert.equal(streamReq.headers.accept, 'text/event-stream');
    const d = sampleDeployment('dep_stream_1');
    await alror.upsertDeployment(d);
    srv.publish('job.updated', { type: 'job.updated', job: { id: 'job_9', status: 'done' } });
    await done;
    assert.equal(events.length, 2);
    assert.equal(events[0].type, 'deployment.updated');
    assert.equal(events[0].data.deployment.id, 'dep_stream_1');
    assert.match(events[0].id, /^\d+$/);
    assert.equal(events[1].type, 'job.updated');
    assert.equal(events[1].data.job.status, 'done');
  });

  test('reconnects after a drop and sends Last-Event-ID', async () => {
    const ac = new AbortController();
    const events = [];
    const errors = [];
    let opens = 0;
    let onOpen;
    const waitOpen = () => new Promise((r) => (onOpen = r));
    let next = waitOpen();
    const done = alror.stream((ev) => events.push(ev), {
      signal: ac.signal,
      onOpen: () => {
        opens++;
        onOpen();
      },
      onError: (e) => errors.push(e),
    });
    await next;
    srv.publish('deployment.event', { type: 'deployment.event', message: 'one' });
    await new Promise((r) => setTimeout(r, 30));
    next = waitOpen();
    srv.dropStreams();
    await next; // reconnected (server sent retry: 20)
    const streams = srv.requests.filter((r) => r.path === '/stream');
    assert.equal(streams.length, 2);
    assert.equal(streams[1].headers['last-event-id'], events[0].id);
    srv.publish('deployment.event', { type: 'deployment.event', message: 'two' });
    await new Promise((r) => setTimeout(r, 30));
    ac.abort();
    await done;
    assert.equal(opens, 2);
    assert.ok(errors.length >= 1);
    assert.deepEqual(events.map((e) => e.data.message), ['one', 'two']);
  });

  test('auth errors reject and are not retried', async () => {
    const err = await new Alror({ server: srv.url, apiKey: 'alr_live_bad' }).stream(() => {}).catch((e) => e);
    assert.ok(err instanceof UnauthorizedError);
    assert.equal(srv.requests.filter((r) => r.path === '/stream').length, 1);
  });

  test('reconnect: false resolves when the stream ends', async () => {
    let opened;
    const o = new Promise((r) => (opened = r));
    const done = alror.stream(() => {}, { reconnect: false, onOpen: () => opened() });
    await o;
    srv.dropStreams();
    // A destroyed socket surfaces as a network error, or as a clean end.
    const res = await done.then(() => 'ended', (e) => e);
    assert.ok(res === 'ended' || res instanceof NetworkError);
  });
});

describe('helpers', () => {
  test('bakeMs / bakeFromMs / isTerminal', () => {
    assert.equal(bakeMs({ bake: 1_500_000 }), 1.5);
    assert.equal(bakeFromMs(120_000), 120_000_000_000);
    assert.equal(isTerminal('promoted'), true);
    assert.equal(isTerminal('rolled_back'), true);
    assert.equal(isTerminal('failed'), true);
    assert.equal(isTerminal('rolling'), false);
  });
});
