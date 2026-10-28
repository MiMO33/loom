export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch('/api' + path, {
    ...init,
    headers: { ...(init.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }), 'X-Agent-Kanban': '1', ...init.headers },
  });
  const body = await response.json();
  if (!response.ok) throw new Error(body.error || `请求失败（${response.status}）`);
  return body;
}

export const post = <T>(path: string, body: unknown = {}) => api<T>(path, { method: 'POST', body: JSON.stringify(body) });

export const removeTask = (id: string) => api<{ deleted: boolean }>(`/tasks/${id}`, { method: 'DELETE' });

export const setTaskArchived = (id: string, archived: boolean) =>
  api(`/tasks/${id}`, { method: 'PATCH', body: JSON.stringify({ archived }) });

export async function copyText(text: string) {
  try {
    await post('/copy', { text });
    return;
  } catch {
    await navigator.clipboard.writeText(text);
  }
}

export async function chooseFolder(title: string): Promise<string | undefined> {
  const res = await post<{ path: string }>('/choose-folder', { title });
  return res.path || undefined;
}

export async function openFolder(path: string) {
  await post('/open-folder', { path });
}

export async function selectProject(path: string) {
  return post<{ lastProjectDir: string; projectDirs: string[] }>('/projects', { path });
}

export async function removeProject(path: string) {
  return api<{ lastProjectDir: string; projectDirs: string[] }>('/projects', {
    method: 'DELETE',
    body: JSON.stringify({ path }),
  });
}

export async function openExternal(url: string) {
  try {
    await post('/open-url', { url });
  } catch {
    window.open(url, '_blank', 'noopener,noreferrer');
  }
}

export function duration(ms: number) {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor(seconds % 3600 / 60);
  return hours ? `${hours}时${minutes}分` : minutes ? `${minutes}分${seconds % 60}秒` : `${seconds}秒`;
}

export const date = (value: string) => new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value));
export const shortPath = (path: string) => path.split('/').filter(Boolean).slice(-2).join('/') || '项目目录';
export const stripAnsi = (text: string) => text.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '').replace(/\x1b\][^\x07]*(?:\x07|\x1b\\)/g, '').replace(/\r/g, '');
