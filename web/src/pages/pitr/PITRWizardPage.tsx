import { useState, useMemo, useCallback, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useMutation } from '@tanstack/react-query';
import {
  Steps, Card, Form, Select, Input, InputNumber, DatePicker, Button, Typography,
  Spin, Empty, Alert, Descriptions, Space, Tag, Checkbox, message, notification,
} from 'antd';
import {
  ArrowLeftOutlined, ArrowRightOutlined, CloseCircleOutlined,
  CheckCircleOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { useLocale } from '../../hooks/useLocale';
import { listAgents } from '../../api/agents';
import { listOrgs } from '../../api/org';
import { startPITR, getPITRStatus, cancelPITR, executePITR } from '../../api/pitr';
import type { AgentInfo, PITROperation } from '../../types';

const { Title, Text } = Typography;
const { Option } = Select;

export default function PITRWizardPage() {
  const navigate = useNavigate();
  const { t } = useLocale();
  const stepTitles = [
    t('pitr.selectAgent'),
    t('pitr.targetTable'),
    t('pitr.preflightCheck'),
    t('pitr.previewChanges'),
  ];
  const [currentStep, setCurrentStep] = useState(0);
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(null);
  const [selectedAgentHostname, setSelectedAgentHostname] = useState<string | null>(null);
  const [targetTable, setTargetTable] = useState('');
  const [recoveryTime, setRecoveryTime] = useState('');
  const [binlogFiles, setBinlogFiles] = useState('');
  const [startTime, setStartTime] = useState('');
  const [startPos, setStartPos] = useState<number | null>(null);
  const [stopPos, setStopPos] = useState<number | null>(null);
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
    () => (agentsQuery.data ?? []).filter((a: AgentInfo) => a.status === 'online' && a.approved),
    [agentsQuery.data],
  );

  // Fetch operation status (polling for steps 2-3; stops once the preview is
  // ready, resumes while the selected SQL is being executed, and stops again
  // at a terminal state)
  const statusQuery = useQuery({
    queryKey: ['pitr-status', operationId],
    queryFn: () => getPITRStatus(operationId!),
    enabled: !!operationId && currentStep >= 2 && currentStep <= 3,
    refetchInterval: (query) => {
      const state = query.state.data?.state;
      if (!operationId || currentStep < 2 || currentStep > 3) return false;
      if (state === 'completed' || state === 'failed' || state === 'cancelled') return false;
      if (state === 'previewed' && !executeMode) return false;
      return 1500;
    },
  });

  const operation: PITROperation | undefined = statusQuery.data;

  // Preview-only flow: the user reviews the generated SQL and selects which
  // statements to execute. reverseSql is the preview entries in execution
  // order (newest-first, LIFO).
  const reverseSql = useMemo(
    () => (operation?.parseResult?.reverseSql ?? []).slice().reverse(),
    [operation],
  );
  const [selected, setSelected] = useState<number[] | null>(null);
  const [executeMode, setExecuteMode] = useState(false);
  useEffect(() => {
    if (selected === null && reverseSql.length > 0) {
      setSelected(reverseSql.map((_, i) => i)); // default: all statements selected
    }
  }, [selected, reverseSql]);

  // Execute the user-selected statements (only then does the database change).
  const executeMutation = useMutation({
    mutationFn: () => executePITR(operationId!, (selected ?? []).map((i) => reverseSql[i].reverseSql)),
    onSuccess: () => {
      setExecuteMode(true);
      message.success(t('pitr.executeStarted'));
    },
    onError: (err: Error) => {
      notification.error({ message: t('common.error'), description: err.message });
    },
  });

  const handleExecute = () => {
    if (!selected || selected.length === 0) {
      message.warning(t('pitr.noSelection'));
      return;
    }
    executeMutation.mutate();
  };

  // Start operation mutation
  const startMutation = useMutation({
    mutationFn: () => startPITR({
      agent_id: selectedAgentId!,
      target_table: targetTable,
      recovery_time: dayjs(recoveryTime).toISOString(),
      // Preview-only: generate the reverse SQL from the binlog without touching
      // the database. The user decides which statements to run themselves.
      mode: 'preview',
      binlog_files: binlogFiles
        ? binlogFiles.split(',').map((s) => s.trim()).filter(Boolean)
        : undefined,
      start_time: startTime ? dayjs(startTime).toISOString() : undefined,
      start_pos: startPos ?? undefined,
      stop_pos: stopPos ?? undefined,
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
      // If going back from executing steps, cancel the operation — unless it
      // already reached a terminal state (failed/cancelled/completed), where
      // canceling would be rejected by the backend.
      if (currentStep >= 2 && operationId) {
        const state = operation?.state;
        if (state === 'failed' || state === 'cancelled' || state === 'completed') {
          setOperationId(null);
          setCurrentStep(currentStep - 1);
          return;
        }
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
  }, [currentStep, operationId, operation, cancelMutation]);

  const handleNextFromStep1 = useCallback(() => {
    if (!selectedAgentId || !targetTable || !recoveryTime) {
      message.warning(t('common.error'));
      return;
    }
    startMutation.mutate();
  }, [selectedAgentId, targetTable, recoveryTime, startMutation]);

  const isPreviewed = operation?.state === 'previewed';
  const isFailed = operation?.state === 'failed' || operation?.state === 'cancelled';
  const isTerminal = isPreviewed || isFailed;

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
    if ((agentsQuery.data ?? []).length === 0) {
      return (
        <Empty description={t('pitr.noAgents')}>
          <Text type="secondary">{t('pitr.noAgentsDesc')}</Text>
          <br /><br />
          <Button type="primary" onClick={() => navigate('/agents')}>{t('pitr.goToAgents')}</Button>
        </Empty>
      );
    }

    if (availableAgents.length === 0) {
      return (
        <Empty description={t('pitr.noOnlineAgents')}>
          <Text type="secondary">{t('pitr.noOnlineAgentsDesc')}</Text>
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
                <Space>
                  {agent.hostname} - MySQL {agent.mySQLVersion || 'N/A'}
                  <Tag color="green">{t('pitr.connected')}</Tag>
                </Space>
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
      <Card size="small" title={t('pitr.parseTargeting')} style={{ marginBottom: 24 }}>
        <Text type="secondary" style={{ display: 'block', marginBottom: 16 }}>
          {t('pitr.parseTargetingHelp')}
        </Text>
        <Form.Item label={t('pitr.binlogFiles')} help={t('pitr.binlogFilesHelp')}>
          <Input
            placeholder={t('pitr.binlogFilesPlaceholder')}
            value={binlogFiles}
            onChange={(e) => setBinlogFiles(e.target.value)}
          />
        </Form.Item>
        <Form.Item label={t('pitr.startTime')} help={t('pitr.startTimeHelp')}>
          <DatePicker
            showTime
            style={{ width: '100%' }}
            value={startTime ? dayjs(startTime) : null}
            onChange={(date) => setStartTime(date ? date.toISOString() : '')}
          />
        </Form.Item>
        <Space size="large">
          <Form.Item label={t('pitr.startPos')} help={t('pitr.startPosHelp')}>
            <InputNumber
              min={0}
              style={{ width: 180 }}
              value={startPos}
              onChange={(v) => setStartPos(v)}
              placeholder="0"
            />
          </Form.Item>
          <Form.Item label={t('pitr.stopPos')} help={t('pitr.stopPosHelp')}>
            <InputNumber
              min={0}
              style={{ width: 180 }}
              value={stopPos}
              onChange={(v) => setStopPos(v)}
              placeholder="0"
            />
          </Form.Item>
        </Space>
      </Card>
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

    if (op.state === 'failed' || op.state === 'cancelled') {
      return (
        <div style={{ textAlign: 'center', padding: 24 }}>
          <CloseCircleOutlined style={{ fontSize: 64, color: '#ff4d4f' }} />
          <Title level={4} style={{ marginTop: 16 }}>
            {op.state === 'cancelled' ? t('pitr.operationCancelled') : t('pitr.operationFailed')}
          </Title>
          {op.error && (
            <Alert type="error" message={op.error} showIcon style={{ maxWidth: 600, margin: '16px auto' }} />
          )}
        </div>
      );
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

    if (op.state === 'failed' || op.state === 'cancelled') {
      return (
        <div style={{ textAlign: 'center', padding: 24 }}>
          <CloseCircleOutlined style={{ fontSize: 64, color: '#ff4d4f' }} />
          <Title level={4} style={{ marginTop: 16 }}>
            {op.state === 'cancelled' ? t('pitr.operationCancelled') : t('pitr.operationFailed')}
          </Title>
          {op.error && (
            <Alert type="error" message={op.error} showIcon style={{ maxWidth: 600, margin: '16px auto' }} />
          )}
        </div>
      );
    }

    const parseRes = op.parseResult;
    if (!parseRes) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip={t('pitr.loadingPreview')} /></div>;
    }

    const isExecuting = op.state === 'executing';
    const isCompleted = op.state === 'completed';
    const selectionDisabled = isExecuting || isCompleted;

    const handleCopyAll = () => {
      const text = reverseSql.map((entry) => entry.reverseSql).filter(Boolean).join('\n');
      navigator.clipboard.writeText(text).then(
        () => message.success(t('pitr.copySuccess')),
        () => message.error(t('common.error')),
      );
    };

    const toggleSelect = (i: number) => {
      setSelected((prev) => {
        const cur = prev ?? [];
        return cur.includes(i) ? cur.filter((x) => x !== i) : [...cur, i];
      });
    };

    return (
      <div>
        <Alert
          type={isCompleted ? 'success' : 'info'}
          message={isCompleted ? t('pitr.recoveryCompleted') : t('pitr.parseCompleted')}
          description={isCompleted
            ? `${t('pitr.rowsRestored')}: ${op.execResult?.rowsRestored?.toLocaleString() ?? '0'}`
            : t('pitr.parseDesc')}
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
        {reverseSql.length > 0 && (
          <Card
            size="small"
            title={t('pitr.generatedSql')}
            style={{ marginTop: 16 }}
            extra={
              <Space>
                <Button size="small" onClick={handleCopyAll} disabled={selectionDisabled}>
                  {t('pitr.copyAll')}
                </Button>
                <Button
                  type="primary"
                  size="small"
                  icon={<CheckCircleOutlined />}
                  onClick={handleExecute}
                  loading={executeMutation.isPending}
                  disabled={selectionDisabled || !selected || selected.length === 0}
                >
                  {t('pitr.executeSelected')}
                </Button>
              </Space>
            }
          >
            <Text type="secondary" style={{ display: 'block', marginBottom: 12 }}>
              {isExecuting
                ? t('pitr.executingDesc')
                : isCompleted
                  ? t('pitr.executedDesc')
                  : t('pitr.selectSqlHint')}
            </Text>
            {isExecuting && (
              <Card size="small" style={{ marginBottom: 12, background: '#fafafa' }}>
                <Descriptions column={2} size="small">
                  <Descriptions.Item label={t('pitr.batches')}>
                    {op.progress?.batchesComplete ?? 0} / {op.progress?.batchesTotal ?? '-'}
                  </Descriptions.Item>
                  <Descriptions.Item label={t('pitr.rowsRestored')}>
                    {op.progress?.rowsRestored?.toLocaleString() ?? '0'}
                  </Descriptions.Item>
                  <Descriptions.Item label={t('pitr.estRemaining')}>
                    {op.progress?.estimatedRemaining || t('pitr.calculating')}
                  </Descriptions.Item>
                </Descriptions>
              </Card>
            )}
            <div style={{ maxHeight: 480, overflowY: 'auto' }}>
              {reverseSql.map((entry, i) => (
                <div key={`${entry.sequence}-${i}`} style={{ marginBottom: 12 }}>
                  <Checkbox
                    checked={(selected ?? []).includes(i)}
                    disabled={selectionDisabled}
                    onChange={() => toggleSelect(i)}
                  >
                    <Text>{i + 1}. [{entry.sqlType}] {entry.tableName}</Text>
                  </Checkbox>
                  <pre style={{
                    background: '#f5f5f5',
                    padding: 12,
                    borderRadius: 4,
                    fontSize: 12,
                    overflowX: 'auto',
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-all',
                    marginTop: 4,
                  }}>
                    {entry.reverseSql}
                  </pre>
                </div>
              ))}
            </div>
          </Card>
        )}
      </div>
    );
  };

  // ---- Main Render ----

  const stepContent = [renderStep0, renderStep1, renderStep2, renderStep3];

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
              <Button icon={<ArrowLeftOutlined />} onClick={handleBack} disabled={startMutation.isPending || cancelMutation.isPending || operation?.state === 'executing'}>
                {t('pitr.stepBack')}
              </Button>
            )}
          </Space>
          <Space>
            <Button icon={<CloseCircleOutlined />} onClick={handleCancel} disabled={cancelMutation.isPending || isTerminal || operation?.state === 'executing'}>
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
            {(isPreviewed || isFailed || operation?.state === 'completed') && (
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
