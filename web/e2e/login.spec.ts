import { test, expect } from '@playwright/test';

test.describe('PITR Wizard E2E', () => {
  test.beforeEach(async ({ page }) => {
    // Navigate to login first
    await page.goto('http://localhost:5173/login');
    await page.waitForLoadState('networkidle');
  });

  test('should display login page', async ({ page }) => {
    await expect(page.getByRole('heading', { name: /sign in/i })).toBeVisible();
    await expect(page.getByLabel(/email/i)).toBeVisible();
    await expect(page.getByLabel(/password/i)).toBeVisible();
  });

  test('should show error with invalid credentials', async ({ page }) => {
    await page.getByLabel(/email/i).fill('wrong@example.com');
    await page.getByLabel(/password/i).fill('wrongpassword');
    await page.getByRole('button', { name: /sign in|login|submit/i }).click();
    // Should show an error message
    await expect(page.getByRole('alert')).toBeVisible();
  });

  test('should complete full login flow', async ({ page }) => {
    await page.getByLabel(/email/i).fill('admin@test.com');
    await page.getByLabel(/password/i).fill('password123');
    await page.getByRole('button', { name: /sign in|login|submit/i }).click();
    // After login, should redirect to agents page
    await page.waitForURL(/\/agents/);
    await expect(page.getByText(/agents/i)).toBeVisible();
  });

  test('should display PITR wizard after login', async ({ page }) => {
    // Login first
    await page.getByLabel(/email/i).fill('admin@test.com');
    await page.getByLabel(/password/i).fill('password123');
    await page.getByRole('button', { name: /sign in|login|submit/i }).click();
    await page.waitForURL(/\/agents/);

    // Navigate to PITR wizard via sidebar
    await page.getByText(/PITR Recovery/i).click();
    await page.waitForURL(/\/pitr\/new/);
    await expect(page.getByText(/point-in-time recovery/i)).toBeVisible();

    // Should show the steps component
    await expect(page.getByText(/select agent/i)).toBeVisible();
    await expect(page.getByText(/target table/i)).toBeVisible();
    await expect(page.getByText(/preflight check/i)).toBeVisible();
    await expect(page.getByText(/preview changes/i)).toBeVisible();
  });

  test('should show agent list in step 1', async ({ page }) => {
    // Login
    await page.getByLabel(/email/i).fill('admin@test.com');
    await page.getByLabel(/password/i).fill('password123');
    await page.getByRole('button', { name: /sign in|login|submit/i }).click();
    await page.waitForURL(/\/agents/);

    // Navigate to PITR wizard
    await page.getByText(/PITR Recovery/i).click();
    await page.waitForURL(/\/pitr\/new/);

    // Should have a select for choosing an agent
    await expect(page.locator('.ant-select')).toBeVisible();
  });

  test('should show audit log page', async ({ page }) => {
    // Login
    await page.getByLabel(/email/i).fill('admin@test.com');
    await page.getByLabel(/password/i).fill('password123');
    await page.getByRole('button', { name: /sign in|login|submit/i }).click();
    await page.waitForURL(/\/agents/);

    // Navigate to audit log
    await page.getByText(/audit log/i).click();
    await page.waitForURL(/\/audit/);
    await expect(page.getByText(/audit log/i)).toBeVisible();
  });
});
