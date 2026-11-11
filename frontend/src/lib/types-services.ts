import { stamp } from './types-library';

// What Overtide's Services and Settings API answers (internal/httpui,
// page_services.go and page_settings.go).

export interface ServiceRow {
  id: string;
  name: string;
  ok: boolean;
  message?: string;
  checkedAt?: string;
  roots: string[];
}

export interface ServiceType {
  value: string;
  label: string;
}

export interface ServiceCategory {
  title: string;
  types: ServiceType[];
}

export interface ServiceDetail {
  id: string;
  type: string;
  name: string;
  url: string;
  username: string;
  hasApiKey: boolean;
  hasPassword: boolean;
  allowAutomaticRemoval: boolean;
}

export interface ConsistencyStatus {
  state: string;
  currentStep: number;
  totalSteps: number;
  stepLabel: string;
}

export interface Settings {
  autoMode: string;
  autoRemoveUnassociatedTorrents: boolean;
  dryRun: boolean;
  privateMinSeedingDays: number;
  autoExcludeFromImportLists: boolean;
  autoRemoveIncompleteTorrents: boolean;
  copiesWithTitles: boolean;
}

/** shortPath keeps a long path's start and end, as the old pages did. */
export function shortPath(v: string): string {
  const max = 88, left = 34, right = 51;
  if (v.length <= max || v.length <= left + right + 3) return v;
  return `${v.slice(0, left)}...${v.slice(v.length - right)}`;
}

/** updated is a time as every page writes it (stamp). */
export function updated(iso?: string): string {
  return stamp(iso);
}
