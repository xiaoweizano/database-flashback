import apiClient from './client';
import type { AuditEntry } from '../types';

export interface AuditFilters {
  orgId: string;
  from?: string;
  to?: string;
  agentId?: string;
  status?: string;
}

export async function listAuditEntries(filters: AuditFilters): Promise<AuditEntry[]> {
  const params: Record<string, string> = { org_id: filters.orgId };
  if (filters.from) params.from = filters.from;
  if (filters.to) params.to = filters.to;
  if (filters.agentId) params.agent_id = filters.agentId;
  if (filters.status) params.status = filters.status;
  const response = await apiClient.get<AuditEntry[]>('/audit', { params });
  return response.data;
}

export async function exportAuditCsv(orgId: string): Promise<Blob> {
  const response = await apiClient.get('/audit/export', {
    params: { org_id: orgId },
    responseType: 'blob',
  });
  return response.data;
}
