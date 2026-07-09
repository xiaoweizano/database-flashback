import apiClient from './client';
import type { PITROperation, ProgressData } from '../types';

export async function startPITR(data: {
  agent_id: string;
  target_table: string;
  recovery_time: string;
  mode: 'preview' | 'execute';
}): Promise<{ operationId: string; status: string }> {
  const response = await apiClient.post<{ operationId: string; status: string }>('/pitr/start', data);
  return response.data;
}

export async function getPITRStatus(id: string): Promise<PITROperation> {
  const response = await apiClient.get<PITROperation>(`/pitr/${id}/status`);
  return response.data;
}

export async function getPITRProgress(id: string): Promise<ProgressData> {
  const response = await apiClient.get<ProgressData>(`/pitr/${id}/progress`);
  return response.data;
}

export async function getPITRPreview(id: string): Promise<{
  operationId: string;
  rowsAffected: number;
  sqlSample: string;
  parsedAt: string;
  state: string;
}> {
  const response = await apiClient.get<{
    operationId: string;
    rowsAffected: number;
    sqlSample: string;
    parsedAt: string;
    state: string;
  }>(`/pitr/${id}/preview`);
  return response.data;
}

export async function cancelPITR(id: string): Promise<void> {
  await apiClient.post(`/pitr/${id}/cancel`);
}
