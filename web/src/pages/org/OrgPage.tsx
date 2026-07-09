import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Card, Button, Table, Modal, Form, Input, Select, Row, Col, Typography, Tag, Spin, message } from 'antd';
import { PlusOutlined, UserAddOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { listOrgs, createOrg, getOrgMembers, inviteMember } from '../../api/org';
import type { CreateOrgRequest, InviteMemberRequest } from '../../types';

const { Title } = Typography;

const memberColumns = [
  {
    title: 'Email',
    dataIndex: 'email',
    key: 'email',
  },
  {
    title: 'Role',
    dataIndex: 'role',
    key: 'role',
    render: (role: string) => (
      <Tag color={role === 'admin' ? 'red' : 'blue'}>{role}</Tag>
    ),
  },
  {
    title: 'Joined At',
    dataIndex: 'joinedAt',
    key: 'joinedAt',
    render: (date: string) => dayjs(date).format('YYYY-MM-DD HH:mm'),
  },
];

export default function OrgPage() {
  const [selectedOrgId, setSelectedOrgId] = useState<string | null>(null);
  const [createModalOpen, setCreateModalOpen] = useState(false);
  const [inviteModalOpen, setInviteModalOpen] = useState(false);
  const [createForm] = Form.useForm();
  const [inviteForm] = Form.useForm();
  const queryClient = useQueryClient();

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
      message.success('Organization created');
      setCreateModalOpen(false);
      createForm.resetFields();
      queryClient.invalidateQueries({ queryKey: ['orgs'] });
    },
    onError: () => {
      message.error('Failed to create organization');
    },
  });

  const inviteMutation = useMutation({
    mutationFn: (data: InviteMemberRequest) => inviteMember(selectedOrgId!, data),
    onSuccess: () => {
      message.success('Member invited');
      setInviteModalOpen(false);
      inviteForm.resetFields();
      queryClient.invalidateQueries({ queryKey: ['org-members', selectedOrgId] });
    },
    onError: () => {
      message.error('Failed to invite member');
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
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0 }}>Organizations</Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateModalOpen(true)}>
          Create Organization
        </Button>
      </div>

      {orgs && orgs.length > 0 ? (
        <Row gutter={[16, 16]}>
          {orgs.map(org => (
            <Col key={org.id} xs={24} sm={12} lg={8}>
              <Card
                hoverable
                onClick={() => setSelectedOrgId(org.id)}
                style={{
                  borderColor: selectedOrgId === org.id ? '#1677ff' : undefined,
                  borderWidth: selectedOrgId === org.id ? 2 : 1,
                }}
              >
                <Card.Meta
                  title={org.name}
                  description={`Created: ${dayjs(org.createdAt).format('YYYY-MM-DD')}`}
                />
              </Card>
            </Col>
          ))}
        </Row>
      ) : (
        <Card>
          <div style={{ textAlign: 'center', padding: 48 }}>
            <Title level={4}>No organizations yet</Title>
            <Typography.Text type="secondary">Create your first organization to get started.</Typography.Text>
            <br /><br />
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateModalOpen(true)}>
              Create Organization
            </Button>
          </div>
        </Card>
      )}

      {selectedOrg && (
        <Card
          style={{ marginTop: 24 }}
          title={`Members: ${selectedOrg.name}`}
          extra={
            <Button type="primary" icon={<UserAddOutlined />} onClick={() => setInviteModalOpen(true)}>
              Invite Member
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
        title="Create Organization"
        open={createModalOpen}
        onOk={() => createForm.submit()}
        onCancel={() => { createForm.resetFields(); setCreateModalOpen(false); }}
        confirmLoading={createMutation.isPending}
      >
        <Form form={createForm} onFinish={handleCreateOrg} layout="vertical">
          <Form.Item name="name" label="Organization Name" rules={[{ required: true, message: 'Please enter a name' }]}>
            <Input placeholder="My Organization" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="Invite Member"
        open={inviteModalOpen}
        onOk={() => inviteForm.submit()}
        onCancel={() => { inviteForm.resetFields(); setInviteModalOpen(false); }}
        confirmLoading={inviteMutation.isPending}
      >
        <Form form={inviteForm} onFinish={handleInviteMember} layout="vertical">
          <Form.Item
            name="email"
            label="Email"
            rules={[
              { required: true, message: 'Please enter an email' },
              { type: 'email', message: 'Please enter a valid email' },
            ]}
          >
            <Input placeholder="user@example.com" />
          </Form.Item>
          <Form.Item name="role" label="Role" initialValue="member">
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
