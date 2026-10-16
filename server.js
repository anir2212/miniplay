const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');

const SECRET = process.env.MINIPLAY_SECRET || 'local-demo-secret-change-me';
const games = new Set(['rhythm', 'reflex']);
const used = new Set();
const scores = [];
const audit = [];

function sign(payload) {
  const encoded = Buffer.from(JSON.stringify(payload)).toString('base64url');
  const mac = crypto.createHmac('sha256', SECRET).update(encoded).digest('base64url');
  return `${encoded}.${mac}`;
}
function verify(token) {
  if (typeof token !== 'string') throw new Error('invalid token');
  const [encoded, mac, extra] = token.split('.');
  if (!encoded || !mac || extra) throw new Error('invalid token');
  const expected = crypto.createHmac('sha256', SECRET).update(encoded).digest();
  const given = Buffer.from(mac, 'base64url');
  if (given.length !== expected.length || !crypto.timingSafeEqual(given, expected)) throw new Error('bad signature');
  const payload = JSON.parse(Buffer.from(encoded, 'base64url').toString());
  if (Date.now() > payload.expires) throw new Error('expired token');
  return payload;
}
function start(user, game) {
  if (!/^[a-zA-Z0-9_-]{1,40}$/.test(user) || !games.has(game)) throw new Error('invalid user or game');
  const payload = { user, game, nonce: crypto.randomUUID(), issued: Date.now(), expires: Date.now() + 120000 };
  return { token: sign(payload), seed: payload.nonce };
}
function replay(game, events) {
  if (!Array.isArray(events) || events.length > 100 || events.some(e => !Number.isInteger(e.ms) || e.ms < 0 || e.ms > 60000)) throw new Error('invalid events');
  if (events.some((e, i) => i && e.ms < events[i-1].ms)) throw new Error('events out of order');
  const hits = new Set();
  for (const event of events) {
    const interval = game === 'rhythm' ? 500 : 900;
    const tolerance = game === 'rhythm' ? 120 : 170;
    const beat = Math.round(event.ms / interval);
    if (beat > 0 && beat <= 20 && Math.abs(event.ms - beat * interval) <= tolerance) hits.add(beat);
  }
  return hits.size * (game === 'rhythm' ? 10 : 15);
}
function submit(token, events, claimedScore, durationMs) {
  const payload = verify(token);
  if (used.has(payload.nonce)) throw new Error('run already submitted');
  if (!Number.isInteger(durationMs) || durationMs < 1000 || durationMs > 60000) throw new Error('implausible duration');
  if (Date.now() - payload.issued < 500) throw new Error('run submitted too fast');
  const score = replay(payload.game, events);
  if (score !== claimedScore || events.some(e => e.ms > durationMs + 250)) {
    audit.push({ user: payload.user, game: payload.game, reason: 'score or duration mismatch' });
    throw new Error('score or duration mismatch');
  }
  used.add(payload.nonce);
  const row = { user: payload.user, game: payload.game, score, at: new Date().toISOString() };
  scores.push(row);
  return row;
}
function leaderboard(game, period = 'all', user = null) {
  if (!games.has(game) || !['all', 'daily', 'weekly'].includes(period)) throw new Error('invalid board');
  const cutoff = period === 'daily' ? Date.now() - 86400000 : period === 'weekly' ? Date.now() - 7*86400000 : 0;
  const best = new Map();
  for (const row of scores) if (row.game === game && Date.parse(row.at) >= cutoff)
    best.set(row.user, Math.max(best.get(row.user) || 0, row.score));
  const board = [...best].map(([name, score]) => ({ user: name, score })).sort((a,b) => b.score-a.score || a.user.localeCompare(b.user));
  return { leaders: board.slice(0, 20), myRank: user ? board.findIndex(row => row.user === user) + 1 : null };
}
function respond(res, status, body) {
  res.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' });
  res.end(JSON.stringify(body));
}
async function body(req) {
  let content = '';
  for await (const chunk of req) {
    content += chunk;
    if (content.length > 20000) throw new Error('body too large');
  }
  return JSON.parse(content);
}
function createServer() {
  return http.createServer(async (req, res) => {
    try {
      const url = new URL(req.url, 'http://localhost');
      if (req.method === 'POST' && url.pathname === '/api/start') {
        const data = await body(req); return respond(res, 200, start(data.user, data.game));
      }
      if (req.method === 'POST' && url.pathname === '/api/submit') {
        const data = await body(req); return respond(res, 200, submit(data.token, data.events, data.score, data.durationMs));
      }
      if (req.method === 'GET' && url.pathname === '/api/leaderboard')
        return respond(res, 200, leaderboard(url.searchParams.get('game'), url.searchParams.get('period') || 'all', url.searchParams.get('user')));
      if (req.method === 'GET' && url.pathname === '/api/admin') return respond(res, 200, { runs: scores.length, rejected: audit.length });
      const file = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      if (!['index.html', 'game.js'].includes(file)) return respond(res, 404, { error: 'not found' });
      res.writeHead(200, { 'content-type': file.endsWith('.js') ? 'text/javascript' : 'text/html' });
      fs.createReadStream(path.join(__dirname, 'public', file)).pipe(res);
    } catch (error) { respond(res, 400, { error: error.message }); }
  });
}
if (require.main === module) createServer().listen(8080, '127.0.0.1', () => console.log('MiniPlay http://127.0.0.1:8080'));
module.exports = { start, verify, replay, submit, leaderboard, createServer };
