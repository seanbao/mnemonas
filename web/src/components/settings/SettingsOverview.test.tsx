import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { SettingsOverview, type SettingsDestination } from './SettingsOverview'

function renderOverview(overrides: Partial<React.ComponentProps<typeof SettingsOverview>> = {}) {
  const onNavigate = vi.fn<(destination: SettingsDestination) => void>()
  render(
    <SettingsOverview
      trashEnabled
      webdavEnabled
      webdavAuthType="users"
      shareEnabled
      alertsEnabled
      diskHealthEnabled
      scrubScheduleEnabled
      onNavigate={onNavigate}
      {...overrides}
    />,
  )
  return onNavigate
}

describe('SettingsOverview', () => {
  it('summarizes protected settings and opens every task', () => {
    const onNavigate = renderOverview()

    expect(screen.getByText('回收站已开启')).toBeTruthy()
    expect(screen.getByText('用户账号认证')).toBeTruthy()
    expect(screen.getByText('分享已启用')).toBeTruthy()
    expect(screen.getByText('三项主动照看已启用')).toBeTruthy()

    const destinations: Array<[string, SettingsDestination]> = [
      ['账户与远程访问：检查登录会话、HTTPS 和公网访问边界。', 'general'],
      ['数据保护：管理回收站、版本保留和自动版本化。', 'retention'],
      ['目录与访问：管理目录配额、访问规则和有效权限复核。', 'users-access'],
      ['分享与协作：设置分享默认策略，并复核已经创建的访问链接。', 'shares'],
      ['设备挂载：查看 WebDAV 挂载地址，并选择适合的认证方式。', 'webdav'],
      ['设备状态与通知：查看磁盘健康、数据校验和异常通知。', 'device-care'],
    ]

    for (const [label, destination] of destinations) {
      fireEvent.click(screen.getByRole('button', { name: label }))
      expect(onNavigate).toHaveBeenLastCalledWith(destination)
    }
  })

  it.each([
    ['basic', '独立凭据'],
    ['none', '匿名访问'],
    ['future', '已启用'],
  ])('describes the %s WebDAV mode', (webdavAuthType, expected) => {
    renderOverview({ webdavAuthType })
    expect(screen.getByText(expected)).toBeTruthy()
  })

  it('summarizes disabled and partially enabled services', () => {
    const { rerender } = render(
      <SettingsOverview
        trashEnabled={false}
        webdavEnabled={false}
        webdavAuthType="none"
        shareEnabled={false}
        alertsEnabled={false}
        diskHealthEnabled={false}
        scrubScheduleEnabled={false}
        onNavigate={() => undefined}
      />,
    )

    expect(screen.getByText('删除将直接生效')).toBeTruthy()
    expect(screen.getByText('未启用')).toBeTruthy()
    expect(screen.getByText('分享未启用')).toBeTruthy()
    expect(screen.getByText('尚未启用主动照看')).toBeTruthy()

    rerender(
      <SettingsOverview
        trashEnabled
        webdavEnabled
        webdavAuthType="users"
        shareEnabled
        alertsEnabled
        diskHealthEnabled={false}
        scrubScheduleEnabled={false}
        onNavigate={() => undefined}
      />,
    )

    expect(screen.getByText('1 / 3 项已启用')).toBeTruthy()
  })
})
