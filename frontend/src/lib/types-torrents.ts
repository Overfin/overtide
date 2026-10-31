// What the Torrents and Unmanaged files API answers (routes_torrents.go).

export interface MediaLink {
  type: string;
  sourceId: number;
  serviceId?: string;
  title: string;
  year?: number;
}

export interface Paging {
  page: number;
  pageSize: number;
  totalPages: number;
  sort: string;
  order: 'asc' | 'desc';
}

export interface TorrentRow {
  hash: string;
  serviceId: string;
  name: string;
  status: string;
  media: MediaLink[];
  formerMedia: MediaLink[];
  state: string;
  progress: string;
  filesMissing: boolean;
  size: string;
  reclaimableKnown: boolean;
  reclaimable: string;
  crossSeeds: number;
  groupKnown: boolean;
  groupReclaimable: string;
  ratio: string;
  seeds: number;
  leechers: number;
  lastActivity: string;
}

export interface TorrentList {
  hasTorrentClient: boolean;
  refreshing: boolean;
  lastError?: string;
  allItems: number;
  totalItems: number;
  current: number;
  superseded: number;
  orphaned: number;
  unassociated: number;
  obsoleteKnown: number;
  obsoleteReclaimable: string;
  filesMissing: number;
  q: string;
  status: string;
  reclaimable: string;
  activity: string;
  paging: Paging;
  torrents: TorrentRow[];
}

export interface Operation {
  id: number;
  label: string;
  status: string;
  message?: string;
}

export interface FilePeer {
  path: string;
  short: string;
  owner?: string;
}

export interface TorrentFile {
  path: string;
  short: string;
  owner?: string;
  size: string;
  exists: boolean;
  identityKnown: boolean;
  links: number;
  peers: FilePeer[];
}

export interface TorrentDetail {
  hash: string;
  serviceId: string;
  name: string;
  status: string;
  client: string;
  state: string;
  progress: string;
  protected: boolean;
  protectionReason?: string;
  obligation?: string;
  crossSeeds: number;
  groupKnown: boolean;
  groupReclaimable: string;
  filesMissing: boolean;
  reclaimableKnown: boolean;
  reclaimable: string;
  hardlinked?: string;
  storageError: boolean;
  media: MediaLink[];
  formerMedia: MediaLink[];
  supersededByHash?: string;
  tracker?: string;
  category?: string;
  tags?: string;
  ratio: string;
  seeds: number;
  leechers: number;
  added: string;
  lastActivity: string;
  savePath?: string;
  contentPath?: string;
}

export interface TorrentDetailAnswer {
  operation?: Operation;
  torrent?: TorrentDetail;
  refreshing: boolean;
  lastError?: string;
  detailUnavailable: boolean;
  files: TorrentFile[];
  fileCount: number;
  filesRead: boolean;
  filesError?: string;
  removeKnown: boolean;
  removeFrees: string;
}

export interface UnmanagedRow {
  paths: { path: string; short: string }[];
  size: string;
  reclaimableKnown: boolean;
  reclaimable: string;
  links: number;
  missingLinks: number;
  modified: string;
}

export interface UnmanagedList {
  scanError?: string;
  allItems: number;
  totalItems: number;
  totalBytes: string;
  reclaimable?: string;
  shared?: string;
  q: string;
  status: string;
  root: string;
  minSizeMib: string;
  rootOptions: { key: string; label: string }[];
  paging: Paging;
  files: UnmanagedRow[];
}

/** mediaHref is a title's page. */
export const mediaHref = (m: MediaLink) => `/library/${m.type}/${m.sourceId}?service_id=${encodeURIComponent(m.serviceId || '')}`;

/** listURL is a paged list's address: its filters, then page, size and sort. */
export function listURL(path: string, filters: Record<string, string>, page: number, size: number, sort: string, order: string): string {
  const q = new URLSearchParams();
  q.set('page', String(page));
  q.set('page_size', String(size));
  q.set('sort', sort);
  q.set('order', order);
  for (const [k, v] of Object.entries(filters)) if (v) q.set(k, v);
  return `${path}?${q}`;
}
