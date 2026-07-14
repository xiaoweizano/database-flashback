import { useState } from 'react';
import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { Layout as AntLayout, Menu, Button } from 'antd';
import {
  TeamOutlined,
  RobotOutlined,
  HistoryOutlined,
  FileTextOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  GlobalOutlined,
} from '@ant-design/icons';
import { useAuth } from '../hooks/useAuth';
import { useLocale } from '../hooks/useLocale';

const { Header, Sider, Content } = AntLayout;

export default function Layout() {
  const [collapsed, setCollapsed] = useState(false);
  const navigate = useNavigate();
  const location = useLocation();
  const { user, logout } = useAuth();
  const { t, locale, toggleLocale } = useLocale();

  const menuItems = [
    {
      key: '/org',
      icon: <TeamOutlined />,
      label: t('nav.organization'),
    },
    {
      key: '/agents',
      icon: <RobotOutlined />,
      label: t('nav.agents'),
    },
    {
      key: '/pitr',
      icon: <HistoryOutlined />,
      label: t('nav.pitr'),
    },
    {
      key: '/audit',
      icon: <FileTextOutlined />,
      label: t('nav.audit'),
    },
  ];

  const getSelectedKey = () => {
    const path = location.pathname;
    if (path.startsWith('/pitr')) return '/pitr';
    if (path.startsWith('/audit')) return '/audit';
    if (path.startsWith('/agents')) return '/agents';
    if (path.startsWith('/org')) return '/org';
    return path;
  };

  return (
    <AntLayout style={{ minHeight: '100vh' }}>
      <Sider
        trigger={null}
        collapsible
        collapsed={collapsed}
        breakpoint="lg"
        collapsedWidth={window.innerWidth < 768 ? 0 : 80}
        className="layout-sidebar"
        style={{
          overflow: 'auto',
          height: '100vh',
          position: 'sticky',
          top: 0,
          left: 0,
          background: 'linear-gradient(180deg, #1a1a2e 0%, #16213e 100%)',
        }}
      >
        <div style={{
          height: 56,
          margin: 16,
          color: '#fff',
          fontWeight: 'bold',
          fontSize: collapsed ? 14 : 20,
          textAlign: 'center',
          lineHeight: '32px',
          overflow: 'hidden',
          whiteSpace: 'nowrap',
          letterSpacing: collapsed ? 0 : 1,
        }}>
          {collapsed ? (
            <span style={{ fontSize: 20 }}>◈</span>
          ) : (
            <span>DBBridge</span>
          )}
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[getSelectedKey()]}
          items={menuItems}
          onClick={({ key }) => navigate(key)}
          style={{ background: 'transparent', borderInlineEnd: 'none' }}
        />
      </Sider>
      <AntLayout>
        <Header className="layout-header">
          <Button
            type="text"
            icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
            onClick={() => setCollapsed(!collapsed)}
            style={{ fontSize: 16, width: 40, height: 40 }}
          />
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <Button
              type="text"
              size="small"
              icon={<GlobalOutlined />}
              onClick={toggleLocale}
              className="lang-switch-btn"
              style={{ fontSize: 13 }}
            >
              {locale === 'zh' ? 'English' : '中文'}
            </Button>
            <span style={{ fontSize: 14, color: '#6b7280' }}>{user?.email}</span>
            <Button type="text" icon={<LogoutOutlined />} onClick={logout} style={{ fontSize: 13 }}>
              {t('nav.logout')}
            </Button>
          </div>
        </Header>
        <Content className="layout-content">
          <Outlet />
        </Content>
      </AntLayout>
    </AntLayout>
  );
}
