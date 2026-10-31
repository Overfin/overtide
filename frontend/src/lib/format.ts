// Sizes, times and words as Overtide's pages write them.

/** bytes: 1.5 GiB, 320 MiB… (binary units, as the server's). */
export function bytes(value: unknown): string {
  let n = Math.max(0, Number(value) || 0);
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  let unit = 0;
  while (n >= 1024 && unit < units.length - 1) {
    n /= 1024;
    unit += 1;
  }
  const digits = unit === 0 ? 0 : n >= 10 ? 1 : 2;
  return `${n.toFixed(digits)} ${units[unit]}`;
}

/** message is an error's text. */
export const message = (err: unknown) => (err instanceof Error ? err.message : String(err));

/** plural: "1 torrent", "3 torrents". */
export const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
