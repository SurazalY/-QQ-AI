import { useTranslation } from 'react-i18next';
import { Clock, Coins, ExternalLink, Gauge, Sparkles } from 'lucide-react';
import { Badge, Card, EmptyState } from '@/components/ui';
import type { MonitorQuota, MonitorQuotaBucket } from '@/api/monitor';
import { cn } from '@/lib/utils';
import {
  formatDurationRemaining,
  formatLocalDateTime,
  formatRelativeAgo,
} from './monitorUtils';

interface QuotaPanelProps {
  agentName?: string;
  quota?: MonitorQuota;
  nowMs: number;
}

function getBucketPct(bucket: MonitorQuotaBucket): number {
  const raw =
    typeof bucket.remaining_percent === 'number' && !Number.isNaN(bucket.remaining_percent)
      ? bucket.remaining_percent
      : (bucket.remaining_fraction ?? 0) * 100;
  return Math.max(0, Math.min(100, raw));
}

function getQuotaBarColor(pct: number): string {
  if (pct >= 50) return 'bg-emerald-500 dark:bg-emerald-400';
  if (pct >= 20) return 'bg-amber-500 dark:bg-amber-400';
  return 'bg-red-500 dark:bg-red-400';
}

function getQuotaTextColor(pct: number): string {
  if (pct >= 50) return 'text-emerald-600 dark:text-emerald-400';
  if (pct >= 20) return 'text-amber-600 dark:text-amber-400';
  return 'text-red-600 dark:text-red-400';
}

export function QuotaPanel({ agentName = 'agy', quota, nowMs }: QuotaPanelProps) {
  const { t } = useTranslation();

  return (
    <Card className="flex flex-col gap-4 h-full">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2.5 min-w-0">
          <div className="w-8 h-8 rounded-lg bg-accent/10 text-accent flex items-center justify-center shrink-0">
            <Gauge size={16} />
          </div>
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-gray-900 dark:text-white truncate">
              {t('monitor.quota.title', { name: agentName })}
            </h2>
            {quota?.fetched_at && (
              <p
                className="text-xs text-gray-400 mt-0.5 truncate"
                title={formatLocalDateTime(quota.fetched_at)}
              >
                {t('monitor.quota.fetchedAt', {
                  time: formatRelativeAgo(quota.fetched_at, nowMs, t),
                })}
                {quota.active_model ? ` · ${quota.active_model}` : ''}
              </p>
            )}
          </div>
        </div>

        {quota?.credits && (
          <div className="flex items-center gap-1.5">
            <Badge variant="outline" className="gap-1.5 font-mono">
              <Coins size={12} className="text-amber-500" />
              <span>
                {t('monitor.quota.credits', {
                  count: Number.isInteger(quota.credits.remaining_credits)
                    ? quota.credits.remaining_credits
                    : Number(quota.credits.remaining_credits.toFixed(2)),
                })}
              </span>
            </Badge>
            {quota.credits.upgrade_uri && (
              <a
                href={quota.credits.upgrade_uri}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs font-medium text-accent hover:bg-accent/10 transition-colors"
                title={quota.credits.upgrade_uri}
              >
                <span>{t('monitor.quota.upgrade')}</span>
                <ExternalLink size={11} />
              </a>
            )}
          </div>
        )}
      </div>

      {/* Groups */}
      {!quota || !quota.groups || quota.groups.length === 0 ? (
        <div className="flex-1 flex flex-col items-center justify-center py-6 text-center">
          <EmptyState
            icon={Gauge}
            message={t('monitor.quota.unavailable')}
          />
          <p className="text-xs text-gray-400 -mt-12 max-w-sm">
            {t('monitor.quota.unavailableHint')}
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-3 flex-1">
          {quota.groups.map((group) => (
            <div
              key={group.name}
              className={cn(
                'rounded-xl border p-3.5 transition-all',
                group.is_current_model_group
                  ? 'border-accent/50 bg-accent/[0.04] dark:bg-accent/[0.06] ring-1 ring-accent/20'
                  : 'border-gray-200/80 dark:border-white/[0.06] bg-gray-50/50 dark:bg-white/[0.02]',
              )}
            >
              <div className="flex items-center justify-between gap-2 mb-2.5">
                <div className="min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-sm font-semibold text-gray-900 dark:text-white truncate">
                      {group.name}
                    </span>
                    {group.is_current_model_group && (
                      <Badge variant="info" className="gap-1 text-[11px]">
                        <Sparkles size={10} />
                        {t('monitor.quota.activeGroup')}
                      </Badge>
                    )}
                  </div>
                  {group.description && (
                    <p className="text-[11px] text-gray-400 truncate mt-0.5">{group.description}</p>
                  )}
                </div>
              </div>

              <div className="space-y-2.5">
                {group.buckets.map((bucket) => {
                  const pct = getBucketPct(bucket);
                  const pctText = `${pct.toFixed(1)}%`;
                  const resetCountdown = formatDurationRemaining(bucket.reset_time, nowMs);
                  const resetTooltip = bucket.reset_time
                    ? t('monitor.quota.resetAt', { time: formatLocalDateTime(bucket.reset_time) })
                    : undefined;

                  return (
                    <div key={bucket.id || bucket.name} className="space-y-1">
                      <div className="flex items-center justify-between gap-2 text-xs">
                        <div className="flex items-center gap-2 min-w-0">
                          <span className="font-medium text-gray-700 dark:text-gray-200 truncate">
                            {bucket.name || bucket.id}
                          </span>
                          {bucket.window && (
                            <span className="px-1.5 py-0.2 rounded bg-gray-200/70 dark:bg-white/[0.08] text-[10px] font-mono text-gray-600 dark:text-gray-300 shrink-0">
                              {bucket.window}
                            </span>
                          )}
                        </div>
                        <div className="flex items-center gap-2.5 shrink-0">
                          {bucket.reset_time && (
                            <span
                              className="inline-flex items-center gap-1 text-[11px] text-gray-500 dark:text-gray-400 cursor-help"
                              title={resetTooltip}
                            >
                              <Clock size={10} />
                              {resetCountdown.expired
                                ? t('monitor.quota.resetDue')
                                : t('monitor.quota.resetsIn', { time: resetCountdown.text })}
                            </span>
                          )}
                          <span className={cn('font-mono font-semibold tabular-nums', getQuotaTextColor(pct))}>
                            {pctText}
                          </span>
                        </div>
                      </div>
                      <div className="h-2 w-full rounded-full bg-gray-200/80 dark:bg-white/[0.08] overflow-hidden">
                        <div
                          className={cn('h-full rounded-full transition-all duration-500', getQuotaBarColor(pct))}
                          style={{ width: `${pct}%` }}
                        />
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}
