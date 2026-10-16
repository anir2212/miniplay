import test from 'node:test';
import assert from 'node:assert/strict';
import { hashSeed, replay, targets } from './logic.ts';

test('target stream is deterministic and distinct between seeds', () => {
  assert.deepEqual(targets('rhythm', 'sample'), targets('rhythm', 'sample'));
  assert.notDeepEqual(targets('reflex', 'sample'), targets('reflex', 'other'));
  assert.equal(hashSeed('sample'), 2531492519);
});

test('one valid tap per target; forged score cannot alter replay', () => {
  const [first] = targets('reflex', 'sample');
  assert.equal(replay('reflex', 'sample', [{ ...first }, { ...first }]), 15);
  assert.equal(replay('reflex', 'sample', [{ ...first, x: 0, y: 0 }]), 0);
  assert.throws(() => replay('rhythm', 'sample', [{ ms: -1, x: 500, y: 500 }]));
});
