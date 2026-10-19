export type ApiResult = {
  status: number;
  body: unknown;
  raw: string;
  correlationId: string;
};

export type AuthKind = 'internal' | 'provider' | 'none';

export type ApiOptions = {
  method?: string;
  token?: string;
  auth?: AuthKind;
  body?: unknown;
  headers?: Record<string, string>;
};

export type ApiCall = (path: string, options?: ApiOptions) => Promise<ApiResult>;

export type LogEntry = {
  id: string;
  at: string;
  method: string;
  path: string;
  status: number;
  correlationId: string;
  raw: string;
};

export async function apiCall(path: string, options: ApiOptions = {}): Promise<ApiResult> {
  const correlationId = crypto.randomUUID();
  const headers: Record<string, string> = {
    'X-Correlation-Id': correlationId,
    ...(options.headers ?? {}),
  };

  if (options.token) {
    headers.Authorization = `Bearer ${options.token}`;
  }
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json';
  }

  const response = await fetch(`/api${path}`, {
    method: options.method ?? 'GET',
    headers,
    body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
  });

  const raw = await response.text();
  let body: unknown = raw;
  try {
    body = raw ? JSON.parse(raw) : null;
  } catch {
    body = raw;
  }

  return { status: response.status, body, raw, correlationId };
}

export async function fetchToken(client: string): Promise<string> {
  const response = await fetch('/token', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ client }),
  });

  const payload = (await response.json()) as {
    access_token?: string;
    error?: string;
    error_description?: string;
  };

  if (!response.ok || !payload.access_token) {
    throw new Error(payload.error_description ?? payload.error ?? 'token request failed');
  }
  return payload.access_token;
}

export function pretty(value: unknown): string {
  if (typeof value === 'string') {
    return value;
  }
  return JSON.stringify(value, null, 2);
}

export type WalletRef = { id: string; playerId: string };
