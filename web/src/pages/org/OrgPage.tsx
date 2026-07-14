import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Card, Button, Table, Modal, Form, Input, Select, Row, Col, Typography, Tag, Spin, message } from 'antd';
import { PlusOutlined, UserAddOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { listOrgs, createOrg, getOrgMembers, inviteMember } from '../../api/org';
import { useLocale } from '../../hooks/useLocale';
import type { CreateOrgRequest, InviteMemberRequest } from '../../types';

const { Title } = Typography;

export default function OrgPage() {
  const [selectedOrgId, setSelectedOrgId] = useState<string | null>(null);
  const [createModalOpen, setCreateModalOpen] = useState(false);
  const [inviteModalOpen, setInviteModalOpen] = useState(false);
  const [createForm] = Form.useForm();
  const [inviteForm] = Form.useForm();
  const queryClient = useQueryClient();
  const { t } = useLocale();

  const memberColumns = [
    {
      title: 'Email',
      dataIndex: 'email',
      key: 'email',
    },
    {
      title: t('org.role'),
      dataIndex: 'role',
      key: 'role',
      render: (role: string) => (
        <Tag color={role === 'admin' ? 'red' : 'blue'}>{role}</Tag>
      ),
    },
    {
      title: t('org.joinedAt'),
      dataIndex: 'joinedAt',
      key: 'joinedAt',
      render: (date: string) => dayjs(date).format('YYYY-MM-DD HH:mm'),
    },
  ];

  const { data: orgs, isLoading: orgsLoading } = useQuery({
    queryKey: ['orgs'],
    queryFn: listOrgs,
  });

  const { data: members, isLoading: membersLoading } = useQuery({
    queryKey: ['org-members', selectedOrgId],
    queryFn: () => getOrgMembers(selectedOrgId!),
    enabled: !!selectedOrgId,
  });

  const createMutation = useMutation({
    mutationFn: (data: CreateOrgRequest) => createOrg(data),
    onSuccess: () => {
      message.success(t('org.created'));
      setCreateModalOpen(false);
      createForm.resetFields();
      queryClient.invalidateQueries({ queryKey: ['orgs'] });
    },
    onError: () => {
      message.error(t('org.createFailed'));
    },
  });

  const inviteMutation = useMutation({
    mutationFn: (data: InviteMemberRequest) => inviteMember(selectedOrgId!, data),
    onSuccess: () => {
      message.success(t('org.invited'));
      setInviteModalOpen(false);
      inviteForm.resetFields();
      queryClient.invalidateQueries({ queryKey: ['org-members', selectedOrgId] });
    },
    onError: () => {
      message.error(t('org.inviteFailed'));
    },
  });

  const selectedOrg = orgs?.find(o => o.id === selectedOrgId);

  const handleCreateOrg = (values: { name: string }) => {
    createMutation.mutate(values);
  };

  const handleInviteMember = (values: { email: string; role: 'admin' | 'member' }) => {
    inviteMutation.mutate(values);
  };

  if (orgsLoading) {
    return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" /></div>;
  }

  return (
    <div>
      <div className="page-header">
        <Title level={3} style={{ margin: 0 }}>{t('org.title')}</Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateModalOpen(true)}>
          {t('org.create')}
        </Button>
      </div>

      {orgs && orgs.length > 0 ? (
        <Row gutter={[16, 16]}>
          {orgs.map(org => (
            <Col key={org.id} xs={24} sm={12} lg={8}>
              <Card
                hoverable
                className="summary-card"
                onClick={() => setSelectedOrgId(org.id)}
                style={{
                  borderColor: selectedOrgId === org.id ? '#6366f1' : undefined,
                  borderWidth: selectedOrgId === org.id ? 2 : 1,
                }}
              >
                <Card.Meta
                  title={<span style={{ fontWeight: 600 }}>{org.name}</span>}
                  description={
                    <span style={{ fontSize: 12, color: '#9ca3af' }}>
                      {t('org.createdAt')}: {dayjs(org.createdAt).format('YYYY-MM-DD')}
                    </span>
                  }
                />
              </Card>
            </Col>
          ))}
        </Row>
      ) : (
        <Card className="summary-card">
          <div style={{ textAlign: 'center', padding: 48 }}>
            <Title level={4} style={{ color: '#6b7280' }}>{t('org.noOrgs')}</Title>
            <Typography.Text type="secondary">{t('org.noOrgsDesc')}</Typography.Text>
            <br /><br />
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateModalOpen(true)}>
              {t('org.create')}
            </Button>
          </div>
        </Card>
      )}

      {selectedOrg && (
        <Card
          className="summary-card"
          style={{ marginTop: 24 }}
          title={
            <span style={{ fontWeight: 600 }}>
              {t('org.members')}: {selectedOrg.name}
            </span>
          }
          extra={
            <Button type="primary" icon={<UserAddOutlined />} onClick={() => setInviteModalOpen(true)}>
              {t('org.inviteMember')}
            </Button>
          }
        >
          <Table
            dataSource={members}
            columns={memberColumns}
            loading={membersLoading}
            rowKey="userId"
            pagination={false}
          />
        </Card>
      )}

      <Modal
        title={t('org.createTitle')}
        open={createModalOpen}
        onOk={() => createForm.submit()}
        onCancel={() => { createForm.resetFields(); setCreateModalOpen(false); }}
        confirmLoading={createMutation.isPending}
        destroyOnClose
      >
        <Form form={createForm} onFinish={handleCreateOrg} layout="vertical">
          <Form.Item name="name" label={t('org.name')} rules={[{ required: true, message: t('org.nameRequired') }]}>
            <Input placeholder={t('org.namePlaceholder')} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={t('org.inviteTitle')}
        open={inviteModalOpen}
        onOk={() => inviteForm.submit()}
        onCancel={() => { inviteForm.resetFields(); setInviteModalOpen(false); }}
        confirmLoading={inviteMutation.isPending}
        destroyOnClose
      >
        <Form form={inviteForm} onFinish={handleInviteMember} layout="vertical">
          <Form.Item
            name="email"
            label="Email"
            rules={[
              { required: true, message: t('auth.validation.emailRequired') },
              { type: 'email', message: t('auth.validation.emailInvalid') },
            ]}
          >
            <Input placeholder="user@example.com" />
          </Form.Item>
          <Form.Item name="role" label={t('org.role')} initialValue="member">
            <Select>
              <Select.Option value="member">Member</Select.Option>
              <Select.Option value="admin">Admin</Select.Option>
            </Select>
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
