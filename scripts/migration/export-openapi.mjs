// Export the frozen public HTTP contract; no legacy Python runtime required.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const baseline = JSON.parse(fs.readFileSync(path.join(root, 'docs/reports/full-go-baseline.json'), 'utf8'))
const paths = {}
for (const route of baseline.inventory.http_routes) {
  if (['/docs', '/docs/oauth2-redirect', '/redoc', '/openapi.json'].includes(route.path)) continue
  for (const method of route.methods) {
    const input = route.input.schemas[method] || {}
    const responses = route.responses.schemas[method] || {}
    const operation = { operationId: `${route.name}_${method.toLowerCase()}`, parameters: input.parameters || [], responses: Object.keys(responses).length ? responses : { '200': { description: 'Successful response' } } }
    if (input.request_body) operation.requestBody = input.request_body
    ;(paths[route.path] ||= {})[method.toLowerCase()] = operation
  }
}
const document = { openapi: '3.1.0', info: { title: 'Astrorder API', version: '1', description: 'Frozen public compatibility contract. Additional Go endpoints may be available.' }, paths, components: { schemas: baseline.schemas.components } }
fs.writeFileSync(path.join(root, 'app-server-go/internal/httpapi/openapi.json'), JSON.stringify(document, null, 2) + '\n')
console.log(`Exported ${Object.keys(paths).length} public paths`)
