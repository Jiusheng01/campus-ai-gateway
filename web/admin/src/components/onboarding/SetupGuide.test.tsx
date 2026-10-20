import {afterEach, expect, test, vi} from 'vitest'
import {cleanup, render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type {OnboardingGuide} from '../../api'
import {ToastProvider} from '../ui/Toast'
import {SetupGuide} from './SetupGuide'

afterEach(cleanup)

function renderGuide(guide: OnboardingGuide, onNavigate = vi.fn()) {
  render(
    <ToastProvider>
      <SetupGuide guide={guide} onNavigate={onNavigate} onHide={() => {}} />
    </ToastProvider>,
  )
  return onNavigate
}

test('a pending step navigates to the page that completes it', async () => {
  const user = userEvent.setup()
  const onNavigate = renderGuide({
    complete: false,
    steps: [
      {id: 'provider', label: '配置渠道', description: '先接入渠道。', done: true, count: 1, action: 'providers'},
      {id: 'model', label: '创建对外模型', description: '再创建模型。', done: false, count: 0, action: 'models'},
      {id: 'request', label: '发送首个请求', description: '最后调用一次。', done: false, count: 0},
    ],
  })

  expect(screen.getByLabelText('已完成 1 / 3 步')).toBeTruthy()
  await user.click(screen.getByRole('button', {name: '去配置'}))
  expect(onNavigate).toHaveBeenCalledWith('models')
})

test('the first-request step shows a runnable sample for a routable model', () => {
  renderGuide({
    complete: false,
    sample_model: 'campus-smart',
    steps: [
      {id: 'provider', label: '配置渠道', description: '', done: true, count: 1, action: 'providers'},
      {id: 'model', label: '创建对外模型', description: '', done: true, count: 1, action: 'models'},
      {id: 'route', label: '添加线路', description: '', done: true, count: 1, action: 'models'},
      {id: 'request', label: '发送首个请求', description: '', done: false, count: 0},
    ],
  })

  expect(screen.getByText(/"model":"campus-smart"/)).toBeTruthy()
})

test('a finished checklist collapses until its steps are asked for', async () => {
  const user = userEvent.setup()
  renderGuide({
    complete: true,
    steps: [
      {id: 'provider', label: '配置渠道', description: '', done: true, count: 1, action: 'providers'},
      {id: 'request', label: '发送首个请求', description: '', done: true, count: 3},
    ],
  })

  expect(screen.queryByText('配置渠道')).toBeNull()
  await user.click(screen.getByRole('button', {name: '查看步骤'}))
  expect(screen.getByText('配置渠道')).toBeTruthy()
})
