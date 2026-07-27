import { useState, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  Card, Table, Button, Typography, Spin, Empty, Alert,
  Tag, DatePicker, Select, Space, message,
} from 'antd';
import { DownloadOutlined, ReloadOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { useLocale } from '../../hooks/useLocale';
import { listOrgs } from '../../api/org';
import { listAuditEntries, exportAuditCsv } from '../../api/audit';
import { listAgents } from '../../api/agents';
import type { AuditEntry, AgentInfo } from '../../types';
import type { ColumnsType } from 'antd/es/table';

const { Title, Text } = Typography;
const { RangePicker } = DatePicker;

const statusColors: Record<string, string> = {
  completed: 'success',
  failed: 'error',
  cancelled: 'default',
  previewed: 'processing',
};

export default function AuditLogPage() {
  const navigate = useNavigate();
  const { t } = useLocale();
  const [dateRange, setDateRange] = useState<[string, string] | null>(null);
  const [statusFilter, setStatusFilter] = useState<string | undefined>(undefined);
  const [agentFilter, setAgentFilter] = useState<string | undefined>(undefined);

  // Get user's org
  const orgsQuery = useQuery({
    queryKey: ['orgs'],
    queryFn: listOrgs,
  });

  const orgId = orgsQuery.data?.[0]?.id;

  // Fetch agents for filter dropdown
  const agentsQuery = useQuery({
    queryKey: ['agents', orgId],
    queryFn: () => listAgents(orgId),
    enabled: !!orgId,
  });

  // Fetch audit entries
  const auditQuery = useQuery({
    queryKey: ['audit', orgId, dateRange, statusFilter, agentFilter],
    queryFn: () => listAuditEntries({
      orgId: orgId!,
      from: dateRange?.[0],
      to: dateRange?.[1],
      status: statusFilter,
      agentId: agentFilter,
    }),
    enabled: !!orgId,
  });

  const handleExport = useCallback(async () => {
    if (!orgId) {
      message.warning(t('audit.noOrgTitle'));
      return;
    }
    try {
      const blob = await exportAuditCsv(orgId, {
        from: dateRange?.[0],
        to: dateRange?.[1],
        status: statusFilter,
        agentId: agentFilter,
      });
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `audit_${orgId}_${dayjs().format('YYYYMMDD_HHmmss')}.csv`;
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
      document.body.removeChild(a);
      message.success(t('common.success'));
    } catch {
      message.error(t('common.error'));
    }
  }, [orgId]);

  const handleDateChange = useCallback((_dates: unknown, dateStrings: [string, string]) => {
    if (dateStrings[0] && dateStrings[1]) {
      setDateRange([dayjs(dateStrings[0]).toISOString(), dayjs(dateStrings[1]).toISOString()]);
    } else {
      setDateRange(null);
    }
  }, []);

  const clearFilters = useCallback(() => {
    setDateRange(null);
    setStatusFilter(undefined);
    setAgentFilter(undefined);
  }, []);

  // Table columns
  const columns: ColumnsType<AuditEntry> = [
    {
      title: 'Operation ID',
      dataIndex: 'operationId',
      key: 'operationId',
      width: 120,
      ellipsis: true,
      render: (id: string) => (
        <Text code style={{ fontSize: 11 }}>{id.substring(0, 8)}...</Text>
      ),
    },
    {
      title: 'Operator',
      dataIndex: 'operator',
      key: 'operator',
      width: 160,
    },
    {
      title: 'Timestamp',
      dataIndex: 'timestamp',
      key: 'timestamp',
      width: 180,
      render: (ts: string) => ts ? dayjs(ts).format('YYYY-MM-DD HH:mm:ss') : '-',
      defaultSortOrder: 'descend',
      sorter: (a: AuditEntry, b: AuditEntry) =>
        dayjs(a.timestamp).unix() - dayjs(b.timestamp).unix(),
    },
    {
      title: 'Target Table',
      dataIndex: 'targetTable',
      key: 'targetTable',
      width: 180,
    },
    {
      title: 'Rows Affected',
      dataIndex: 'rowsAffected',
      key: 'rowsAffected',
      width: 120,
      render: (val: number) => val?.toLocaleString() || '0',
    },
    {
      title: 'Status',
      dataIndex: 'status',
      key: 'status',
      width: 120,
      render: (status: string) => (
        <Tag color={statusColors[status] || 'default'}>{status}</Tag>
      ),
    },
  ];

  // ---- Loading/Error/Empty states ----

  if (orgsQuery.isLoading) {
    return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip={t('audit.loadingOrg')} /></div>;
  }

  if (orgsQuery.error || !orgId) {
    return (
      <Card>
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Alert
            type="error"
            message={t('audit.noOrgTitle')}
            description={t('audit.noOrgDesc')}
            showIcon
          />
          <br />
          <Button type="primary" onClick={() => navigate('/org')}>{t('audit.goToOrg')}</Button>
        </div>
      </Card>
    );
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0 }}>{t('audit.title')}</Title>
        <Space>
          <Button icon={<DownloadOutlined />} onClick={handleExport}>
            Export CSV
          </Button>
        </Space>
      </div>

      <Card style={{ marginBottom: 16 }}>
        <Space wrap>
          <div>
            <Text strong style={{ marginRight: 8 }}>{t('audit.dateRange')}</Text>
            <RangePicker
              onChange={handleDateChange}
              value={dateRange ? [dayjs(dateRange[0]), dayjs(dateRange[1])] : null}
            />
          </div>
          <div>
            <Text strong style={{ marginRight: 8 }}>{t('audit.status')}</Text>
            <Select
              placeholder={t('audit.allStatuses')}
              allowClear
              style={{ width: 140 }}
              value={statusFilter}
              onChange={setStatusFilter}
            >
              <Select.Option value="completed">{t('audit.completed')}</Select.Option>
              <Select.Option value="failed">{t('audit.failed')}</Select.Option>
              <Select.Option value="cancelled">{t('audit.cancelled')}</Select.Option>
              <Select.Option value="previewed">{t('audit.previewed')}</Select.Option>
            </Select>
          </div>
          <div>
            <Text strong style={{ marginRight: 8 }}>{t('audit.agentId')}</Text>
            <Select
              placeholder={t('audit.allAgents')}
              allowClear
              style={{ width: 180 }}
              value={agentFilter}
              onChange={setAgentFilter}
              loading={agentsQuery.isLoading}
            >
              {(agentsQuery.data ?? []).map((agent: AgentInfo) => (
                <Select.Option key={agent.id} value={agent.id}>
                  {agent.hostname}
                </Select.Option>
              ))}
            </Select>
          </div>
          <Button icon={<ReloadOutlined />} onClick={() => auditQuery.refetch()}>
            Refresh
          </Button>
          {(dateRange || statusFilter || agentFilter) && (
            <Button onClick={clearFilters}>{t('audit.clearFilters')}</Button>
          )}
        </Space>
      </Card>

      {auditQuery.isLoading ? (
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Spin size="large" tip={t('audit.loadingAudit')} />
        </div>
      ) : auditQuery.error ? (
        <Card>
          <Alert
            type="error"
            message={t('audit.loadFailed')}
            action={<Button size="small" onClick={() => auditQuery.refetch()}>{t('common.retry')}</Button>}
            showIcon
          />
        </Card>
      ) : (auditQuery.data ?? []).length === 0 ? (
        <Card>
          <Empty description={t('audit.noEntries')}>
            <Text type="secondary">
              {dateRange || statusFilter || agentFilter
                ? t('audit.noEntriesFiltered')
                : t('audit.noEntriesDefault')}
            </Text>
            <br /><br />
            <Space>
              {(dateRange || statusFilter || agentFilter) && (
                <Button onClick={clearFilters}>{t('audit.clearFilters')}</Button>
              )}
              <Button type="primary" onClick={() => navigate('/pitr/new')}>
                Start New Recovery
              </Button>
            </Space>
          </Empty>
        </Card>
      ) : (
        <Table
          dataSource={auditQuery.data}
          columns={columns}
          rowKey="operationId"
          pagination={{ pageSize: 20, showSizeChanger: true, showTotal: (total) => t('audit.totalEntries').replace('{total}', total) }}
          onRow={(record: AuditEntry) => ({
            onClick: () => navigate(`/audit/${record.operationId}`),
            style: { cursor: 'pointer' },
          })}
          scroll={{ x: 900 }}
        />
      )}
    </div>
  );
}
