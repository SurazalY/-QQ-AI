import { useEffect, useState, useCallback, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import {
  Activity,
  Server,
  Layers,
  MessageSquare,
  Clock,
  ChevronRight,
  Pause,
  Play,
} from 'lucide-react';
import { StatCard, Badge, EmptyState } from '@/components/ui';
import { getStatus, type SystemStatus } from '@/api/status';
import { listProjects, type ProjectSummary } from '@/api/projects';
import { listSessions, type Session } from '@/api/sessions';
import {
  clearMonitorErrors,
  getMonitor,
  listMonitorErrors,
  triggerMonitorProbe,
  type MonitorErrorsSnapshot,
  type MonitorSnapshot,
} from '@/api/monitor';
import {
  ErrorsPanel,
  HealthPanel,
  QuotaPanel,
  formatLocalDateTime,
  formatRelativeAgo,
  getStatusLabel,
  getStatusVariant,
} from '@/components/Monitor';
import { cn, formatUptime, formatTime } from '@/lib/utils';

const MAX_ITEMS = 4;
const MONITOR_POLL_INTERVAL_MS = 10_000;

export default function Dashboard() {
  const { t } = useTranslation();
  const [monitor, setMonitor] = useState<MonitorSnapshot | null>(null);
  const [monitorError, setMonitorError] = useState('');
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [projects, setProjects] = useState<ProjectSummary[]>([]);
  const [recentSessions, setRecentSessions] = useState<(Session & { project: string })[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // Auto-refresh & live countdown clock
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [isTabVisible, setIsTabVisible] = useState(
    () => (typeof document !== 'undefined' ? !document.hidden : true),
  );
  const [nowMs, setNowMs] = useState(() => Date.now());

  // Probe state
  const [probing, setProbing] = useState(false);
  const [probeError, setProbeError] = useState('');

  // Error filter & clear state
  const [sourceFilter, setSourceFilter] = useState('');
  const [levelFilter, setLevelFilter] = useState('');
  const [filteredErrors, setFilteredErrors] = useState<MonitorErrorsSnapshot | null>(null);
  const [loadingFilteredErrors, setLoadingFilteredErrors] = useState(false);
  const [clearingErrors, setClearingErrors] = useState(false);

  const filtersRef = useRef({ source: '', level: '' });
  filtersRef.current = { source: sourceFilter, level: levelFilter };

  const fetchFilteredErrors = useCallback(async (source: string, level: string) => {
    if (!source && !level) {
      setFilteredErrors(null);
      return;
    }
    try {
      setLoadingFilteredErrors(true);
      const res = await listMonitorErrors({
        source: source || undefined,
        level: level || undefined,
        limit: 100,
      });
      if (filtersRef.current.source === source && filtersRef.current.level === level) {
        setFilteredErrors(res);
      }
    } catch {
      // Keep previous list on transient background failure
    } finally {
      setLoadingFilteredErrors(false);
    }
  }, []);

  const fetchMonitorOnly = useCallback(async () => {
    try {
      const m = await getMonitor();
      setMonitor(m);
      setMonitorError('');
      const { source, level } = filtersRef.current;
      if (source || level) {
        void fetchFilteredErrors(source, level);
      }
    } catch (e: any) {
      setMonitorError(e?.message || 'Failed to load monitor');
    }
  }, [fetchFilteredErrors]);

  const fetchAll = useCallback(
    async (isInitial = false) => {
      try {
        if (isInitial) setLoading(true);
        setError('');
        const [mRes, sRes, pRes] = await Promise.allSettled([
          getMonitor(),
          getStatus(),
          listProjects(),
        ]);

        if (mRes.status === 'fulfilled') {
          setMonitor(mRes.value);
          setMonitorError('');
        } else {
          setMonitorError(mRes.reason?.message || 'Failed to load monitor');
        }

        if (sRes.status === 'fulfilled') {
          setStatus(sRes.value);
        }

        if (pRes.status === 'fulfilled') {
          const projs = pRes.value.projects || [];
          setProjects(projs);

          const sessResults = await Promise.allSettled(
            projs.map((proj) =>
              listSessions(proj.name).then((r) => ({
                project: proj.name,
                sessions: r.sessions || [],
              })),
            ),
          );
          const allSessions: (Session & { project: string })[] = [];
          for (const r of sessResults) {
            if (r.status === 'fulfilled') {
              for (const sess of r.value.sessions) {
                allSessions.push({ ...sess, project: r.value.project });
              }
            }
          }
          allSessions.sort(
            (a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime(),
          );
          setRecentSessions(allSessions.slice(0, MAX_ITEMS));
        }

        if (
          mRes.status === 'rejected' &&
          sRes.status === 'rejected' &&
          pRes.status === 'rejected'
        ) {
          setError(sRes.reason?.message || mRes.reason?.message || 'Failed to load dashboard');
        }

        const { source, level } = filtersRef.current;
        if (source || level) {
          void fetchFilteredErrors(source, level);
        }
      } catch (e: any) {
        setError(e?.message || 'Failed to load dashboard');
      } finally {
        setLoading(false);
      }
    },
    [fetchFilteredErrors],
  );

  // Initial load + header cc:refresh listener
  useEffect(() => {
    void fetchAll(true);
    const handler = () => void fetchAll(false);
    window.addEventListener('cc:refresh', handler);
    return () => window.removeEventListener('cc:refresh', handler);
  }, [fetchAll]);

  // Tab visibility tracking
  useEffect(() => {
    const onVisibilityChange = () => {
      const visible = !document.hidden;
      setIsTabVisible(visible);
      if (visible && autoRefresh) {
        void fetchMonitorOnly();
      }
    };
    document.addEventListener('visibilitychange', onVisibilityChange);
    return () => document.removeEventListener('visibilitychange', onVisibilityChange);
  }, [autoRefresh, fetchMonitorOnly]);

  // 10s Monitor polling (paused when tab hidden or toggle off)
  useEffect(() => {
    if (!autoRefresh || !isTabVisible) return;
    const timer = window.setInterval(() => {
      void fetchMonitorOnly();
    }, MONITOR_POLL_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [autoRefresh, isTabVisible, fetchMonitorOnly]);

  // 1s UI clock for live countdowns and relative timestamps
  useEffect(() => {
    const timer = window.setInterval(() => {
      setNowMs(Date.now());
    }, 1000);
    return () => window.clearInterval(timer);
  }, []);

  // Re-fetch when error filters change
  useEffect(() => {
    if (!sourceFilter && !levelFilter) {
      setFilteredErrors(null);
      return;
    }
    void fetchFilteredErrors(sourceFilter, levelFilter);
  }, [sourceFilter, levelFilter, fetchFilteredErrors]);

  const handleProbe = async () => {
    try {
      setProbing(true);
      setProbeError('');
      const updated = await triggerMonitorProbe();
      setMonitor(updated);
      setMonitorError('');
      if (sourceFilter || levelFilter) {
        void fetchFilteredErrors(sourceFilter, levelFilter);
      }
    } catch (e: any) {
      setProbeError(e?.message || 'Probe failed');
    } finally {
      setProbing(false);
    }
  };

  const handleClearErrors = async () => {
    try {
      setClearingErrors(true);
      await clearMonitorErrors();
      setFilteredErrors((prev) =>
        prev
          ? {
              total_retained: 0,
              recent_15m_count: 0,
              recent_1h_count: 0,
              filtered_count: 0,
              items: [],
            }
          : null,
      );
      setMonitor((prev) =>
        prev
          ? {
              ...prev,
              errors: {
                total_retained: 0,
                recent_15m_count: 0,
                recent_1h_count: 0,
                items: [],
              },
            }
          : prev,
      );
      await fetchMonitorOnly();
    } finally {
      setClearingErrors(false);
    }
  };

  if (loading && !monitor && !status) {
    return (
      <div className="flex items-center justify-center h-64 text-gray-400">
        <Activity className="animate-pulse" size={24} />
      </div>
    );
  }

  if (error && !monitor && !status) {
    return <div className="text-center py-16 text-red-500">{error}</div>;
  }

  const primaryAgentLabel =
    monitor?.probe?.cli_binary ||
    monitor?.services?.agents?.[0]?.cli_binary ||
    monitor?.services?.agents?.[0]?.display_name ||
    monitor?.probe?.agent ||
    'agy';

  const displayedErrors: MonitorErrorsSnapshot = filteredErrors ??
    monitor?.errors ?? {
      total_retained: 0,
      recent_15m_count: 0,
      recent_1h_count: 0,
      items: [],
    };

  const summaryCounts = {
    recent_15m_count: monitor?.errors?.recent_15m_count ?? displayedErrors.recent_15m_count ?? 0,
    recent_1h_count: monitor?.errors?.recent_1h_count ?? displayedErrors.recent_1h_count ?? 0,
    total_retained: monitor?.errors?.total_retained ?? displayedErrors.total_retained ?? 0,
  };

  return (
    <div className="space-y-6 animate-fade-in">
      {/* Monitor Top Bar */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2.5 flex-wrap">
          <h1 className="text-base font-semibold text-gray-900 dark:text-white flex items-center gap-2">
            <span
              className={cn(
                'w-2 h-2 rounded-full shrink-0',
                autoRefresh && isTabVisible ? 'bg-emerald-500 animate-pulse' : 'bg-gray-400',
              )}
            />
            {t('monitor.title')}
          </h1>
          {monitor && (
            <Badge variant={getStatusVariant(monitor.overall_status)}>
              {getStatusLabel(monitor.overall_status, t)}
            </Badge>
          )}
          {monitor?.updated_at && (
            <span
              className="text-xs text-gray-400"
              title={formatLocalDateTime(monitor.updated_at)}
            >
              {t('monitor.lastUpdated', {
                time: formatRelativeAgo(monitor.updated_at, nowMs, t),
              })}
            </span>
          )}
          {monitorError && (
            <span className="text-xs text-red-500 truncate max-w-xs" title={monitorError}>
              {monitorError}
            </span>
          )}
        </div>

        <button
          type="button"
          onClick={() => setAutoRefresh((v) => !v)}
          className={cn(
            'inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium border transition-all',
            autoRefresh
              ? 'bg-accent/10 text-accent border-accent/25 hover:bg-accent/15'
              : 'bg-gray-100/80 dark:bg-white/[0.05] text-gray-500 dark:text-gray-400 border-gray-200/80 dark:border-white/[0.08]',
          )}
        >
          {autoRefresh ? <Pause size={12} /> : <Play size={12} />}
          <span>{t('monitor.autoRefresh')}</span>
          {!autoRefresh && (
            <span className="opacity-75">({t('monitor.autoRefreshPaused')})</span>
          )}
        </button>
      </div>

      {/* 3 Core Continuous Monitoring Panels (First Screen) */}
      {monitor && (
        <div className="space-y-4">
          <div className="grid grid-cols-1 xl:grid-cols-12 gap-4 items-stretch">
            <div className="xl:col-span-5">
              <HealthPanel
                monitor={monitor}
                nowMs={nowMs}
                probing={probing}
                probeError={probeError}
                onProbe={handleProbe}
              />
            </div>
            <div className="xl:col-span-7">
              <QuotaPanel
                agentName={primaryAgentLabel}
                quota={monitor.quota}
                nowMs={nowMs}
              />
            </div>
          </div>

          <ErrorsPanel
            errors={displayedErrors}
            summaryCounts={summaryCounts}
            sourceFilter={sourceFilter}
            levelFilter={levelFilter}
            onSourceChange={setSourceFilter}
            onLevelChange={setLevelFilter}
            loadingFiltered={loadingFilteredErrors}
            clearing={clearingErrors}
            onClear={handleClearErrors}
          />
        </div>
      )}

      {/* System Summary Stats */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <StatCard
          label={t('dashboard.version')}
          value={status?.version || monitor?.services?.cc_connect?.version || '-'}
          accent
        />
        <StatCard
          label={t('dashboard.uptime')}
          value={
            status
              ? formatUptime(status.uptime_seconds)
              : monitor?.services?.cc_connect
                ? formatUptime(monitor.services.cc_connect.uptime_seconds)
                : '-'
          }
        />
        <StatCard
          label={t('dashboard.platforms')}
          value={status?.connected_platforms?.length ?? monitor?.services?.platforms?.filter((p) => p.connected).length ?? 0}
        />
        <StatCard
          label={t('dashboard.projects')}
          value={status?.projects_count ?? monitor?.services?.cc_connect?.projects_count ?? 0}
        />
      </div>

      {/* Projects */}
      <section>
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-white flex items-center gap-1.5">
            <Server size={14} className="text-gray-400" />
            {t('nav.projects')}
          </h3>
          <Link to="/projects" className="text-xs text-accent hover:underline flex items-center gap-0.5">
            {t('common.viewAll')} <ChevronRight size={12} />
          </Link>
        </div>
        {projects.length === 0 ? (
          <div className="rounded-xl border border-gray-200/80 dark:border-white/[0.06] bg-white dark:bg-white/[0.02] p-8">
            <EmptyState message={t('projects.noProjects')} icon={Layers} />
          </div>
        ) : (
          <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-4 gap-3">
            {projects.slice(0, MAX_ITEMS).map((p) => (
              <Link
                key={p.name}
                to={`/chat/${p.name}`}
                className="block p-4 rounded-xl border border-gray-200/80 dark:border-white/[0.06] bg-white dark:bg-white/[0.02] hover:border-accent/40 hover:shadow-md hover:shadow-accent/5 transition-all"
              >
                <div className="flex items-center gap-2.5 mb-3">
                  <div className="w-8 h-8 rounded-lg bg-accent/10 flex items-center justify-center shrink-0">
                    <Server size={14} className="text-accent" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-semibold text-gray-900 dark:text-white truncate">{p.name}</p>
                    <p className="text-xs text-gray-400 font-mono">{p.agent_type}</p>
                  </div>
                </div>
                <div className="flex flex-wrap gap-1 mb-2">
                  {p.platforms?.slice(0, 3).map((pl) => (
                    <Badge key={pl} className="text-xs">{pl}</Badge>
                  ))}
                  {(p.platforms?.length ?? 0) > 3 && (
                    <Badge className="text-xs">+{p.platforms!.length - 3}</Badge>
                  )}
                </div>
                <p className="text-xs text-gray-400">{p.sessions_count} sessions</p>
              </Link>
            ))}
          </div>
        )}
      </section>

      {/* Recent Sessions */}
      <section>
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-white flex items-center gap-1.5">
            <MessageSquare size={14} className="text-gray-400" />
            {t('dashboard.recentSessions')}
          </h3>
          <Link to="/chat" className="text-xs text-accent hover:underline flex items-center gap-0.5">
            {t('common.viewAll')} <ChevronRight size={12} />
          </Link>
        </div>
        {recentSessions.length === 0 ? (
          <div className="rounded-xl border border-gray-200/80 dark:border-white/[0.06] bg-white dark:bg-white/[0.02] p-8">
            <p className="text-xs text-gray-400 text-center">{t('sessions.noSessions')}</p>
          </div>
        ) : (
          <div className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-4 gap-3">
            {recentSessions.map((sess) => (
              <Link
                key={`${sess.project}-${sess.id}`}
                to={`/chat/${sess.project}`}
                className="block p-4 rounded-xl border border-gray-200/80 dark:border-white/[0.06] bg-white dark:bg-white/[0.02] hover:border-accent/40 hover:shadow-md hover:shadow-accent/5 transition-all"
              >
                <div className="flex items-center gap-2.5 mb-3">
                  <div className={`w-2 h-2 rounded-full shrink-0 ${sess.active ? 'bg-green-400' : 'bg-gray-300 dark:bg-gray-600'}`} />
                  <p className="text-sm font-medium text-gray-900 dark:text-white truncate">
                    {sess.name || sess.id}
                  </p>
                </div>
                <Badge className="text-xs mb-2">{sess.project}</Badge>
                {sess.last_message && (
                  <p className="text-xs text-gray-400 truncate mb-2">
                    {sess.last_message.content.slice(0, 60)}
                  </p>
                )}
                <div className="flex items-center gap-1 text-xs text-gray-400">
                  <Clock size={10} />
                  {formatTime(sess.updated_at)}
                </div>
              </Link>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
