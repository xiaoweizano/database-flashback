import { useParams, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  Card, Descriptions, Badge, Button, Spin, Typography, Space, Tag,
} from 'antd';
import { ArrowLeftOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { getPITRStatus } from '../../api/pitr';

const { Title, Text } = Typography;

const stateColors: Record<string, 'processing' | 'success' | 'error' | 'default'> = {
  preflight: 'processing',
  confirmed: 'processing',
  parsing: 'processing',
  previewed: 'processing',
  executing: 'processing',
  completed: 'success',
  failed: 'error',
  cancelled: 'default',
};

export default function PITRDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  const { data: operation, isLoading, error } = useQuery({
    queryKey: ['pitr-status', id],
    queryFn: () => getPITRStatus(id!),
    enabled: !!id,
    refetchInterval: (query) => {
      const state = query.state.data?.state;
      if (state === 'executing' || state === 'preflight' || state === 'confirmed' || state === 'parsing' || state === 'previewed') {
        return 3000;
      }
      return false;
    },
  });

  if (isLoading) {
    return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip="Loading operation..." /></div>;
  }

  if (error || !operation) {
    return (
      <Card>
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Typography.Text type="danger">Failed to load operation details.</Typography.Text>
          <br /><br />
          <Space>
            <Button onClick={() => navigate(-1)}>Go Back</Button>
            <Button onClick={() => navigate('/audit')}>Audit Log</Button>
          </Space>
        </div>
      </Card>
    );
  }

  const stateColor = stateColors[operation.state] || 'default';

  return (
    <div>
      <Space style={{ marginBottom: 16 }}>
        <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/audit')}>
          Back to Audit
        </Button>
      </Space>

      <Card>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 24 }}>
          <div>
            <Title level={4} style={{ margin: 0 }}>PITR Operation Detail</Title>
            <Text type="secondary">ID: {operation.id}</Text>
          </div>
          <Badge status={stateColor} text={operation.state} />
        </div>

        <Descriptions bordered column={1}>
          <Descriptions.Item label="Operation ID">{operation.id}</Descriptions.Item>
          <Descriptions.Item label="Agent ID">{operation.agentId}</Descriptions.Item>
          <Descriptions.Item label="Target Table">{operation.targetTable}</Descriptions.Item>
          <Descriptions.Item label="Recovery Time">
            {dayjs(operation.recoveryTime).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
          <Descriptions.Item label="Mode">{operation.mode}</Descriptions.Item>
          <Descriptions.Item label="State">
            <Tag color={stateColor}>{operation.state}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="Created At">
            {dayjs(operation.createdAt).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
          <Descriptions.Item label="Updated At">
            {dayjs(operation.updatedAt).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
        </Descriptions>

        {operation.preflightResult && (
          <Card size="small" title="Preflight Results" style={{ marginTop: 16 }}>
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label="Binlog Files">
                {operation.preflightResult.binlogFiles?.join(', ') || '-'}
              </Descriptions.Item>
              <Descriptions.Item label="Earliest Time">
                {operation.preflightResult.earliestTime
                  ? dayjs(operation.preflightResult.earliestTime).format('YYYY-MM-DD HH:mm:ss')
                  : '-'}
              </Descriptions.Item>
              <Descriptions.Item label="Estimated Size">
                {operation.preflightResult.estimatedSize
                  ? `${(operation.preflightResult.estimatedSize / 1024 / 1024).toFixed(1)} MB`
                  : '-'}
              </Descriptions.Item>
            </Descriptions>
          </Card>
        )}

        {operation.parseResult && (
          <Card size="small" title="Parse Results" style={{ marginTop: 16 }}>
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label="Rows Affected">
                {operation.parseResult.rowsAffected?.toLocaleString() || '0'}
              </Descriptions.Item>
              <Descriptions.Item label="SQL Sample">
                <pre style={{
                  background: '#f5f5f5',
                  padding: 8,
                  borderRadius: 4,
                  fontSize: 12,
                  margin: 0,
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-all',
                }}>
                  {operation.parseResult.sqlSample || '-'}
                </pre>
              </Descriptions.Item>
            </Descriptions>
          </Card>
        )}

        {operation.execResult && (
          <Card size="small" title="Execution Results" style={{ marginTop: 16 }}>
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label="Rows Restored">
                {operation.execResult.rowsRestored?.toLocaleString() || '0'}
              </Descriptions.Item>
              <Descriptions.Item label="Duration">
                {operation.execResult.duration || '-'}
              </Descriptions.Item>
              <Descriptions.Item label="Executed At">
                {operation.execResult.executedAt
                  ? dayjs(operation.execResult.executedAt).format('YYYY-MM-DD HH:mm:ss')
                  : '-'}
              </Descriptions.Item>
            </Descriptions>
          </Card>
        )}

        {operation.error && (
          <Card size="small" title="Error" style={{ marginTop: 16 }}>
            <Text type="danger">{operation.error}</Text>
          </Card>
        )}

        {operation.progress && (
          <Card size="small" title="Progress" style={{ marginTop: 16 }}>
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label="Batches">
                {operation.progress.batchesComplete} / {operation.progress.batchesTotal}
              </Descriptions.Item>
              <Descriptions.Item label="Rows Restored">
                {operation.progress.rowsRestored?.toLocaleString() || '0'}
              </Descriptions.Item>
              <Descriptions.Item label="Estimated Remaining">
                {operation.progress.estimatedRemaining || '-'}
              </Descriptions.Item>
            </Descriptions>
          </Card>
        )}
      </Card>
    </div>
  );
}
