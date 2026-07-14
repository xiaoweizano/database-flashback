import apiClient from './client';
import type { AgentInfo } from '../types';

export async function listAgents(orgId?: string): Promise<AgentInfo[]> {
  const params = orgId ? { orgId } : undefined;
  const response = await apiClient.get<AgentInfo[]>('/agents', { params });
  return response.data;
}

export async function getAgent(id: string): Promise<AgentInfo> {
  const response = await apiClient.get<AgentInfo>(`/agents/${id}`);
  return response.data;
}

export async function approveAgent(id: string): Promise<void> {
  await apiClient.post(`/agents/${id}/approve`);
}
