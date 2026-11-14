// What the API sends for adding: the Adding page, its live panel, imports,
// destinations and the adding settings.

export interface AddingRow {
  title: string;
  year?: number;
  kind: 'film' | 'series';
  source: string;
  when?: string;
  result: string;
  dryRun: boolean;
  /** id names it for POST /api/adding/remove; removable: not downloading. */
  id: string;
  removable: boolean;
}

export interface AddingGroup {
  state: string;
  rows: AddingRow[];
  count: number;
}

export interface Adding {
  mode: string;
  manage: string;
  searches: number;
  groups: AddingGroup[];
  sources: { source: string; count: number }[];
  total: number;
  matching: number;
  nextRead?: string;
  nextRun?: string;
  lastError?: string;
}

export interface Now {
  doing: string;
  detail?: string;
  since?: string;
  running: boolean;
  searches: number;
  budget: number;
  dryRun: boolean;
  downloads: { title: string; kind: string; status: string; done?: string; percent: number; queued: boolean }[];
  holding: string[];
  next: { title: string; kind: string; when: string }[];
}

export interface Summary {
  at: string;
  dryRun: boolean;
  tagged: number;
  unmonitored: number;
  seriesTaken: number;
  filmsQueued: number;
  episodesWanted: number;
  seasons: number;
  following: number;
  monitored: number;
  error?: string;
}

export interface Destination {
  index: number;
  for: string;
  service: string;
  rootFolder: string;
  profileName: string;
}

export interface Choice {
  value: string;
  label: string;
}

export interface DestinationChoices {
  category: string;
  folder: string;
  profile: string;
}

export interface DestinationOptions {
  categories: Choice[];
  folders: Choice[];
  profiles: Choice[];
  unreachable: string[];
  current?: DestinationChoices;
}

export interface WaitingFile {
  path: string;
  why: string;
}

export interface WaitingImport {
  serviceId: string;
  downloadId: string;
  series: string;
  release: string;
  grabbedAt: string;
  files: WaitingFile[];
}

export interface EpisodeOption {
  id: number;
  season: number;
  episode: number;
  title: string;
  /** translated: TMDB's name in the server's language. */
  translated?: string;
  airDate?: string;
  /** runtime: TMDB's, in minutes. */
  runtime?: number;
}

/** Meaning is what a file's number means in one order. */
export interface Meaning {
  order: string;
  season: number;
  episode: number;
  title?: string;
  translated?: string;
  airDate?: string;
  runtime?: number;
  missing?: boolean;
}

export interface FileFacts {
  /** seconds: how long the file lasts, absent when it can't be read. */
  seconds?: number;
  meanings: Meaning[];
}

export interface ImportOptions {
  episodes: EpisodeOption[];
  guesses: Record<string, number[]>;
  files: Record<string, FileFacts>;
}

export interface AddingSettings {
  mode: string;
  manage: string;
  searches: number;
  titlesPerDay: number;
  upgradesPerDay: number;
  summary?: Summary;
  destinations: Destination[];
  eventsUrl: string;
  eventsProblem?: string;
}
