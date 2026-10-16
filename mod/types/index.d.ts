// The values the wakewire mod keeps in $.state for its footer label.

/** What the footer shows of this session's watch. */
export type WakewireLine = {
  /** watching: all is well; paused: wakes are held; trouble: see the status line */
  kind: 'watching' | 'paused' | 'trouble'
  /** the watch, as <host>/<owner>/<repo> */
  watch: string
  /** changes held while paused */
  held: number
}

declare module 'claude-code' {
  interface PluginState {
    wakewire: { line: WakewireLine | null }
  }
}
