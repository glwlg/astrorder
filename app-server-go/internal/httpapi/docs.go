package httpapi

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.json
var openAPI []byte

func init() {
	for _, path := range []string{"/openapi.json", "/docs", "/redoc", "/docs/oauth2-redirect"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			registeredRoutes = append(registeredRoutes, registeredRoute{Registration: Registration{Kind: KindHTTP, Method: method, Path: path, Auth: AuthPublic, Ownership: OwnershipGo}, serve: serveAPIDocs})
		}
	}
}
func serveAPIDocs(_ *api, w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/openapi.json" {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodHead {
			w.Write(openAPI)
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	w.Write([]byte(`<!doctype html><html lang="zh"><meta charset="utf-8"><title>Astrorder API</title><style>body{font:16px system-ui;max-width:1100px;margin:32px auto;padding:16px}input{width:100%;padding:10px;box-sizing:border-box}article{padding:12px;border-bottom:1px solid #aaa}code{font-size:14px}pre{overflow:auto}</style><h1>Astrorder API</h1><p>公开兼容接口契约。<a href="/openapi.json">下载 OpenAPI JSON</a></p><input id="filter" aria-label="筛选接口" placeholder="筛选路径或方法"><main id="routes"></main><script>fetch('/openapi.json').then(r=>r.json()).then(spec=>{const rows=[];for(const [path,methods] of Object.entries(spec.paths)){for(const [method,operation] of Object.entries(methods)){const row=document.createElement('article');const title=document.createElement('code');title.textContent=method.toUpperCase()+' '+path;row.append(title);const detail=document.createElement('details');const summary=document.createElement('summary');summary.textContent='请求与响应';const pre=document.createElement('pre');pre.textContent=JSON.stringify(operation,null,2);detail.append(summary,pre);row.append(detail);document.getElementById('routes').append(row);rows.push(row)}}document.getElementById('filter').oninput=e=>{for(const row of rows)row.hidden=!row.textContent.toLowerCase().includes(e.target.value.toLowerCase())}}).catch(()=>{document.getElementById('routes').textContent='接口契约加载失败'})</script></html>`))
}
