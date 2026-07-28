import { useLocale } from '../../hooks/useLocale';
import { Typography, Card } from 'antd';

const { Title } = Typography;

export default function OrgSettingsPage() {
  const { t } = useLocale();
  return (
    <div>
      <Title level={3}>{t('org.settingsTitle')}</Title>
      <Card title={t('org.generalSettings')}>
        <Typography.Text>
          {t('org.settingsDesc')}
        </Typography.Text>
      </Card>
    </div>
  );
}
