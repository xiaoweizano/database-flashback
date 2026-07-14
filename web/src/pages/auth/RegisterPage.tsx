import { useState } from 'react';
import { useNavigate, Navigate, Link } from 'react-router-dom';
import { Form, Input, Button, Card, Typography, message } from 'antd';
import { MailOutlined, LockOutlined, GlobalOutlined } from '@ant-design/icons';
import { useAuth } from '../../hooks/useAuth';
import { useLocale } from '../../hooks/useLocale';

const { Title } = Typography;

export default function RegisterPage() {
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();
  const { register, isAuthenticated } = useAuth();
  const { t, locale, toggleLocale } = useLocale();

  if (isAuthenticated) {
    return <Navigate to="/" replace />;
  }

  const handleSubmit = async (values: { email: string; password: string; confirm: string }) => {
    if (values.password !== values.confirm) {
      message.error(t('auth.passwordsDoNotMatch'));
      return;
    }
    setLoading(true);
    try {
      await register(values.email, values.password);
      message.success(t('auth.registerSuccess'));
      navigate('/', { replace: true });
    } catch (error: unknown) {
      const err = error as { response?: { data?: { error?: string } } };
      message.error(err.response?.data?.error || t('auth.registerFailed'));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="auth-container">
      <Card className="auth-card">
        <div className="auth-header">
          <div className="auth-logo">
            <svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
              <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/>
            </svg>
          </div>
          <Title level={2} style={{ margin: '0 0 4px', fontSize: 24, fontWeight: 700, color: '#1a1a2e' }}>
            {t('auth.signUp')}
          </Title>
          <Typography.Text type="secondary" style={{ fontSize: 14 }}>
            {t('auth.createAccount')}
          </Typography.Text>
        </div>
        <Form
          name="register"
          onFinish={handleSubmit}
          layout="vertical"
          requiredMark={false}
          size="large"
        >
          <Form.Item
            name="email"
            label={t('auth.email')}
            rules={[
              { required: true, message: t('auth.validation.emailRequired') },
              { type: 'email', message: t('auth.validation.emailInvalid') },
            ]}
          >
            <Input prefix={<MailOutlined />} placeholder={t('auth.placeholder.email')} />
          </Form.Item>
          <Form.Item
            name="password"
            label={t('auth.password')}
            rules={[
              { required: true, message: t('auth.validation.passwordRequired') },
              { min: 6, message: t('auth.validation.passwordMin') },
            ]}
          >
            <Input.Password prefix={<LockOutlined />} placeholder={t('auth.placeholder.password')} />
          </Form.Item>
          <Form.Item
            name="confirm"
            label={t('auth.confirmPassword')}
            dependencies={['password']}
            rules={[
              { required: true, message: t('auth.validation.confirmRequired') },
              ({ getFieldValue }) => ({
                validator(_rule, value) {
                  if (!value || getFieldValue('password') === value) {
                    return Promise.resolve();
                  }
                  return Promise.reject(new Error(t('auth.passwordsDoNotMatch')));
                },
              }),
            ]}
          >
            <Input.Password prefix={<LockOutlined />} placeholder={t('auth.placeholder.confirmPassword')} />
          </Form.Item>
          <Form.Item style={{ marginBottom: 16 }}>
            <Button type="primary" htmlType="submit" loading={loading} block size="large">
              {t('auth.signUp')}
            </Button>
          </Form.Item>
        </Form>
        <div style={{ textAlign: 'center', marginBottom: 12 }}>
          <Typography.Text style={{ fontSize: 13, color: '#9ca3af' }}>
            {t('auth.hasAccount')}{' '}
            <Link to="/login" style={{ fontWeight: 600 }}>{t('auth.signInNow')}</Link>
          </Typography.Text>
        </div>
        <div style={{ textAlign: 'center', borderTop: '1px solid #f3f4f6', paddingTop: 12 }}>
          <Button
            type="text"
            size="small"
            icon={<GlobalOutlined />}
            onClick={toggleLocale}
            style={{ fontSize: 12, color: '#9ca3af' }}
          >
            {locale === 'zh' ? 'English' : '中文'}
          </Button>
        </div>
      </Card>
    </div>
  );
}
