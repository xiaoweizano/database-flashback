import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Card, Descriptions, Badge, Button, Spin, Typography, message, Space } from 'antd';
import { ArrowLeftOutlined, CheckCircleOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { getAgent, approveAgent } from '../../api/agents';

const { Title } = Typography;

const statusBadge: Record<string, 'success' | 'error' | 'default'> = {
  online: 'success',
  error: 'error',
  offline: 'default',
};

export default function AgentDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const { data: agent, isLoading, error } = useQuery({
    queryKey: ['agent', id],
    queryFn: () => getAgent(id!),
    enabled: !!id,
  });

  const approveMutation = useMutation({
    mutationFn: () => approveAgent(id!),
    onSuccess: () => {
      message.success('Agent approved');
      queryClient.invalidateQueries({ queryKey: ['agent', id] });
    },
    onError: () => {
      message.error('Failed to approve agent');
    },
  });

  if (isLoading) {
    return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" /></div>;
  }

  if (error || !agent) {
    return (
      <Card>
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Typography.Text type="danger">Failed to load agent details.</Typography.Text>
          <br /><br />
          <Button onClick={() => navigate('/agents')}>Back to Agents</Button>
        </div>
      </Card>
    );
  }

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/agents')}>
          Back
        </Button>
      </Space>

      <Card>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 24 }}>
          <div>
            <Title level={4} style={{ margin: 0 }}>{agent.hostname}</Title>
            <Badge status={statusBadge[agent.status]} text={agent.status} />
          </div>
          <Button
            type="primary"
            icon={<CheckCircleOutlined />}
            onClick={() => approveMutation.mutate()}
            loading={approveMutation.isPending}
          >
            Approve Agent
          </Button>
        </div>

        <Descriptions bordered column={1}>
          <Descriptions.Item label="ID">{agent.id}</Descriptions.Item>
          <Descriptions.Item label="Hostname">{agent.hostname}</Descriptions.Item>
          <Descriptions.Item label="MySQL Version">
            {agent.mysqlVersion || '-'}
          </Descriptions.Item>
          <Descriptions.Item label="Status">
            <Badge status={statusBadge[agent.status]} text={agent.status} />
          </Descriptions.Item>
          <Descriptions.Item label="Last Seen">
            {agent.lastSeen ? dayjs(agent.lastSeen).format('YYYY-MM-DD HH:mm:ss') : '-'}
          </Descriptions.Item>
          <Descriptions.Item label="Created At">
            {dayjs(agent.createdAt).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
        </Descriptions>
      </Card>
    </div>
  );
}
