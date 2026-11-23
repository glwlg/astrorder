export interface FetchLaterInit extends RequestInit {
  activateAfter?: number
}

export interface FetchLaterResult {
  readonly activated: boolean
}

type FetchLaterFn = (input: RequestInfo | URL, init?: FetchLaterInit) => FetchLaterResult

declare global {
  // eslint-disable-next-line no-var
  var fetchLater: FetchLaterFn | undefined
}

/**
 * Installs a minimal `fetchLater()` polyfill when the host browser does not natively
 * support the Deferred Fetch API (e.g. Safari/Firefox), falling back to `fetch(..., { keepalive: true })`
 * or `navigator.sendBeacon()` on `visibilitychange` / `activateAfter`.
 */
export function ensureFetchLater(): FetchLaterFn | null {
  if (typeof globalThis === 'undefined' || typeof document === 'undefined') return null
  if (typeof globalThis.fetchLater === 'function') return globalThis.fetchLater

  const polyfill: FetchLaterFn = (url, init = {}) => {
    let timeoutHandle: ReturnType<typeof setTimeout> | undefined
    let activated = false

    function sendNow() {
      if (!(init.signal && init.signal.aborted)) {
        const supportsKeepalive = typeof Request !== 'undefined' && 'keepalive' in Request.prototype
        const hasHeaders = Boolean(init.headers)
        if (supportsKeepalive || init.method !== 'POST' || hasHeaders) {
          void fetch(url, Object.assign({}, init, { keepalive: true })).catch(() => undefined)
          activated = true
        } else if (typeof navigator !== 'undefined' && typeof navigator.sendBeacon === 'function') {
          activated = navigator.sendBeacon(
            typeof url === 'string' || url instanceof URL ? url : url.url,
            (init.body ?? null) as BodyInit | null,
          )
        }
      }
      destroy()
    }

    function onVisibilityChange() {
      if (document.visibilityState === 'hidden') sendNow()
    }

    function destroy() {
      document.removeEventListener('visibilitychange', onVisibilityChange)
      if (timeoutHandle !== undefined) clearTimeout(timeoutHandle)
      timeoutHandle = undefined
    }

    if (document.visibilityState === 'hidden') {
      queueMicrotask(sendNow)
    } else {
      document.addEventListener('visibilitychange', onVisibilityChange)
      if (typeof init.activateAfter === 'number' && init.activateAfter >= 0) {
        timeoutHandle = setTimeout(sendNow, init.activateAfter)
      }
    }

    if (init.signal) {
      if (init.signal.aborted) destroy()
      else init.signal.addEventListener('abort', destroy, { once: true })
    }

    return {
      get activated() {
        return activated
      },
    }
  }

  globalThis.fetchLater = polyfill
  return polyfill
}
