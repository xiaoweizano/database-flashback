import apiClient from './client';
import type { AgentInfo } from '../types';

export async function listAgents(): Promise<AgentInfo[]> {
  const response = await apiClient.get<AgentInfo[]>('/agents');
  return response.data;
}

export async function getAgent(id: string): Promise<AgentInfo> {
  const response = await apiClient.get<AgentInfo>(`/agents/${id}`);
  return response.data;
}

export async function approveAgent(id: string): Promise<void> {
  await apiClient.post(`/agents/${id}/approve`);
}
