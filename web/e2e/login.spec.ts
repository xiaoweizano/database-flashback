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

  test('should navigate to all sidebar pages after login', async ({ page }) => {
    // Login
    await page.getByLabel(/email/i).fill('admin@test.com');
    await page.getByLabel(/password/i).fill('password123');
    await page.getByRole('button', { name: /sign in|login|submit/i }).click();
    await page.waitForURL(/\/agents/);
    await expect(page.getByText(/agents/i)).toBeVisible();

    // Navigate to PITR list page
    await page.getByText(/recovery operations/i).click();
    await page.waitForURL(/\/pitr/);
    await expect(page.getByText(/recovery operations|pitr operations/i).or(page.locator('h1'))).toBeVisible();

    // Navigate back to agents
    await page.getByText(/agents/i).click();
    await page.waitForURL(/\/agents/);
    await expect(page.getByText(/agents/i)).toBeVisible();
  });
});

test.describe('PITR Wizard Flow', () => {
  test('should display all wizard steps on the PITR creation page', async ({ page }) => {
    await page.goto('http://localhost:5173/login');
    await page.waitForLoadState('networkidle');

    // Login
    await page.getByLabel(/email/i).fill('admin@test.com');
    await page.getByLabel(/password/i).fill('password123');
    await page.getByRole('button', { name: /sign in|login|submit/i }).click();
    await page.waitForURL(/\/agents/);

    // Navigate to new PITR recovery
    await page.getByText(/PITR Recovery|new recovery/i).click();
    await page.waitForURL(/\/pitr\/new/);

    // Verify wizard structure
    await expect(page.getByText(/point-in-time recovery/i)).toBeVisible();

    // Should show step progression (usually steps, stepper, or progress indicator)
    const stepIndicators = page.locator('.ant-steps-item, [class*="step"], [class*="Step"], [role=tab]');
    const stepCount = await stepIndicators.count();
    expect(stepCount).toBeGreaterThanOrEqual(3);
  });

  test('should show navigation to PITR detail page from list', async ({ page }) => {
    await page.goto('http://localhost:5173/login');
    await page.waitForLoadState('networkidle');

    // Login
    await page.getByLabel(/email/i).fill('admin@test.com');
    await page.getByLabel(/password/i).fill('password123');
    await page.getByRole('button', { name: /sign in|login|submit/i }).click();
    await page.waitForURL(/\/agents/);

    // Go to recovery operations list
    await page.getByText(/recovery operations/i).click();
    await page.waitForURL(/\/pitr/);
    await expect(page.getByText(/recovery operations|pitr operations/i).or(page.locator('h1'))).toBeVisible();

    // If there are rows in the list, clicking one should navigate to detail
    const rows = page.locator('table tr, .ant-table-row, [class*="row"], [class*="Row"]');
    const rowCount = await rows.count();
    if (rowCount > 0) {
      await rows.first().click();
      // Should navigate to a detail page with an ID
      await expect(page).toHaveURL(/\/pitr\/[a-f0-9-]+/);
    }
  });

  test('should show empty state when no agents are registered', async ({ page }) => {
    await page.goto('http://localhost:5173/login');
    await page.waitForLoadState('networkidle');

    // Login
    await page.getByLabel(/email/i).fill('admin@test.com');
    await page.getByLabel(/password/i).fill('password123');
    await page.getByRole('button', { name: /sign in|login|submit/i }).click();
    await page.waitForURL(/\/agents/);

    // Check for empty state or agent list
    const emptyState = page.getByText(/no agents|no data|empty/i);
    const tableRows = page.locator('table tr, .ant-table-row');
    const hasEmptyState = await emptyState.isVisible().catch(() => false);

    if (!hasEmptyState) {
      // If no empty state, the agent table should be visible
      await expect(tableRows.first()).toBeVisible();
    }
  });
});
