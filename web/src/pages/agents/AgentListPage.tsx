import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Table, Badge, Card, Typography, Button, Space, Spin, message } from 'antd';
import { CopyOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { listAgents } from '../../api/agents';
import { listOrgs } from '../../api/org';
import { useLocale } from '../../hooks/useLocale';
import type { AgentInfo } from '../../types';

const { Title, Text } = Typography;

const statusBadge: Record<string, 'success' | 'error' | 'default'> = {
  online: 'success',
  error: 'error',
  offline: 'default',
};

export default function AgentListPage() {
  const navigate = useNavigate();
  const { t } = useLocale();

  const { data: orgs, isLoading: orgsLoading } = useQuery({
    queryKey: ['orgs'],
    queryFn: listOrgs,
  });

  const selectedOrgId = orgs?.[0]?.id;

  const { data: agents, isLoading: agentsLoading, error } = useQuery({
    queryKey: ['agents', selectedOrgId],
    queryFn: () => listAgents(selectedOrgId),
    enabled: !!selectedOrgId,
  });

  const columns = [
    {
      title: t('agents.hostname'),
      dataIndex: 'hostname',
      key: 'hostname',
    },
    {
      title: t('agents.status'),
      dataIndex: 'status',
      key: 'status',
      render: (status: string) => (
        <Badge status={statusBadge[status] || 'default'} text={status} />
      ),
    },
    {
      title: t('agents.mysqlVersion'),
      dataIndex: 'mysqlVersion',
      key: 'mysqlVersion',
    },
    {
      title: t('agents.lastSeen'),
      dataIndex: 'lastSeen',
      key: 'lastSeen',
      render: (date: string) => date ? dayjs(date).format('YYYY-MM-DD HH:mm') : '-',
    },
    {
      title: t('agents.created'),
      dataIndex: 'createdAt',
      key: 'createdAt',
      render: (date: string) => dayjs(date).format('YYYY-MM-DD'),
    },
  ];

  const handleCopyCommand = () => {
    try {
      navigator.clipboard.writeText('agent --config=<registration-token>');
      message.success(t('agents.copied'));
    } catch {
      message.error(t('agents.copyFailed'));
    }
  };

  if (!orgsLoading && orgs?.length === 0) {
    return (
      <Card className="summary-card">
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Title level={4}>{t('agents.noOrg')}</Title>
          <Text type="secondary">{t('agents.noOrgDesc')}</Text>
          <br /><br />
          <Button type="primary" onClick={() => navigate('/org')}>
            {t('agents.goToOrg')}
          </Button>
        </div>
      </Card>
    );
  }

  if (error) {
    return (
      <Card className="summary-card">
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Text type="danger">{t('agents.loadFailed')}</Text>
        </div>
      </Card>
    );
  }

  return (
    <div>
      <div className="page-header">
        <Title level={3} style={{ margin: 0 }}>{t('agents.title')}</Title>
      </div>

      <Card className="agent-command-card" style={{ marginBottom: 24 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
          <div>
            <Text strong>{t('agents.register')}</Text>
            <br />
            <Text type="secondary" style={{ fontSize: 12 }}>
              {t('agents.registerDesc')}
            </Text>
          </div>
          <Space>
            <code style={{
              padding: '4px 8px',
              background: '#f5f5f5',
              borderRadius: 4,
              fontSize: 13,
            }}>
              agent --config=&lt;registration-token&gt;
            </code>
            <Button icon={<CopyOutlined />} size="small" onClick={handleCopyCommand}>
              {t('agents.copy')}
            </Button>
          </Space>
        </div>
      </Card>

      {(orgsLoading || agentsLoading) ? (
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Spin size="large" />
        </div>
      ) : (
        <Table
          dataSource={agents}
          columns={columns}
          rowKey="id"
          onRow={(record: AgentInfo) => ({
            onClick: () => navigate(`/agents/${record.id}`),
            style: { cursor: 'pointer' },
          })}
        />
      )}
    </div>
  );
}
