# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: sample.spec.ts >> Sample Group >> fails looking for a missing element
- Location: tests/sample.spec.ts:9:7

# Error details

```
Error: expect(locator).toBeVisible() failed

Locator: getByTestId('nonexistent')
Expected: visible
Timeout: 1000ms
Error: element(s) not found

Call log:
  - Expect "toBeVisible" with timeout 1000ms
  - waiting for getByTestId('nonexistent')

```

```yaml
- heading "hi" [level=1]
```

# Test source

```ts
  1  | import { test, expect } from '@playwright/test'
  2  | 
  3  | test.describe('Sample Group', () => {
  4  |   test('passes', async ({ page }) => {
  5  |     await page.goto('data:text/html,<h1>hi</h1>')
  6  |     await expect(page.getByRole('heading')).toBeVisible()
  7  |   })
  8  | 
  9  |   test('fails looking for a missing element', async ({ page }) => {
  10 |     await page.goto('data:text/html,<h1>hi</h1>')
> 11 |     await expect(page.getByTestId('nonexistent')).toBeVisible({ timeout: 1000 })
     |                                                   ^ Error: expect(locator).toBeVisible() failed
  12 |   })
  13 | })
  14 | 
```