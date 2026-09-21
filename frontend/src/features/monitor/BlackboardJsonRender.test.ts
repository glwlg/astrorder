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
})
