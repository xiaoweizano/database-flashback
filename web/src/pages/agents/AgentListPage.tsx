import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Table, Badge, Card, Typography, Button, Space, Spin, message, Tag, Modal, Input, Form } from 'antd';
import { CopyOutlined, CheckCircleOutlined, CloseCircleOutlined, PlusOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { listAgents, registerAgent } from '../../api/agents';
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
  const queryClient = useQueryClient();
  const { t } = useLocale();
  const [registerOpen, setRegisterOpen] = useState(false);
  const [form] = Form.useForm();
  const [regResult, setRegResult] = useState<{ hostname: string; token: string } | null>(null);

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
      message.error('Failed to register agent');
    },
  });

  const handleRegister = () => {
    form.validateFields().then((values) => {
      registerMutation.mutate(values);
    });
  };

  const handleClose = () => {
    setRegisterOpen(false);
    form.resetFields();
    setRegResult(null);
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
    {
      title: t('agents.approved'),
      dataIndex: 'approved',
      key: 'approved',
      render: (approved: boolean) => approved
        ? <Tag icon={<CheckCircleOutlined />} color="success">Yes</Tag>
        : <Tag icon={<CloseCircleOutlined />} color="error">No</Tag>,
    },
  ];

  const fallbackCopy = (text: string) => {
    try {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
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
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setRegisterOpen(true)}>
          {t('agents.registerAgent')}
        </Button>
      </div>

      {regResult ? (
        <Card className="agent-command-card" style={{ marginBottom: 24 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
            <div>
              <Text strong>{t('agents.registerSuccess')}</Text>
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
                maxWidth: 400,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
              }}>
                agent --config={regResult.token}
              </code>
              <Button icon={<CopyOutlined />} size="small" onClick={() => {
                const text = `agent --config=${regResult.token}`;
                navigator.clipboard?.writeText
                  ? navigator.clipboard.writeText(text).then(() => message.success(t('agents.copied')))
                  : fallbackCopy(text);
              }}>
                {t('agents.copy')}
              </Button>
            </Space>
          </div>
        </Card>
      ) : (
        <Card className="agent-command-card" style={{ marginBottom: 24 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
            <div>
              <Text strong>{t('agents.register')}</Text>
              <br />
              <Text type="secondary" style={{ fontSize: 12 }}>
                {t('agents.registerNoToken')}
              </Text>
            </div>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setRegisterOpen(true)}>
              {t('agents.registerAgent')}
            </Button>
          </div>
        </Card>
      )}

      <Modal
        title={t('agents.registerAgent')}
        open={registerOpen}
        onOk={handleRegister}
        onCancel={handleClose}
        confirmLoading={registerMutation.isPending}
        okText={t('common.submit')}
        cancelText={t('common.cancel')}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="hostname"
            label={t('agents.hostname')}
            rules={[{ required: true, message: 'Please enter the server hostname' }]}
          >
            <Input placeholder="e.g. db-server-01" />
          </Form.Item>
          <Form.Item
            name="mySQLVersion"
            label={t('agents.mysqlVersion')}
          >
            <Input placeholder="e.g. 8.0.32 (optional)" />
          </Form.Item>
        </Form>
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
          onRow={(record: AgentInfo) => ({
            onClick: () => navigate(`/agents/${record.id}`),
            style: { cursor: 'pointer' },
          })}
        />
      )}
    </div>
  );
}
