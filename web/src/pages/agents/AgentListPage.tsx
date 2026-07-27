import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  Table, Badge, Card, Typography, Button, Space, Spin, message, Modal,
  Input, Form, Descriptions, Tooltip,
} from 'antd';
import {
  CheckCircleOutlined, PlusOutlined,
  DeleteOutlined, EyeOutlined, CheckOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';
import 'dayjs/locale/zh-cn';
import { listAgents, registerAgent, rejectAgent } from '../../api/agents';
import { listOrgs } from '../../api/org';
import { useLocale } from '../../hooks/useLocale';
import type { AgentInfo } from '../../types';

dayjs.extend(relativeTime);

const { Title, Text } = Typography;

const statusBadge: Record<string, 'success' | 'error' | 'default'> = {
  online: 'success',
  error: 'error',
  offline: 'default',
};

function fmtTime(ts: string | undefined | null): string {
  if (!ts) return '-';
  const d = dayjs(ts);
  const daysDiff = dayjs().diff(d, 'day');
  if (daysDiff < 7) return d.locale('zh-cn').fromNow();
  return d.format('YYYY年M月D日 HH:mm');
}

export default function AgentListPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { t } = useLocale();
  const [registerOpen, setRegisterOpen] = useState(false);
  const [form] = Form.useForm();
  const [regResult, setRegResult] = useState<{ hostname: string; token: string } | null>(null);
  const [detailAgent, setDetailAgent] = useState<AgentInfo | null>(null);

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

  const registerMutation = useMutation({
    mutationFn: (values: { hostname: string; mySQLVersion?: string }) =>
      registerAgent({ orgId: selectedOrgId!, ...values }),
    onSuccess: (data) => {
      setRegResult({ hostname: data.agent.hostname, token: data.registrationToken });
      queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
    onError: () => {
      message.error(t('agents.registerFailed'));
    },
  });

  const rejectMutation = useMutation({
    mutationFn: (id: string) => rejectAgent(id),
    onSuccess: () => {
      message.success(t('agents.rejected'));
      queryClient.invalidateQueries({ queryKey: ['agents'] });
    },
    onError: () => {
      message.error(t('agents.rejectFailed'));
    },
  });

  const handleRegister = () => {
    form.validateFields().then((values) => {
      registerMutation.mutate(values);
    });
  };

  const handleCloseRegister = () => {
    setRegisterOpen(false);
    setTimeout(() => {
      form.resetFields();
      setRegResult(null);
      registerMutation.reset();
    }, 300);
  };

  const showRejectConfirm = (id: string) => {
    Modal.confirm({
      title: t('agents.rejectTitle'),
      content: t('agents.rejectConfirm'),
      okText: t('common.confirm'),
      okType: 'danger',
      cancelText: t('common.cancel'),
      onOk: () => rejectMutation.mutate(id),
    });
  };

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
      width: 100,
      render: (status: string) => (
        <Badge status={statusBadge[status] || 'default'} text={status} />
      ),
    },
    {
      title: t('agents.mysqlVersion'),
      dataIndex: 'mySQLVersion',
      key: 'mySQLVersion',
      width: 120,
      render: (v: string) => v || '-',
    },
    {
      title: t('agents.lastSeen'),
      dataIndex: 'lastSeen',
      key: 'lastSeen',
      width: 180,
      render: (date: string) => fmtTime(date),
    },
    {
      title: t('agents.created'),
      dataIndex: 'createdAt',
      key: 'createdAt',
      width: 180,
      render: (date: string) => fmtTime(date),
    },
    {
      title: t('common.action'),
      key: 'action',
      width: 100,
      render: (_: unknown, record: AgentInfo) => (
        <Space size="small">
          <Tooltip title={t('agents.viewDetail')}>
            <Button
              type="link"
              size="small"
              icon={<EyeOutlined />}
              onClick={(e) => { e.stopPropagation(); setDetailAgent(record); }}
            />
          </Tooltip>
          <Tooltip title={t('agents.delete')}>
            <Button
              type="link"
              size="small"
              danger
              icon={<DeleteOutlined />}
              onClick={(e) => { e.stopPropagation(); showRejectConfirm(record.id); }}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

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
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setRegisterOpen(true)}>
          {t('agents.registerAgent')}
        </Button>
      </div>

      {/* Registration notification banner */}
      {regResult && (
        <Card
          style={{ marginBottom: 24, borderLeft: '4px solid #52c41a' }}
          size="small"
        >
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12 }}>
            <div style={{ flex: 1, minWidth: 0 }}>
              <Text strong style={{ color: '#52c41a' }}>
                <CheckOutlined /> {t('agents.registerSuccess')}
              </Text>
              <br />
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t('agents.registerDesc')}
              </Text>
            </div>
            <Text
              copyable={{ text: `agent --config=${regResult.token}` }}
              style={{
                fontFamily: 'monospace',
                fontSize: 13,
                background: '#f5f5f5',
                padding: '4px 8px',
                borderRadius: 4,
                maxWidth: 420,
              }}
              ellipsis
            >
              agent --config={regResult.token}
            </Text>
            <Button size="small" onClick={() => setRegResult(null)}>
              {t('common.close')}
            </Button>
          </div>
        </Card>
      )}

      {/* Register Modal */}
      <Modal
        title={t('agents.registerAgent')}
        open={registerOpen}
        onCancel={handleCloseRegister}
        footer={null}
        width={520}
        destroyOnClose
      >
        {registerMutation.isSuccess && regResult ? (
          <div style={{ textAlign: 'center', padding: '24px 0' }}>
            <CheckCircleOutlined style={{ fontSize: 48, color: '#52c41a', marginBottom: 16 }} />
            <Title level={4}>{t('agents.registerSuccess')}</Title>
            <Text type="secondary" style={{ display: 'block', marginBottom: 16 }}>
              {t('agents.registerDesc')}
            </Text>
            <div
              style={{
                background: '#f5f5f5',
                borderRadius: 8,
                padding: '12px 16px',
                marginBottom: 24,
                textAlign: 'left',
              }}
            >
              <Text
                copyable={{ text: `agent --config=${regResult.token}` }}
                style={{
                  fontFamily: 'monospace',
                  fontSize: 14,
                  wordBreak: 'break-all',
                }}
              >
                agent --config={regResult.token}
              </Text>
            </div>
            <Button type="primary" size="large" onClick={handleCloseRegister}>
              <CheckOutlined /> {t('common.done')}
            </Button>
          </div>
        ) : (
          <Form form={form} layout="vertical">
            <Form.Item
              name="hostname"
              label={t('agents.hostname')}
              rules={[{ required: true, message: t('agents.hostnameRequired') }]}
            >
              <Input placeholder="e.g. db-server-01" />
            </Form.Item>
            <Form.Item
              name="mySQLVersion"
              label={t('agents.mysqlVersion')}
            >
              <Input placeholder="e.g. 8.0.32" />
            </Form.Item>
            <Form.Item style={{ marginBottom: 0, textAlign: 'right' }}>
              <Space>
                <Button onClick={handleCloseRegister}>{t('common.cancel')}</Button>
                <Button
                  type="primary"
                  onClick={handleRegister}
                  loading={registerMutation.isPending}
                >
                  {t('common.submit')}
                </Button>
              </Space>
            </Form.Item>
          </Form>
        )}
      </Modal>

      {/* Detail Modal */}
      <Modal
        title={t('agents.detail')}
        open={!!detailAgent}
        onCancel={() => setDetailAgent(null)}
        footer={<Button onClick={() => setDetailAgent(null)}>{t('common.close')}</Button>}
        width={600}
      >
        {detailAgent && (
          <Descriptions bordered column={1} size="small">
            <Descriptions.Item label="ID">{detailAgent.id}</Descriptions.Item>
            <Descriptions.Item label={t('agents.hostname')}>{detailAgent.hostname}</Descriptions.Item>
            <Descriptions.Item label={t('agents.mysqlVersion')}>
              {detailAgent.mySQLVersion || '-'}
            </Descriptions.Item>
            <Descriptions.Item label={t('agents.status')}>
              <Badge status={statusBadge[detailAgent.status]} text={detailAgent.status} />
            </Descriptions.Item>
            <Descriptions.Item label={t('agents.lastSeen')}>
              {fmtTime(detailAgent.lastSeen)}
            </Descriptions.Item>
            <Descriptions.Item label={t('agents.created')}>
              {fmtTime(detailAgent.createdAt)}
            </Descriptions.Item>
          </Descriptions>
        )}
      </Modal>

      {(orgsLoading || agentsLoading) ? (
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Spin size="large" />
        </div>
      ) : (
        <Table
          dataSource={agents}
          columns={columns}
          rowKey="id"
          pagination={false}
          onRow={(record: AgentInfo) => ({
            onClick: () => setDetailAgent(record),
            style: { cursor: 'pointer' },
          })}
        />
      )}
    </div>
  );
}
