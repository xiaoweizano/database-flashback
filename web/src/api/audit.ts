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

export async function getAuditEntry(orgId: string, operationId: string): Promise<AuditEntry> {
  const response = await apiClient.get<AuditEntry[]>('/audit', {
    params: { org_id: orgId, operation_id: operationId },
  });
  if (response.data.length === 0) {
    throw new Error('Audit entry not found');
  }
  return response.data[0];
}

export async function exportAuditCsv(orgId: string, filters?: { from?: string; to?: string; status?: string; agentId?: string }): Promise<Blob> {
  const params: Record<string, string> = { org_id: orgId };
  if (filters?.from) params.from = filters.from;
  if (filters?.to) params.to = filters.to;
  if (filters?.status) params.status = filters.status;
  if (filters?.agentId) params.agent_id = filters.agentId;
  const response = await apiClient.get('/audit/export', {
    params,
    responseType: 'blob',
  });
  return response.data;
}
