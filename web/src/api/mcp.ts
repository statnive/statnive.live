import { request } from './admin';

// Typed client for the self-serve MCP-token endpoints (PR-A backend).
// Reuses admin.ts's shared `request` (session cookie + CI bearer + HttpError).

export interface MCPToken {
  token_id: string;
  name: string;
  site_ids: number[];
  role: 'viewer' | 'api';
  created_at: number;
  expires_at: number; // 0 = never
  last_used_at: number; // 0 = never used
  /** Raw secret — present ONLY on the mint response, shown once. */
  token?: string;
}

export interface MCPConnection {
  enabled: boolean;
  transport: string;
  url: string;
  role: string;
  sites: number[];
  add_command_template: string;
}

export interface MintTokenBody {
  name: string;
  site_ids?: number[];
  role?: 'viewer' | 'api';
  ttl_days?: number;
}

export async function listMCPTokens(): Promise<MCPToken[]> {
  const res = await request<{ tokens: MCPToken[] }>('GET', '/api/mcp/tokens');
  return res.tokens ?? [];
}

export async function createMCPToken(body: MintTokenBody): Promise<MCPToken> {
  return request<MCPToken>('POST', '/api/mcp/tokens', body);
}

export async function revokeMCPToken(id: string): Promise<void> {
  await request<void>('DELETE', `/api/mcp/tokens/${encodeURIComponent(id)}`);
}

export async function getMCPConnection(): Promise<MCPConnection> {
  return request<MCPConnection>('GET', '/api/mcp/connection');
}
