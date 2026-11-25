import { useTranslation } from 'react-i18next';
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Clock,
  Cpu,
  Play,
  Radio,
  Terminal,
  WifiOff,
  XCircle,
} from 'lucide-react';
import { Badge, Button, Card } from '@/components/ui';
import type { MonitorSnapshot } from '@/api/monitor';
import { cn } from '@/lib/utils';
import {
  formatDurationRemaining,
  formatLocalDateTime,
  formatRelativeAgo,
  getStatusLabel,
  getStatusVariant,
} from './monitorUtils';

interface HealthPanelProps {
  monitor: MonitorSnapshot;
  nowMs: number;
  probing: boolean;
  probeError?: string;
  onProbe: () => void;
}

export function HealthPanel({
  monitor,
  nowMs,
  probing,
  probeError,
  onProbe,
}: HealthPanelProps) {
  const { t } = useTranslation();
  const { probe, services, overall_status } = monitor;

  const primaryAgent =
    services.agents.find(
      (a) =>
        (!probe.project || a.project === probe.project) &&
        (!probe.agent || a.name === probe.agent),
    ) || services.agents[0];

  const agentLabel =
    probe.cli_binary ||
    primaryAgent?.cli_binary ||
    primaryAgent?.display_name ||
    probe.agent ||
    'agy';

  const cliVersion = probe.cli_version || primaryAgent?.cli_version || '-';
  const activeModel = probe.active_model || primaryAgent?.active_model || '-';

  const totalActiveSessions = services.agents.reduce((sum, a) => sum + (a.active_sessions || 0), 0);
  const totalAliveSessions = services.agents.reduce((sum, a) => sum + (a.alive_sessions || 0), 0);
  const allSessions = services.agents.flatMap((a) =>
    (a.sessions || []).map((s) => ({ ...s, project: a.project, agent: a.name })),
  );

  const isProbingNow = probing || probe.in_progress;
  const nextCountdown = formatDurationRemaining(probe.next_probe_at, nowMs);
  const errorText = probeError || probe.last_error;

  return (
    <Card className="flex flex-col gap-4 h-full">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2.5 min-w-0">
          <div
            className={cn(
              'w-8 h-8 rounded-lg flex items-center justify-center shrink-0',
              overall_status === 'healthy'
                ? 'bg-emerald-500/12 text-emerald-600 dark:text-emerald-400'
                : overall_status === 'degraded'
                  ? 'bg-amber-500/12 text-amber-600 dark:text-amber-400'
                  : 'bg-red-500/12 text-red-600 dark:text-red-400',
            )}
          >
            <Activity size={16} />
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-2 flex-wrap">
              <h2 className="text-sm font-semibold text-gray-900 dark:text-white truncate">
                {t('monitor.health.title', { name: agentLabel })}
              </h2>
              <Badge variant={getStatusVariant(overall_status)}>
                {getStatusLabel(overall_status, t)}
              </Badge>
            </div>
            {primaryAgent?.project && (
              <p className="text-xs text-gray-400 truncate mt-0.5">
                {primaryAgent.project} · {primaryAgent.display_name || primaryAgent.name}
              </p>
            )}
          </div>
        </div>

        <Button
          size="sm"
          variant="primary"
          loading={isProbingNow}
          onClick={onProbe}
        >
          {!isProbingNow && <Play size={12} />}
          {isProbingNow ? t('monitor.health.probing') : t('monitor.health.probeNow')}
        </Button>
      </div>

      {/* Key Metrics */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-2.5">
        <div className="rounded-lg border border-gray-200/80 dark:border-white/[0.06] bg-gray-50/70 dark:bg-white/[0.02] px-3 py-2.5">
          <div className="flex items-center gap-1.5 text-[11px] text-gray-500 dark:text-gray-400">
            <Terminal size={12} />
            <span>{t('monitor.health.cliVersion')}</span>
          </div>
          <p className="text-sm font-semibold font-mono text-gray-900 dark:text-white mt-1 truncate" title={cliVersion}>
            {cliVersion}
          </p>
        </div>

        <div className="rounded-lg border border-gray-200/80 dark:border-white/[0.06] bg-gray-50/70 dark:bg-white/[0.02] px-3 py-2.5">
          <div className="flex items-center gap-1.5 text-[11px] text-gray-500 dark:text-gray-400">
            <Cpu size={12} />
            <span>{t('monitor.health.activeModel')}</span>
          </div>
          <p className="text-sm font-semibold font-mono text-gray-900 dark:text-white mt-1 truncate" title={activeModel}>
            {activeModel}
          </p>
        </div>

        <div className="rounded-lg border border-gray-200/80 dark:border-white/[0.06] bg-gray-50/70 dark:bg-white/[0.02] px-3 py-2.5">
          <div className="flex items-center gap-1.5 text-[11px] text-gray-500 dark:text-gray-400">
            <Radio size={12} />
            <span>{t('monitor.health.aliveSessions')}</span>
          </div>
          <div className="flex items-baseline gap-1.5 mt-1">
            <span
              className={cn(
                'text-sm font-semibold font-mono',
                totalActiveSessions > 0 && totalAliveSessions === totalActiveSessions
                  ? 'text-emerald-600 dark:text-emerald-400'
                  : totalActiveSessions > 0 && totalAliveSessions < totalActiveSessions
                    ? 'text-amber-600 dark:text-amber-400'
                    : 'text-gray-900 dark:text-white',
              )}
            >
              {totalAliveSessions} / {totalActiveSessions}
            </span>
          </div>
        </div>
      </div>

      {/* Probe Details */}
      <div className="rounded-lg border border-gray-200/80 dark:border-white/[0.06] bg-gray-50/50 dark:bg-white/[0.02] p-3 space-y-2.5">
        <div className="flex items-center justify-between gap-2">
          <span className="text-xs font-medium text-gray-600 dark:text-gray-300 flex items-center gap-1.5">
            <Clock size={12} className="text-gray-400" />
            {t('monitor.health.lastProbe')}
          </span>
          <Badge variant={getStatusVariant(probe.status)} className="text-[11px]">
            {getStatusLabel(probe.status, t)}
          </Badge>
        </div>

        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 text-xs">
          <div>
            <p className="text-[11px] text-gray-400">{t('monitor.health.lastProbe')}</p>
            <p
              className="font-medium text-gray-800 dark:text-gray-200 mt-0.5 truncate"
              title={formatLocalDateTime(probe.last_checked_at)}
            >
              {probe.last_checked_at
                ? formatRelativeAgo(probe.last_checked_at, nowMs, t)
                : t('monitor.health.neverProbed')}
            </p>
          </div>
          <div>
            <p className="text-[11px] text-gray-400">{t('monitor.health.latency')}</p>
            <p className="font-medium font-mono text-gray-800 dark:text-gray-200 mt-0.5">
              {probe.last_checked_at ? `${probe.latency_ms} ms` : '-'}
            </p>
          </div>
          <div>
            <p className="text-[11px] text-gray-400">{t('monitor.health.failures')}</p>
            <p
              className={cn(
                'font-medium font-mono mt-0.5',
                probe.consecutive_failures > 0
                  ? 'text-red-600 dark:text-red-400'
                  : 'text-gray-800 dark:text-gray-200',
              )}
            >
              {probe.consecutive_failures}
            </p>
          </div>
          <div>
            <p className="text-[11px] text-gray-400">{t('monitor.health.nextProbe')}</p>
            <p
              className="font-medium font-mono text-gray-800 dark:text-gray-200 mt-0.5"
              title={formatLocalDateTime(probe.next_probe_at)}
            >
              {isProbingNow
                ? t('monitor.health.probing')
                : probe.next_probe_at
                  ? nextCountdown.expired
                    ? t('monitor.time.dueNow')
                    : nextCountdown.text
                  : '-'}
            </p>
          </div>
        </div>

        {errorText && (
          <div className="rounded-md bg-red-500/10 border border-red-500/20 px-2.5 py-2 text-xs text-red-600 dark:text-red-400 flex items-start gap-2">
            <AlertTriangle size={14} className="shrink-0 mt-0.5" />
            <span className="break-all leading-relaxed">{errorText}</span>
          </div>
        )}
      </div>

      {/* Active ACP Sessions (when any exist) */}
      {allSessions.length > 0 && (
        <div className="space-y-1.5">
          <p className="text-xs font-medium text-gray-500 dark:text-gray-400">
            {t('monitor.health.activeSessionsList')} ({allSessions.length})
          </p>
          <div className="flex flex-wrap gap-1.5 max-h-20 overflow-y-auto">
            {allSessions.map((sess) => (
              <span
                key={`${sess.project}-${sess.session_key}`}
                title={`${sess.project} · ${sess.session_key}${sess.agent_session_id ? ` (${sess.agent_session_id})` : ''}`}
                className={cn(
                  'inline-flex items-center gap-1.5 px-2 py-1 rounded-md text-[11px] font-mono border',
                  sess.alive
                    ? 'bg-emerald-500/8 border-emerald-500/20 text-emerald-700 dark:text-emerald-300'
                    : 'bg-red-500/8 border-red-500/20 text-red-700 dark:text-red-300',
                )}
              >
                <span
                  className={cn(
                    'w-1.5 h-1.5 rounded-full shrink-0',
                    sess.alive ? 'bg-emerald-500' : 'bg-red-500',
                  )}
                />
                <span className="truncate max-w-[160px]">{sess.session_key}</span>
                <span className="opacity-70">
                  ({sess.alive ? t('monitor.health.alive') : t('monitor.health.dead')})
                </span>
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Platform Connections */}
      <div className="space-y-2 mt-auto pt-1">
        <p className="text-xs font-medium text-gray-500 dark:text-gray-400">
          {t('monitor.health.platforms')}
        </p>
        {services.platforms.length === 0 ? (
          <p className="text-xs text-gray-400">{t('monitor.health.noPlatforms')}</p>
        ) : (
          <div className="space-y-1.5">
            {services.platforms.map((pl) => {
              const statusVariant = !pl.connected
                ? 'danger'
                : pl.degraded
                  ? 'warning'
                  : 'success';
              const statusText = !pl.connected
                ? t('monitor.health.disconnected')
                : pl.degraded
                  ? t('monitor.status.degraded')
                  : t('monitor.health.connected');

              return (
                <div
                  key={`${pl.project}-${pl.name}`}
                  className={cn(
                    'rounded-lg border px-3 py-2 text-xs transition-colors',
                    pl.degraded
                      ? 'border-amber-400/40 bg-amber-500/5 dark:border-amber-500/30 dark:bg-amber-900/10'
                      : !pl.connected
                        ? 'border-red-400/40 bg-red-500/5 dark:border-red-500/30 dark:bg-red-900/10'
                        : 'border-gray-200/80 dark:border-white/[0.06] bg-gray-50/50 dark:bg-white/[0.02]',
                  )}
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex items-center gap-2 min-w-0">
                      {!pl.connected ? (
                        <WifiOff size={13} className="text-red-500 shrink-0" />
                      ) : pl.degraded ? (
                        <AlertTriangle size={13} className="text-amber-500 shrink-0" />
                      ) : (
                        <CheckCircle2 size={13} className="text-emerald-500 shrink-0" />
                      )}
                      <span className="font-medium text-gray-900 dark:text-white truncate">
                        {pl.name}
                      </span>
                      <span className="text-gray-400 truncate">({pl.project})</span>
                    </div>
                    <Badge variant={statusVariant} className="text-[11px] shrink-0">
                      {statusText}
                    </Badge>
                  </div>
                  {(pl.degraded_reason || pl.degraded_since) && (
                    <div className="mt-1.5 pl-5 text-[11px] text-amber-700 dark:text-amber-300/90 space-y-0.5">
                      {pl.degraded_reason && <p className="break-words">{pl.degraded_reason}</p>}
                      {pl.degraded_since && (
                        <p className="text-amber-600/80 dark:text-amber-400/80" title={formatLocalDateTime(pl.degraded_since)}>
                          {t('monitor.health.degradedSince', {
                            time: formatRelativeAgo(pl.degraded_since, nowMs, t),
                          })}
                        </p>
                      )}
                    </div>
                  )}
                  {!pl.connected && !pl.degraded_reason && (
                    <div className="mt-1 pl-5 text-[11px] text-red-600 dark:text-red-400 flex items-center gap-1">
                      <XCircle size={11} />
                      <span>{t('monitor.health.disconnected')}</span>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </Card>
  );
}
