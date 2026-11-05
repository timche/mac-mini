import { expect, mock, test, type TestBody } from 'claude-code/testing'

const TOOL = 'mcp__show-image__show_image'
const MIB = 1024 * 1024
const ok = (stdout: string) => ({ exitCode: 0, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false })

type World = { sizes?: number[]; width?: number; height?: number; isMissing?: boolean }
const PNG = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=='

const world = ($: Parameters<TestBody>[0], on: Parameters<TestBody>[1], { sizes = [1000], width = 400, height = 200, isMissing = false }: World = {}) => {
  const commands: string[][] = []
  let stats = 0

  mock.env(on, { TMPDIR: '/t/' })
  on('fs.stat', (_$, e) => {
    if (isMissing) {
      throw new Error('ENOENT')
    }

    return e.path.startsWith('/t/')
      ? { value: { kind: 'file', size: sizes[Math.min(stats++, sizes.length - 1)] ?? 0, mtimeMs: 0, isLink: false, realPath: e.path } }
      : { value: { kind: 'file', size: 1, mtimeMs: 0, isLink: false, realPath: '/x/shot.png' } }
  })
  on('process.run', (_$, e) => {
    commands.push([...e.argv])

    return { value: ok(e.argv[0] === 'mktemp' ? '/t/show-image.abc\n' : `f\n  pixelWidth: ${width}\n  pixelHeight: ${height}\n`) }
  })
  on('fs.read', () => ({ value: { base64: PNG } }))

  return commands
}

const REPLY = 'c68be964-85c5-4f99-896a-3f2a1b0c9d8e'

const reply = ($: Parameters<TestBody>[0], uuid = REPLY) =>
  $.session.append({
    uuid,
    door: 'response',
    origin: { kind: 'model', model: 'm' },
    message: { type: 'assistant', role: 'assistant', content: [{ type: 'text', text: 'done' }] },
  })

const endTurn = ($: Parameters<TestBody>[0]) =>
  $.turn.complete({ answer: 'done', durationMs: 1, isAborted: false, turnId: 'turn', reason: 'answer' })

const mount = ($: Parameters<TestBody>[0], requestId = REPLY, viewport = { columns: 200, rows: 60 }) =>
  $.ui.mount({
    plugin: 'show-image',
    surface: 'terminal',
    component: 'AssistantMessage',
    requestId,
    props: { text: 'done', isFirstOfReply: true },
    viewport,
  })

const stubEngine = (on: Parameters<TestBody>[1]) => {
  on('turn.complete', (_$, e) => ({ text: e.answer }))
  on('ui.render', () => ({ type: 'engine', ref: 0 }))
}

test('draws the picture from bytes under the final reply, because Herdr refuses a file path', async ($, on) => {
  world($, on)
  stubEngine(on)
  await $.tool.call({ tool: TOOL, path: 'shot.png', columns: 40 })
  await reply($)
  await endTurn($)
  const image = await (await mount($)).find({ type: 'Image' })

  expect(image?.props.source).toEqual({ png: PNG })
  expect(image?.props).toEqual(expect.objectContaining({ columns: 40, rows: 10 }))
})

test('draws it under the last reply of the turn, not one before it', async ($, on) => {
  world($, on)
  stubEngine(on)
  await reply($)
  await $.tool.call({ tool: TOOL, path: 'shot.png' })
  await reply($, '2a56bb66-ec63-4d7c-ba83-97f09b5fdf7e')
  await endTurn($)

  expect(await (await mount($)).find({ type: 'Image' })).toBeUndefined()
  expect(await (await mount($, '2a56bb66-ec63-4d7c-ba83-97f09b5fdf7e')).find({ type: 'Image' })).toBeDefined()
})

test('finds the reply whose request id has its last group zeroed', async ($, on) => {
  world($, on)
  stubEngine(on)
  await $.tool.call({ tool: TOOL, path: 'shot.png' })
  await reply($)
  await endTurn($)

  expect(await (await mount($, 'c68be964-85c5-4f99-896a-000000000000')).find({ type: 'Image' })).toBeDefined()
})

test('shows a copy, so a later screenshot at the same path leaves the row alone', async ($, on) => {
  const commands = world($, on)
  const ran = await $.tool.call({ tool: TOOL, path: 'shot.png' })

  expect(ran.result).toMatch(/\/x\/shot\.png/)
  expect(commands.some(argv => argv[0] === 'sips' && argv.includes('/x/shot.png') && argv.at(-1)?.startsWith('/t/show-image.abc/'))).toBe(true)
})

test('shrinks a picture over the 2 MiB limit until it fits', async ($, on) => {
  const commands = world($, on, { sizes: [5 * MIB, 3 * MIB, MIB] })
  await $.tool.call({ tool: TOOL, path: 'shot.png' })

  expect(commands.filter(argv => argv.includes('-Z')).map(argv => argv[argv.indexOf('-Z') + 1])).toEqual(['2400', '1800'])
})

test('fails rather than draws a picture that never fits', async ($, on) => {
  world($, on, { sizes: [5 * MIB] })
  const ran = await $.tool.call({ tool: TOOL, path: 'shot.png' })

  expect(ran).toEqual({ deny: expect.stringMatching(/stays over/) })
})

test('keeps a tall picture inside the visible rows, in proportion', async ($, on) => {
  world($, on, { width: 100, height: 1000 })
  stubEngine(on)
  await $.tool.call({ tool: TOOL, path: 'shot.png' })
  await reply($)
  await endTurn($)
  const image = await (await mount($, REPLY, { columns: 200, rows: 30 })).find({ type: 'Image' })

  expect(image?.props).toEqual(expect.objectContaining({ columns: 5, rows: 24 }))
})

test('refuses a path that is not there', async ($, on) => {
  world($, on, { isMissing: true })
  const ran = await $.tool.call({ tool: TOOL, path: 'gone.png' })

  expect(ran).toEqual({ deny: expect.stringMatching(/does not exist/) })
})
