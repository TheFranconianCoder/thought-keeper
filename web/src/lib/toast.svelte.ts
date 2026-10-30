export type ToastTone = 'info' | 'success' | 'warning' | 'error';

export interface Toast {
  id: number;
  message: string;
  tone: ToastTone;
}

let nextID = 1;

export const toasts = $state<{ items: Toast[] }>({ items: [] });

/** toast shows one transient message. Errors stay a little longer than quiet confirmations. */
export function toast(message: string, tone: ToastTone = 'info', ttl?: number): number {
  const id = nextID++;
  toasts.items = [...toasts.items.slice(-3), { id, message, tone }];
  const timeout = ttl ?? (tone === 'error' ? 5200 : 3200);
  setTimeout(() => dismissToast(id), timeout);
  return id;
}

export function dismissToast(id: number): void {
  toasts.items = toasts.items.filter((item) => item.id !== id);
}

export function clearToasts(): void {
  toasts.items = [];
}
