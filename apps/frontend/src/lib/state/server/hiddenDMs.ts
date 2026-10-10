import { computed, signal } from '@chatto/client/reactivity';
import { Codecs, serverSlot } from '@chatto/client/storage/slot';

/** Device-local sidebar choices, scoped to one server and account. No chat content is saved. */
export class HiddenDMs {
  readonly #values = signal<Record<string, string[]>>({});
  readonly #ids;

  constructor(serverId: string, viewerId: () => string | null | undefined) {
    const slot = () => {
      const accountId = viewerId();
      return accountId
        ? serverSlot(
            serverId,
            `hiddenDMs:${accountId}`,
            [],
            Codecs.json<string[]>(
              (value): value is string[] =>
                Array.isArray(value) && value.every((id) => typeof id === 'string')
            )
          )
        : null;
    };
    this.#ids = computed(() => {
      const accountId = viewerId();
      if (!accountId) return [];
      return this.#values.get()[accountId] ?? slot()?.get() ?? [];
    });
    this.setHidden = (roomId, hidden) => {
      const storage = slot();
      if (!storage) return;
      const accountId = viewerId();
      if (!accountId) return;
      const ids = this.#values.get()[accountId] ?? storage.get();
      if (ids.includes(roomId) === hidden) return;
      const next = hidden ? [...ids, roomId] : ids.filter((id) => id !== roomId);
      storage.set(next);
      // Browser storage is best effort; keep the current session usable if it fails.
      this.#values.set({ ...this.#values.get(), [accountId]: next });
    };
  }

  /** Whether this account has hidden the conversation from its normal DM list. */
  isHidden(roomId: string): boolean {
    return this.#ids.get().includes(roomId);
  }

  /** Hide or restore a conversation without changing membership or notifications. */
  readonly setHidden: (roomId: string, hidden: boolean) => void;
}
