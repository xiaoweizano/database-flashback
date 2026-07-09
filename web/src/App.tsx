import { Routes, Route, Navigate } from 'react-router-dom';
import { AuthProvider } from './hooks/useAuth';
import ProtectedRoute from './components/ProtectedRoute';
import Layout from './components/Layout';
import LoginPage from './pages/auth/LoginPage';
import RegisterPage from './pages/auth/RegisterPage';
import OrgPage from './pages/org/OrgPage';
import OrgSettingsPage from './pages/org/OrgSettingsPage';
import AgentListPage from './pages/agents/AgentListPage';
import AgentDetailPage from './pages/agents/AgentDetailPage';

export default function App() {
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/" element={<ProtectedRoute><Layout /></ProtectedRoute>}>
          <Route index element={<Navigate to="/agents" replace />} />
          <Route path="org" element={<OrgPage />} />
          <Route path="org/settings" element={<OrgSettingsPage />} />
          <Route path="agents" element={<AgentListPage />} />
          <Route path="agents/:id" element={<AgentDetailPage />} />
        </Route>
      </Routes>
    </AuthProvider>
  );
}
