import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { CookiesForm, ModelForm } from '../src/pages/SettingsPage'
import { mockHTTP, response } from './fixtures'

const initial = { base_url: 'https://provider.test/v1', model: 'old-model', configured: true, capability: 'multimodal' }
it('saves text and vision independently, clears entered keys, and tests saved settings only', async () => {
  const user = userEvent.setup()
  const fetch = mockHTTP((path, init) => path.endsWith('/test')
    ? response({ ok: true, message: 'Saved model works' })
    : response({ ...JSON.parse(init.body as string), api_key: undefined, configured: true }))
  render(<><ModelForm kind="text" initial={initial} /><ModelForm kind="vision" initial={initial} /></>)
  await user.clear(screen.getByLabelText('Text model name'))
  await user.type(screen.getByLabelText('Text model name'), 'new-text-model')
  await user.type(screen.getByLabelText('Text API key'), 'secret-text')
  await user.type(screen.getByLabelText('Vision API key'), 'unsaved-vision-key')
  await user.click(screen.getByRole('button', { name: 'Test saved text settings' }))
  expect(fetch.mock.calls[0][0]).toBe('/api/v1/settings/text/test')
  expect(fetch.mock.calls[0][1]!.body).toBeUndefined()
  expect(screen.getByLabelText('Text API key')).toHaveValue('secret-text')
  await user.click(screen.getByRole('button', { name: 'Save text settings' }))
  await waitFor(() => expect(screen.getByLabelText('Text API key')).toHaveValue(''))
  expect(fetch.mock.calls[1][0]).toBe('/api/v1/settings/text')
  expect(JSON.parse(fetch.mock.calls[1][1]!.body as string)).toMatchObject({ model: 'new-text-model', api_key: 'secret-text' })
  expect(screen.getByLabelText('Vision API key')).toHaveValue('unsaved-vision-key')
  expect(fetch.mock.calls.some(([path]) => path.includes('vision'))).toBe(false)
  expect(localStorage.length).toBe(0)
  await user.click(screen.getByRole('button', { name: 'Save text settings' }))
  expect(JSON.parse(fetch.mock.calls[2][1]!.body as string).api_key).toBe('')
})
it('reports model test errors without reporting success', async () => {
  const user = userEvent.setup()
  mockHTTP(() => response({ code: 'model_test_failed', message: 'Bad credentials', request_id: 'test-7', retryable: true }, 502))
  render(<ModelForm kind="vision" initial={initial} />)
  await user.click(screen.getByRole('button', { name: 'Test saved vision settings' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Bad credentials (test-7)')
})
it('uploads raw Netscape cookies and requires confirmation before shared deletion', async () => {
  const user = userEvent.setup()
  const fetch = mockHTTP(() => response({ ok: true }))
  const text = '# Netscape HTTP Cookie File\n.example.test\tTRUE\t/\tTRUE\t0\tsession\tsecret'
  const file = new File([text], 'cookies.txt', { type: 'text/plain' })
  Object.defineProperty(file, 'text', { value: vi.fn().mockResolvedValue(text) })
  render(<CookiesForm initial={false} />)
  await user.upload(screen.getByLabelText('Netscape cookies file'), file)
  await user.click(screen.getByRole('button', { name: 'Upload cookies' }))
  expect(await screen.findByText('Cookies uploaded.')).toBeInTheDocument()
  expect(fetch.mock.calls[0][1]).toMatchObject({ method: 'PUT', body: text, headers: { 'Content-Type': 'text/plain; charset=utf-8' } })
  await user.click(screen.getByRole('button', { name: 'Remove cookies…' }))
  expect(fetch).toHaveBeenCalledTimes(1)
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Confirm removal' }))
  expect(await screen.findByText('Cookies removed.')).toBeInTheDocument()
  expect(fetch.mock.calls[1][1]!.method).toBe('DELETE')
})
