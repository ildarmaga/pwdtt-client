import { expireCompactLabel, expireLabel } from './expireLabel.ts';
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

describe('expireLabel', () => {
  it('keeps unlimited regular users', () => {
    const stats = { expire: 0, expireKind: 'user', expireState: 'unlimited' };
    assert.equal(expireLabel(stats), 'Бессрочно');
    assert.equal(expireCompactLabel(stats), '∞');
  });

  it('treats missing new fields as the old expire unix date', () => {
    const expireDate = new Date(2026, 8, 25, 12, 0, 0);
    const now = new Date(2026, 8, 13, 12, 0, 0).getTime();
    const label = expireLabel({ expire: Math.floor(expireDate.getTime() / 1000) }, now);
    assert.match(label, /Осталось: 12 дней/);
    assert.match(label, /до 25\.09\.2026/);
  });

  it('shows promo pending instead of unlimited', () => {
    const stats = { expire: 0, expireKind: 'promo', expireState: 'pending' };
    assert.equal(expireLabel(stats), 'Промо · срок начнётся при подключении');
  });

  it('shows promo remaining with hours', () => {
    const now = Date.UTC(2026, 8, 20, 12, 0, 0);
    const stats = {
      expire: now / 1000 + 2 * 86400 + 5 * 3600,
      expireKind: 'promo',
      expireState: 'active',
      remainingSeconds: 2 * 86400 + 5 * 3600,
      serverTs: now / 1000,
      fetchedAt: now,
    };
    assert.equal(expireLabel(stats, now), 'Промо · устройству осталось: 2 дня 5 часов');
  });

  it('ticks promo remaining locally', () => {
    const now = Date.UTC(2026, 8, 20, 12, 0, 0);
    const stats = {
      expire: now / 1000 + 90 * 60,
      expireKind: 'promo',
      expireState: 'active',
      remainingSeconds: 90 * 60,
      serverTs: now / 1000,
      fetchedAt: now,
    };
    const later = now + 30 * 60 * 1000;
    assert.equal(expireLabel(stats, later), 'Промо · устройству осталось: 1 час');
  });

  it('shows expired promo device', () => {
    const stats = {
      expire: 1,
      expireKind: 'promo',
      expireState: 'expired',
      remainingSeconds: 0,
    };
    assert.equal(expireLabel(stats), 'Срок устройства истёк');
  });
});
