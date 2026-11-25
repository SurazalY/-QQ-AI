import type { TFunction } from 'i18next';

export type BadgeVariant = 'default' | 'success' | 'warning' | 'danger' | 'info' | 'outline';

export function getStatusVariant(status?: string): BadgeVariant {
  switch (status) {
    case 'healthy':
      return 'success';
    case 'degraded':
      return 'warning';
    case 'unhealthy':
      return 'danger';
    default:
      return 'default';
  }
}

export function getStatusLabel(status: string | undefined, t: TFunction): string {
  switch (status) {
    case 'healthy':
      return t('monitor.status.healthy');
    case 'degraded':
      return t('monitor.status.degraded');
    case 'unhealthy':
      return t('monitor.status.unhealthy');
    default:
      return t('monitor.status.unknown');
  }
}

export function formatLocalDateTime(iso?: string): string {
  if (!iso) return '-';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '-';
  return d.toLocaleString();
}

export function formatShortTime(iso?: string): string {
  if (!iso) return '-';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '-';
  const now = new Date();
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate();
  if (sameDay) {
    return d.toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }
  const month = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  const time = d.toLocaleTimeString([], { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
  return `${month}-${day} ${time}`;
}

export function formatDurationRemaining(targetIso: string | undefined, nowMs: number): { text: string; expired: boolean } {
  if (!targetIso) return { text: '-', expired: false };
  const targetMs = new Date(targetIso).getTime();
  if (Number.isNaN(targetMs)) return { text: '-', expired: false };
  const diffSec = Math.floor((targetMs - nowMs) / 1000);
  if (diffSec <= 0) {
    return { text: '', expired: true };
  }
  const d = Math.floor(diffSec / 86400);
  const h = Math.floor((diffSec % 86400) / 3600);
  const m = Math.floor((diffSec % 3600) / 60);
  const s = diffSec % 60;

  if (d > 0) return { text: `${d}d ${h}h`, expired: false };
  if (h > 0) return { text: `${h}h ${m}m`, expired: false };
  if (m > 0) return { text: `${m}m ${s}s`, expired: false };
  return { text: `${s}s`, expired: false };
}

export function formatRelativeAgo(iso: string | undefined, nowMs: number, t: TFunction): string {
  if (!iso) return '-';
  const ts = new Date(iso).getTime();
  if (Number.isNaN(ts)) return '-';
  const diffSec = Math.max(0, Math.floor((nowMs - ts) / 1000));
  if (diffSec < 5) return t('monitor.time.now');
  if (diffSec < 60) return t('monitor.time.secondsAgo', { count: diffSec });
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return t('monitor.time.minutesAgo', { count: diffMin });
  const diffHr = Math.floor(diffMin / 60);
  if (diffHr < 24) return t('monitor.time.hoursAgo', { count: diffHr });
  const diffDay = Math.floor(diffHr / 24);
  return t('monitor.time.daysAgo', { count: diffDay });
}
