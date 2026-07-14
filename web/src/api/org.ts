import apiClient from './client';
import type { Organization, Member, CreateOrgRequest, InviteMemberRequest } from '../types';

export async function listOrgs(): Promise<Organization[]> {
  const response = await apiClient.get<{ organizations: Organization[] }>('/orgs');
  return response.data.organizations;
}

export async function createOrg(data: CreateOrgRequest): Promise<Organization> {
  const response = await apiClient.post<{ organization: Organization }>('/orgs', data);
  return response.data.organization;
}

export async function getOrgMembers(orgId: string): Promise<Member[]> {
  const response = await apiClient.get<{ members: Member[] }>(`/orgs/${orgId}/members`);
  return response.data.members;
}

export async function inviteMember(orgId: string, data: InviteMemberRequest): Promise<void> {
  await apiClient.post(`/orgs/${orgId}/invite`, data);
}
