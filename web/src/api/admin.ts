import { authSignal } from '../state/auth';

// Typed API client for /api/admin/*. Every call uses credentials:
// 'include' so the session cookie flows; the legacy bearer header is
// attached too when authSignal is non-empty (CI smoke path). All write
// handlers go through POST / PATCH with a JSON body.

export interface AdminUserSiteRef {
  site_id: number;
  hostname: string;
  role: 'admin' | 'viewer' | 'api';
}

export interface AdminUser {
  user_id: string;
  site_id: number;
  email: string;
  username: string;
  role: 'admin' | 'viewer' | 'api';
  disabled: boolean;
  /** Per-site role grants — populated when per_site_admin flag is ON. */
  sites: AdminUserSiteRef[];
  created_at: number;
  updated_at: number;
}

export interface AdminGoal {
  goal_id: string;
  site_id: number;
  /** Hostname of the owning site — populated when per_site_admin flag is ON. */
  hostname: string;
  name: string;
  match_type: 'event_name_equals';
  pattern: string;
  value: number;
  enabled: boolean;
  created_at: number;
  updated_at: number;
}

export interface AdminSite {
  site_id: number;
  hostname: string;
  slug: string;
  plan: string;
  enabled: boolean;
  tz: string;
  // ISO 4217 alpha-3 (EUR, USD, IRR, ...). Display label only — no
  // minor-unit math. Default 'EUR' on the server (migration 007).
  currency: string;
  created_at: number;
  // Per-site privacy + bot-tracking policy (PR D2 — migration 006).
  // Defaults: respect_dnt=false, respect_gpc=false, track_bots=true.
  // Operators with EU visitors flip respect_dnt + respect_gpc to true.
  respect_dnt: boolean;
  respect_gpc: boolean;
  track_bots: boolean;
}

// SitePolicyPatch is the shape the admin UI sends to PATCH
// /api/admin/sites/{id} when toggling any settable field. Every key
// is optional so the server can distinguish "field omitted" (no
// change) from "field present" (apply it). Currency + tz join the
// privacy flags as PATCH-able attributes.
export interface SitePolicyPatch {
  enabled?: boolean;
  respect_dnt?: boolean;
  respect_gpc?: boolean;
  track_bots?: boolean;
  currency?: string;
  tz?: string;
}

// CurrencyOption mirrors internal/sites/currencies.go. Returned by
// GET /api/admin/currencies; consumed by the Add/Edit Site form
// dropdowns.
export interface CurrencyOption {
  code: string;
  symbol: string;
  name: string;
}

// TimezoneOption mirrors internal/sites/timezones.go. Returned by
// GET /api/admin/timezones with Offset computed at request time.
export interface TimezoneOption {
  iana: string;
  label: string;
  offset: string;
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
  };

  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
  }

  const token = authSignal.value;
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  const res = await fetch(path, {
    method,
    headers,
    credentials: 'include',
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();

  if (!res.ok) {
    let msg = `${method} ${path}: HTTP ${res.status}`;
    try {
      const parsed = JSON.parse(text) as { error?: string };
      if (parsed.error) msg = parsed.error;
    } catch {
      if (text) msg = text;
    }
    throw new Error(msg);
  }

  return text ? (JSON.parse(text) as T) : (undefined as T);
}

// ---------------- Users ----------------

export async function listUsers(siteID: number): Promise<AdminUser[]> {
  const res = await request<{ users: AdminUser[] }>('GET', `/api/admin/users?site_id=${siteID}`);
  return res.users ?? [];
}

export async function createUser(
  siteID: number,
  body: {
    email: string;
    username: string;
    password: string;
    sites: { site_id: number; role: string }[];
  },
): Promise<AdminUser> {
  return request<AdminUser>('POST', `/api/admin/users?site_id=${siteID}`, body);
}

export async function updateUserSites(
  id: string,
  sites: { site_id: number; role: string }[],
): Promise<void> {
  await request<void>('PATCH', `/api/admin/users/${id}/sites`, { sites });
}

export async function updateUser(
  id: string,
  body: { username: string; role: AdminUser['role'] },
): Promise<AdminUser> {
  return request<AdminUser>('PATCH', `/api/admin/users/${id}`, body);
}

export async function resetPassword(id: string, password: string): Promise<void> {
  await request<void>('POST', `/api/admin/users/${id}/password`, { password });
}

export async function disableUser(id: string): Promise<void> {
  await request<void>('POST', `/api/admin/users/${id}/disable`);
}

export async function enableUser(id: string): Promise<void> {
  await request<void>('POST', `/api/admin/users/${id}/enable`);
}

// ---------------- Goals ----------------

export async function listGoals(siteID: number): Promise<AdminGoal[]> {
  const res = await request<{ goals: AdminGoal[] }>('GET', `/api/admin/goals?site_id=${siteID}`);
  return res.goals ?? [];
}

export async function createGoal(
  siteID: number,
  body: {
    name: string;
    match_type: 'event_name_equals';
    pattern: string;
    value: number;
    enabled: boolean;
  },
): Promise<AdminGoal> {
  return request<AdminGoal>('POST', `/api/admin/goals?site_id=${siteID}`, body);
}

export async function updateGoal(
  siteID: number,
  id: string,
  body: {
    name: string;
    match_type: 'event_name_equals';
    pattern: string;
    value: number;
    enabled: boolean;
  },
): Promise<AdminGoal> {
  return request<AdminGoal>('PATCH', `/api/admin/goals/${id}?site_id=${siteID}`, body);
}

export async function disableGoal(siteID: number, id: string): Promise<void> {
  await request<void>('POST', `/api/admin/goals/${id}/disable?site_id=${siteID}`);
}

// ---------------- Sites ----------------

export async function listSites(): Promise<AdminSite[]> {
  const res = await request<{ sites: AdminSite[] }>('GET', '/api/admin/sites');
  return res.sites ?? [];
}

export async function createSite(body: {
  hostname: string;
  slug?: string;
  tz?: string;
  currency?: string;
}): Promise<AdminSite> {
  return request<AdminSite>('POST', '/api/admin/sites', body);
}

export async function updateSiteEnabled(
  siteID: number,
  enabled: boolean,
): Promise<AdminSite> {
  return request<AdminSite>('PATCH', `/api/admin/sites/${siteID}`, { enabled });
}

// updateSitePolicy patches any settable site attribute — privacy
// flags, currency, tz, or enabled. Caller passes only the fields they
// want to change; omitted fields are left unchanged on the server.
// The single PATCH endpoint accepts every key so the SPA's Edit Site
// form can submit one request rather than one per dimension.
export async function updateSitePolicy(
  siteID: number,
  patch: SitePolicyPatch,
): Promise<AdminSite> {
  return request<AdminSite>('PATCH', `/api/admin/sites/${siteID}`, patch);
}

// ---------------- Options (currencies + timezones dropdowns) ----------------

export async function listCurrencies(): Promise<CurrencyOption[]> {
  const res = await request<{ currencies: CurrencyOption[] }>(
    'GET',
    '/api/admin/currencies',
  );
  return res.currencies ?? [];
}

export async function listTimezones(): Promise<TimezoneOption[]> {
  const res = await request<{ timezones: TimezoneOption[] }>(
    'GET',
    '/api/admin/timezones',
  );
  return res.timezones ?? [];
}
