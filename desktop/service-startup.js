async function startServices(control) {
  const states = {}
  for (const target of ['daemon', 'app']) {
    const result = await control(target, 'start')
    if (!result || result.ok === false || result.state !== 'running') {
      throw new Error(result?.message || `${target} 启动失败`)
    }
    states[target] = result
  }
  return states
}

module.exports = { startServices }
