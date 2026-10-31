// Live updates: Overtide pushes a "revision" on /ui/events each time
// something changes (a task ran, a removal moved on, the disks' bytes
// ticked). The panel then asks /ui/status what stands (operations in
// progress, failures to notice), and tells the page, which reloads or shows
// "Updates available" according to its policy (follow).
//
// The server ends a stream every few minutes on purpose (a connection can
// go silently dead), and EventSource can't tell that from a network
// failure: consecutive failures are counted, reset on each open, and only
// after several in a row does the panel fall back to asking every 5 s.

export interface Revision {
  kind: string;
  revision?: number;
}

interface StatusNotice {
  id: number | string;
  label?: string;
  message?: string;
  status?: string;
}

export const live = $state({
  pendingOperations: 0,
  notices: [] as StatusNotice[],
  /** updates: a list page has changes it didn't show yet. */
  updates: false,
});

type Listener = (revision: Revision) => void;
const listeners = new Set<Listener>();
const applyListeners = new Set<() => void>();

const maxReconnectAttempts = 5;
let events: EventSource | null = null;
let pollTimer: ReturnType<typeof setInterval> | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectAttempts = 0;
let etag = '';
// A new stream always replays the current revision first: only a strictly
// higher number than the last one seen is a real change.
let lastRevision: number | null = null;
let started = false;

/** startLive opens the stream; the panel calls it once signed in. */
export function startLive() {
  if (started) return;
  started = true;
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) refreshStatus({ kind: 'visibility' });
  });
  addEventListener('pagehide', () => {
    events?.close();
    events = null;
  });
  connect();
}

function connect() {
  if (!('EventSource' in window)) {
    startPolling();
    return;
  }
  // An EventSource left open keeps retrying forever, each try holding one
  // of the browser's few connections to this site: always close the old one.
  events?.close();
  events = new EventSource('/ui/events');
  events.addEventListener('revision', (event) => {
    try {
      refreshStatus(JSON.parse((event as MessageEvent).data));
    } catch {
      refreshStatus({ kind: 'background' });
    }
  });
  events.onopen = () => {
    reconnectAttempts = 0;
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  };
  events.onerror = () => {
    events?.close();
    events = null;
    reconnectAttempts++;
    if (reconnectAttempts > maxReconnectAttempts) {
      startPolling();
      return;
    }
    const delay = Math.min(500 * 2 ** (reconnectAttempts - 1), 8000);
    reconnectTimer = setTimeout(connect, delay);
  };
}

function startPolling() {
  if (pollTimer) return;
  pollTimer = setInterval(() => {
    if (!document.hidden) refreshStatus({ kind: 'poll' });
  }, 5000);
}

async function refreshStatus(invalidation: Revision) {
  const headers: Record<string, string> = {};
  if (etag) headers['If-None-Match'] = etag;
  try {
    const response = await fetch('/ui/status', { headers, cache: 'no-store' });
    if (response.status === 304) return;
    if (!response.ok) throw new Error(`status ${response.status}`);
    etag = response.headers.get('ETag') || '';
    const status = await response.json();
    live.pendingOperations = Number(status.pendingOperations) || 0;
    live.notices = status.notices || [];
    const pushed = invalidation.kind !== 'poll' && invalidation.kind !== 'visibility';
    if (pushed) {
      if (lastRevision === null) {
        if (typeof invalidation.revision === 'number') lastRevision = invalidation.revision;
        return;
      }
      if (typeof invalidation.revision === 'number') {
        if (invalidation.revision <= lastRevision) return;
        lastRevision = invalidation.revision;
      }
    }
    const kind = pushed ? invalidation.kind : status.kind;
    for (const listener of listeners) listener({ kind: kind || 'background', revision: status.revision });
  } catch {
    startPolling();
  }
  void reconnectTimer;
}

/** urgent: a change people must see at once (a removal, a failure). */
export const urgent = (kind: string) => /mutation|operation|removal|failure/.test(kind);

/**
 * follow ties a page to the live updates, the way Overtide's pages always
 * were. Call it in an $effect; it returns what stops it.
 * - 'immediate': every change reloads it.
 * - 'stable-list': a list people read; task, startup and disk-byte ticks
 *   are ignored, other changes show "Updates available" (reload on click),
 *   urgent ones reload it.
 * storage, when given, is called on disk-byte ticks instead of reload (the
 * cheap byte totals only).
 */
export function follow(policy: 'immediate' | 'stable-list', reload: () => void, storage?: () => void): () => void {
  const listener: Listener = ({ kind }) => {
    if (kind === 'storage' && storage) {
      storage();
      return;
    }
    if (policy === 'stable-list') {
      if (kind === 'tasks' || kind === 'startup' || kind === 'storage') return;
      if (!urgent(kind)) {
        live.updates = true;
        return;
      }
    }
    reload();
  };
  listeners.add(listener);
  const apply = () => reload();
  applyListeners.add(apply);
  return () => {
    listeners.delete(listener);
    applyListeners.delete(apply);
  };
}

/** applyUpdates is "Updates available"'s button: the page reloads. */
export function applyUpdates() {
  live.updates = false;
  for (const apply of applyListeners) apply();
}
