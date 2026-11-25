import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  AlertCircle,
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Trash2,
} from 'lucide-react';
import { Badge, Button, Card, Modal } from '@/components/ui';
import type { MonitorErrorItem, MonitorErrorsSnapshot } from '@/api/monitor';
import { cn } from '@/lib/utils';
import { formatLocalDateTime, formatShortTime } from './monitorUtils';

interface ErrorsPanelProps {
  errors: MonitorErrorsSnapshot;
  summaryCounts: {
    recent_15m_count: number;
    recent_1h_count: number;
    total_retained: number;
  };
  sourceFilter: string;
  levelFilter: string;
  onSourceChange: (source: string) => void;
  onLevelChange: (level: string) => void;
  loadingFiltered?: boolean;
  clearing: boolean;
  onClear: () => Promise<void> | void;
}

const SOURCE_FILTERS = [
  { key: '', labelKey: 'monitor.errors.allSources' },
  { key: 'agy_stream', labelKey: 'monitor.errors.source.agy_stream' },
  { key: 'acp_process', labelKey: 'monitor.errors.source.acp_process' },
  { key: 'engine', labelKey: 'monitor.errors.source.engine' },
  { key: 'platform', labelKey: 'monitor.errors.source.platform' },
  { key: 'probe', labelKey: 'monitor.errors.source.probe' },
] as const;

const LEVEL_FILTERS = [
  { key: '', labelKey: 'monitor.errors.allLevels' },
  { key: 'error', labelKey: 'monitor.errors.level.error' },
  { key: 'warn', labelKey: 'monitor.errors.level.warn' },
] as const;

function getSourceBadgeClass(source: string): string {
  switch (source) {
    case 'agy_stream':
      return 'bg-purple-100/90 dark:bg-purple-900/25 text-purple-700 dark:text-purple-300 border border-purple-200/60 dark:border-purple-500/20';
    case 'acp_process':
      return 'bg-rose-100/90 dark:bg-rose-900/25 text-rose-700 dark:text-rose-300 border border-rose-200/60 dark:border-rose-500/20';
    case 'engine':
      return 'bg-blue-100/90 dark:bg-blue-900/25 text-blue-700 dark:text-blue-300 border border-blue-200/60 dark:border-blue-500/20';
    case 'platform':
      return 'bg-cyan-100/90 dark:bg-cyan-900/25 text-cyan-700 dark:text-cyan-300 border border-cyan-200/60 dark:border-cyan-500/20';
    case 'probe':
      return 'bg-amber-100/90 dark:bg-amber-900/25 text-amber-700 dark:text-amber-300 border border-amber-200/60 dark:border-amber-500/20';
    default:
      return '';
  }
}

export function ErrorsPanel({
  errors,
  summaryCounts,
  sourceFilter,
  levelFilter,
  onSourceChange,
  onLevelChange,
  loadingFiltered,
  clearing,
  onClear,
}: ErrorsPanelProps) {
  const { t } = useTranslation();
  const [expandedIds, setExpandedIds] = useState<Record<string, boolean>>({});
  const [confirmClearOpen, setConfirmClearOpen] = useState(false);

  const toggleExpand = (id: string) => {
    setExpandedIds((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  const handleConfirmClear = async () => {
    await onClear();
    setConfirmClearOpen(false);
    setExpandedIds({});
  };

  const items: MonitorErrorItem[] = errors?.items || [];
  const hasActiveFilter = Boolean(sourceFilter || levelFilter);

  return (
    <Card className="flex flex-col gap-4">
      {/* Header & Summary Counters */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2.5 flex-wrap">
          <div className="w-8 h-8 rounded-lg bg-red-500/10 text-red-500 flex items-center justify-center shrink-0">
            <AlertCircle size={16} />
          </div>
          <h2 className="text-sm font-semibold text-gray-900 dark:text-white">
            {t('monitor.errors.title')}
          </h2>

          <div className="flex items-center gap-1.5 flex-wrap">
            <Badge
              variant={summaryCounts.recent_15m_count > 0 ? 'danger' : 'default'}
              className="font-mono text-[11px]"
            >
              {t('monitor.errors.recent15m')}: {summaryCounts.recent_15m_count}
            </Badge>
            <Badge
              variant={summaryCounts.recent_1h_count > 0 ? 'warning' : 'default'}
              className="font-mono text-[11px]"
            >
              {t('monitor.errors.recent1h')}: {summaryCounts.recent_1h_count}
            </Badge>
            <Badge variant="outline" className="font-mono text-[11px]">
              {t('monitor.errors.totalRetained')}: {summaryCounts.total_retained}
            </Badge>
          </div>
        </div>

        <Button
          size="sm"
          variant="danger"
          disabled={summaryCounts.total_retained === 0 || clearing}
          loading={clearing}
          onClick={() => setConfirmClearOpen(true)}
        >
          {!clearing && <Trash2 size={12} />}
          {t('monitor.errors.clear')}
        </Button>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap items-center justify-between gap-2.5">
        <div className="flex flex-wrap items-center gap-1.5">
          {SOURCE_FILTERS.map((f) => {
            const active = sourceFilter === f.key;
            return (
              <button
                key={f.key || 'all'}
                type="button"
                onClick={() => onSourceChange(f.key)}
                className={cn(
                  'px-2.5 py-1 rounded-lg text-xs font-medium transition-all border',
                  active
                    ? 'bg-accent/15 text-accent border-accent/30'
                    : 'bg-gray-100/70 dark:bg-white/[0.04] text-gray-600 dark:text-gray-400 border-transparent hover:bg-gray-200/70 dark:hover:bg-white/[0.08]',
                )}
              >
                {t(f.labelKey)}
              </button>
            );
          })}
        </div>

        <div className="flex items-center gap-1.5">
          {LEVEL_FILTERS.map((lf) => {
            const active = levelFilter === lf.key;
            return (
              <button
                key={lf.key || 'all-levels'}
                type="button"
                onClick={() => onLevelChange(lf.key)}
                className={cn(
                  'px-2.5 py-1 rounded-lg text-xs font-medium transition-all border',
                  active
                    ? 'bg-gray-900 text-white dark:bg-white dark:text-black border-transparent'
                    : 'bg-gray-100/70 dark:bg-white/[0.04] text-gray-600 dark:text-gray-400 border-transparent hover:bg-gray-200/70 dark:hover:bg-white/[0.08]',
                )}
              >
                {t(lf.labelKey)}
              </button>
            );
          })}
        </div>
      </div>

      {/* Scrollable Error List */}
      {items.length === 0 ? (
        <div className="rounded-xl border border-dashed border-gray-200 dark:border-white/[0.08] py-8 px-4 text-center">
          <CheckCircle2 size={24} className="mx-auto text-emerald-500/70 mb-2" />
          <p className="text-xs text-gray-500 dark:text-gray-400">
            {loadingFiltered
              ? t('common.loading')
              : hasActiveFilter
                ? t('monitor.errors.emptyFiltered')
                : t('monitor.errors.empty')}
          </p>
        </div>
      ) : (
        <div className="max-h-[400px] overflow-y-auto space-y-2 pr-1">
          {items.map((item) => {
            const isWarn = item.level === 'warn';
            const hasDetail = Boolean(item.detail && item.detail.trim());
            const isExpanded = Boolean(expandedIds[item.id]);
            const sourceLabel = t(`monitor.errors.source.${item.source}`, item.source);

            return (
              <div
                key={item.id}
                className={cn(
                  'rounded-lg border transition-colors',
                  isWarn
                    ? 'border-amber-300/50 dark:border-amber-500/20 bg-amber-50/40 dark:bg-amber-950/10'
                    : 'border-gray-200/80 dark:border-white/[0.07] bg-gray-50/60 dark:bg-white/[0.02]',
                )}
              >
                <div
                  role={hasDetail ? 'button' : undefined}
                  tabIndex={hasDetail ? 0 : undefined}
                  onClick={hasDetail ? () => toggleExpand(item.id) : undefined}
                  onKeyDown={
                    hasDetail
                      ? (e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            toggleExpand(item.id);
                          }
                        }
                      : undefined
                  }
                  className={cn(
                    'px-3 py-2.5 flex items-start gap-2.5 text-xs',
                    hasDetail && 'cursor-pointer hover:bg-gray-100/60 dark:hover:bg-white/[0.03] rounded-lg',
                  )}
                >
                  <div className="mt-0.5 shrink-0">
                    {isWarn ? (
                      <AlertTriangle size={14} className="text-amber-500" />
                    ) : (
                      <AlertCircle size={14} className="text-red-500" />
                    )}
                  </div>

                  <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span
                        className="font-mono text-[11px] text-gray-500 dark:text-gray-400 shrink-0"
                        title={formatLocalDateTime(item.timestamp)}
                      >
                        {formatShortTime(item.timestamp)}
                      </span>

                      <Badge className={cn('text-[11px] px-1.5 py-0', getSourceBadgeClass(item.source))}>
                        {sourceLabel}
                      </Badge>

                      {item.code && (
                        <Badge
                          variant={isWarn ? 'warning' : 'danger'}
                          className="font-mono text-[11px] px-1.5 py-0"
                        >
                          {item.code}
                        </Badge>
                      )}

                      {item.project && (
                        <Badge variant="outline" className="text-[11px] px-1.5 py-0">
                          {item.project}
                        </Badge>
                      )}

                      {item.session_key && (
                        <span
                          className="text-[11px] font-mono text-gray-400 truncate max-w-[200px]"
                          title={item.session_key}
                        >
                          {item.session_key}
                        </span>
                      )}
                    </div>

                    <p className="text-gray-800 dark:text-gray-200 break-words leading-relaxed">
                      {item.summary}
                    </p>
                  </div>

                  {hasDetail && (
                    <span className="inline-flex items-center gap-0.5 text-[11px] text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 shrink-0 mt-0.5">
                      {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                      <span className="hidden sm:inline">
                        {isExpanded ? t('monitor.errors.hideDetail') : t('monitor.errors.showDetail')}
                      </span>
                    </span>
                  )}
                </div>

                {hasDetail && isExpanded && (
                  <div className="px-3 pb-3 pt-0.5 pl-9">
                    <pre className="rounded-md bg-gray-900/95 dark:bg-black/70 text-gray-100 p-2.5 text-[11px] font-mono whitespace-pre-wrap break-all overflow-x-auto border border-gray-800 dark:border-white/[0.08]">
                      {item.detail}
                    </pre>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {/* Confirm Clear Modal */}
      <Modal
        open={confirmClearOpen}
        onClose={() => !clearing && setConfirmClearOpen(false)}
        title={t('monitor.errors.clearConfirmTitle')}
      >
        <p className="text-sm text-gray-600 dark:text-gray-300 mb-6">
          {t('monitor.errors.clearConfirmDesc', { count: summaryCounts.total_retained })}
        </p>
        <div className="flex justify-end gap-2">
          <Button
            variant="secondary"
            size="sm"
            disabled={clearing}
            onClick={() => setConfirmClearOpen(false)}
          >
            {t('common.cancel')}
          </Button>
          <Button
            variant="danger"
            size="sm"
            loading={clearing}
            onClick={handleConfirmClear}
          >
            {t('monitor.errors.clear')}
          </Button>
        </div>
      </Modal>
    </Card>
  );
}
