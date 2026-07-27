import { useParams, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Card, Descriptions, Badge, Button, Spin, Typography, Collapse, Tag, Space } from 'antd';
import { ArrowLeftOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { getAuditEntry } from '../../api/audit';
import { listOrgs } from '../../api/org';

const { Title, Text } = Typography;

const statusColors: Record<string, string> = {
  completed: 'success',
  failed: 'error',
  cancelled: 'default',
  previewed: 'processing',
};

export default function AuditDetailPage() {
  const { operationId } = useParams<{ operationId: string }>();
  const navigate = useNavigate();

  const orgsQuery = useQuery({
    queryKey: ['orgs'],
    queryFn: listOrgs,
  });

  const orgId = orgsQuery.data?.[0]?.id;

  const { data: entry, isLoading, error } = useQuery({
    queryKey: ['audit-entry', operationId],
    queryFn: () => getAuditEntry(orgId!, operationId!),
    enabled: !!orgId && !!operationId,
  });

  if (isLoading || orgsQuery.isLoading) {
    return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" /></div>;
  }

  if (error || !entry) {
    return (
      <Card>
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Typography.Text type="danger">Failed to load audit entry.</Typography.Text>
          <br /><br />
          <Button onClick={() => navigate('/audit')}>Back to Audit Log</Button>
        </div>
      </Card>
    );
  }

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/audit')}>
          Back
        </Button>
      </Space>

      <Card>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 24 }}>
          <div>
            <Title level={4} style={{ margin: 0 }}>Audit Entry</Title>
            <Text code style={{ fontSize: 12 }}>{entry.operationId}</Text>
          </div>
          <Tag color={statusColors[entry.status] || 'default'}>{entry.status}</Tag>
        </div>

        <Descriptions bordered column={1}>
          <Descriptions.Item label="Operation ID">
            <Text copyable>{entry.operationId}</Text>
          </Descriptions.Item>
          <Descriptions.Item label="Operator">{entry.operator}</Descriptions.Item>
          <Descriptions.Item label="Timestamp">
            {dayjs(entry.timestamp).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
          <Descriptions.Item label="Organization ID">
            <Text copyable>{entry.orgId}</Text>
          </Descriptions.Item>
          <Descriptions.Item label="Agent ID">
            <Text copyable>{entry.agentId}</Text>
          </Descriptions.Item>
          <Descriptions.Item label="Target Table">{entry.targetTable}</Descriptions.Item>
          <Descriptions.Item label="Recovery Time">
            {dayjs(entry.recoveryTime).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
          <Descriptions.Item label="Rows Affected">
            {entry.rowsAffected?.toLocaleString() || '0'}
          </Descriptions.Item>
          <Descriptions.Item label="Status">
            <Badge status={statusColors[entry.status] as 'success' | 'error' | 'default' | 'processing'} text={entry.status} />
          </Descriptions.Item>
        </Descriptions>

        {entry.errorDetails && (
          <div style={{ marginTop: 24 }}>
            <Collapse
              items={[{
                key: 'error-details',
                label: 'Error Details',
                children: (
                  <pre style={{
                    background: '#fff2f0',
                    border: '1px solid #ffccc7',
                    borderRadius: 6,
                    padding: 12,
                    fontSize: 13,
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-word',
                    margin: 0,
                  }}>
                    {entry.errorDetails}
                  </pre>
                ),
              }]}
            />
          </div>
        )}
      </Card>
    </div>
  );
}
