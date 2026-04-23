import { authSignal } from '../state/auth';

// Typed API client for /api/admin/*. Every call uses credentials:
// 'include' so the session cookie flows; the legacy bearer header is
// attached too when authSignal is non-empty (CI smoke path). All write
// handlers go through POST / PATCH with a JSON body.

export interface AdminUser {
  user_id: string;
  site_id: number;
  email: string;
  username: string;
  role: 'admin' | 'viewer' | 'api';
  disabled: boolean;
  created_at: number;
  updated_at: number;
}

export interface AdminGoal {
  goal_id: string;
  site_id: number;
  name: string;
  match_type: 'event_name_equals';
  pattern: string;
  value_rials: number;
  enabled: boolean;
  created_at: number;
  updated_at: number;
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

export async function listUsers(): Promise<AdminUser[]> {
  const res = await request<{ users: AdminUser[] }>('GET', '/api/admin/users');
  return res.users ?? [];
}

export async function createUser(body: {
  email: string;
  username: string;
  password: string;
  role: AdminUser['role'];
}): Promise<AdminUser> {
  return request<AdminUser>('POST', '/api/admin/users', body);
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

export async function listGoals(): Promise<AdminGoal[]> {
  const res = await request<{ goals: AdminGoal[] }>('GET', '/api/admin/goals');
  return res.goals ?? [];
}

export async function createGoal(body: {
  name: string;
  match_type: 'event_name_equals';
  pattern: string;
  value_rials: number;
  enabled: boolean;
}): Promise<AdminGoal> {
  return request<AdminGoal>('POST', '/api/admin/goals', body);
}

export async function updateGoal(
  id: string,
  body: {
    name: string;
    match_type: 'event_name_equals';
    pattern: string;
    value_rials: number;
    enabled: boolean;
  },
): Promise<AdminGoal> {
  return request<AdminGoal>('PATCH', `/api/admin/goals/${id}`, body);
}

export async function disableGoal(id: string): Promise<void> {
  await request<void>('POST', `/api/admin/goals/${id}/disable`);
}
