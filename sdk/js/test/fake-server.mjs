// In-process fake of the Alror platform API (contract v1, sections 5 and 6),
// built on node:http. Used by the SDK tests; not a test file itself.
import http from 'node:http';

export const GOOD_KEY = 'alr_live_0123456789abcdefghijklmnopqrstuv';
export const READ_KEY = 'alr_live_readonlyreadonlyreadonlyread';

const SCOPES = {
  [GOOD_KEY]: ['deploy:read', 'deploy:write', 'jobs:run', 'config:write'],
  [READ_KEY]: ['deploy:read'],
};

const NEEDS = [
  // [method, path regex, scope]
  ['GET', /^\/whoami$/, null],
  ['GET', /^\/config$/, 'deploy:read'],
  ['PUT', /^\/config$/, 'config:write'],
  ['GET', /^\/deployments$/, 'deploy:read'],
  ['POST', /^\/deployments$/, 'deploy:write'],
  ['PUT', /^\/deployments\/[^/]+$/, 'deploy:write'],
  ['GET', /^\/deployments\/[^/]+$/, 'deploy:read'],
  ['GET', /^\/deployments\/[^/]+\/events$/, 'deploy:read'],
  ['POST', /^\/deployments\/[^/]+\/events$/, 'deploy:write'],
  ['GET', /^\/rollbacks\/recent$/, 'deploy:read'],
  ['POST', /^\/jobs$/, 'deploy:write'],
  ['GET', /^\/jobs$/, 'deploy:read'],
  ['POST', /^\/jobs\/claim$/, 'jobs:run'],
  ['POST', /^\/jobs\/[^/]+\/heartbeat$/, 'jobs:run'],
  ['POST', /^\/jobs\/[^/]+\/finish$/, 'jobs:run'],
  ['GET', /^\/stream$/, 'deploy:read'],
];

export function sampleConfig() {
  return {
    project: 'shop',
    services: [{ name: 'checkout-api', paths: ['services/checkout/'], target: 'simulated', cluster: '', namespace: '', critical: true }],
    metrics: { provider: 'synthetic' },
    policy: { max_regression: { error_rate: 0.25, latency_p95: 0.15 }, alpha: 0.05, auto_rollback: true, bake_scale: 1 },
    notify: {},
  };
}

export function sampleDeployment(id = 'dep_20261002T093000_a1b2c3', over = {}) {
  return {
    id,
    service: 'checkout-api',
    image: 'registry.example.com/checkout:v2',
    ref: '#42',
    risk: { score: 62, level: 'medium', factors: [{ name: 'critical_path', detail: 'touches payments', points: 20 }], services: ['checkout-api'], ai_authored: true },
    plan: { strategy: 'canary', steps: [{ weight: 5, bake: 120_000_000_000 }, { weight: 100, bake: 0 }] },
    status: 'rolling',
    step_index: 0,
    weight: 5,
    created_at: '2026-10-02T09:30:00Z',
    updated_at: '2026-10-02T09:31:00Z',
    ...over,
  };
}

export async function startFakeServer(opts = {}) {
  const state = {
    config: sampleConfig(),
    deployments: new Map(),
    events: new Map(),
    jobs: [],
    nextJob: 1,
    claimWaitMs: opts.claimWaitMs ?? 150,
    waiters: [],
    streams: new Set(),
    listCalls: [],
  };
  /** Faults consumed in order: {method?, path?, status?, body?, delayMs?, destroy?, times?} */
  const faults = [];
  const requests = [];

  const send = (res, status, body) => {
    if (body === undefined) {
      res.writeHead(status);
      res.end();
      return;
    }
    const s = JSON.stringify(body);
    res.writeHead(status, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(s) });
    res.end(s);
  };
  const fail = (res, status, code, message) => send(res, status, { error: { code, message } });

  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, 'http://x');
    const chunks = [];
    for await (const c of req) chunks.push(c);
    const raw = Buffer.concat(chunks).toString('utf8');
    let body;
    try {
      body = raw ? JSON.parse(raw) : undefined;
    } catch {
      return fail(res, 400, 'bad_request', 'invalid JSON');
    }
    const path = url.pathname.replace(/^\/api\/v1/, '');
    requests.push({ method: req.method, path, query: Object.fromEntries(url.searchParams), headers: req.headers, body });

    if (!url.pathname.startsWith('/api/v1/')) return fail(res, 404, 'not_found', 'no such route');

    const fi = faults.findIndex((f) => (!f.method || f.method === req.method) && (!f.path || f.path === path));
    if (fi !== -1) {
      const f = faults[fi];
      if (--f.times <= 0) faults.splice(fi, 1);
      if (f.delayMs) await new Promise((r) => setTimeout(r, f.delayMs));
      if (f.destroy) return req.socket.destroy();
      if (f.status) return f.raw !== undefined ? (res.writeHead(f.status), res.end(f.raw)) : fail(res, f.status, f.code || 'internal', f.message || 'injected failure');
    }

    // Auth: Bearer API key.
    const auth = req.headers.authorization || '';
    const key = auth.startsWith('Bearer ') ? auth.slice(7) : '';
    const scopes = SCOPES[key];
    if (!scopes) return fail(res, 401, 'unauthorized', 'missing or invalid API key');
    const rule = NEEDS.find(([m, re]) => m === req.method && re.test(path));
    if (!rule) return fail(res, 404, 'not_found', `no route ${req.method} ${path}`);
    if (rule[2] && !scopes.includes(rule[2])) return fail(res, 403, 'forbidden', `API key lacks scope ${rule[2]}`);

    const seg = path.split('/').filter(Boolean).map(decodeURIComponent);
    const now = () => new Date().toISOString();

    // Route key, with ids replaced by "*": "GET /deployments/*/events".
    const route = seg.map((s, i) => (i === 1 && !['claim', 'recent'].includes(s) ? '*' : s)).join('/');
    switch (`${req.method} /${route}`) {
      case 'GET /whoami':
        return send(res, 200, { org: { id: 'org_1', slug: 'acme', name: 'Acme' }, actor: { type: 'api_key', id: 'key_1', label: 'ci' }, scopes });
      case 'GET /config':
        return send(res, 200, state.config);
      case 'PUT /config':
        if (!body || !Array.isArray(body.services)) return fail(res, 422, 'validation', 'services required');
        state.config = body;
        return send(res, 200, state.config);
      case 'GET /deployments': {
        const q = url.searchParams;
        const limit = Number(q.get('limit') || 50);
        if (!Number.isInteger(limit) || limit < 1 || limit > 1000) return fail(res, 422, 'validation', 'limit must be 1 to 1000');
        // created_at desc, id desc
        const cmp = (a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id);
        let list = [...state.deployments.values()].sort(cmp);
        for (const k of ['status', 'service', 'environment']) if (q.get(k)) list = list.filter((d) => d[k] === q.get(k));
        const before = q.get('before');
        if (before) {
          const cur = state.deployments.get(before);
          if (cur) list = list.filter((d) => cmp(cur, d) < 0);
          else if (/^\d{4}-/.test(before) && !Number.isNaN(Date.parse(before))) list = list.filter((d) => Date.parse(d.created_at) < Date.parse(before));
          else return fail(res, 422, 'validation', `unknown cursor ${before}`);
        }
        state.listCalls.push(Object.fromEntries(q));
        return send(res, 200, list.slice(0, limit));
      }
      case 'POST /deployments': {
        if (!state.config.services.some((s) => s.name === body.service)) return fail(res, 422, 'unknown_service', `unknown service "${body.service}"`);
        // The server owns the timestamps: created_at from the first insert, updated_at = now.
        const prev = state.deployments.get(body.id);
        const stored = {
          ...body,
          created_at: prev ? prev.created_at : body.created_at || now(),
          updated_at: now(),
          environment: body.environment || prev?.environment || 'production',
          source: body.source || prev?.source || req.headers['x-alror-source'] || 'cli',
        };
        state.deployments.set(body.id, stored);
        publish('deployment.updated', { type: 'deployment.updated', deployment: stored, at: now() });
        return send(res, 201, stored);
      }
      case 'PUT /deployments/*':
        if (!state.deployments.has(seg[1])) return fail(res, 404, 'not_found', 'deployment not found');
        {
          const prev = state.deployments.get(seg[1]);
          state.deployments.set(seg[1], {
            ...body,
            id: seg[1],
            created_at: prev.created_at,
            updated_at: now(),
            environment: body.environment || prev.environment,
            source: body.source || prev.source,
          });
        }
        return send(res, 200, state.deployments.get(seg[1]));
      case 'GET /deployments/*': {
        const matches = [...state.deployments.keys()].filter((id) => id.startsWith(seg[1]));
        if (matches.length === 0) return fail(res, 404, 'not_found', `deployment ${seg[1]} not found`);
        if (matches.length > 1) return fail(res, 409, 'ambiguous', `prefix ${seg[1]} matches ${matches.length} deployments`);
        return send(res, 200, state.deployments.get(matches[0]));
      }
      case 'GET /deployments/*/events':
        if (!state.deployments.has(seg[1])) return fail(res, 404, 'not_found', 'deployment not found');
        return send(res, 200, state.events.get(seg[1]) || []);
      case 'POST /deployments/*/events': {
        if (!state.deployments.has(seg[1])) return fail(res, 404, 'not_found', 'deployment not found');
        const list = state.events.get(seg[1]) || [];
        list.push(body);
        state.events.set(seg[1], list);
        publish('deployment.event', { type: 'deployment.event', deployment_id: seg[1], event: body, at: now() });
        return send(res, 201, body);
      }
      case 'GET /rollbacks/recent': {
        const since = url.searchParams.get('since');
        if (!since || Number.isNaN(Date.parse(since)) || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)$/.test(since)) {
          return fail(res, 400, 'bad_request', 'since must be RFC 3339');
        }
        const counts = {};
        for (const d of state.deployments.values()) {
          if (d.status === 'rolled_back' && d.updated_at >= since) counts[d.service] = (counts[d.service] || 0) + 1;
        }
        return send(res, 200, counts);
      }
      case 'POST /jobs': {
        if (body.kind === 'deploy' && (!body.payload || !body.payload.service || !body.payload.image)) return fail(res, 422, 'validation', 'service and image are required');
        if (body.kind === 'rollback' && (!body.payload || !body.payload.deployment_id)) return fail(res, 422, 'validation', 'deployment_id is required');
        if (body.kind !== 'deploy' && body.kind !== 'rollback') return fail(res, 422, 'validation', 'unknown kind');
        const job = { id: `job_${state.nextJob++}`, kind: body.kind, payload: body.payload, status: 'queued', created_at: now() };
        state.jobs.push(job);
        const w = state.waiters.shift();
        if (w) w();
        return send(res, 201, job);
      }
      case 'GET /jobs': {
        const st = url.searchParams.get('status');
        return send(res, 200, st ? state.jobs.filter((j) => j.status === st) : state.jobs);
      }
      case 'POST /jobs/claim': {
        const take = () => state.jobs.find((j) => j.status === 'queued');
        let job = take();
        if (!job) {
          // Long-poll: wait for an enqueue or the window to pass.
          await new Promise((resolve) => {
            const t = setTimeout(resolve, state.claimWaitMs);
            state.waiters.push(() => {
              clearTimeout(t);
              resolve();
            });
          });
          job = take();
        }
        if (!job) return send(res, 204);
        Object.assign(job, { status: 'claimed', claimed_by: body.worker || body.runner, claimed_at: now(), heartbeat_at: now(), attempts: (job.attempts || 0) + 1 });
        return send(res, 200, job);
      }
      case 'POST /jobs/*/heartbeat': {
        const job = state.jobs.find((j) => j.id === seg[1]);
        if (!job) return fail(res, 404, 'not_found', 'job not found');
        if (job.status !== 'claimed' || job.claimed_by !== body.worker) return fail(res, 409, 'lease_lost', 'job is no longer claimed by this runner');
        job.heartbeat_at = now();
        return send(res, 200, job);
      }
      case 'POST /jobs/*/finish': {
        const job = state.jobs.find((j) => j.id === seg[1]);
        if (!job) return fail(res, 404, 'not_found', 'job not found');
        if (job.status === body.status) return send(res, 200, job); // idempotent repeat
        if (job.status !== 'claimed' || (body.worker && job.claimed_by !== body.worker)) return fail(res, 409, 'lease_lost', 'job is not claimed by this runner');
        if (body.status !== 'done' && body.status !== 'failed') return fail(res, 422, 'validation', 'status must be done or failed');
        Object.assign(job, { status: body.status, finished_at: now() });
        if (body.error) job.error = body.error;
        if (body.deployment_id) job.deployment_id = body.deployment_id;
        return send(res, 200, job);
      }
      case 'GET /stream': {
        res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', Connection: 'keep-alive' });
        res.write(': connected\n\nretry: 20\n\n');
        const s = { res, lastEventId: req.headers['last-event-id'] || '' };
        state.streams.add(s);
        res.on('close', () => state.streams.delete(s));
        opts.onStream?.(s);
        return;
      }
      default:
        return fail(res, 404, 'not_found', `no route ${req.method} ${path}`);
    }
  });

  let eventId = 0;
  function publish(type, data) {
    eventId++;
    for (const s of state.streams) s.res.write(`id: ${eventId}\nevent: ${type}\ndata: ${JSON.stringify(data)}\n\n`);
  }

  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const url = `http://127.0.0.1:${server.address().port}`;
  return {
    url,
    state,
    faults,
    requests,
    publish,
    /** Add a fault matching the next `times` requests. */
    fault(f) {
      faults.push({ times: 1, ...f });
    },
    /** Close every open SSE connection (simulates a dropped stream). */
    dropStreams() {
      for (const s of state.streams) s.res.destroy();
    },
    close: () =>
      new Promise((r) => {
        for (const s of state.streams) s.res.destroy();
        server.closeAllConnections?.();
        server.close(() => r());
      }),
  };
}
