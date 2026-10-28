import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/api/chatTest.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { createChatStreamParser, extractResponseContent, responseProblem, formatChatError, buildChatRequest, streamChatRequest } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const event = (type, fields = {}) => `event: ${type}\ndata: ${JSON.stringify({ type, ...fields })}\n\n`
const done = event('response.completed', { response: { status: 'completed', output: [] } })

function parse(input, protocol = 'responses', split = false) {
  let text = ''
  const parser = createChatStreamParser(protocol, value => { text += value })
  if (split) for (const character of input) parser.push(character)
  else parser.push(input)
  parser.finish()
  return text
}

test('Responses string delta is displayed; reasoning/tool arguments are not answers', () => {
  const input = event('response.reasoning_summary_text.delta', { delta: 'hidden' }) +
    event('response.function_call_arguments.delta', { delta: '{"hidden":true}' }) +
    event('response.output_text.delta', { delta: '你好' }) + event('response.output_text.delta', { delta: '，世界' }) +
    event('response.output_text.done', { text: '你好，世界' }) +
    event('response.completed', { response: { status: 'completed', output: [{ type: 'message', content: [{ type: 'output_text', text: '你好，世界' }] }] } })
  assert.equal(parse(input), '你好，世界')
})

test('completed snapshot can provide or complete text without duplicate output', () => {
  const snapshot = event('response.completed', { response: { status: 'completed', output: [{ type: 'message', content: [{ type: 'output_text', text: '完整回复' }] }] } })
  assert.equal(parse(snapshot), '完整回复')
  assert.equal(parse(event('response.output_text.delta', { delta: '完整' }) + snapshot), '完整回复')
})

test('SSE event name fallback, comments, multiline data, CRLF and no-space data', () => {
  const input = '\uFEFF: ping\r\n\r\nevent: response.output_text.delta\r\ndata:{"delta":\r\ndata: "你好🙂"}\r\n\r\n' + done.replaceAll('\n', '\r\n')
  assert.equal(parse(input, 'responses', true), '你好🙂')
  assert.equal(parse(input.replaceAll('\r\n', '\r'), 'responses', true), '你好🙂')
})

test('failure, incomplete and truncated streams do not silently succeed', () => {
  for (const [input, pattern] of [
    [event('response.failed', { response: { status: 'failed', error: { message: 'test provider error' } } }), /test provider error/],
    [event('response.incomplete', { response: { status: 'incomplete', incomplete_details: { reason: 'max_output_tokens' } } }), /max_output_tokens/],
    [event('error', { message: 'test stream error' }), /test stream error/],
    [event('response.output_text.delta', { delta: 'partial' }), /提前结束/],
    ['data: invalid json\n\n', /无法解析/],
  ]) assert.throws(() => parse(input), pattern)
})

test('Chat and Anthropic streaming still work', () => {
  assert.equal(parse('data:{"choices":[{"delta":{"content":"chat text"}}]}\n\ndata: [DONE]\n\n', 'openai'), 'chat text')
  assert.equal(parse(event('content_block_delta', { delta: { type: 'text_delta', text: 'anthropic text' } }) + event('message_stop'), 'anthropic'), 'anthropic text')
  assert.throws(() => parse(event('error', { error: { message: 'anthropic error' } }), 'anthropic'), /anthropic error/)
})

test('nonstream Responses text, refusals, tool-only and semantic errors', () => {
  assert.equal(extractResponseContent({ output_text: 'flat text' }, 'responses'), 'flat text')
  assert.equal(extractResponseContent({ output: [{ type: 'message', content: 'string text' }, { type: 'message', content: [{ type: 'refusal', refusal: 'refused' }] }] }, 'responses'), 'string textrefused')
  assert.equal(extractResponseContent({ output: [{ type: 'function_call', arguments: 'not answer' }] }, 'responses'), '')
  assert.equal(responseProblem({ status: 'failed', error: { message: 'failed' } }), 'failed')
  assert.match(responseProblem({ status: 'incomplete', incomplete_details: { reason: 'max_output_tokens' } }), /max_output_tokens/)
  assert.equal(formatChatError({ error: { message: 'object error' } }), 'object error')
})

test('request body uses the selected protocol and explicit stream flag', () => {
  const responses = buildChatRequest('test', '你好', 'responses', true)
  assert.equal(responses.url, '/v1/responses')
  assert.equal(responses.body.input[0].content, '你好')
  assert.equal(responses.body.stream, true)
  assert.equal(buildChatRequest('test', 'hi', 'responses', false).body.stream, false)
  assert.equal(buildChatRequest('test', 'hi', 'anthropic', true).body.max_tokens, 4096)
})

async function runMockResponse(response, protocol = 'responses') {
  const previous = globalThis.fetch
  globalThis.fetch = async () => response
  try {
    return await new Promise(resolve => {
      let text = '', requestId = ''
      streamChatRequest('test-key-not-real', 'test-model', 'hi', protocol,
        delta => { text += delta },
        () => resolve({ text, requestId, done: true }),
        error => resolve({ text, requestId, error }),
        id => { requestId = id },
      )
    })
  } finally { globalThis.fetch = previous }
}

test('UTF-8 decoding preserves Chinese/emoji split into single bytes', async () => {
  const bytes = new TextEncoder().encode(event('response.output_text.delta', { delta: '中文🙂' }) + done)
  const body = new ReadableStream({ start(controller) { for (const byte of bytes) controller.enqueue(new Uint8Array([byte])); controller.close() } })
  const result = await runMockResponse(new Response(body, { headers: { 'Content-Type': 'text/event-stream', 'X-Request-Id': 'req_test' } }))
  assert.deepEqual(result, { text: '中文🙂', requestId: 'req_test', done: true })
})

test('HTTP and HTTP200 SSE errors retain request id and partial answer', async () => {
  const http = await runMockResponse(new Response(JSON.stringify({ error: { message: 'test invalid input' } }), { status: 400, headers: { 'Content-Type': 'application/json', 'X-Request-Id': 'req_bad' } }))
  assert.match(http.error, /HTTP 400: test invalid input/)
  assert.equal(http.requestId, 'req_bad')
  const failed = await runMockResponse(new Response(event('response.output_text.delta', { delta: 'partial' }) + event('response.failed', { response: { error: { message: 'test failed' } } }), { headers: { 'Content-Type': 'text/event-stream', 'X-Request-Id': 'req_failed' } }))
  assert.equal(failed.text, 'partial')
  assert.equal(failed.requestId, 'req_failed')
  assert.equal(failed.error, 'test failed')
})

test('terminal frame finishes and cancels a stream that keeps the connection open', async () => {
  let cancelled = false
  const body = new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode(done)) }, cancel() { cancelled = true } })
  const result = await runMockResponse(new Response(body, { headers: { 'Content-Type': 'text/event-stream' } }))
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(result.done, true)
  assert.equal(cancelled, true)
})

test('JSON fallback supports successful snapshots and semantic failure', async () => {
  const success = await runMockResponse(new Response(JSON.stringify({ output_text: 'json answer' }), { headers: { 'Content-Type': 'application/json' } }))
  assert.equal(success.text, 'json answer')
  assert.equal(success.done, true)
  const failed = await runMockResponse(new Response(JSON.stringify({ status: 'failed', error: { message: 'test semantic failure' } }), { headers: { 'Content-Type': 'application/json' } }))
  assert.equal(failed.error, 'test semantic failure')
})
