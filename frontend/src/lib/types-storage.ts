// What the Home and Storage API answers (internal/httpui/routes_home.go,
// routes_storage.go).

export interface StorageClaim {
  service: string;
  bytes: number;
  roots: string[];
}

export interface StoragePlan {
  targetUsagePercent: number;
  criticalUsagePercent: number;
  needBytes: number;
  selectedBytes: number;
  message: string;
  actions: number;
}

export interface StorageDevice {
  path: string;
  name: string;
  rootLabels: string[];
  filesystem: string;
  available: boolean;
  error?: string;
  totalBytes: number;
  freeBytes: number;
  unmanagedBytes: number;
  reservedBytes: number;
  usableBytes: number;
  overtideUsedBytes: number;
  capacityBytes: number;
  elsewhereBytes: number;
  claimed: StorageClaim[];
  enabled: boolean;
  plan?: StoragePlan;
  planError?: string;
}

export interface Capability {
  path: string;
  serviceName: string;
  serviceType: string;
  purpose?: string;
  reachable: boolean;
  filesystem?: string;
  freeBytes: number;
  reservedBytes: number;
  selfReportedCount: number;
  selfReportedSizeBytes: number;
}

export interface Kept {
  key: string;
  name: string;
  from?: string;
  keptAt?: string;
}

export interface Storage {
  devices: StorageDevice[];
  capabilities: Capability[];
  kept: Kept[];
  autoRemovalDisabled: boolean;
}

export interface Home {
  error?: string;
  devices: StorageDevice[];
  totalMedia: number;
  hasMovieLibrary: boolean;
  movies: number;
  hasSeriesLibrary: boolean;
  series: number;
  libraryBytes: number;
  totalTorrents: number;
  current: number;
  superseded: number;
  orphaned: number;
  unassociated: number;
  obsoleteKnown: number;
  obsoleteReclaimable: number;
  unmanagedError?: string;
  services: { name: string; ok: boolean }[];
  stats: { reclaimedBytes: number; runs: number; mediaRemoved: number; torrentsRemoved: number; mediaBytes: number; last30Bytes: number };
}

/** StatsDevice is GET /api/storage/stats's: a device's bytes only. */
export interface StatsDevice {
  representativePath: string;
  available: boolean;
  totalBytes: number;
  freeBytes: number;
  targetUsagePercent: number;
  claimed: { service: string; bytes: number }[];
  unmanagedBytes: number;
  reservedBytes: number;
  usableBytes: number;
  overtideUsedBytes: number;
  capacityBytes: number;
  elsewhereBytes: number;
}

export interface DeviceSettings {
  path: string;
  rootLabels: string[];
  filesystem: string;
  name: string;
  targetUsagePercent: number;
  criticalUsagePercent: number;
  automaticRemovalEnabled: boolean;
}

export interface CleanupRow {
  key: string;
  shown: string;
  title: string;
  meta: string;
  with?: string;
}

export interface CleanupPlan {
  path: string;
  rootLabels: string[];
  filesystem: string;
  dryRun: boolean;
  message: string;
  selectedBytes: number;
  excluded: string[];
  torrents: CleanupRow[];
  media: CleanupRow[];
}

/** human writes bytes as the server does (cleanup.Human): "550.0 B". */
export function human(value: number): string {
  let n = Math.max(0, Number(value) || 0);
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let unit = 0;
  while (n >= 1024 && unit < units.length - 1) {
    n /= 1024;
    unit++;
  }
  return `${n.toFixed(1)} ${units[unit]}`;
}

/** share is part of whole as a percentage with three decimals, as the
 * bar's widths and legend say it: "50.000". */
export const share = (part: number, whole: number) => (whole ? ((part / whole) * 100).toFixed(3) : '0');

/** identity is how a device is told apart: its roots (or its one path),
 * and its filesystem. */
export const identity = (d: { rootLabels: string[]; path: string; filesystem: string }) =>
  `${d.rootLabels.length ? d.rootLabels.join(', ') : d.path}${d.filesystem ? ` · ${d.filesystem}` : ''}`;

/** patchBytes updates a device's byte totals from a stats tick, without
 * touching its plan's rows (those only change on a real revision). */
export function patchBytes(device: StorageDevice, stats: StatsDevice) {
  if (!stats.available) return;
  Object.assign(device, {
    totalBytes: stats.totalBytes, freeBytes: stats.freeBytes, unmanagedBytes: stats.unmanagedBytes, reservedBytes: stats.reservedBytes,
    usableBytes: stats.usableBytes, overtideUsedBytes: stats.overtideUsedBytes, capacityBytes: stats.capacityBytes, elsewhereBytes: stats.elsewhereBytes,
  });
  for (const claim of device.claimed) {
    const ticked = stats.claimed.find((c) => c.service === claim.service);
    if (ticked) claim.bytes = ticked.bytes;
  }
  if (device.plan) device.plan.targetUsagePercent = stats.targetUsagePercent;
}
