export const unsavedChangesConfirmationMessage = '当前有未保存的更改。离开此页面将丢弃这些更改，是否继续？'

export function confirmDiscardUnsavedChanges(): boolean {
  return window.confirm(unsavedChangesConfirmationMessage)
}
