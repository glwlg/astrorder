import { describe, expect, it } from 'vitest'
import { autoTransformBlackboardToSpec } from './BlackboardJsonRender'

describe('blackboard component contracts', () => {
  it('accepts component as an alias for type', () => {
    const spec = autoTransformBlackboardToSpec('plan', {
      component: 'Checklist',
      props: { title: '计划', items: [{ label: '完成接口', done: true }] },
    })!

    expect(spec.elements[spec.root]).toMatchObject({
      type: 'Checklist',
      props: { title: '计划', items: [{ label: '完成接口', done: true }] },
    })
  })

  it('accepts component at root level without nested props wrapper', () => {
    const spec = autoTransformBlackboardToSpec('llm_gateway_model_sync_development', {
      component: 'Checklist',
      title: 'LLM 网关与模型协同进阶开发清单',
      description: '核心原则...',
      items: [
        { label: '第一项', done: true, assignee: 'Codex' },
        { label: '第二项', done: false, assignee: 'Codex' },
      ],
    })!

    expect(spec.elements[spec.root]).toMatchObject({
      type: 'Checklist',
      props: {
        title: 'LLM 网关与模型协同进阶开发清单',
        items: expect.arrayContaining([{ label: '第一项', done: true, assignee: 'Codex' }]),
      },
    })
  })
})
