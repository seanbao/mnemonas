import { create } from 'zustand'

interface SettingsDraftState {
  hasPendingChanges: boolean
  setHasPendingChanges: (hasPendingChanges: boolean) => void
}

export const useSettingsDraftStore = create<SettingsDraftState>((set) => ({
  hasPendingChanges: false,
  setHasPendingChanges: (hasPendingChanges) => set({ hasPendingChanges }),
}))
