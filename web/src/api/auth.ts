import apiClient from './client';
import type { AuthResponse, LoginRequest, RegisterRequest, User } from '../types';

export async function login(data: LoginRequest): Promise<AuthResponse> {
  const response = await apiClient.post<AuthResponse>('/auth/login', data);
  return response.data;
}

export async function register(data: RegisterRequest): Promise<AuthResponse> {
  const response = await apiClient.post<AuthResponse>('/auth/register', data);
  return response.data;
}

export async function refreshToken(): Promise<{ token: string }> {
  const response = await apiClient.post<{ token: string }>('/auth/refresh');
  return response.data;
}

export async function getProfile(): Promise<User> {
  const response = await apiClient.get<User>('/auth/profile');
  return response.data;
}
