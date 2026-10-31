// Removals open in a window over whatever page asked: a library title, a
// torrent, unmanaged files. Pages call openRemoval with the same parameters
// the removal API takes; the panel (App.svelte) shows RemovalWindow.
export type RemovalKind = 'media' | 'torrent' | 'unmanaged';

export interface RemovalRequest {
  kind: RemovalKind;
  // media: type, id, service_id; torrent: hash, service_id; unmanaged:
  // path (one or more).
  params: Record<string, string | string[]>;
}

export const removal = $state<{ request: RemovalRequest | null }>({ request: null });

export function openRemoval(kind: RemovalKind, params: RemovalRequest['params']) {
  removal.request = { kind, params };
}

export function closeRemoval() {
  removal.request = null;
}
