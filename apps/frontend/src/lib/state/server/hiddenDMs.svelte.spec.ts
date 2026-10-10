import { beforeEach, describe, expect, it } from 'vitest';
import { signal } from '@chatto/client/reactivity';
import { serverSlot, Codecs } from '@chatto/client/storage/slot';
import { HiddenDMs } from './hiddenDMs';

describe('HiddenDMs', () => {
  beforeEach(() => localStorage.clear());

  it('persists hiding and restoring independently for each server and account', () => {
    const account = signal<string | null>('alice');
    const preferences = new HiddenDMs('first', () => account.get());
    preferences.setHidden('dm', true);
    expect(preferences.isHidden('dm')).toBe(true);
    expect(new HiddenDMs('first', () => 'alice').isHidden('dm')).toBe(true);
    expect(new HiddenDMs('second', () => 'alice').isHidden('dm')).toBe(false);
    account.set('bob');
    expect(preferences.isHidden('dm')).toBe(false);
    preferences.setHidden('other', true);
    account.set(null);
    expect(preferences.isHidden('other')).toBe(false);
    preferences.setHidden('dm', true);
    account.set('alice');
    expect(preferences.isHidden('dm')).toBe(true);
    expect(preferences.isHidden('other')).toBe(false);
    preferences.setHidden('dm', false);
    expect(preferences.isHidden('dm')).toBe(false);
    expect(new HiddenDMs('first', () => 'alice').isHidden('dm')).toBe(false);
  });

  it('rejects corrupt preferences and repeated hides do not duplicate IDs', () => {
    const storage = serverSlot<unknown>('first', 'hiddenDMs:alice', null, Codecs.json());
    storage.set({ room: true });
    const preferences = new HiddenDMs('first', () => 'alice');
    expect(preferences.isHidden('dm')).toBe(false);
    preferences.setHidden('dm', true);
    preferences.setHidden('dm', true);
    expect(storage.get()).toEqual(['dm']);
  });
});
