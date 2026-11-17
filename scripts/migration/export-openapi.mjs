// Export the frozen public HTTP contract; no legacy Python runtime required.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const baseline = JSON.parse(fs.readFileSync(path.join(root, 'docs/reports/full-go-baseline.json'), 'utf8'))
const schemaNames = { JevConfigPatch: 'SystemOneConfigPatch', JevHandoffCuratePayload: 'SystemOneHandoffCuratePayload' }
function displayNames(value) {
  if (typeof value === 'string') {
    for (const [oldName, newName] of Object.entries(schemaNames)) value = value.replaceAll(oldName, newName)
    return value.replace(/\bJev\b/g, 'System One')
  }
  if (Array.isArray(value)) return value.map(displayNames)
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, displayNames(item)]))
  return value
}
const paths = {}
for (const route of baseline.inventory.http_routes) {
  if (['/docs', '/docs/oauth2-redirect', '/redoc', '/openapi.json', '/api/v1/tools/jev-filter'].includes(route.path)) continue
  for (const method of route.methods) {
    const input = route.input.schemas[method] || {}
    const responses = route.responses.schemas[method] || {}
    const operation = { operationId: `${route.name.replaceAll('jev', 'system_one')}_${method.toLowerCase()}`, parameters: input.parameters || [], responses: Object.keys(responses).length ? responses : { '200': { description: 'Successful response' } } }
    if (input.request_body) operation.requestBody = input.request_body
    ;(paths[route.path] ||= {})[method.toLowerCase()] = displayNames(operation)
  }
}
const schemas = Object.fromEntries(Object.entries(baseline.schemas.components).map(([name, schema]) => [schemaNames[name] || name, displayNames(schema)]))
delete schemas.JevFilterPayload
const optionalIdentity = (title) => ({ anyOf: [{ type: 'string', maxLength: 256 }, { type: 'null' }], title })
Object.assign(schemas.BotGroupMember.properties, {
  id: optionalIdentity('Member Id'),
  source_session_id: optionalIdentity('Source Session Id'),
  fork_session_id: optionalIdentity('Fork Session Id'),
})
schemas.SendGroupMessagePayload.properties.target_member_id = optionalIdentity('Target Member Id')
const groupMessage = schemas.SendGroupMessagePayload
delete groupMessage.properties.text.minLength
delete groupMessage.required
groupMessage.properties.attachment_ids = {
  type: 'array', maxItems: 32, uniqueItems: true,
  items: { type: 'string', minLength: 1, maxLength: 256 },
  title: 'Attachment Ids',
}
groupMessage.anyOf = [
  { required: ['text'], properties: { text: { minLength: 1 } } },
  { required: ['attachment_ids'], properties: { attachment_ids: { minItems: 1 } } },
]
const document = { openapi: '3.1.0', info: { title: 'Astrorder API', version: '1', description: 'Frozen public compatibility contract. Additional Go endpoints may be available.' }, paths, components: { schemas } }
fs.writeFileSync(path.join(root, 'app-server-go/internal/httpapi/openapi.json'), JSON.stringify(document, null, 2) + '\n')
console.log(`Exported ${Object.keys(paths).length} public paths`)
