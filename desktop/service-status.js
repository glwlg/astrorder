const labels = { running: '运行中', stopped: '已停止', unhealthy: '异常', conflict: '端口冲突', unknown: '状态未知' }
const errorBox = document.querySelector('#error-box')
const errorText = document.querySelector('#error-text')

function showError(msg) {
  if (!msg) {
    if (errorBox) errorBox.classList.remove('visible')
    if (errorText) errorText.textContent = ''
    return
  }
  if (errorText) errorText.textContent = msg
  if (errorBox) errorBox.classList.add('visible')
}

async function refresh() {
  try {
    const states = await window.astrorderDesktop.status()
    for (const target of ['app', 'daemon']) {
      const section = document.querySelector(`[data-target="${target}"]`)
      if (!section) continue
      const state = states[target]?.state || 'unknown'
      const badge = section.querySelector('.status-badge')
      if (badge) {
        badge.className = `status-badge status-${state}`
        const txt = badge.querySelector('.status-text')
        if (txt) txt.textContent = labels[state] || state
      }

      const actions = target === 'daemon' && states.daemon?.startup_task === 'not_installed'
        ? ['install']
        : state === 'running' ? ['restart', 'stop'] : state === 'stopped' ? ['start'] : []

      const actionsContainer = section.querySelector('.card-actions')
      if (actionsContainer) {
        actionsContainer.replaceChildren(...actions.map(action => {
          const button = document.createElement('button')
          button.className = 'btn'
          if (action === 'start' || action === 'install') button.classList.add('btn-primary')
          if (action === 'stop') button.classList.add('btn-danger')
          button.textContent = ({ install: '设置登录启动', start: '启动服务', restart: '重启', stop: '停止' })[action] || action
          button.onclick = async () => {
            button.disabled = true
            showError('')
            try {
              await window.astrorderDesktop.run(target, action)
            } catch (cause) {
              showError(cause.message || String(cause))
            }
            await refresh()
          }
          return button
        }))
      }
    }
  } catch (cause) {
    showError(cause.message || String(cause))
  }
}

refresh()
setInterval(refresh, 3000)
