// What the Library, title, History and Tasks pages' API answers
// (internal/httpui/routes_library.go).

export interface Option {
  value: string;
  label: string;
}

export interface LibraryRow {
  type: string;
  sourceId: number;
  serviceId: string;
  title: string;
  year?: number;
  value: number;
  size: string;
  torrents: number;
  removable: boolean;
}

export interface LibraryList {
  refreshing: boolean;
  error?: string;
  hasMediaLibrary: boolean;
  hasTorrentClient: boolean;
  total: number;
  all: number;
  page: number;
  pageSize: number;
  totalPages: number;
  sort: string;
  order: string;
  query: string;
  type: string;
  typeOptions: Option[];
  source?: string;
  sourceOptions?: Option[];
  torrent: string;
  showNoFiles: boolean;
  rows: LibraryRow[];
}

export interface TorrentRow {
  hash: string;
  name: string;
  size: string;
  ratio: number;
  seeds: number;
  leechers: number;
  reclaimable?: string;
}

export interface FilePath {
  path: string;
  short: string;
  owner?: string;
}

export interface FileRow extends FilePath {
  size: string;
  exists: boolean;
  identityKnown: boolean;
  links?: number;
  sharedWith?: FilePath[];
}

export interface MediaDetail {
  refreshing: boolean;
  error?: string;
  media: {
    type: string;
    sourceId: number;
    serviceId: string;
    serviceName?: string;
    title: string;
    year?: number;
    value: number;
    reasons: { label: string; value: string; note?: string }[];
    size: string;
    path: string;
    addedAt?: string;
    tmdbId?: number;
    tvdbId?: number;
    imdbId?: string;
  };
  current: TorrentRow[];
  superseded: TorrentRow[];
  orphaned: TorrentRow[];
  unassociated: TorrentRow[];
  files: FileRow[];
  fileCount: number;
  filesKnown: boolean;
  filesError?: string;
  removeMedia?: string;
  removeWithCurrent?: string;
  keptKey: string;
  keepName: string;
  kept: boolean;
  removable: boolean;
}

export interface Operation {
  id: number;
  label: string;
  status: string;
  message?: string;
}

export interface HistoryEvent {
  id: number;
  createdAt?: string;
  label: string;
  kind: string;
  serviceName?: string;
  dryRun: boolean;
  status: string;
  reclaimable: string;
  error?: string;
  files: { owner: string; selected: boolean; path: string }[];
  results: string[];
  errors: string[];
}

// One row of History: a title's files added on one day, or a removal.
export interface Activity {
  action: 'added' | 'removed';
  at: string;
  title: string;
  year?: number;
  kind: string;
  service?: string;
  episodes?: string;
  files: number;
  size: string;
  origin?: string;
  status?: string;
  error?: string;
  operation?: number;
  serviceId?: string;
  sourceId?: number;
  day?: string;
}

export interface ActivityPage {
  rows: Activity[];
  total: number;
  page: number;
  pages: number;
  runs: CleanupRun[];
}

export interface ImportedFile {
  at: string;
  episode?: string;
  path: string;
  size: string;
  release?: string;
}

export interface CleanupRun {
  completedAt?: string;
  status: string;
  usageBefore: number;
  usageAfter: number;
  mediaRemoved: number;
  torrentsRemoved: number;
  reclaimed: string;
}

export interface Task {
  id: string;
  name: string;
  description: string;
  schedule: string;
  lastFinished?: string;
  duration: string;
  nextRun?: string;
  scheduled: boolean;
  state: string;
  priority?: string;
  waiting?: string;
  lastError?: string;
  lastWarning?: string;
  canRun: boolean;
  busy: boolean;
}

export interface Workflow {
  name: string;
  description: string;
  revision: number;
  state: string;
  currentStep: number;
  totalSteps: number;
  stepName?: string;
  updatedAt?: string;
}

/** stamp is a time as every page writes it: "Nov 10 20:02", the year
 * too when it isn't this one ("Mar 3, 2025 08:10"), or Never. */
export function stamp(iso?: string): string {
  if (!iso) return 'Never';
  const d = new Date(iso);
  const date = d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', ...(d.getFullYear() !== new Date().getFullYear() ? { year: 'numeric' } : {}) });
  return `${date} ${d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })}`;
}

/** day is a date: "Oct 31", the year too when it isn't this one. */
export function day(iso?: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', ...(d.getFullYear() !== new Date().getFullYear() ? { year: 'numeric' } : {}) });
}
