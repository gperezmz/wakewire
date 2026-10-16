// The values the wakewire mod keeps in $.state for its footer label.

/** What the footer shows of this session's watch. */
export type WakewireLine = {
  /** the watch, as <host>/<owner>/<repo> */
  watch: string
  /** wakes are held */
  paused: boolean
  /** changes held while paused */
  held: number
  /** a few words naming what is wrong, or null when nothing is */
  trouble: string | null
  /** the changes in a wake whose turn has not ended yet; 0 when none */
  woke: number
  /** a poll just ran */
  pulse: boolean
  /** changes found per hour over the last hours, oldest first */
  activity: number[]
}

declare module 'claude-code' {
  interface PluginState {
    wakewire: { line: WakewireLine | null }
  }
}
