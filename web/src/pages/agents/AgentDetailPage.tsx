import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Card, Descriptions, Badge, Button, Spin, Typography, message, Space, Modal } from 'antd';
import { ArrowLeftOutlined, CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { getAgent, approveAgent, rejectAgent } from '../../api/agents';
import { useLocale } from '../../hooks/useLocale';

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
  const { t } = useLocale();

  const { data: agent, isLoading, error } = useQuery({
    queryKey: ['agent', id],
    queryFn: () => getAgent(id!),
    enabled: !!id,
  });

  const approveMutation = useMutation({
    mutationFn: () => approveAgent(id!),
    onSuccess: () => {
      message.success(t('agents.approveSuccess'));
      queryClient.invalidateQueries({ queryKey: ['agent', id] });
    },
    onError: () => {
      message.error(t('agents.approveFailed'));
    },
  });

  const rejectMutation = useMutation({
    mutationFn: () => rejectAgent(id!),
    onSuccess: () => {
      message.success(t('agents.rejected'));
      navigate('/agents');
    },
    onError: () => {
      message.error(t('agents.rejectFailed'));
    },
  });

  const showRejectConfirm = () => {
    Modal.confirm({
      title: t('agents.rejectTitle'),
      content: t('agents.rejectConfirmDetail'),
      okText: t('common.confirm'),
      okType: 'danger',
      cancelText: t('common.cancel'),
      onOk: () => rejectMutation.mutate(),
    });
  };

  if (isLoading) {
    return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" /></div>;
  }

  if (error || !agent) {
    return (
      <Card>
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Typography.Text type="danger">{t('agents.loadDetailFailed')}</Typography.Text>
          <br /><br />
          <Button onClick={() => navigate('/agents')}>{t('agents.backToList')}</Button>
        </div>
      </Card>
    );
  }

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/agents')}>
          {t('agents.back')}
        </Button>
      </Space>

      <Card>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 24 }}>
          <div>
            <Title level={4} style={{ margin: 0 }}>{agent.hostname}</Title>
            <Badge status={statusBadge[agent.status]} text={agent.status} />
          </div>
          <Space>
            <Button
              type="primary"
              icon={<CheckCircleOutlined />}
              onClick={() => approveMutation.mutate()}
              loading={approveMutation.isPending}
            >
              {t('agents.approve')}
            </Button>
            {!agent.approved && (
              <Button
                danger
                icon={<CloseCircleOutlined />}
                onClick={showRejectConfirm}
                loading={rejectMutation.isPending}
              >
                {t('agents.rejectTitle')}
              </Button>
            )}
          </Space>
        </div>

        <Descriptions bordered column={1}>
          <Descriptions.Item label={t('agents.id')}>{agent.id}</Descriptions.Item>
          <Descriptions.Item label={t('agents.hostname')}>{agent.hostname}</Descriptions.Item>
          <Descriptions.Item label={t('agents.mysqlVersion')}>
            {agent.mySQLVersion || '-'}
          </Descriptions.Item>
          <Descriptions.Item label={t('agents.status')}>
            <Badge status={statusBadge[agent.status]} text={agent.status} />
          </Descriptions.Item>
          <Descriptions.Item label={t('agents.lastSeen')}>
            {agent.lastSeen ? dayjs(agent.lastSeen).format('YYYY-MM-DD HH:mm:ss') : '-'}
          </Descriptions.Item>
          <Descriptions.Item label={t('agents.created')}>
            {dayjs(agent.createdAt).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
        </Descriptions>
      </Card>
    </div>
  );
}
