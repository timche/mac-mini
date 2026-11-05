export type Shown = { file: string; source: string; width: number; height: number; columns: number }

declare module 'claude-code' {
  interface PluginState {
    'show-image': { pending: Shown[]; attached: Record<string, Shown[]> }
  }
}
