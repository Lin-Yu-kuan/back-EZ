import type {
  APIResponse,
  BatchResponseData,
  BatchTargets,
  ClientNode,
  DeployTask,
  IPGroup,
  RestorePoint,
} from '../types/spec';

let csrfToken: string | null = localStorage.getItem('backez_csrf_token');

export function setCSRFToken(token: string | null) {
  csrfToken = token;
  if (token) {
    localStorage.setItem('backez_csrf_token', token);
  } else {
    localStorage.removeItem('backez_csrf_token');
  }
}

export function getCSRFToken(): string | null {
  return csrfToken;
}

export class APIClientError extends Error {
  code: string;
  details?: unknown;

  constructor(code: string, message: string, details?: unknown) {
    super(message);
    this.name = 'APIClientError';
    this.code = code;
    this.details = details;
  }
}

async function request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers || {});
  headers.set('Content-Type', 'application/json');

  const method = options.method?.toUpperCase() || 'GET';
  if (method === 'POST' || method === 'PATCH' || method === 'DELETE' || method === 'PUT') {
    if (csrfToken) {
      headers.set('X-CSRF-Token', csrfToken);
    }
  }

  const response = await fetch(endpoint, {
    ...options,
    headers,
    credentials: 'include', // Include Session Cookie
  });

  if (response.status === 401) {
    setCSRFToken(null);
    window.dispatchEvent(new CustomEvent('backez:unauthorized'));
    throw new APIClientError('UNAUTHORIZED', '請先登入');
  }

  const result: APIResponse<T> = await response.json();
  if (!result.ok) {
    const err = result.error;
    throw new APIClientError(err?.code || 'INTERNAL', err?.message || '未知錯誤', err?.details);
  }

  return result.data as T;
}

export const api = {
  async login(password: string): Promise<string> {
    const data = await request<{ csrf_token: string }>('/api/v1/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: 'admin', password }),
    });
    setCSRFToken(data.csrf_token);
    return data.csrf_token;
  },

  async logout(): Promise<void> {
    try {
      await request('/api/v1/auth/logout', { method: 'POST' });
    } finally {
      setCSRFToken(null);
    }
  },

  async getClients(groupId?: string): Promise<ClientNode[]> {
    const q = groupId ? `?group_id=${encodeURIComponent(groupId)}` : '';
    return request<ClientNode[]>(`/api/v1/clients${q}`);
  },

  async getClient(ip: string): Promise<ClientNode> {
    return request<ClientNode>(`/api/v1/clients/${encodeURIComponent(ip)}`);
  },

  async patchClientSettings(ip: string, autoRestore: boolean): Promise<void> {
    await request(`/api/v1/clients/${encodeURIComponent(ip)}/settings`, {
      method: 'PATCH',
      body: JSON.stringify({ auto_restore_on_reboot: autoRestore }),
    });
  },

  async getGroups(): Promise<IPGroup[]> {
    return request<IPGroup[]>('/api/v1/groups');
  },

  async createOrUpdateGroup(group: Partial<IPGroup>): Promise<void> {
    await request('/api/v1/groups', {
      method: 'POST',
      body: JSON.stringify(group),
    });
  },

  async deleteGroup(groupId: string): Promise<void> {
    await request(`/api/v1/groups/${encodeURIComponent(groupId)}`, {
      method: 'DELETE',
    });
  },

  async wakeOnLAN(targets: BatchTargets): Promise<BatchResponseData> {
    return request<BatchResponseData>('/api/v1/power/wol', {
      method: 'POST',
      body: JSON.stringify({ targets }),
    });
  },

  async getRestorePoints(ip: string): Promise<{ active_point_id: string | null; restore_points: RestorePoint[] }> {
    return request<{ active_point_id: string | null; restore_points: RestorePoint[] }>(
      `/api/v1/clients/${encodeURIComponent(ip)}/restore-points`
    );
  },

  async restorePointAction(payload: {
    action: 'create' | 'switch' | 'delete';
    point_id?: string;
    name?: string;
    description?: string;
    targets: BatchTargets;
  }): Promise<BatchResponseData> {
    return request<BatchResponseData>('/api/v1/restore-points/action', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  },

  async startDeploy(masterIp: string, targetIps: string[]): Promise<DeployTask> {
    return request<DeployTask>('/api/v1/deploy/start', {
      method: 'POST',
      body: JSON.stringify({ master_ip: masterIp, target_ips: targetIps }),
    });
  },

  async cancelDeploy(taskId: string): Promise<void> {
    await request('/api/v1/deploy/cancel', {
      method: 'POST',
      body: JSON.stringify({ task_id: taskId }),
    });
  },

  async retryDeploy(taskId: string): Promise<void> {
    await request('/api/v1/deploy/retry', {
      method: 'POST',
      body: JSON.stringify({ task_id: taskId }),
    });
  },

  async getTasks(limit = 50, offset = 0): Promise<DeployTask[]> {
    return request<DeployTask[]>(`/api/v1/tasks?limit=${limit}&offset=${offset}`);
  },

  async getAuditLogs(limit = 50, offset = 0): Promise<unknown[]> {
    return request<unknown[]>(`/api/v1/audit?limit=${limit}&offset=${offset}`);
  },
};

