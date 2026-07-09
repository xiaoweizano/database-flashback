import { Typography, Card } from 'antd';

const { Title } = Typography;

export default function OrgSettingsPage() {
  return (
    <div>
      <Title level={3}>Organization Settings</Title>
      <Card title="General Settings">
        <Typography.Text>
          Organization settings will be available here. Admin access is required to modify settings.
        </Typography.Text>
      </Card>
    </div>
  );
}
