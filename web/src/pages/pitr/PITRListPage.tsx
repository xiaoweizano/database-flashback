import { useState, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  Card, Table, Button, Typography, Spin, Empty, Tag,
  DatePicker, Space,
} from 'antd';
import { HistoryOutlined, ReloadOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { listOrgs } from '../../api/org';
import { listPITROperations } from '../../api/pitr';
import type { PITROperation } from '../../types';
import type { ColumnsType } from 'antd/es/table';

const { Title, Text } = Typography;
const { RangePicker } = DatePicker;

const stateColors: Record<string, string> = {
  completed: 'success',
  failed: 'error',
  cancelled: 'default',
  previewed: 'processing',
  preflight: 'processing',
  confirmed: 'processing',
  parsing: 'processing',
  executing: 'processing',
};

export default function PITRListPage() {
  const navigate = useNavigate();
  const [dateRange, setDateRange] = useState<[string, string] | null>(null);

  const orgsQuery = useQuery({
    queryKey: ['orgs'],
    queryFn: listOrgs,
  });

  const orgId = orgsQuery.data?.[0]?.id;

  const pitrQuery = useQuery({
    queryKey: ['pitr-list', orgId, dateRange],
    queryFn: () => listPITROperations(
      orgId!,
      dateRange?.[0],
      dateRange?.[1],
    ),
    enabled: !!orgId,
  });

  const handleDateChange = useCallback((_dates: unknown, dateStrings: [string, string]) => {
    if (dateStrings[0] && dateStrings[1]) {
      setDateRange([dayjs(dateStrings[0]).toISOString(), dayjs(dateStrings[1]).toISOString()]);
    } else {
      setDateRange(null);
    }
  }, []);

  const columns: ColumnsType<PITROperation> = [
    {
      title: 'Operation ID',
      dataIndex: 'id',
      key: 'id',
      width: 120,
      ellipsis: true,
      render: (id: string) => (
        <Text code style={{ fontSize: 11 }}>{id.substring(0, 8)}...</Text>
      ),
    },
    {
      title: 'Agent ID',
      dataIndex: 'agentId',
      key: 'agentId',
      width: 120,
      ellipsis: true,
      render: (id: string) => (
        <Text code style={{ fontSize: 11 }}>{id.substring(0, 8)}...</Text>
      ),
    },
    {
      title: 'Target Table',
      dataIndex: 'targetTable',
      key: 'targetTable',
      width: 160,
    },
    {
      title: 'Recovery Time',
      dataIndex: 'recoveryTime',
      key: 'recoveryTime',
      width: 180,
      render: (ts: string) => ts ? dayjs(ts).format('YYYY-MM-DD HH:mm:ss') : '-',
    },
    {
      title: 'State',
      dataIndex: 'state',
      key: 'state',
      width: 120,
      render: (state: string) => (
        <Tag color={stateColors[state] || 'default'}>{state}</Tag>
      ),
    },
    {
      title: 'Created',
      dataIndex: 'createdAt',
      key: 'createdAt',
      width: 180,
      render: (ts: string) => dayjs(ts).format('YYYY-MM-DD HH:mm:ss'),
      defaultSortOrder: 'descend',
      sorter: (a: PITROperation, b: PITROperation) =>
        dayjs(a.createdAt).unix() - dayjs(b.createdAt).unix(),
    },
  ];

  if (orgsQuery.isLoading) {
    return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" /></div>;
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0 }}>PITR Operations</Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={() => pitrQuery.refetch()}>
            Refresh
          </Button>
          <Button type="primary" icon={<HistoryOutlined />} onClick={() => navigate('/pitr/new')}>
            New Recovery
          </Button>
        </Space>
      </div>

      <Card style={{ marginBottom: 16 }}>
        <Space wrap>
          <div>
            <Text strong style={{ marginRight: 8 }}>Date Range</Text>
            <RangePicker
              onChange={handleDateChange}
              value={dateRange ? [dayjs(dateRange[0]), dayjs(dateRange[1])] : null}
            />
          </div>
          {dateRange && (
            <Button onClick={() => setDateRange(null)}>Clear Filters</Button>
          )}
        </Space>
      </Card>

      {pitrQuery.isLoading ? (
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Spin size="large" />
        </div>
      ) : pitrQuery.error ? (
        <Card>
          <div style={{ textAlign: 'center', padding: 48 }}>
            <Text type="danger">Failed to load PITR operations.</Text>
            <br /><br />
            <Button onClick={() => pitrQuery.refetch()}>Retry</Button>
          </div>
        </Card>
      ) : (pitrQuery.data ?? []).length === 0 ? (
        <Card>
          <Empty
            description={
              dateRange
                ? 'No PITR operations found in this time range.'
                : 'No PITR operations yet.'
            }
          >
            <Button type="primary" onClick={() => navigate('/pitr/new')}>
              Start New Recovery
            </Button>
          </Empty>
        </Card>
      ) : (
        <Table
          dataSource={pitrQuery.data}
          columns={columns}
          rowKey="id"
          pagination={{ pageSize: 20, showSizeChanger: true }}
          onRow={(record: PITROperation) => ({
            onClick: () => navigate(`/pitr/${record.id}`),
            style: { cursor: 'pointer' },
          })}
          scroll={{ x: 900 }}
        />
      )}
    </div>
  );
}
