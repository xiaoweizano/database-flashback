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

export type PITRState = 'preflight' | 'confirmed' | 'parsing' | 'previewed' | 'executing' | 'completed' | 'failed' | 'cancelled';

export interface PreflightResult {
  checkedAt: string;
  binlogFiles: string[];
  earliestTime: string;
  estimatedSize: number;
}

export interface ParseResult {
  parsedAt: string;
  rowsAffected: number;
  sqlSample: string;
}

export interface ExecResult {
  executedAt: string;
  rowsRestored: number;
  duration: string;
}

export interface OperationProgress {
  operationId: string;
  state: PITRState;
  batchesComplete: number;
  batchesTotal: number;
  rowsRestored: number;
  estimatedRemaining: string;
}

export interface PITROperation {
  id: string;
  orgId: string;
  agentId: string;
  targetTable: string;
  recoveryTime: string;
  mode: string;
  state: PITRState;
  preflightResult: PreflightResult | null;
  parseResult: ParseResult | null;
  execResult: ExecResult | null;
  progress: OperationProgress | null;
  error: string;
  createdAt: string;
  updatedAt: string;
}

export interface ProgressData {
  operationId: string;
  state: PITRState;
  batchesComplete: number;
  batchesTotal: number;
  rowsRestored: number;
  estimatedRemaining: string;
}

export interface AuditEntry {
  operationId: string;
  operator: string;
  timestamp: string;
  orgId: string;
  agentId: string;
  targetTable: string;
  recoveryTime: string;
  rowsAffected: number;
  status: string;
  errorDetails: string;
}
