import { useParams, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  Card, Descriptions, Badge, Button, Spin, Typography, Space, Tag, Table,
} from 'antd';
import { ArrowLeftOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';
import 'dayjs/locale/zh-cn';
import { useLocale } from '../../hooks/useLocale';
import { getPITRStatus } from '../../api/pitr';

dayjs.extend(relativeTime);

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

function fmtTime(ts: string | undefined | null): string {
  if (!ts) return '-';
  const d = dayjs(ts);
  if (!d.isValid() || d.year() < 2000) return '-';
  const daysDiff = dayjs().diff(d, 'day');
  if (daysDiff < 7) return d.locale('zh-cn').fromNow();
  return d.format('YYYY年M月D日 HH:mm');
}

export default function PITRDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { t } = useLocale();

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
    return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip={t('pitr.loadingPreview')} /></div>;
  }

  if (error || !operation) {
    return (
      <Card>
        <div style={{ textAlign: 'center', padding: 48 }}>
          <Typography.Text type="danger">{t('common.error')}</Typography.Text>
          <br /><br />
          <Space>
            <Button onClick={() => navigate(-1)}>{t('pitr.stepBack')}</Button>
            <Button onClick={() => navigate('/audit')}>{t('audit.title')}</Button>
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
            <Title level={4} style={{ margin: 0 }}>{t('pitr.detail')}</Title>
            <Text type="secondary">ID: {operation.id}</Text>
          </div>
          <Badge status={stateColor} text={operation.state} />
        </div>

        <Descriptions bordered column={1}>
          <Descriptions.Item label={t('audit.operationId')}>{operation.id}</Descriptions.Item>
          <Descriptions.Item label={t('audit.agentId')}>{operation.agentId}</Descriptions.Item>
          <Descriptions.Item label={t('pitr.targetTable')}>{operation.targetTable}</Descriptions.Item>
          <Descriptions.Item label={t('pitr.recoveryTime')}>
            {dayjs(operation.recoveryTime).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
          <Descriptions.Item label={t('pitr.mode')}>{operation.mode}</Descriptions.Item>
          <Descriptions.Item label={t('pitr.state')}>
            <Tag color={stateColor}>{operation.state}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label={t('pitr.createdAt')}>
            {dayjs(operation.createdAt).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
          <Descriptions.Item label="更新时间">
            {dayjs(operation.updatedAt).format('YYYY-MM-DD HH:mm:ss')}
          </Descriptions.Item>
        </Descriptions>

        {operation.preflightResult && (
          <Card size="small" title={t('pitr.preflightCompleted')} style={{ marginTop: 16 }}>
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label={t('pitr.binlogFiles')}>
                {operation.preflightResult.binlogFiles?.join(', ') || '-'}
              </Descriptions.Item>
              <Descriptions.Item label={t('pitr.earliestTime')}>
                {operation.preflightResult.earliestTime
                  ? dayjs(operation.preflightResult.earliestTime).format('YYYY-MM-DD HH:mm:ss')
                  : '-'}
              </Descriptions.Item>
              <Descriptions.Item label={t('pitr.estimatedSize')}>
                {operation.preflightResult.estimatedSize
                  ? `${(operation.preflightResult.estimatedSize / 1024 / 1024).toFixed(1)} MB`
                  : '-'}
              </Descriptions.Item>
            </Descriptions>
          </Card>
        )}

        {operation.parseResult && (
          <Card size="small" title={t('pitr.estimatedChanges')} style={{ marginTop: 16 }}>
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label={t('pitr.rowsAffected')}>
                {operation.parseResult.rowsAffected?.toLocaleString() || '0'}
              </Descriptions.Item>
            </Descriptions>

            {operation.parseResult.reverseSql && operation.parseResult.reverseSql.length > 0 ? (
              <div style={{ marginTop: 16 }}>
                <Text strong style={{ marginBottom: 8, display: 'block' }}>{t('pitr.sampleSql')}</Text>
                <Table
                  dataSource={operation.parseResult.reverseSql}
                  rowKey="sequence"
                  size="small"
                  pagination={false}
                  columns={[
                    {
                      title: '#',
                      dataIndex: 'sequence',
                      key: 'sequence',
                      width: 40,
                    },
                    {
                      title: {t('pitr.mode')},
                      dataIndex: 'sqlType',
                      key: 'sqlType',
                      width: 80,
                      render: (type: string) => {
                        const color = type === 'INSERT' ? 'success' : type === 'UPDATE' ? 'processing' : 'error';
                        return <Tag color={color}>{type}</Tag>;
                      },
                    },
                    {
                      title: {t('pitr.targetTable')},
                      dataIndex: 'tableName',
                      key: 'tableName',
                      width: 120,
                    },
                    {
                      title: {t('pitr.sampleSql')},
                      dataIndex: 'reverseSql',
                      key: 'reverseSql',
                      ellipsis: true,
                      render: (sql: string) => (
                        <Text copyable style={{ fontSize: 12, fontFamily: 'monospace' }}>
                          {sql.length > 80 ? sql.substring(0, 80) + '...' : sql}
                        </Text>
                      ),
                    },
                    {
                      title: {t('pitr.rowsAffected')},
                      dataIndex: 'rowsAffected',
                      key: 'rowsAffected',
                      width: 60,
                    },
                  ]}
                />
              </div>
            ) : operation.parseResult.sqlSample ? (
              <div style={{ marginTop: 16 }}>
                <Text strong style={{ marginBottom: 8, display: 'block' }}>{t('pitr.sampleSql')}</Text>
                <pre style={{
                  background: '#f5f5f5',
                  padding: 8,
                  borderRadius: 4,
                  fontSize: 12,
                  margin: 0,
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-all',
                }}>
                  {operation.parseResult.sqlSample}
                </pre>
              </div>
            ) : (
              <div style={{ marginTop: 16 }}>
                <Text type="secondary">{t('common.noData')}</Text>
              </div>
            )}
          </Card>
        )}

        {operation.execResult && (
          <Card size="small" title={t('pitr.executingRecovery')} style={{ marginTop: 16 }}>
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label={t('pitr.rowsRestored')}>
                {operation.execResult.rowsRestored?.toLocaleString() || '0'}
              </Descriptions.Item>
              <Descriptions.Item label={t('pitr.duration')}>
                {operation.execResult.duration || '-'}
              </Descriptions.Item>
              <Descriptions.Item label={t('pitr.createdAt')}>
                {operation.execResult.executedAt
                  ? dayjs(operation.execResult.executedAt).format('YYYY-MM-DD HH:mm:ss')
                  : '-'}
              </Descriptions.Item>
            </Descriptions>
          </Card>
        )}

        {operation.error && (
          <Card size="small" title={t('common.error')} style={{ marginTop: 16 }}>
            <Text type="danger">{operation.error}</Text>
          </Card>
        )}

        {operation.progress && (
          <Card size="small" title={t('pitr.batchProgress')} style={{ marginTop: 16 }}>
            <Descriptions bordered column={1} size="small">
              <Descriptions.Item label={t('pitr.batches')}>
                {operation.progress.batchesComplete} / {operation.progress.batchesTotal}
              </Descriptions.Item>
              <Descriptions.Item label={t('pitr.rowsRestored')}>
                {operation.progress.rowsRestored?.toLocaleString() || '0'}
              </Descriptions.Item>
              <Descriptions.Item label={t('pitr.estRemaining')}>
                {operation.progress.estimatedRemaining || '-'}
              </Descriptions.Item>
            </Descriptions>
          </Card>
        )}
      </Card>
    </div>
  );
}
