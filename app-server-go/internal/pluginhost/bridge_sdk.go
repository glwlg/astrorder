package pluginhost

import "net/http"

const bridgeSDKJS = `(function () {
  if (typeof window === 'undefined' || window.astrorderPlugin) {
    return;
  }

  var nextId = 1;
  var pending = Object.create(null);
  var contextListeners = [];
  var lastContext = null;

  function sendRequest(type, extra, timeoutMs) {
    return new Promise(function (resolve, reject) {
      if (!window.parent || window.parent === window) {
        reject(new Error('Astrorder plugin bridge requires parent host window'));
        return;
      }
      var requestId = 'ext-' + (nextId++);
      var timer = setTimeout(function () {
        delete pending[requestId];
        reject(new Error('Astrorder plugin bridge request timed out: ' + type));
      }, timeoutMs || 30000);

      pending[requestId] = {
        resolve: resolve,
        reject: reject,
        timer: timer
      };

      var msg = { type: type, id: requestId, requestId: requestId };
      if (extra && typeof extra === 'object') {
        var keys = Object.keys(extra);
        for (var i = 0; i < keys.length; i++) {
          msg[keys[i]] = extra[keys[i]];
        }
      }
      window.parent.postMessage(msg, '*');
    });
  }

  function sendFireAndForget(type, extra) {
    if (!window.parent || window.parent === window) {
      return;
    }
    var msg = { type: type };
    if (extra && typeof extra === 'object') {
      var keys = Object.keys(extra);
      for (var i = 0; i < keys.length; i++) {
        msg[keys[i]] = extra[keys[i]];
      }
    }
    window.parent.postMessage(msg, '*');
  }

  window.addEventListener('message', function (event) {
    if (event.source !== window.parent) {
      return;
    }
    var msg = event.data;
    if (!msg || typeof msg !== 'object') {
      return;
    }
    if (msg.type === 'astrorder:context-changed') {
      var ctx = msg.context || msg.payload || null;
      lastContext = ctx;
      for (var i = 0; i < contextListeners.length; i++) {
        try {
          contextListeners[i](ctx);
        } catch (_) {}
      }
      return;
    }
    if (msg.type === 'astrorder:response') {
      var reqId = msg.requestId !== undefined ? msg.requestId : msg.id;
      var entry = pending[reqId];
      if (!entry) {
        return;
      }
      clearTimeout(entry.timer);
      delete pending[reqId];
      if (msg.ok === false && msg.error && !msg.data && !msg.result) {
        entry.reject(new Error(String(msg.error)));
        return;
      }
      var res = msg.data !== undefined ? msg.data : (msg.result !== undefined ? msg.result : msg.context);
      entry.resolve(res);
    }
  });

  window.astrorderPlugin = {
    getContext: function () {
      return sendRequest('astrorder:get-context', {}, 10000).then(function (ctx) {
        lastContext = ctx;
        return ctx;
      });
    },

    onContextChange: function (cb) {
      if (typeof cb !== 'function') {
        return function () {};
      }
      contextListeners.push(cb);
      if (lastContext) {
        try {
          cb(lastContext);
        } catch (_) {}
      }
      return function () {
        var idx = contextListeners.indexOf(cb);
        if (idx >= 0) {
          contextListeners.splice(idx, 1);
        }
      };
    },

    fetch: function (path, init) {
      var safeInit = init || {};
      return sendRequest('astrorder:fetch', { path: String(path || ''), init: safeInit }, 30000);
    },

    toast: function (message, tone) {
      sendFireAndForget('astrorder:toast', { message: String(message || ''), tone: tone || 'info' });
    },

    openPanel: function (panelId, params) {
      sendFireAndForget('astrorder:open-panel', { panelId: String(panelId || ''), params: params || {} });
    },

    insertToComposer: function (text) {
      sendFireAndForget('astrorder:insert-composer', { text: String(text || '') });
    },

    notifyResize: function (height) {
      var h = typeof height === 'number' && isFinite(height) && height > 0
        ? Math.round(height)
        : Math.max(
            document.documentElement ? document.documentElement.scrollHeight : 0,
            document.body ? document.body.scrollHeight : 0
          );
      if (h > 0) {
        sendFireAndForget('astrorder:resize', { height: h });
      }
    }
  };
})();
`

func serveBridgeSDK(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(bridgeSDKJS))
	}
}
