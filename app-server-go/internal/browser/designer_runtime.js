function browserDesigner(command) {
  const properties = [
    'font-size', 'color', 'background-color', 'width', 'height', 'border-radius', 'gap',
    'padding-top', 'padding-right', 'padding-bottom', 'padding-left',
    'margin-top', 'margin-right', 'margin-bottom', 'margin-left',
  ]
  const key = Symbol.for('astrorder.browserDesigner.v1')
  let state = window[key]
  if (!state || state.url !== location.href) {
    state = {
      id: Date.now().toString(36) + Math.random().toString(36).slice(2),
      url: location.href, selected: null, elements: new Map(), history: [], next: 0,
    }
    window[key] = state
  }
  const fail = message => { throw new Error(message) }
  const connected = entry => {
    if (!entry || !entry.node.isConnected) fail('页面元素已变化，请重新选取；可刷新页面清除临时调整')
    return entry
  }
  const selector = node => {
    const parts = []
    for (let current = node; current && current.nodeType === 1; current = current.parentElement) {
      const tag = current.localName
      if (current.id) {
        const id = '#' + CSS.escape(current.id)
        if (document.querySelectorAll(id).length === 1) { parts.unshift(id); break }
      }
      const testId = current.getAttribute('data-testid')
      if (testId) {
        const test = '[data-testid="' + CSS.escape(testId) + '"]'
        if (document.querySelectorAll(test).length === 1) { parts.unshift(test); break }
      }
      const siblings = current.parentElement
        ? Array.from(current.parentElement.children).filter(sibling => sibling.localName === tag) : [current]
      parts.unshift(tag + (siblings.length > 1 ? ':nth-of-type(' + (siblings.indexOf(current) + 1) + ')' : ''))
    }
    return parts.join(' > ').slice(0, 2048)
  }
  const remember = node => {
    if (!(node instanceof HTMLElement) && !(node instanceof SVGElement)) fail('请选取一个页面元素')
    if (node.localName === 'iframe' || node.localName === 'canvas') fail('暂不支持调整嵌入页面或画布内部的元素')
    for (const entry of state.elements.values()) if (entry.node === node) return entry
    if (state.elements.size >= 100) fail('本次已选取过多元素，请完成修改后刷新页面')
    const entry = {
      id: 'element-' + (++state.next), node, selector: selector(node),
      tag: node.localName, text: (node.innerText || node.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 240),
      classes: Array.from(node.classList).slice(0, 16),
      original: new Map(),
    }
    state.elements.set(entry.id, entry)
    return entry
  }
  const inline = (node, property) => ({
    value: node.style.getPropertyValue(property), priority: node.style.getPropertyPriority(property),
  })
  const restore = (node, property, value) => {
    if (value.value) node.style.setProperty(property, value.value, value.priority)
    else node.style.removeProperty(property)
  }
  const same = (left, right) => left.value === right.value && left.priority === right.priority
  const validateEdits = () => {
    const expected = new Map()
    for (const step of state.history) expected.set(step.entry.id + ':' + step.property, step)
    for (const step of expected.values()) {
      connected(step.entry)
      if (!same(inline(step.entry.node, step.property), step.next)) fail('页面已修改该样式，请重新加载页面后再调整')
    }
  }
  const describe = entry => {
    if (!entry || !entry.node.isConnected) return null
    const rect = entry.node.getBoundingClientRect()
    const style = getComputedStyle(entry.node)
    return {
      id: entry.id, selector: entry.selector, tag: entry.tag, text: entry.text, classes: entry.classes,
      rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
      styles: Object.fromEntries(properties.map(property => [property, style.getPropertyValue(property)])),
      has_parent: Boolean(entry.node.parentElement),
    }
  }
  try {
    if (command.action !== 'state' && command.document_id !== state.id) fail('页面已变化，请刷新预览后重新选取')
    switch (command.action) {
      case 'state': break
      case 'select': {
        const node = document.elementFromPoint(
          Math.min(innerWidth - 1, command.ratio_x * innerWidth),
          Math.min(innerHeight - 1, command.ratio_y * innerHeight),
        )
        state.selected = remember(node)
        break
      }
      case 'parent': {
        const entry = connected(state.selected)
        if (!entry.node.parentElement) fail('已经是最外层元素')
        state.selected = remember(entry.node.parentElement)
        break
      }
      case 'apply': {
        const entry = connected(state.elements.get(command.element_id))
        if (state.selected !== entry) fail('选取的元素已变化，请重新调整')
        const property = command.property
        if (!properties.includes(property) || !CSS.supports(property, command.value)) fail('样式值无效')
        if (state.history.length >= 500) fail('本次调整次数过多，请完成修改后刷新页面')
        if (!entry.original.has(property)) {
          entry.original.set(property, { ...inline(entry.node, property), computed: getComputedStyle(entry.node).getPropertyValue(property) })
        }
        const previous = inline(entry.node, property)
        const original = entry.original.get(property)
        const normalized = document.createElement('span').style
        normalized.setProperty(property, command.value)
        if (normalized.getPropertyValue(property) === original.computed) restore(entry.node, property, original)
        else entry.node.style.setProperty(property, command.value, 'important')
        const next = inline(entry.node, property)
        if (!same(previous, next)) state.history.push({ entry, property, previous, next })
        break
      }
      case 'undo': {
        const last = state.history.at(-1)
        if (!last) break
        connected(last.entry)
        if (!same(inline(last.entry.node, last.property), last.next)) fail('页面已修改该样式，请刷新后重新调整')
        restore(last.entry.node, last.property, last.previous)
        state.history.pop()
        state.selected = last.entry
        break
      }
      case 'reset': {
        validateEdits()
        // Restore only properties touched by this editor, preserving other
        // inline styles and page changes made while the editor was open.
        for (const entry of state.elements.values()) {
          if (!entry.node.isConnected) continue
          for (const [property, original] of entry.original) restore(entry.node, property, original)
        }
        state.history = []
        for (const entry of state.elements.values()) entry.original.clear()
        break
      }
      case 'export':
        validateEdits()
        for (const entry of state.elements.values()) if (entry.original.size) connected(entry)
        break
      default: fail('未知的界面调整操作')
    }
    const changes = []
    for (const entry of state.elements.values()) {
      const edits = []
      for (const [property, original] of entry.original) {
        const current = inline(entry.node, property)
        if (!same(current, original)) edits.push({ property, before: original.computed, after: current.value })
      }
      if (edits.length) changes.push({
        element_id: entry.id, selector: entry.selector, tag: entry.tag, text: entry.text, classes: entry.classes, properties: edits,
      })
    }
    return {
      document_id: state.id, url: location.href, title: document.title,
      viewport: { width: innerWidth, height: innerHeight },
      selected: describe(state.selected), changes, can_undo: state.history.length > 0,
    }
  } catch (error) {
    return { error: error.message }
  }
}
