import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const identifier = /^[A-Za-z_$][\w$]*$/
const quote = value => JSON.stringify(value)

/** Small, strict OpenAPI 3 schema renderer. Unsupported constructs fail loudly. */
export function generateTypes(spec) {
  const schemas = spec.components?.schemas
  if (!spec.openapi?.startsWith('3.') || !schemas || !Object.keys(schemas).length) {
    throw new Error('Expected OpenAPI 3 with non-empty components.schemas')
  }
  const render = schema => {
    if (schema === true) return 'unknown'
    if (schema === false) return 'never'
    if (!schema || typeof schema !== 'object') throw new Error('Invalid schema')
    for (const keyword of ['not', 'if', 'then', 'else', 'patternProperties', 'prefixItems']) {
      if (keyword in schema) throw new Error(`Unsupported schema keyword: ${keyword}`)
    }
    let result
    if (schema.$ref) {
      const prefix = '#/components/schemas/'
      if (!schema.$ref.startsWith(prefix)) throw new Error(`Unsupported external reference: ${schema.$ref}`)
      const name = schema.$ref.slice(prefix.length)
      if (!identifier.test(name) || !(name in schemas)) throw new Error(`Unknown schema reference: ${name}`)
      result = name
    } else if ('const' in schema) result = quote(schema.const)
    else if (schema.enum) result = schema.enum.map(quote).join(' | ') || 'never'
    else if (schema.oneOf || schema.anyOf) result = `(${(schema.oneOf || schema.anyOf).map(render).join(' | ')})`
    else if (schema.allOf) result = `(${schema.allOf.map(render).join(' & ')})`
    else if (Array.isArray(schema.type)) result = schema.type.map(type => render({ ...schema, type, nullable: false })).join(' | ')
    else {
      switch (schema.type) {
        case 'null': result = 'null'; break
        case 'string': result = 'string'; break
        case 'integer': case 'number': result = 'number'; break
        case 'boolean': result = 'boolean'; break
        case 'array':
          if (!schema.items) throw new Error('Array schema requires items')
          result = `Array<${render(schema.items)}>`; break
        case 'object': case undefined: {
          if (schema.type === undefined && !schema.properties && !('additionalProperties' in schema)) {
            if (Object.keys(schema).every(key => ['description', 'title', 'example', 'nullable'].includes(key))) { result = 'unknown'; break }
            throw new Error(`Missing schema type: ${JSON.stringify(schema)}`)
          }
          const properties = Object.entries(schema.properties || {}).map(([name, value]) =>
            `  ${quote(name)}${schema.required?.includes(name) ? '' : '?'}: ${render(value)};`)
          result = properties.length ? `{\n${properties.join('\n')}\n}` : 'Record<string, never>'
          if (schema.additionalProperties) {
            const record = `Record<string, ${render(schema.additionalProperties)}>`
            result = properties.length ? `(${result} & ${record})` : record
          }
          break
        }
        default: throw new Error(`Unsupported schema type: ${schema.type}`)
      }
    }
    return schema.nullable ? `(${result} | null)` : result
  }
  const declarations = Object.entries(schemas).sort(([a], [b]) => a.localeCompare(b, 'en')).map(([name, schema]) => {
    if (!identifier.test(name)) throw new Error(`Invalid TypeScript schema name: ${name}`)
    return `export type ${name} = ${render(schema)}\n`
  })
  return '// Generated from api/openapi.json by web/scripts/generate-api.mjs. Do not edit.\n\n' + declarations.join('\n')
}

export async function main(args) {
  const check = args.includes('--check')
  const value = flag => { const index = args.indexOf(flag); return index < 0 ? undefined : args[index + 1] }
  const input = resolve(value('--input') || resolve(here, '../../api/openapi.json'))
  const output = resolve(value('--output') || resolve(here, '../src/generated/api.ts'))
  const text = generateTypes(JSON.parse(await readFile(input, 'utf8')))
  if (check) {
    if (await readFile(output, 'utf8') !== text) throw new Error('Generated API types are stale. Run npm run generate:api.')
    console.log('Generated API types are current.')
  } else {
    await mkdir(dirname(output), { recursive: true })
    await writeFile(output, text, 'utf8')
    console.log(`Generated ${output}`)
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch(error => { console.error(error); process.exitCode = 1 })
}
