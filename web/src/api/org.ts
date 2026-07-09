import apiClient from './client';
import type { Organization, Member, CreateOrgRequest, InviteMemberRequest } from '../types';

export async function listOrgs(): Promise<Organization[]> {
  const response = await apiClient.get<Organization[]>('/orgs');
  return response.data;
}

export async function createOrg(data: CreateOrgRequest): Promise<Organization> {
  const response = await apiClient.post<Organization>('/orgs', data);
  return response.data;
}

export async function getOrgMembers(orgId: string): Promise<Member[]> {
  const response = await apiClient.get<Member[]>(`/orgs/${orgId}/members`);
  return response.data;
}

export async function inviteMember(orgId: string, data: InviteMemberRequest): Promise<void> {
  await apiClient.post(`/orgs/${orgId}/invite`, data);
}
