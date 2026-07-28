import { useState, useMemo, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useMutation } from '@tanstack/react-query';
import {
  Steps, Card, Form, Select, Input, DatePicker, Button, Typography,
  Spin, Empty, Alert, Progress, Descriptions, Space, Tag, message, notification,
} from 'antd';
import {
  ArrowLeftOutlined, ArrowRightOutlined, CloseCircleOutlined,
  CheckCircleOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { useLocale } from '../../hooks/useLocale';
import { listAgents } from '../../api/agents';
import { listOrgs } from '../../api/org';
import { startPITR, getPITRStatus, getPITRProgress, cancelPITR } from '../../api/pitr';
import type { AgentInfo, PITROperation, ProgressData } from '../../types';

const { Title, Text } = Typography;
const { Option } = Select;

const stateColors: Record<string, string> = {
  preflight: 'processing',
  confirmed: 'processing',
  parsing: 'processing',
  previewed: 'processing',
  executing: 'processing',
  completed: 'success',
  failed: 'error',
  cancelled: 'default',
};

function getStateTag(state: string) {
  const color = stateColors[state] || 'default';
  return <Tag color={color}>{state}</Tag>;
}

export default function PITRWizardPage() {
  const navigate = useNavigate();
  const { t } = useLocale();
  const stepTitles = [
    t('pitr.selectAgent'),
    t('pitr.targetTable'),
    t('pitr.preflightCheck'),
    t('pitr.previewChanges'),
    t('pitr.execute'),
  ];
  const [currentStep, setCurrentStep] = useState(0);
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(null);
  const [selectedAgentHostname, setSelectedAgentHostname] = useState<string | null>(null);
  const [targetTable, setTargetTable] = useState('');
  const [recoveryTime, setRecoveryTime] = useState('');
  const [mysqlDsn, setMysqlDsn] = useState('');
  const [operationId, setOperationId] = useState<string | null>(null);

  // Fetch agents list
  const orgsQuery = useQuery({
    queryKey: ['orgs'],
    queryFn: listOrgs,
  });
  const orgId = orgsQuery.data?.[0]?.id;

  const agentsQuery = useQuery({
    queryKey: ['agents', orgId],
    queryFn: () => listAgents(orgId),
    enabled: !!orgId,
  });

  const availableAgents = useMemo(
    () => agentsQuery.data ?? [],
    [agentsQuery.data],
  );

  // Fetch operation status (polling for steps 2-4)
  const statusQuery = useQuery({
    queryKey: ['pitr-status', operationId],
    queryFn: () => getPITRStatus(operationId!),
    enabled: !!operationId && currentStep >= 2 && currentStep <= 4,
    refetchInterval: currentStep >= 2 && currentStep <= 4 ? 1500 : false,
  });

  const operation: PITROperation | undefined = statusQuery.data;

  // Fetch progress (step 4 only)
  const progressQuery = useQuery({
    queryKey: ['pitr-progress', operationId],
    queryFn: () => getPITRProgress(operationId!),
    enabled: !!operationId && currentStep === 4,
    refetchInterval: currentStep === 4 ? 2000 : false,
  });

  const progress: ProgressData | undefined = progressQuery.data;

  // Start operation mutation
  const startMutation = useMutation({
    mutationFn: () => startPITR({
      agent_id: selectedAgentId!,
      target_table: targetTable,
      recovery_time: dayjs(recoveryTime).toISOString(),
      mode: 'execute',
      mysql_dsn: mysqlDsn,
    }),
    onSuccess: (data) => {
      setOperationId(data.operationId);
      setCurrentStep(2);
      notification.success({ message: t('pitr.startRecovery'), description: `ID: ${data.operationId}` });
    },
    onError: (err: Error) => {
      notification.error({ message: t('common.error'), description: err.message });
    },
  });

  // Cancel operation mutation
  const cancelMutation = useMutation({
    mutationFn: () => cancelPITR(operationId!),
    onSuccess: () => {
      notification.success({ message: t('pitr.operationCancelled') });
      navigate('/pitr/new');
    },
    onError: (err: Error) => {
      notification.error({ message: t('common.error'), description: err.message });
    },
  });

  const handleCancel = useCallback(() => {
    if (operationId) {
      cancelMutation.mutate();
    } else {
      navigate('/pitr/new');
    }
  }, [operationId, cancelMutation, navigate]);

  const handleBack = useCallback(() => {
    if (currentStep > 0) {
      // If going back from executing steps, cancel the operation
      if (currentStep >= 2 && operationId) {
        cancelMutation.mutate(undefined, {
          onSuccess: () => {
            setOperationId(null);
            setCurrentStep(currentStep - 1);
          },
        });
        return;
      }
      setCurrentStep(currentStep - 1);
    }
  }, [currentStep, operationId, cancelMutation]);

  const handleNextFromStep1 = useCallback(() => {
    if (!selectedAgentId || !targetTable || !recoveryTime || !mysqlDsn) {
      message.warning(t('common.error'));
      return;
    }
    startMutation.mutate();
  }, [selectedAgentId, targetTable, recoveryTime, mysqlDsn, startMutation]);

  // Compute progress bar percent
  const progressPercent = useMemo(() => {
    if (!progress) return 0;
    if (progress.batchesTotal <= 0) return 0;
    return Math.round((progress.batchesComplete / progress.batchesTotal) * 100);
  }, [progress]);

  const isCompleted = operation?.state === 'completed';
  const isFailed = operation?.state === 'failed' || operation?.state === 'cancelled';

  // ---- Step Renderers ----

  const renderStep0 = () => {
    if (agentsQuery.isLoading) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip={t('pitr.loadingAgents')} /></div>;
    }
    if (agentsQuery.error) {
      return (
        <Alert
          type="error"
          message={t('pitr.loadAgentsFailed')}
          description={t('pitr.loadAgentsDesc')}
          action={<Button size="small" danger onClick={() => agentsQuery.refetch()}>{t('common.retry')}</Button>}
          showIcon
        />
      );
    }
    if (availableAgents.length === 0) {
      return (
        <Empty description={t('pitr.noAgents')}>
          <Text type="secondary">{t('pitr.noAgentsDesc')}</Text>
          <br /><br />
          <Button type="primary" onClick={() => navigate('/agents')}>{t('pitr.goToAgents')}</Button>
        </Empty>
      );
    }

    return (
      <Form layout="vertical">
        <Form.Item label={t('pitr.selectAgent')} required>
          <Select
            placeholder={t('pitr.selectAgentPlaceholder')}
            style={{ width: '100%' }}
            value={selectedAgentId}
            onChange={(value) => {
              const agent = availableAgents.find((a: AgentInfo) => a.id === value);
              if (agent) {
                setSelectedAgentId(agent.id);
                setSelectedAgentHostname(agent.hostname);
              }
            }}
          >
            {availableAgents.map((agent: AgentInfo) => (
              <Option key={agent.id} value={agent.id}>
                {agent.hostname} - MySQL {agent.mySQLVersion || 'N/A'}
              </Option>
            ))}
          </Select>
        </Form.Item>
        {selectedAgentId && (
          <Card size="small" title={t('pitr.agentDetails')} style={{ marginTop: 16 }}>
            <Descriptions column={1} size="small">
              <Descriptions.Item label={t('pitr.hostname')}>{selectedAgentHostname}</Descriptions.Item>
              <Descriptions.Item label={t('pitr.status')}>
                <Tag color={availableAgents.find((a: AgentInfo) => a.id === selectedAgentId)?.status === 'online' ? 'green' : 'default'}>
                  {availableAgents.find((a: AgentInfo) => a.id === selectedAgentId)?.status || '-'}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label={t('pitr.mysqlVersion')}>
                {availableAgents.find((a: AgentInfo) => a.id === selectedAgentId)?.mySQLVersion || '-'}
              </Descriptions.Item>
            </Descriptions>
          </Card>
        )}
      </Form>
    );
  };

  const renderStep1 = () => (
    <Form layout="vertical">
      <Form.Item label={t('pitr.targetTable')} required help={t('pitr.targetTableHelp')}>
        <Input
          placeholder={t('pitr.targetTablePlaceholder')}
          value={targetTable}
          onChange={(e) => setTargetTable(e.target.value)}
        />
      </Form.Item>
      <Form.Item label={t('pitr.recoveryTime')} required>
        <DatePicker
          showTime
          style={{ width: '100%' }}
          value={recoveryTime ? dayjs(recoveryTime) : null}
          onChange={(date) => setRecoveryTime(date ? date.toISOString() : '')}
        />
      </Form.Item>
      <Form.Item label={t('pitr.mysqlDsn')} required help={t('pitr.mysqlDsnHelp')}>
        <Input.Password
          placeholder={t('pitr.mysqlDsnPlaceholder')}
          value={mysqlDsn}
          onChange={(e) => setMysqlDsn(e.target.value)}
        />
      </Form.Item>
    </Form>
  );

  const renderStep2 = () => {
    if (statusQuery.isLoading) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip={t('pitr.runningPreflight')} /></div>;
    }
    if (statusQuery.error) {
      return (
        <Alert
          type="error"
          message={t('pitr.fetchStatusFailed')}
          action={<Button size="small" onClick={() => statusQuery.refetch()}>{t('common.retry')}</Button>}
          showIcon
        />
      );
    }

    const op = statusQuery.data;
    if (!op) {
      return <Empty description={t('common.noData')} />;
    }

    if (op.state === 'preflight') {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip={t('pitr.runningPreflight')} /></div>;
    }

    const preflight = op.preflightResult;
    if (!preflight) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip={t('pitr.runningPreflight')} /></div>;
    }

    return (
      <div>
        <Alert
          type="info"
          message={t('pitr.preflightCompleted')}
          description={t('pitr.preflightDesc')}
          showIcon
          style={{ marginBottom: 16 }}
        />
        <Card size="small" title={t('pitr.binlogConfig')}>
          <Descriptions column={1} size="small">
            <Descriptions.Item label={t('pitr.binlogFiles')}>
              {preflight.binlogFiles?.join(', ') || '-'}
            </Descriptions.Item>
            <Descriptions.Item label={t('pitr.earliestTime')}>
              {preflight.earliestTime ? dayjs(preflight.earliestTime).format('YYYY-MM-DD HH:mm:ss') : '-'}
            </Descriptions.Item>
            <Descriptions.Item label={t('pitr.estimatedSize')}>
              {preflight.estimatedSize ? `${(preflight.estimatedSize / 1024 / 1024).toFixed(1)} MB` : '-'}
            </Descriptions.Item>
            <Descriptions.Item label={t('pitr.checkedAt')}>
              {preflight.checkedAt ? dayjs(preflight.checkedAt).format('YYYY-MM-DD HH:mm:ss') : '-'}
            </Descriptions.Item>
          </Descriptions>
        </Card>
      </div>
    );
  };

  const renderStep3 = () => {
    if (statusQuery.isLoading) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip={t('pitr.loadingPreview')} /></div>;
    }
    if (statusQuery.error) {
      return (
        <Alert
          type="error"
          message={t('pitr.loadPreviewFailed')}
          action={<Button size="small" onClick={() => statusQuery.refetch()}>{t('common.retry')}</Button>}
          showIcon
        />
      );
    }

    const op = statusQuery.data;
    if (!op) {
      return <Empty description={t('common.noData')} />;
    }

    const parseRes = op.parseResult;
    if (!parseRes) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip={t('pitr.loadingPreview')} /></div>;
    }

    return (
      <div>
        <Alert
          type="success"
          message={t('pitr.parseCompleted')}
          description={t('pitr.parseDesc')}
          showIcon
          style={{ marginBottom: 16 }}
        />
        <Card size="small" title={t('pitr.estimatedChanges')}>
          <Descriptions column={1} size="small">
            <Descriptions.Item label={t('pitr.rowsAffected')}>
              <Text strong>{parseRes.rowsAffected?.toLocaleString() || '0'}</Text>
            </Descriptions.Item>
            <Descriptions.Item label={t('pitr.recoveryTime')}>
              {dayjs(op.recoveryTime).format('YYYY-MM-DD HH:mm:ss')}
            </Descriptions.Item>
            <Descriptions.Item label={t('pitr.targetTable')}>{op.targetTable}</Descriptions.Item>
          </Descriptions>
        </Card>
        {parseRes.sqlSample && (
          <Card size="small" title={t('pitr.sampleSql')} style={{ marginTop: 16 }}>
            <pre style={{
              background: '#f5f5f5',
              padding: 12,
              borderRadius: 4,
              fontSize: 12,
              overflowX: 'auto',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
            }}>
              {parseRes.sqlSample}
            </pre>
          </Card>
        )}
      </div>
    );
  };

  const renderStep4 = () => {
    if (isCompleted) {
      return (
        <div style={{ textAlign: 'center', padding: 24 }}>
          <CheckCircleOutlined style={{ fontSize: 64, color: '#52c41a' }} />
          <Title level={4} style={{ marginTop: 16 }}>{t('pitr.recoveryCompleted')}</Title>
          {operation?.execResult && (
            <Card size="small" style={{ maxWidth: 400, margin: '16px auto' }}>
              <Descriptions column={1} size="small">
                <Descriptions.Item label={t('pitr.rowsRestored')}>
                  <Text strong>{operation.execResult.rowsRestored?.toLocaleString()}</Text>
                </Descriptions.Item>
                <Descriptions.Item label={t('pitr.duration')}>{operation.execResult.duration || '-'}</Descriptions.Item>
              </Descriptions>
            </Card>
          )}
        </div>
      );
    }

    if (isFailed) {
      return (
        <div style={{ textAlign: 'center', padding: 24 }}>
          <CloseCircleOutlined style={{ fontSize: 64, color: '#ff4d4f' }} />
          <Title level={4} style={{ marginTop: 16 }}>{
            operation?.state === 'cancelled'
              ? t('pitr.operationCancelled')
              : t('pitr.operationFailed')
          }</Title>
          {operation?.error && (
            <Alert type="error" message={operation.error} showIcon style={{ maxWidth: 400, margin: '16px auto' }} />
          )}
        </div>
      );
    }

    // Still executing - show progress
    if (progressQuery.isLoading && !progress) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip={t('pitr.startingExecution')} /></div>;
    }

    return (
      <div style={{ padding: 24 }}>
        <Alert
          type="info"
          message={t('pitr.executingRecovery')}
          description={t('pitr.executingDesc')}
          showIcon
          style={{ marginBottom: 24 }}
        />
        <Card>
          <div style={{ textAlign: 'center', marginBottom: 16 }}>
            <Text type="secondary">{t('pitr.batchProgress')}</Text>
          </div>
          <Progress
            type="circle"
            percent={progressPercent}
            status={isFailed ? 'exception' : 'active'}
            size={200}
            style={{ display: 'block', margin: '0 auto 24px' }}
          />
          <Descriptions column={2} size="small" style={{ maxWidth: 400, margin: '0 auto' }}>
            <Descriptions.Item label={t('pitr.batches')}>{progress?.batchesComplete ?? 0} / {progress?.batchesTotal ?? '-'}</Descriptions.Item>
            <Descriptions.Item label={t('pitr.rowsRestored')}>{progress?.rowsRestored?.toLocaleString() ?? '0'}</Descriptions.Item>
            <Descriptions.Item label={t('pitr.estRemaining')}>{progress?.estimatedRemaining || t('pitr.calculating')}</Descriptions.Item>
            <Descriptions.Item label={t('pitr.status')}>{getStateTag(operation?.state || 'executing')}</Descriptions.Item>
          </Descriptions>
        </Card>
      </div>
    );
  };

  // ---- Main Render ----

  const stepContent = [renderStep0, renderStep1, renderStep2, renderStep3, renderStep4];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={3} style={{ margin: 0 }}>{t('pitr.title')}</Title>
      </div>

      <Card>
        <Steps
          current={currentStep}
          items={stepTitles.map((title) => ({ title }))}
          style={{ marginBottom: 32 }}
        />

        <div style={{ minHeight: 300 }}>
          {stepContent[currentStep]()}
        </div>

        <div style={{ marginTop: 24, display: 'flex', justifyContent: 'space-between' }}>
          <Space>
            {currentStep > 0 && (
              <Button icon={<ArrowLeftOutlined />} onClick={handleBack} disabled={startMutation.isPending || cancelMutation.isPending}>
                {t('pitr.stepBack')}
              </Button>
            )}
          </Space>
          <Space>
            <Button icon={<CloseCircleOutlined />} onClick={handleCancel} disabled={cancelMutation.isPending}>
              {t('common.cancel')}
            </Button>
            {currentStep === 0 && (
              <Button
                type="primary"
                icon={<ArrowRightOutlined />}
                onClick={() => {
                  if (!selectedAgentId) {
                    message.warning(t('pitr.selectAgent'));
                    return;
                  }
                  setCurrentStep(1);
                }}
              >
                {t('pitr.stepNext')}
              </Button>
            )}
            {currentStep === 1 && (
              <Button
                type="primary"
                loading={startMutation.isPending}
                icon={<ArrowRightOutlined />}
                onClick={handleNextFromStep1}
              >
                {t('pitr.startRecovery')}
              </Button>
            )}
            {currentStep === 2 && (
              <Button
                type="primary"
                icon={<ArrowRightOutlined />}
                onClick={() => setCurrentStep(3)}
              >
                {t('pitr.continuePreview')}
              </Button>
            )}
            {currentStep === 3 && (
              <Button
                type="primary"
                icon={<ArrowRightOutlined />}
                onClick={() => setCurrentStep(4)}
              >
                {t('pitr.executeRecovery')}
              </Button>
            )}
            {(isCompleted || isFailed) && (
              <Button type="primary" onClick={() => navigate('/audit')}>
                {t('pitr.viewAuditLog')}
              </Button>
            )}
          </Space>
        </div>
      </Card>
    </div>
  );
}
