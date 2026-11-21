import { expect, test } from '@playwright/test'

test('lock screen and test network are visible', async ({ page }) => {
  page.on('console', message => { if (message.type() === 'error') console.log('[browser console]', message.text()) })
  page.on('pageerror', error => console.log('[browser pageerror]', error.message))
  await page.goto('/')
  await expect(page.getByText('Кошелёк заблокирован')).toBeVisible()
  await page.getByLabel('Пароль').fill('test-password')
  await page.getByRole('button', { name: 'Разблокировать' }).click()
  await expect(page.getByText('Кошелёк заблокирован')).toBeHidden()
  await expect(page.getByText('ТЕСТОВАЯ СЕТЬ')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Кошельки' })).toBeVisible()
})

test('receives, sends and shows a transaction in history on the mock profile', async ({ page }) => {
  page.on('console', message => { if (message.type() === 'error') console.log('[browser console]', message.text()) })
  page.on('pageerror', error => console.log('[browser pageerror]', error.message))
  await page.goto('/')
  await page.getByLabel('Пароль').fill('test-password')
  await page.getByRole('button', { name: 'Разблокировать' }).click()

  // Open the wallet, then receive BTC using its explicit asset action.
  await page.getByRole('button', { name: 'Открыть →' }).click()
  await page.getByRole('button', { name: 'Получить BTC' }).click()
  await expect(page.getByRole('heading', { name: 'Получить BTC' })).toBeVisible()
  await expect(page.getByText('tb1qtestaddress000000000000000000000000000')).toBeVisible()
  await page.getByRole('button', { name: 'Назад' }).click()

  // TRX is a fee asset: it can be received but is not a sendable asset.
  await page.getByRole('button', { name: 'Получить TRX' }).click()
  await expect(page.getByRole('heading', { name: 'Получить TRX' })).toBeVisible()
  await expect(page.getByText('TQn9Y2khDD95J42FQtQTdwVVRx9h6f8z5D')).toBeVisible()
  await page.getByRole('button', { name: 'Назад' }).click()

  // Prepare a BTC transfer from the wallet screen.
  await page.getByRole('button', { name: 'Отправить' }).click()
  await page.locator('select').first().selectOption('BTC')
  await page.locator('input').nth(0).fill('tb1qrecipient0000000000000000000000000000000')
  await page.locator('input').nth(1).fill('0.001')
  await page.getByRole('button', { name: 'Проверить перевод' }).click()

  await expect(page.getByRole('heading', { name: 'Проверьте перевод' })).toBeVisible()
  await expect(page.getByText('tb1qrecipient0000000000000000000000000000000')).toBeVisible()
  await expect(
    page.locator('dt').filter({ hasText: 'Сумма' }).locator('xpath=following-sibling::dd[1]'),
  ).toHaveText('0.001 BTC')
  await page.getByRole('button', { name: 'Далее' }).click()

  const confirmation = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Подтверждение' }) })
  await expect(confirmation).toBeVisible()
  await confirmation.locator('input[type="password"]').nth(0).fill('test-password')
  await page.getByRole('button', { name: 'Подписать и отправить' }).click()

  await expect(page.getByRole('heading', { name: 'История операций' })).toBeVisible()
  await expect(page.getByText('BTC · Исходящая')).toBeVisible()
  await expect(page.getByText('0.001 · pending')).toBeVisible()
  await expect(page.getByText('tb1qrecipient0000000000000000000000000000000')).toBeVisible()
})

test('a locked wallet redirects every route to the lock screen', async ({ page }) => {
  await page.goto('/#/history')
  await expect(page.getByText('Кошелёк заблокирован')).toBeVisible()
  await expect(page).toHaveURL(/#\/lock$/)
})

test('locking returns to the lock screen and hides wallet data', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('Пароль').fill('test-password')
  await page.getByRole('button', { name: 'Разблокировать' }).click()
  await expect(page.getByRole('heading', { name: 'Кошельки' })).toBeVisible()
  await page.getByRole('button', { name: /Заблокировать/ }).click()
  await expect(page.getByText('Кошелёк заблокирован')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Кошельки' })).toBeHidden()
})

test('send form explains invalid input before contacting Go', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('Пароль').fill('test-password')
  await page.getByRole('button', { name: 'Разблокировать' }).click()
  await page.getByRole('button', { name: 'Открыть →' }).click()
  await page.getByRole('button', { name: 'Отправить' }).click()

  await page.getByRole('button', { name: 'Проверить перевод' }).click()
  await expect(page.getByRole('alert')).toHaveText('Укажите адрес получателя.')

  await page.locator('#send-recipient').fill('tb1qrecipient0000000000000000000000000000000')
  await page.locator('#send-amount').fill('0.123456789')
  await page.getByRole('button', { name: 'Проверить перевод' }).click()
  await expect(page.getByRole('alert')).toContainText('не более 8')

  await page.locator('#send-amount').fill('0')
  await page.getByRole('button', { name: 'Проверить перевод' }).click()
  await expect(page.getByRole('alert')).toHaveText('Сумма должна быть больше нуля.')
})

test('receive screen can verify the address against the wallet keys', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('Пароль').fill('test-password')
  await page.getByRole('button', { name: 'Разблокировать' }).click()
  await page.getByRole('button', { name: 'Открыть →' }).click()
  await page.getByRole('button', { name: 'Получить BTC' }).click()
  await expect(page.getByRole('heading', { name: 'Получить BTC' })).toBeVisible()

  await page.getByRole('button', { name: 'Проверить адрес ключами кошелька' }).click()
  await page.locator('#verify-password').fill('test-password')
  await page.getByRole('button', { name: 'Проверить', exact: true }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Адрес подтверждён' })).toBeVisible()
  await expect(page.locator('#verify-password')).toHaveValue('')
})
