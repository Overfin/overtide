// The removal window's calculator: what's selected, and what removing it
// would do (storage reclaimed, files selected, what still holds the data).
// It works from the facts the window was given only, and never asks the
// server: a checkbox recalculates at once. The server rebuilds and checks
// everything again when the removal runs.
import type { RemovalModel } from '../../lib/types-removal';

/** An action is a form field and its value ("torrent" + a hash…). */
export const actionKey = (name: string, value: string) => `${name}\u0000${value}`;

export function splitAction(key: string): [string, string] {
  const split = key.indexOf('\u0000');
  return [key.slice(0, split), key.slice(split + 1)];
}

/** initialActions are the actions selected when the window opens. */
export function initialActions(model: RemovalModel): Set<string> {
  return new Set((model.files || []).filter((f) => f.selectable && f.selected).map((f) => actionKey(f.actionName, f.actionValue)));
}

/** selectedActions are those picked, and those always part of it. */
export function selectedActions(model: RemovalModel, picked: Set<string>): Set<string> {
  const selected = new Set(picked);
  for (const file of model.files || []) {
    if (file.always) selected.add(actionKey(file.actionName, file.actionValue));
  }
  return selected;
}

/** physicalActions are the actions a physical file's checkbox stands for. */
export function physicalActions(model: RemovalModel, physicalKey: string): string[] {
  return [...new Set((model.files || []).filter((f) => f.selectable && f.physicalKey === physicalKey).map((f) => actionKey(f.actionName, f.actionValue)))];
}

/** physicalState is a physical file's checkbox: all, some or none of its
 * actions selected; null when it stands for none. */
export function physicalState(model: RemovalModel, physicalKey: string, actions: Set<string>): { checked: boolean; indeterminate: boolean } | null {
  const keys = physicalActions(model, physicalKey);
  if (keys.length === 0) return null;
  const selected = keys.filter((k) => actions.has(k)).length;
  return { checked: selected === keys.length, indeterminate: selected > 0 && selected < keys.length };
}

export interface Consequences {
  reclaimed: number;
  selectedFiles: number;
  selectedBytes: number;
  guidance: { title: string; action: string } | null;
}

/** consequences is what removing the selected actions would do. */
export function consequences(model: RemovalModel, actions: Set<string>): Consequences {
  interface Group { size: number; links: number; known: Set<string>; selected: Set<string>; facts: { owner: string; ownerKey: string; selected: boolean }[] }
  const groups = new Map<string, Group>();
  const logicalMedia = new Map<string, number>();
  let selectedFiles = 0;
  for (const file of model.files || []) {
    const selected = file.always || actions.has(actionKey(file.actionName, file.actionValue));
    if (selected && file.owner === 'media') {
      selectedFiles++;
      const logicalKey = file.identityKnown ? `${file.device}:${file.inode}` : file.path;
      if (!logicalMedia.has(logicalKey)) logicalMedia.set(logicalKey, Number(file.sizeBytes) || 0);
    }
    if (!file.exists || !file.identityKnown) continue;
    const key = `${file.device}:${file.inode}`;
    let group = groups.get(key);
    if (!group) {
      group = { size: Number(file.sizeBytes) || 0, links: Number(file.links) || 0, known: new Set(), selected: new Set(), facts: [] };
      groups.set(key, group);
    }
    group.links = Math.max(group.links, Number(file.links) || 0);
    group.known.add(file.path);
    group.facts.push({ owner: file.owner, ownerKey: file.ownerKey, selected });
    if (selected) group.selected.add(file.path);
  }

  let reclaimed = 0;
  const blockers = new Map<string, string>();
  for (const group of groups.values()) {
    if (group.links > 0 && group.selected.size >= group.links) reclaimed += group.size;
    const selectedMedia = group.facts.some((f) => f.owner === 'media' && f.selected);
    if (selectedMedia && group.selected.size < group.links) {
      for (const fact of group.facts) {
        if (fact.owner === 'torrent' && !fact.selected) {
          const hash = String(fact.ownerKey).toLowerCase();
          blockers.set(hash, model.torrentStatuses?.[hash] || 'UNASSOCIATED');
        }
      }
    }
  }
  const selectedBytes = [...logicalMedia.values()].reduce((sum, size) => sum + size, 0);
  return { reclaimed, selectedFiles, selectedBytes, guidance: guidance(model, blockers, reclaimed, selectedFiles) };
}

function guidance(model: RemovalModel, blockers: Map<string, string>, reclaimed: number, selectedFiles: number) {
  if (blockers.size === 0) return null;
  let fileName = model.mediaType === 'movie' ? 'movie file' : model.mediaType === 'series' ? 'episode file' : 'library file';
  const plural = selectedFiles !== 1;
  if (plural) fileName += 's';
  const possessive = plural ? 'their' : 'its';
  const title = reclaimed === 0
    ? `Removing the selected ${fileName} alone will not reclaim storage space.`
    : `Removing the selected ${fileName} alone will not reclaim all of ${possessive} storage space.`;
  const allCurrent = [...blockers.values()].every((status) => status === 'CURRENT' || status === 'ASSOCIATED');
  let action: string;
  if (blockers.size === 1 && allCurrent) action = `${plural ? 'They are' : 'It is'} hardlinked to the current torrent. Also select that torrent below to reclaim the shared data.`;
  else if (allCurrent) action = 'They are hardlinked to current torrents. Also select those torrents below to reclaim the shared data.';
  else action = 'They are hardlinked to related torrents. Also select those torrents below to reclaim the shared data.';
  return { title, action };
}
