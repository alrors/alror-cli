'use strict';
// HTTPS GET with redirects, timeouts and a simple HTTPS_PROXY (CONNECT)
// tunnel, using only Node built-ins.

const http = require('http');
const https = require('https');
const tls = require('tls');

const MAX_BYTES = 200 * 1024 * 1024;

function proxyFor(target, env = process.env) {
  const proxy = env.HTTPS_PROXY || env.https_proxy || env.ALL_PROXY || env.all_proxy;
  if (!proxy) return null;
  const noProxy = (env.NO_PROXY || env.no_proxy || '').split(',').map((s) => s.trim().toLowerCase()).filter(Boolean);
  const host = target.hostname.toLowerCase();
  for (const np of noProxy) {
    if (np === '*') return null;
    const bare = np.replace(/^\*?\./, '').replace(/:\d+$/, '');
    if (host === bare || host.endsWith(`.${bare}`)) return null;
  }
  try {
    const u = new URL(proxy.includes('://') ? proxy : `http://${proxy}`);
    return u.protocol === 'http:' || u.protocol === 'https:' ? u : null;
  } catch {
    return null;
  }
}

function tunnel(proxy, target, timeoutMs) {
  return new Promise((resolve, reject) => {
    const port = target.port || 443;
    const headers = { Host: `${target.hostname}:${port}` };
    if (proxy.username) {
      const cred = `${decodeURIComponent(proxy.username)}:${decodeURIComponent(proxy.password)}`;
      headers['Proxy-Authorization'] = `Basic ${Buffer.from(cred).toString('base64')}`;
    }
    const mod = proxy.protocol === 'https:' ? https : http;
    const req = mod.request({
      host: proxy.hostname,
      port: proxy.port || (proxy.protocol === 'https:' ? 443 : 80),
      method: 'CONNECT',
      path: `${target.hostname}:${port}`,
      headers,
      timeout: timeoutMs,
    });
    req.on('connect', (res, socket) => {
      if (res.statusCode !== 200) {
        socket.destroy();
        reject(new Error(`proxy CONNECT failed with HTTP ${res.statusCode}`));
        return;
      }
      resolve(socket);
    });
    req.on('timeout', () => req.destroy(new Error('proxy connection timed out')));
    req.on('error', reject);
    req.end();
  });
}

/**
 * Download a URL into a Buffer.
 * @param {string} url
 * @param {{timeoutMs?: number, maxRedirects?: number, env?: NodeJS.ProcessEnv}} [opts]
 * @returns {Promise<Buffer>}
 */
async function download(url, opts = {}) {
  const { timeoutMs = 60_000, maxRedirects = 5, env = process.env } = opts;
  let current = new URL(url);
  for (let hop = 0; hop <= maxRedirects; hop++) {
    if (current.protocol !== 'https:' && current.protocol !== 'http:') {
      throw new Error(`refusing to download from ${current.protocol} URL`);
    }
    const proxy = current.protocol === 'https:' ? proxyFor(current, env) : null;
    const reqOpts = {
      method: 'GET',
      headers: { 'User-Agent': 'alror-npm-installer', Accept: 'application/octet-stream' },
      timeout: timeoutMs,
    };
    if (proxy) {
      const socket = await tunnel(proxy, current, timeoutMs);
      reqOpts.agent = false;
      reqOpts.createConnection = () => tls.connect({ socket, servername: current.hostname });
    }
    const res = await new Promise((resolve, reject) => {
      const mod = current.protocol === 'https:' ? https : http;
      const req = mod.request(current, reqOpts, resolve);
      req.on('timeout', () => req.destroy(new Error(`timed out downloading ${current.href}`)));
      req.on('error', reject);
      req.end();
    });
    const status = res.statusCode || 0;
    if (status >= 300 && status < 400 && res.headers.location) {
      res.resume();
      current = new URL(res.headers.location, current);
      continue;
    }
    if (status !== 200) {
      res.resume();
      throw new Error(`GET ${current.href}: HTTP ${status}`);
    }
    return await new Promise((resolve, reject) => {
      const chunks = [];
      let total = 0;
      res.on('data', (c) => {
        total += c.length;
        if (total > MAX_BYTES) res.destroy(new Error('download too large'));
        else chunks.push(c);
      });
      res.on('end', () => resolve(Buffer.concat(chunks)));
      res.on('error', reject);
      res.on('aborted', () => reject(new Error('download aborted')));
    });
  }
  throw new Error(`too many redirects for ${url}`);
}

module.exports = { download, proxyFor };
