const labels = { running: '运行中', stopped: '已停止', unhealthy: '异常', conflict: '端口冲突', unknown: '状态未知' }
const error = document.querySelector('#error')

async function refresh() {
  try {
    const states = await window.astrorderDesktop.status()
    for (const target of ['app', 'daemon']) {
      const section = document.querySelector(`[data-target="${target}"]`)
      const state = states[target].state
      section.querySelector('span').textContent = labels[state] || state
      const actions = target === 'daemon' && states.daemon.startup_task === 'not_installed'
        ? ['install']
        : state === 'running' ? ['restart', 'stop'] : state === 'stopped' ? ['start'] : []
      section.querySelector('div').replaceChildren(...actions.map(action => {
        const button = document.createElement('button')
        button.textContent = ({ install: '设置登录启动', start: '启动', restart: '重启', stop: '停止' })[action]
        button.onclick = async () => {
          button.disabled = true
          error.textContent = ''
          try { await window.astrorderDesktop.run(target, action) }
          catch (cause) { error.textContent = cause.message }
          await refresh()
        }
        return button
      }))
    }
  } catch (cause) {
    error.textContent = cause.message
  }
}

refresh()
