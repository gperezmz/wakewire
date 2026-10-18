import { describe, expect, mock, test } from 'claude-code/testing'

import { glow } from '../hooks/register'

// A fake wakewire binary: answers each argv the mod runs, and records them.
function binary(
  on: any,
  opts: { protocol?: number; holds?: boolean; batch?: { id: number; wake: string; count?: number } | null; gate?: Promise<void> } = {},
) {
  const calls: string[][] = []
  const state = { holds: opts.holds ?? true, batch: opts.batch ?? null, acked: [] as number[], problems: [] as { code: string; text: string }[], pausedSince: null as string | null, held: 0 }
  const status = () => ({ watch: 'github.com/o/x', polledAt: null, pausedSince: state.pausedSince, held: state.held, pending: 0, problems: state.problems })
  on('process.run', ($: any, e: any) => {
    const argv = [...e.argv] as string[]
    calls.push(argv)
    const out = (v: unknown) => ({ value: { exitCode: 0, stdout: JSON.stringify(v), stderr: '', isStdoutTruncated: false, isStderrTruncated: false } })
    const [, cmd, sub] = argv
    if (cmd === 'version') return out({ version: 'v1.0.0', commit: 'abc', protocol: opts.protocol ?? 2 })
    if (cmd === 'adapter' && sub === 'hello') return out({ protocol: 2, holds: state.holds, watch: 'github.com/o/x', status: status() })
    if (cmd === 'adapter' && sub === 'next') {
      // The tick asks for the next batch and nothing more: --reader is gone,
      // and a binary that no longer takes it would refuse the call.
      expect(argv.slice(1, 4)).toEqual(['adapter', 'next', '--session'])
      expect(argv.slice(5)).toEqual(['--json'])
      expect(argv).not.toContain('--reader')
      if (opts.gate) {
        const answer = { state: state.holds ? 'armed' : 'idle', polled: true, batch: state.batch, status: status() }
        return opts.gate.then(() => out(answer))
      }
      const batch = state.batch
      return out({ state: state.holds ? 'armed' : 'idle', polled: true, batch, status: status() })
    }
    if (cmd === 'adapter' && sub === 'ack') {
      state.acked.push(Number(argv[3]))
      state.batch = null
      return out({ batch: Number(argv[3]), delivered: 1 })
    }
    if (cmd === 'adapter' && sub === 'ended') return out({ session: argv[4], dormant: true })
    if (cmd === 'pause') return { value: { ...out({}).value, stdout: 'watch: github.com/o/x\npaused: true\noutcome: paused\nhelp[1]: Run `wakewire resume github.com/o/x` to deliver everything held as one wake\n' } }
    return { value: { exitCode: 1, stdout: '', stderr: 'unknown', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  return { calls, state }
}

// What the mod registered with the engine, which world resets and records.
let tools: unknown[] = []
let commands: string[] = []

// The status lines the mod showed, in order; world records them.
let seen: (string | undefined)[] = []

function statuses(_on: any): (string | undefined)[] {
  return seen
}

function submits(on: any, drop = false): string[] {
  const texts: string[] = []
  on('prompt.submit', ($: any, e: any) => {
    if (e.origin?.kind === 'plugin' && e.origin?.name === 'wakewire') {
      expect(e.origin?.asUser).toBe(true)
      texts.push(e.text)
    } else {
      return { text: e.text, origin: e.origin }
    }
    return drop ? { drop: 'refused by a test' } : { text: e.text, origin: e.origin }
  })
  return texts
}

const start = { cwd: '/repo', surface: 'terminal', isInteractive: true } as const

// world answers what the engine would beneath the plugins: the session's
// start, end and id, and the registrations the mod makes.
function world(on: any, ids: string[] = ['sess-1']) {
  let n = 0
  on('session.start', ($: any, e: any) => ({ cwd: e.cwd }))
  on('session.end', ($: any, e: any) => ({ sessionId: e.sessionId }))
  on('session.id', () => ({ value: ids[Math.min(n++, ids.length - 1)] }))
  tools = []
  commands = []
  on('tool.register', (_$: any, e: any) => {
    tools.push(e)
    return { value: undefined }
  })
  on('command.register', (_$: any, e: any) => {
    commands.push(e.name)
    return { value: undefined }
  })
  on('turn.start', ($: any, e: any) => ({ turnId: e.turnId }))
  on('turn.complete', () => ({ text: '' }))
  on('ui.render', { component: 'SessionMode' }, () => ({ type: 'Box', props: {}, children: [] }))
  on('classic.SessionStart', () => ({}))
  seen = []
  on('ui.status', ($: any, e: any) => {
    seen.push(e.text)
    return { value: undefined }
  })
}

describe('wakewire mod', () => {
  test('a held watch arms at start, a batch is submitted as a wake, then acknowledged', async ($, on) => {
    const clock = mock.clock(on)
    world(on)
    const bin = binary(on, { batch: { id: 7, wake: '[wakewire] github.com/o/x: 1 change since 09:00 (batch 7)' } })
    const sent = submits(on)
    const seen = statuses(on)
    await $.session.start(start)
    expect(bin.calls.map(c => c.slice(1, 3).join(' '))).toEqual(['version --json', 'adapter hello'])
    expect(seen.at(-1)).toBe(undefined) // all is well: nothing on the status line
    const footer = await $.ui.mount({ plugin: 'wakewire', surface: 'terminal', component: 'SessionMode', props: { modes: ['focus'] } })
    expect((await footer.find({ type: 'Text', text: '↯' })) !== undefined).toBe(true)
    expect((await footer.find({ type: 'Text', text: /▁/ })) === undefined).toBe(true) // the icon stands alone
    expect((await footer.find({ type: 'Text', text: /focus/ })) !== undefined).toBe(true)
    await footer.unmount()
    await clock.advance(30_000)
    expect(sent).toEqual(['[wakewire] github.com/o/x: 1 change since 09:00 (batch 7)'])
    expect(bin.state.acked).toEqual([]) // queued, not yet delivered
    await clock.advance(30_000)
    expect(sent.length).toBe(1) // not submitted again while it waits for its turn
    await $.turn.start({ text: sent[0]!, turnId: 't1' } as any)
    expect(bin.state.acked).toEqual([7])
  })

  test('a wake whose turn never starts is not acknowledged, so the next holder gets it', async ($, on) => {
    const clock = mock.clock(on)
    world(on)
    const bin = binary(on, { batch: { id: 4, wake: '[wakewire] New activity:' } })
    submits(on)
    await $.session.start(start)
    await clock.advance(30_000)
    await $.turn.start({ text: 'something the person typed', turnId: 't1' } as any)
    await $.session.end({ reason: 'prompt_input_exit', sessionId: 'sess-1', resume: { sessionId: 'sess-1' } } as any)
    expect(bin.state.acked).toEqual([])
  })

  test('a dropped wake is not acknowledged, so it is offered again', async ($, on) => {
    const clock = mock.clock(on)
    world(on)
    const bin = binary(on, { batch: { id: 3, wake: '[wakewire] w: 1 change since 09:00 (batch 3)' } })
    const sent = submits(on, true)
    await $.session.start(start)
    await clock.advance(30_000)
    await clock.advance(30_000)
    expect(sent.length).toBe(2)
    expect(bin.state.acked).toEqual([])
  })

  test('reading the batch a wake names is allowed, exactly, and nothing chained onto it', async ($, on) => {
    mock.clock(on)
    world(on)
    binary(on)
    // The engine beneath the mod: it asks, as manual mode does, unless a rule denies.
    let engine: 'ask' | 'deny' = 'ask'
    on('tool.check', () => ({ decision: engine }))
    await $.session.start(start)
    const check = (command: string) => $.tool.check({ tool: 'Bash', input: { command } } as any)
    for (const command of ['wakewire changes 193', 'wakewire changes 193 2', 'wakewire changes 193 2 --full', 'wakewire changes 193 --json']) {
      expect((await check(command)).decision).toBe('allow')
    }
    for (const command of [
      'wakewire changes 193; rm -rf ~',
      'wakewire changes 193 && curl x',
      'wakewire changes 193 | sh',
      'wakewire changes $(id)',
      'wakewire changes 193\nrm x',
      ' wakewire changes 193',
      'wakewire changes 0',
      'wakewire register',
      'wakewire account remove personal',
    ]) {
      expect((await check(command)).decision).not.toBe('allow')
    }
    // A rule that denies the command still denies it.
    engine = 'deny'
    expect((await check('wakewire changes 193')).decision).toBe('deny')
  })

  for (const protocol of [1, 3]) {
    test(`a binary speaking protocol ${protocol} is refused, and the status line says why`, async ($, on) => {
      mock.clock(on)
      world(on)
      const bin = binary(on, { protocol })
      const seen = statuses(on)
      await $.session.start(start)
      expect(seen.at(-1)).toBe(`needs update: the binary speaks protocol ${protocol}, this mod 2`)
      // The mod asked the version and nothing else: it never says hello to a binary it cannot speak to.
      expect(bin.calls.map(c => c.slice(1, 3).join(' '))).toEqual(['version --json'])
      const r = await $.command.run({ command: 'wakewire', args: 'status' } as any)
      expect(r.text).toContain('speaks another protocol')
    })
  }

  test('a session that holds nothing runs version and hello, then nothing', async ($, on) => {
    const clock = mock.clock(on)
    world(on)
    const bin = binary(on, { holds: false })
    await $.session.start(start)
    await clock.advance(120_000)
    expect(bin.calls.length).toBe(2)
  })

  test('the session end is reported and the footer label cleared', async ($, on) => {
    mock.clock(on)
    world(on)
    const bin = binary(on)
    await $.session.start(start)
    const footer = await $.ui.mount({ plugin: 'wakewire', surface: 'terminal', component: 'SessionMode', props: { modes: [] } })
    expect((await footer.find({ type: 'Text', text: '↯' })) !== undefined).toBe(true)
    await $.session.end({ reason: 'clear', sessionId: 'sess-1', resume: { sessionId: 'sess-1' } } as any)
    expect((await footer.find({ type: 'Text', text: '↯' })) === undefined).toBe(true)
    await footer.unmount()
    const ended = bin.calls.find(c => c[2] === 'ended')
    expect(ended).toEqual(['wakewire', 'adapter', 'ended', '--session', 'sess-1', '--reason', 'clear', '--json'])
  })

  test('the mod registers no changes tool and never runs wakewire changes: the agent reads a batch itself', async ($, on) => {
    mock.clock(on)
    world(on)
    const bin = binary(on)
    await $.session.start(start)
    expect(tools).toEqual([])
    expect(commands).toEqual(['wakewire'])
    // A call for the old tool is nobody's: the mod does not answer it with the binary.
    await Promise.resolve($.tool.call({ tool: 'mcp__wakewire__changes', batch: 5 } as any)).catch(() => undefined)
    expect(bin.calls.some(c => c[1] === 'changes')).toBe(false)
  })

  test('/wakewire pause runs pause on the watch without a model turn', async ($, on) => {
    mock.clock(on)
    world(on)
    const bin = binary(on)
    await $.session.start(start)
    const r = await $.command.run({ command: 'wakewire', args: 'pause 1h' } as any)
    // The mod hands the person the binary's own answer, TOON as it is.
    expect(r.text).toBe('watch: github.com/o/x\npaused: true\noutcome: paused\nhelp[1]: Run `wakewire resume github.com/o/x` to deliver everything held as one wake')
    expect(bin.calls.find(c => c[1] === 'pause')).toEqual(['wakewire', 'pause', 'github.com/o/x', '--for', '1h'])
  })

  test('a watch taken over stops the ticks and says by whom', async ($, on) => {
    const clock = mock.clock(on)
    world(on)
    const seen = statuses(on)
    const calls: string[][] = []
    on('process.run', ($: any, e: any) => {
      calls.push([...e.argv])
      const v = (x: unknown) => ({ value: { exitCode: 0, stdout: JSON.stringify(x), stderr: '', isStdoutTruncated: false, isStderrTruncated: false } })
      if (e.argv[1] === 'version') return v({ protocol: 2 })
      if (e.argv[2] === 'hello') return v({ protocol: 2, holds: true, watch: 'github.com/o/x', status: { watch: 'github.com/o/x', polledAt: null, pausedSince: null, held: 0, pending: 0, problems: [] } })
      return v({ state: 'taken-over', by: 'claude-code:other' })
    })
    await $.session.start(start)
    await clock.advance(30_000)
    await clock.advance(90_000)
    expect(seen.at(-1)).toBe('taken over by claude-code:other')
    expect(calls.filter(c => c[2] === 'next').length).toBe(1)
  })

  test('a Bash call that ran wakewire register arms the mod through hello', async ($, on) => {
    const clock = mock.clock(on)
    world(on)
    const bin = binary(on, { holds: false })
    on('tool.call', { tool: 'Bash' }, () => ({ result: { stdout: 'registered', stderr: '', interrupted: false } }))
    await $.session.start(start)
    bin.state.holds = true
    await $.tool.call({ tool: 'Bash', command: 'wakewire register' } as any)
    await clock.advance(30_000)
    expect(bin.calls.filter(c => c[2] === 'hello').length).toBe(2)
    expect(bin.calls.filter(c => c[2] === 'next').length).toBe(1)
  })

  test('after /clear the new session id holds nothing: the ticks stop', async ($, on) => {
    const clock = mock.clock(on)
    world(on, ['old', 'new'])
    const bin = binary(on)
    await $.session.start(start)
    bin.state.holds = false
    await clock.advance(30_000)
    await clock.advance(60_000)
    expect(bin.calls.filter(c => c[2] === 'next').length).toBe(0)
    expect(bin.calls.filter(c => c[2] === 'hello').map(c => c[4])).toEqual(['old', 'new'])
  })

  test('a problem goes on the status line and turns the dot red; it clears when the problem does', async ($, on) => {
    const clock = mock.clock(on)
    world(on)
    const bin = binary(on)
    await $.session.start(start)
    const footer = await $.ui.mount({ plugin: 'wakewire', surface: 'terminal', component: 'SessionMode', props: { modes: [] } })
    bin.state.problems = [{ code: 'token_refused', text: 'not watching GitHub — GitHub refused the token (401).' }]
    await clock.advance(30_000)
    expect(seen.at(-1)).toBe('not watching GitHub — GitHub refused the token (401)')
    const dot: any = await footer.find({ type: 'Text', text: '↯' })
    expect(dot?.props?.color).toBe('error')
    expect((await footer.find({ type: 'Text', text: / GitHub refused the token/ })) !== undefined).toBe(true)
    bin.state.problems = []
    await clock.advance(30_000)
    expect(seen.at(-1)).toBe(undefined)
    expect(((await footer.find({ type: 'Text', text: '↯' })) as any)?.props?.color).toBe('success')
    await footer.unmount()
  })

  test('a dry run or a failed register arms nothing', async ($, on) => {
    const clock = mock.clock(on)
    world(on)
    const bin = binary(on, { holds: false })
    let fail = false
    on('tool.call', { tool: 'Bash' }, () =>
      fail ? { result: { stdout: '', stderr: 'held', interrupted: false }, isError: true } : { result: { stdout: '', stderr: '', interrupted: false } })
    await $.session.start(start)
    bin.state.holds = true
    await $.tool.call({ tool: 'Bash', command: 'wakewire register --dry-run' } as any)
    fail = true
    await $.tool.call({ tool: 'Bash', command: 'wakewire register' } as any)
    await clock.advance(30_000)
    expect(bin.calls.filter(c => c[2] === 'hello').length).toBe(1)
    expect(bin.calls.filter(c => c[2] === 'next').length).toBe(0)
  })

  test('a tick still waiting when the session is cleared delivers nothing', async ($, on) => {
    const clock = mock.clock(on)
    world(on, ['old', 'old', 'old', 'new'])
    let release = () => {}
    const gate = new Promise<void>(resolve => {
      release = resolve
    })
    const bin = binary(on, { batch: { id: 9, wake: '[wakewire] New activity:' }, gate })
    const sent = submits(on)
    await $.session.start(start)
    await clock.advance(30_000)
    await $.session.end({ reason: 'clear', sessionId: 'old', resume: { sessionId: 'old' } } as any)
    release()
    await clock.settle()
    expect(sent).toEqual([])
    expect(bin.state.acked).toEqual([])
  })

  test('a wake marks the footer until the person next types, and a poll makes the icon bold', async ($, on) => {
    const clock = mock.clock(on)
    world(on)
    const bin = binary(on, { batch: { id: 5, wake: '[wakewire] New activity:', count: 2 } })
    const sent = submits(on)
    await $.session.start(start)
    const footer = await $.ui.mount({ plugin: 'wakewire', surface: 'terminal', component: 'SessionMode', props: { modes: [] } })
    await clock.advance(30_000)
    expect((await footer.find({ type: 'Text', text: / 2 new/ })) !== undefined).toBe(true)
    await $.turn.start({ text: sent[0]!, turnId: 't9' } as any)
    await $.turn.complete({ turnId: 't9', answer: 'ok', durationMs: 1, isAborted: false } as any)
    expect((await footer.find({ type: 'Text', text: / 2 new/ })) !== undefined).toBe(true) // still: not seen yet
    expect(((await footer.find({ type: 'Text', text: '↯' })) as any)?.props?.weight).toBe('bold') // just polled
    await clock.advance(3_000)
    expect(((await footer.find({ type: 'Text', text: '↯' })) as any)?.props?.weight).toBe(undefined)
    await $.prompt.submit({ text: 'thanks', origin: { kind: 'composer' } } as any)
    expect((await footer.find({ type: 'Text', text: / 2 new/ })) === undefined).toBe(true)
    expect(bin.state.acked).toEqual([5])
    await footer.unmount()
  })

  test('a pause shows on the footer over a wake not yet seen', async ($, on) => {
    const clock = mock.clock(on)
    world(on)
    const bin = binary(on, { batch: { id: 5, wake: '[wakewire] New activity:', count: 2 } })
    submits(on)
    await $.session.start(start)
    const footer = await $.ui.mount({ plugin: 'wakewire', surface: 'terminal', component: 'SessionMode', props: { modes: [] } })
    await clock.advance(30_000)
    expect((await footer.find({ type: 'Text', text: / 2 new/ })) !== undefined).toBe(true)
    bin.state.pausedSince = '2026-10-17T12:00:00Z'
    bin.state.held = 3
    await clock.advance(30_000)
    expect((await footer.find({ type: 'Text', text: / paused · 3 held/ })) !== undefined).toBe(true)
    expect((await footer.find({ type: 'Text', text: / 2 new/ })) === undefined).toBe(true)
    expect(((await footer.find({ type: 'Text', text: '↯' })) as any)?.props?.color).toBe('warning')
    await footer.unmount()
  })

  test('after compaction the session is told it holds the watch', async ($, on) => {
    mock.clock(on)
    world(on)
    binary(on)
    await $.session.start(start)
    const r: any = await $.classic.SessionStart({ source: 'compact' } as any)
    const note = String(r.additionalContext ?? '')
    expect(note).toContain('holds the wakewire watch for github.com/o/x')
    // What people wrote is read with the CLI; no tool is named.
    expect(note).toContain('`wakewire changes <batch>`')
    expect(note).not.toContain('mcp__wakewire__changes')
  })

  test('the icon cools from the accent to green as the last change ages', () => {
    const now = Date.parse('2026-10-17T12:00:00Z')
    const ago = (m: number) => new Date(now - m * 60_000).toISOString()
    expect(glow(null, now)).toBe('success')
    expect(glow(ago(2), now)).toBe('claude')
    expect(glow(ago(20), now)).toBe('#d9a05b')
    expect(glow(ago(45), now)).toBe('#a6b35f')
    expect(glow(ago(90), now)).toBe('success')
  })
})
