import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Table, Badge, Card, Typography, Button, Space, Spin, message } from 'antd';
import { CopyOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { listAgents } from '../../api/agents';
import type { AgentInfo } from '../../types';

const { Title } = Typography;

const statusBadge: Record<string, 'success' | 'error' | 'default'> = {
  online: 'success',
  error: 'error',
  offline: 'default',
};

const columns = [
  {
    title: 'Hostname',
    dataIndex: 'hostname',
    key: 'hostname',
  },
  {
    title: 'Status',
    dataIndex: 'status',
    key: 'status',
    render: (status: string) => (
      <Badge status={statusBadge[status] || 'default'} text={status} />
    ),
  },
  {
    title: 'MySQL Version',
    dataIndex: 'mysqlVersion',
    key: 'mysqlVersion',
  },
  {
    title: 'Last Seen',
    dataIndex: 'lastSeen',
    key: 'lastSeen',
    render: (date: string) => date ? dayjs(date).format('YYYY-MM-DD HH:mm') : '-',
  },
  {
    title: 'Created',
    dataIndex: 'createdAt',
    key: 'createdAt',
    render: (date: string) => dayjs(date).format('YYYY-MM-DD'),
  },
];

export default function AgentListPage() {
  const navigate = useNavigate();

  const { data: agents, isLoading, error } = useQuery({
    queryKey: ['agents'],
    queryFn: listAgents,
  });

  const handleCopyCommand = () => {
    try {
      navigator.clipboard.writeText('agent --config=<registration-token>');
      message.success('Command copied to clipboard');
    } catch {
      message.error('Failed to copy command');
    }
  };

  if (error) {
    return (
      <Card>
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Typography.Text type="danger">Failed to load agents. Please try again later.</Typography.Text>
        </div>
      </Card>
    );
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0 }}>Agents</Title>
      </div>

      <Card style={{ marginBottom: 24 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <Typography.Text strong>Register a New Agent</Typography.Text>
            <br />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              Run this command on the server you want to monitor:
            </Typography.Text>
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
              Copy
            </Button>
          </Space>
        </div>
      </Card>

      {isLoading ? (
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
