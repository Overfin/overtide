// How services are named on every page: their app, and their state as
// they say it (internal/inventory/health.go).

export const appNames: Record<string, string> = {
  radarr: 'Radarr',
  sonarr: 'Sonarr',
  qbittorrent: 'qBittorrent',
  overfin: 'Overfin',
  prowlarr: 'Prowlarr',
  jellyfin: 'Jellyfin',
  seerr: 'Seerr',
};

export const states: Record<string, { label: string; kind: 'ok' | 'warn' | 'err' }> = {
  running: { label: 'Running', kind: 'ok' },
  warnings: { label: 'Warnings', kind: 'warn' },
  down: { label: 'Not answering', kind: 'err' },
};

/** app is "Radarr 5.14.0", or the app alone when its version isn't known. */
export const app = (type: string, version?: string) => [appNames[type] ?? type, version].filter(Boolean).join(' ');

/** folders says what a service's paths are. */
export const folders = (type: string) =>
  type === 'qbittorrent' ? 'Download folders' : type === 'radarr' || type === 'sonarr' ? 'Root folders' : 'Folders';
