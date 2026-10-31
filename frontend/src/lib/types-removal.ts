// What the removal API answers (internal/httpui/routes_removal.go).

export interface RemovalFile {
  path: string;
  owner: 'media' | 'torrent' | 'unmanaged' | string;
  ownerKey: string;
  actionName: string;
  actionValue: string;
  always: boolean;
  selected: boolean;
  exists: boolean;
  sizeBytes: number;
  identityKnown: boolean;
  device?: number;
  inode?: number;
  links?: number;
  physicalKey: string;
  selectable: boolean;
}

export interface RemovalModel {
  kind: string;
  dryRun: boolean;
  mediaType?: string;
  files: RemovalFile[] | null;
  torrentStatuses?: Record<string, string>;
  warnings?: string[];
}

export interface GroupFile {
  key: string;
  physicalKey: string;
  filename: string;
  label: string;
  size: string;
  selected: boolean;
}

export interface FileGroup {
  label: string;
  complete: boolean;
  totalFiles: number;
  size: string;
  files: GroupFile[];
}

export interface RemovalView {
  kind: 'media' | 'torrent' | 'unmanaged';
  label: string;
  dryRun: boolean;
  operationToken: string;
  mediaType?: string;
  mediaId?: number;
  mediaServiceId?: string;
  hash?: string;
  torrentServiceId?: string;
  unmanagedPaths: string[];
  warnings: string[];
  managedSectionLabel?: string;
  managedGroups: FileGroup[];
  torrentTarget: boolean;
  relatedManaged: { title: string; year?: number; type: string; sourceId: number; serviceId: string; fileCount: number; groups: FileGroup[] }[];
  relatedGroups: { label: string; torrents: { hash: string; name: string; fileCount: number; size: string; meta: string; selectable: boolean; selected: boolean }[] }[];
  relatedTorrentCount: number;
  selectedTorrentCount: number;
  preservedTorrentCount: number;
  relatedUnmanaged: { path: string; short: string; size: string; selectable: boolean; selected: boolean }[];
  fileGroups: { paths: { label: string; path: string; short: string }[]; pathCount: number; exists: boolean; size: string; identityKnown: boolean; links: number; missingLinks: number; error?: string }[];
  pathCount: number;
  canUnmonitorMovies: boolean;
  canUnmonitorEpisodes: boolean;
  canExcludeMovies: boolean;
  canExcludeSeries: boolean;
  selection: RemovalModel;
}

/** An operation as the server reports it (History, the pages of what it's about). */
export interface OperationNotice {
  id: number;
  label: string;
  status: string;
  message?: string;
}
