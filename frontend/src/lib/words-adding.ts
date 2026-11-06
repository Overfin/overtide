// How the Adding page names what the API sends as values.

export const sourceLabels: Record<string, string> = {
  request: 'Wishes',
  library: 'Library',
  overfin: 'Overfin',
};

export const kindLabels: Record<string, string> = { film: 'Film', series: 'Series', season: 'Season', episode: 'Episode' };

/** groups are the Adding page's lists, by state, as the API orders them. */
export const groups: Record<string, { label: string; hint: string; when: string; open: boolean }> = {
  seasons: { label: 'Whole seasons', hint: "Seasons with episodes missing, 7 days or more after their last episode aired: one search for the whole season. One marked Needs a look isn't searched again.", when: 'Next search', open: true },
  episodes: { label: 'Episodes wanted', hint: "Episodes that aired without coming through Sonarr's RSS. Just aired: searched first, every day for 14 days. Missing: after wishes.", when: 'Next search', open: true },
  waiting: { label: 'Waiting', hint: "In the order they're tried: what people wished for, in Overfin's turns, then the films taken from Radarr (with \"Everything\"), then Overfin's suggestions, in Overfin's order.", when: 'Next try', open: true },
  'not released': { label: 'Not out yet', hint: "Monitored two days before their release, so Radarr's or Sonarr's RSS catches it.", when: 'Next step', open: false },
  imported: { label: 'Imported', hint: "Overtide's, with their files.", when: 'Since', open: false },
};

/** short is a date and time as the panel writes them: "Oct 30 15:04". */
export function short(iso?: string): string {
  if (!iso) return '';
  return new Date(iso).toLocaleString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).replace(',', '');
}

/** clock is a time of day: "15:04". */
export function clock(iso?: string): string {
  if (!iso) return '';
  return new Date(iso).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
}
