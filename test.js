const test = require('node:test');
const assert = require('node:assert/strict');
const { start, verify, replay, submit, leaderboard, createServer } = require('./server');

test('signed token and replay scoring', () => {
  const {token} = start('alice', 'rhythm');
  assert.equal(verify(token).user, 'alice');
  assert.equal(replay('rhythm', [{ms:500},{ms:1000},{ms:1020}]), 20);
  assert.throws(() => verify(token + 'x'));
});
test('idempotent submission and ranking', async () => {
  const {token} = start('bob', 'reflex');
  await new Promise(resolve => setTimeout(resolve, 550));
  const row = submit(token, [{ms:900}], 15, 1000);
  assert.equal(row.score, 15);
  assert.throws(() => submit(token, [{ms:900}], 15, 1000));
  assert.equal(leaderboard('reflex', 'all', 'bob').myRank, 1);
});
test('HTTP start and leaderboard endpoints', async () => {
  const server = createServer();
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try {
    const base = `http://127.0.0.1:${server.address().port}`;
    const started = await fetch(base + '/api/start', { method: 'POST', headers: {'content-type':'application/json'}, body: JSON.stringify({user:'http-user',game:'rhythm'}) });
    assert.equal(started.status, 200);
    assert.equal(verify((await started.json()).token).user, 'http-user');
    const board = await fetch(base + '/api/leaderboard?game=rhythm');
    assert.equal(board.status, 200);
    assert.ok(Array.isArray((await board.json()).leaders));
  } finally { await new Promise(resolve => server.close(resolve)); }
});
