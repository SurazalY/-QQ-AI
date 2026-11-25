import api from './client';

export type MonitorOverallStatus = 'healthy' | 'degraded' | 'unhealthy';
export type MonitorProbeState = 'healthy' | 'degraded' | 'unhealthy' | 'unknown';
export type MonitorErrorSource = 'agy_stream' | 'acp_process' | 'engine' | 'platform' | 'probe' | string;
export type MonitorErrorLevel = 'error' | 'warn' | string;

export interface MonitorCCConnectService {
  status: string;
  version: string;
  uptime_seconds: number;
  projects_count: number;
}

export interface MonitorPlatformService {
  project: string;
  name: string;
  connected: boolean;
  degraded: boolean;
  degraded_reason?: string;
  degraded_since?: string;
  status: MonitorOverallStatus | string;
}

export interface MonitorAgentSessionStatus {
  session_key: string;
  agent_session_id?: string;
  alive: boolean;
}

export interface MonitorAgentService {
  project: string;
  name: string;
  display_name: string;
  cli_binary?: string;
  cli_version?: string;
  active_model?: string;
  active_sessions: number;
  alive_sessions: number;
  live_sessions: number;
  sessions: MonitorAgentSessionStatus[];
  status: MonitorOverallStatus | string;
}

export interface MonitorServices {
  cc_connect: MonitorCCConnectService;
  platforms: MonitorPlatformService[];
  agents: MonitorAgentService[];
}

export interface MonitorProbeStatus {
  project?: string;
  agent?: string;
  cli_binary?: string;
  cli_version?: string;
  active_model?: string;
  status: MonitorProbeState | string;
  latency_ms: number;
  last_checked_at?: string;
  next_probe_at?: string;
  interval_seconds: number;
  consecutive_failures: number;
  last_error?: string;
  in_progress: boolean;
}

export interface MonitorQuotaBucket {
  id: string;
  name: string;
  description?: string;
  window: string;
  remaining_fraction: number;
  remaining_percent: number;
  reset_time?: string;
}

export interface MonitorQuotaGroup {
  name: string;
  description?: string;
  is_current_model_group: boolean;
  buckets: MonitorQuotaBucket[];
}

export interface MonitorQuotaCredits {
  remaining_credits: number;
  upgrade_uri?: string;
}

export interface MonitorQuota {
  fetched_at: string;
  active_model?: string;
  description?: string;
  groups: MonitorQuotaGroup[];
  credits?: MonitorQuotaCredits;
}

export interface MonitorErrorItem {
  id: string;
  timestamp: string;
  source: MonitorErrorSource;
  level: MonitorErrorLevel;
  project?: string;
  session_key?: string;
  code?: string;
  summary: string;
  detail?: string;
}

export interface MonitorErrorsSnapshot {
  total_retained: number;
  recent_15m_count: number;
  recent_1h_count: number;
  filtered_count?: number;
  items: MonitorErrorItem[];
}

export interface MonitorSnapshot {
  updated_at: string;
  overall_status: MonitorOverallStatus | string;
  services: MonitorServices;
  probe: MonitorProbeStatus;
  quota?: MonitorQuota;
  errors: MonitorErrorsSnapshot;
}

export interface ListMonitorErrorsParams {
  source?: string;
  level?: string;
  limit?: number;
}

export interface ClearMonitorErrorsResult {
  message: string;
  total_retained: number;
}

export const getMonitor = () => api.get<MonitorSnapshot>('/monitor');

export const listMonitorErrors = (params?: ListMonitorErrorsParams) => {
  const query: Record<string, string> = {};
  if (params?.source) query.source = params.source;
  if (params?.level) query.level = params.level;
  if (params?.limit && params.limit > 0) query.limit = String(params.limit);
  return api.get<MonitorErrorsSnapshot>('/monitor/errors', Object.keys(query).length ? query : undefined);
};

export const triggerMonitorProbe = () => api.post<MonitorSnapshot>('/monitor/probe');

export const clearMonitorErrors = () => api.delete<ClearMonitorErrorsResult>('/monitor/errors');
