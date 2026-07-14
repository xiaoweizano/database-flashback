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
import { listAgents } from '../../api/agents';
import { listOrgs } from '../../api/org';
import { startPITR, getPITRStatus, getPITRProgress, cancelPITR } from '../../api/pitr';
import type { AgentInfo, PITROperation, ProgressData } from '../../types';

const { Title, Text } = Typography;
const { Option } = Select;

const stepTitles = ['Select Agent', 'Target Table', 'Preflight Check', 'Preview Changes', 'Execute'];

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
  const [currentStep, setCurrentStep] = useState(0);
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(null);
  const [selectedAgentHostname, setSelectedAgentHostname] = useState<string | null>(null);
  const [targetTable, setTargetTable] = useState('');
  const [recoveryTime, setRecoveryTime] = useState('');
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

  const onlineAgents = useMemo(
    () => (agentsQuery.data ?? []).filter((a: AgentInfo) => a.status === 'online'),
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
    }),
    onSuccess: (data) => {
      setOperationId(data.operationId);
      setCurrentStep(2);
      notification.success({ message: 'PITR operation started', description: `Operation ID: ${data.operationId}` });
    },
    onError: (err: Error) => {
      notification.error({ message: 'Failed to start PITR operation', description: err.message });
    },
  });

  // Cancel operation mutation
  const cancelMutation = useMutation({
    mutationFn: () => cancelPITR(operationId!),
    onSuccess: () => {
      notification.success({ message: 'Operation cancelled' });
      navigate('/pitr/new');
    },
    onError: (err: Error) => {
      notification.error({ message: 'Failed to cancel', description: err.message });
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
    if (!selectedAgentId || !targetTable || !recoveryTime) {
      message.warning('Please fill in all fields');
      return;
    }
    startMutation.mutate();
  }, [selectedAgentId, targetTable, recoveryTime, startMutation]);

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
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip="Loading agents..." /></div>;
    }
    if (agentsQuery.error) {
      return (
        <Alert
          type="error"
          message="Failed to load agents"
          description="Could not fetch the agent list. Please try again."
          action={<Button size="small" danger onClick={() => agentsQuery.refetch()}>Retry</Button>}
          showIcon
        />
      );
    }
    if (onlineAgents.length === 0) {
      return (
        <Empty description="No online agents found">
          <Text type="secondary">You need at least one online agent to start a PITR recovery.</Text>
          <br /><br />
          <Button type="primary" onClick={() => navigate('/agents')}>Go to Agents</Button>
        </Empty>
      );
    }

    return (
      <Form layout="vertical">
        <Form.Item label="Select Agent" required>
          <Select
            placeholder="Choose an online agent"
            style={{ width: '100%' }}
            value={selectedAgentId}
            onChange={(value) => {
              const agent = onlineAgents.find((a: AgentInfo) => a.id === value);
              if (agent) {
                setSelectedAgentId(agent.id);
                setSelectedAgentHostname(agent.hostname);
              }
            }}
          >
            {onlineAgents.map((agent: AgentInfo) => (
              <Option key={agent.id} value={agent.id}>
                {agent.hostname} - MySQL {agent.mysqlVersion || 'N/A'}
              </Option>
            ))}
          </Select>
        </Form.Item>
        {selectedAgentId && (
          <Card size="small" title="Agent Details" style={{ marginTop: 16 }}>
            <Descriptions column={1} size="small">
              <Descriptions.Item label="Hostname">{selectedAgentHostname}</Descriptions.Item>
              <Descriptions.Item label="Status">
                <Tag color="green">online</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="MySQL Version">
                {onlineAgents.find((a: AgentInfo) => a.id === selectedAgentId)?.mysqlVersion || '-'}
              </Descriptions.Item>
            </Descriptions>
          </Card>
        )}
      </Form>
    );
  };

  const renderStep1 = () => (
    <Form layout="vertical">
      <Form.Item label="Target Table" required help="Format: schema.table_name (e.g. mydb.orders)">
        <Input
          placeholder="e.g. mydb.orders"
          value={targetTable}
          onChange={(e) => setTargetTable(e.target.value)}
        />
      </Form.Item>
      <Form.Item label="Recovery Time" required>
        <DatePicker
          showTime
          style={{ width: '100%' }}
          value={recoveryTime ? dayjs(recoveryTime) : null}
          onChange={(date) => setRecoveryTime(date ? date.toISOString() : '')}
        />
      </Form.Item>
    </Form>
  );

  const renderStep2 = () => {
    if (statusQuery.isLoading) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip="Running preflight checks..." /></div>;
    }
    if (statusQuery.error) {
      return (
        <Alert
          type="error"
          message="Failed to fetch status"
          action={<Button size="small" onClick={() => statusQuery.refetch()}>Retry</Button>}
          showIcon
        />
      );
    }

    const op = statusQuery.data;
    if (!op) {
      return <Empty description="No operation data" />;
    }

    if (op.state === 'preflight') {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip="Running preflight checks..." /></div>;
    }

    const preflight = op.preflightResult;
    if (!preflight) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip="Waiting for preflight results..." /></div>;
    }

    return (
      <div>
        <Alert
          type="info"
          message="Preflight checks completed"
          description="Review the binlog configuration details below."
          showIcon
          style={{ marginBottom: 16 }}
        />
        <Card size="small" title="Binlog Configuration">
          <Descriptions column={1} size="small">
            <Descriptions.Item label="Binlog Files">
              {preflight.binlogFiles?.join(', ') || '-'}
            </Descriptions.Item>
            <Descriptions.Item label="Earliest Available Time">
              {preflight.earliestTime ? dayjs(preflight.earliestTime).format('YYYY-MM-DD HH:mm:ss') : '-'}
            </Descriptions.Item>
            <Descriptions.Item label="Estimated Size">
              {preflight.estimatedSize ? `${(preflight.estimatedSize / 1024 / 1024).toFixed(1)} MB` : '-'}
            </Descriptions.Item>
            <Descriptions.Item label="Checked At">
              {preflight.checkedAt ? dayjs(preflight.checkedAt).format('YYYY-MM-DD HH:mm:ss') : '-'}
            </Descriptions.Item>
          </Descriptions>
        </Card>
      </div>
    );
  };

  const renderStep3 = () => {
    if (statusQuery.isLoading) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip="Loading preview..." /></div>;
    }
    if (statusQuery.error) {
      return (
        <Alert
          type="error"
          message="Failed to load preview"
          action={<Button size="small" onClick={() => statusQuery.refetch()}>Retry</Button>}
          showIcon
        />
      );
    }

    const op = statusQuery.data;
    if (!op) {
      return <Empty description="No operation data" />;
    }

    const parseRes = op.parseResult;
    if (!parseRes) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip="Waiting for parse results..." /></div>;
    }

    return (
      <div>
        <Alert
          type="success"
          message="Binlog parsing completed"
          description="Review the estimated changes below before executing."
          showIcon
          style={{ marginBottom: 16 }}
        />
        <Card size="small" title="Estimated Changes">
          <Descriptions column={1} size="small">
            <Descriptions.Item label="Rows Affected">
              <Text strong>{parseRes.rowsAffected?.toLocaleString() || '0'}</Text>
            </Descriptions.Item>
            <Descriptions.Item label="Recovery Time">
              {dayjs(op.recoveryTime).format('YYYY-MM-DD HH:mm:ss')}
            </Descriptions.Item>
            <Descriptions.Item label="Target Table">{op.targetTable}</Descriptions.Item>
          </Descriptions>
        </Card>
        {parseRes.sqlSample && (
          <Card size="small" title="Sample SQL" style={{ marginTop: 16 }}>
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
          <Title level={4} style={{ marginTop: 16 }}>PITR Recovery Completed</Title>
          {operation?.execResult && (
            <Card size="small" style={{ maxWidth: 400, margin: '16px auto' }}>
              <Descriptions column={1} size="small">
                <Descriptions.Item label="Rows Restored">
                  <Text strong>{operation.execResult.rowsRestored?.toLocaleString()}</Text>
                </Descriptions.Item>
                <Descriptions.Item label="Duration">{operation.execResult.duration || '-'}</Descriptions.Item>
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
          <Title level={4} style={{ marginTop: 16 }}>Operation {operation?.state === 'cancelled' ? 'Cancelled' : 'Failed'}</Title>
          {operation?.error && (
            <Alert type="error" message={operation.error} showIcon style={{ maxWidth: 400, margin: '16px auto' }} />
          )}
        </div>
      );
    }

    // Still executing - show progress
    if (progressQuery.isLoading && !progress) {
      return <div style={{ textAlign: 'center', padding: 48 }}><Spin size="large" tip="Starting execution..." /></div>;
    }

    return (
      <div style={{ padding: 24 }}>
        <Alert
          type="info"
          message="Executing PITR Recovery"
          description="The recovery is in progress. This may take a few moments."
          showIcon
          style={{ marginBottom: 24 }}
        />
        <Card>
          <div style={{ textAlign: 'center', marginBottom: 16 }}>
            <Text type="secondary">Batch Progress</Text>
          </div>
          <Progress
            type="circle"
            percent={progressPercent}
            status={isFailed ? 'exception' : 'active'}
            size={200}
            style={{ display: 'block', margin: '0 auto 24px' }}
          />
          <Descriptions column={2} size="small" style={{ maxWidth: 400, margin: '0 auto' }}>
            <Descriptions.Item label="Batches">{progress?.batchesComplete ?? 0} / {progress?.batchesTotal ?? '-'}</Descriptions.Item>
            <Descriptions.Item label="Rows Restored">{progress?.rowsRestored?.toLocaleString() ?? '0'}</Descriptions.Item>
            <Descriptions.Item label="Est. Remaining">{progress?.estimatedRemaining || 'Calculating...'}</Descriptions.Item>
            <Descriptions.Item label="Status">{getStateTag(operation?.state || 'executing')}</Descriptions.Item>
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
        <Title level={3} style={{ margin: 0 }}>Point-in-Time Recovery</Title>
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
                Back
              </Button>
            )}
          </Space>
          <Space>
            <Button icon={<CloseCircleOutlined />} onClick={handleCancel} disabled={cancelMutation.isPending}>
              Cancel
            </Button>
            {currentStep === 0 && (
              <Button
                type="primary"
                icon={<ArrowRightOutlined />}
                onClick={() => {
                  if (!selectedAgentId) {
                    message.warning('Please select an agent');
                    return;
                  }
                  setCurrentStep(1);
                }}
              >
                Next
              </Button>
            )}
            {currentStep === 1 && (
              <Button
                type="primary"
                loading={startMutation.isPending}
                icon={<ArrowRightOutlined />}
                onClick={handleNextFromStep1}
              >
                Start Recovery
              </Button>
            )}
            {currentStep === 2 && (
              <Button
                type="primary"
                icon={<ArrowRightOutlined />}
                onClick={() => setCurrentStep(3)}
              >
                Continue to Preview
              </Button>
            )}
            {currentStep === 3 && (
              <Button
                type="primary"
                icon={<ArrowRightOutlined />}
                onClick={() => setCurrentStep(4)}
              >
                Execute Recovery
              </Button>
            )}
            {(isCompleted || isFailed) && (
              <Button type="primary" onClick={() => navigate('/audit')}>
                View Audit Log
              </Button>
            )}
          </Space>
        </div>
      </Card>
    </div>
  );
}
