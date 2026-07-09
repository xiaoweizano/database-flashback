export interface User {
  id: string;
  email: string;
  role: string;
}

export interface Organization {
  id: string;
  name: string;
  createdAt: string;
}

export interface Member {
  userId: string;
  email: string;
  role: 'admin' | 'member';
  joinedAt: string;
}

export interface AgentInfo {
  id: string;
  hostname: string;
  mysqlVersion: string;
  status: 'online' | 'offline' | 'error';
  lastSeen: string;
  createdAt: string;
}

export interface AuthResponse {
  token: string;
  user: User;
}

export interface LoginRequest {
  email: string;
  password: string;
}

export interface RegisterRequest {
  email: string;
  password: string;
}

export interface CreateOrgRequest {
  name: string;
}

export interface InviteMemberRequest {
  email: string;
  role: 'admin' | 'member';
}
