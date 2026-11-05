import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Shown } from '../types'

const TOOL = 'show_image'
const FULL_NAME = 'mcp__show-image__show_image'
// The Image element's own limit on bytes it holds.
const MAX_BYTES = 2 * 1024 * 1024
const SHRINK_TO = [2400, 1800, 1200, 800, 600]
const DEFAULT_COLUMNS = 120
const MAX_ROWS = 40
// Every draw needs the bytes again and a transcript redraws often, but each entry can be almost 3 MB of base64.
const CACHED_IMAGES = 8
// A terminal cell is about twice as tall as it is wide.
const CELL_ASPECT = 2
// What a transcript row indents and borders.
const ROW_MARGIN = 6

const run = async ($: EngineInterface, argv: string[]) => {
  const { exitCode, stdout, stderr } = await $.process.run(argv)

  if (exitCode !== 0) {
    throw new Error(`${argv[0]}: ${stderr.trim()}`)
  }

  return stdout
}

let tempDir: Promise<string> | undefined

const makeTempDir = async ($: EngineInterface) => {
  const tmp = (await $.env.get('TMPDIR').catch(() => undefined)) || '/tmp'

  return (await run($, ['mktemp', '-d', `${tmp.replace(/\/+$/, '')}/show-image.XXXXXX`])).trim()
}

const fileSize = async ($: EngineInterface, file: string) => (await $.fs.stat(file)).size

// The picture is copied because the path a session names is often overwritten by its next screenshot, and the
// transcript draws this row again long after.
const prepare = async ($: EngineInterface, source: string) => {
  tempDir ??= makeTempDir($).catch(error => {
    tempDir = undefined
    throw error
  })
  const file = `${await tempDir}/${Date.now()}-${Math.random().toString(36).slice(2, 8)}.png`

  await run($, ['sips', '-s', 'format', 'png', source, '--out', file])

  for (const size of SHRINK_TO) {
    if ((await fileSize($, file)) <= MAX_BYTES) {
      break
    }

    await run($, ['sips', '-s', 'format', 'png', '-Z', String(size), source, '--out', file])
  }

  if ((await fileSize($, file)) > MAX_BYTES) {
    throw new Error(`${source} stays over ${MAX_BYTES} bytes at ${SHRINK_TO.at(-1)} pixels`)
  }

  const dimensions = await run($, ['sips', '-g', 'pixelWidth', '-g', 'pixelHeight', file])
  const width = Number(/pixelWidth: (\d+)/.exec(dimensions)?.[1] ?? 0)
  const height = Number(/pixelHeight: (\d+)/.exec(dimensions)?.[1] ?? 0)

  if (width === 0 || height === 0) {
    throw new Error(`sips could not read the size of ${source}`)
  }

  return { file, width, height }
}

const cellBox = (shown: Shown, viewport?: { columns: number; rows: number }) => {
  const columns = Math.min(shown.columns, (viewport?.columns ?? 80) - ROW_MARGIN, 255)
  const maxRows = Math.min(MAX_ROWS, Math.max(1, (viewport?.rows ?? MAX_ROWS) - ROW_MARGIN))
  const rows = Math.round((columns * shown.height) / shown.width / CELL_ASPECT)

  return rows <= maxRows
    ? { columns: Math.max(1, columns), rows: Math.max(1, rows) }
    : { columns: Math.max(1, Math.round((maxRows * CELL_ASPECT * shown.width) / shown.height)), rows: maxRows }
}

const pending = atom({ plugin: 'show-image', key: 'pending' } as const, [])
const attached = atom({ plugin: 'show-image', key: 'attached' } as const, {})

// An assistant row's requestId can carry a zeroed last group where its stored row's uuid does not.
const rowKey = (id: string) => id.slice(0, 23)

const cache = new Map<string, Promise<string>>()

const pngBytes = ($: EngineInterface, file: string) => {
  const known = cache.get(file)

  if (known !== undefined) {
    return known
  }

  const loaded = $.fs.read(file, { as: 'bytes' }).then(({ base64 }) => base64)
  cache.set(file, loaded)
  loaded.catch(() => cache.delete(file))

  const oldest = cache.keys().next().value

  if (cache.size > CACHED_IMAGES && oldest !== undefined) {
    cache.delete(oldest)
  }

  return loaded
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    await $.tool.register({
      name: TOOL,
      description:
        'Show an image file to the user, drawn in their terminal. The user is on another machine and cannot open a path on this one, so call this for any screenshot, chart or picture they should see, instead of giving them its path. It is drawn under your final message of this turn, so do not say it is above. It does not put the image in your context: use Read to look at it yourself.',
      inputSchema: {
        type: 'object',
        properties: {
          path: { type: 'string', description: 'Absolute or working-directory-relative path of a PNG, JPEG, GIF, WebP, HEIC or TIFF file' },
          columns: { type: 'integer', minimum: 4, maximum: 255, description: `Width in terminal columns, ${DEFAULT_COLUMNS} by default` },
        },
        required: ['path'],
      },
      isDeferred: false,
    })

    return next(e)
  })

  on('tool.call', { tool: FULL_NAME }, async ($, e) => {
    const input = e as { path?: unknown; columns?: unknown }

    if (typeof input.path !== 'string') {
      return { deny: 'path must be a string' }
    }

    const source = (await $.fs.stat(input.path, { resolve: true }).catch(() => undefined))?.realPath

    if (source === undefined) {
      return { deny: `${input.path} does not exist` }
    }

    const shown: Shown = { source, ...(await prepare($, source)), columns: typeof input.columns === 'number' ? input.columns : DEFAULT_COLUMNS }
    await update($, pending, all => [...all, shown])

    return { result: `Queued ${source} (${shown.width}x${shown.height}) to show under your final message of this turn.` }
  }).catch(($, e, next) => ({ deny: `show_image failed: ${next.error.message ?? next.error.kind}` }))

  // A tool's own row is folded in a tool group and hidden in focus view, where the final reply is what is left to see.
  let lastReply: string | undefined

  on('session.append', { door: 'response' }, ($, e, next) => {
    if (e.agentId === undefined && e.message.content.some(block => block.type === 'text')) {
      lastReply = rowKey(e.uuid)
    }

    return next(e)
  })

  on('turn.complete', async ($, e, next) => {
    const images = await read($, pending)

    if (e.agentId === undefined && lastReply !== undefined && images.length > 0) {
      const reply = lastReply
      await update($, attached, all => ({ ...all, [reply]: [...(all[reply] ?? []), ...images] }))
      await update($, pending, () => [])
    }

    return next(e)
  })

  on('ui.render', { component: 'AssistantMessage' }, async ($, e, next) => {
    const images = e.surface === 'terminal' && e.requestId !== undefined ? (await read($, attached))[rowKey(e.requestId)] : undefined

    if (images === undefined || e.surface !== 'terminal') {
      return next(e)
    }

    const { Box, Image } = $.ui.resolve(e)
    const pictures = await Promise.all(
      images.map(async shown => {
        const png = await pngBytes($, shown.file).catch(() => undefined)

        // The bytes, never `file`: Herdr refuses the kitty file medium, and a path means nothing on the app's machine.
        return png === undefined ? [] : [<Image source={{ png }} {...cellBox(shown, e.viewport)} alt={`${shown.source} (${shown.width}x${shown.height})`} />]
      }),
    )

    return (
      <Box flexDirection="column">
        {await next(e)}
        {pictures.flat()}
      </Box>
    )
  })
}
