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

export function applyThemeAndZoom(theme?: string, textSize?: number) {
  const root = document.documentElement;
  if (theme === 'light' || theme === 'dark') {
    root.dataset.theme = theme;
  } else {
    delete root.dataset.theme;
  }
  const zoom = textSize && textSize >= 100 ? textSize / 100 : 1;
  root.style.setProperty('--zoom', String(zoom));
  try {
    localStorage.setItem('loom.theme', theme || 'system');
    localStorage.setItem('loom.textSize', String(textSize || 100));
  } catch {
    // ignore storage errors
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

function backdropClick(el: HTMLElement, on: (event: PointerEvent | MouseEvent) => boolean) {
  let down = false;
  let up = false;
  el.addEventListener(
    'pointerdown',
    event => {
      down = on(event);
      up = false;
    },
    true,
  );
  el.addEventListener(
    'pointerup',
    event => {
      up = down && on(event);
    },
    true,
  );
  return (event: MouseEvent) => up && on(event);
}

let confirmationPending: HTMLDialogElement | null = null;

export function confirmAction(title: string, message: string, actionLabel = '确认'): Promise<boolean> {
  if (confirmationPending) return Promise.resolve(false);

  const dialog = document.createElement('dialog');
  dialog.className = 'action-confirm';
  dialog.setAttribute('role', 'alertdialog');
  dialog.setAttribute('aria-labelledby', 'actionConfirmTitle');
  dialog.setAttribute('aria-describedby', 'actionConfirmMessage');

  const heading = document.createElement('h2');
  heading.id = 'actionConfirmTitle';
  heading.textContent = title;

  const description = document.createElement('p');
  description.id = 'actionConfirmMessage';
  description.textContent = message;

  const bar = document.createElement('div');
  bar.className = 'action-confirm-bar';

  const cancel = document.createElement('button');
  cancel.type = 'button';
  cancel.className = 'button';
  cancel.textContent = '取消';

  const accept = document.createElement('button');
  accept.type = 'button';
  accept.className = 'button danger-fill';
  accept.textContent = actionLabel;

  bar.append(cancel, accept);
  dialog.append(heading, description, bar);

  const previousFocus = document.activeElement as HTMLElement | null;
  document.body.append(dialog);

  return new Promise<boolean>(resolve => {
    confirmationPending = dialog;
    let settled = false;

    const finish = (accepted: boolean) => {
      if (settled) return;
      settled = true;
      if (dialog.open) dialog.close();
      dialog.remove();
      confirmationPending = null;
      if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true });
      resolve(accepted);
    };

    cancel.onclick = () => finish(false);
    accept.onclick = () => finish(true);
    dialog.addEventListener('cancel', event => {
      event.preventDefault();
      finish(false);
    });

    const onBackdrop = backdropClick(dialog, event => {
      const rect = dialog.getBoundingClientRect();
      return (
        event.target === dialog &&
        (event.clientX < rect.left ||
          event.clientX > rect.right ||
          event.clientY < rect.top ||
          event.clientY > rect.bottom)
      );
    });
    dialog.addEventListener('click', event => {
      if (onBackdrop(event)) finish(false);
    });

    dialog.addEventListener('keydown', event => {
      event.stopPropagation();
      if (event.key === 'Escape') {
        event.preventDefault();
        finish(false);
        return;
      }
      if (event.key !== 'Tab') return;
      const boundary = event.shiftKey ? cancel : accept;
      if (document.activeElement !== boundary) return;
      event.preventDefault();
      (event.shiftKey ? accept : cancel).focus();
    });

    requestAnimationFrame(() => {
      if (!dialog.isConnected) return;
      dialog.showModal();
      accept.focus({ preventScroll: true });
    });
  });
}
