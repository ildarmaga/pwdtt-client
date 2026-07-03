package backend

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"wg-turn-client/core"
)

const vkLoginUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var vkLoginAllowedHosts = []string{
	"vk.com", "m.vk.com", "login.vk.com", "id.vk.com", "oauth.vk.com",
	"vk.ru", "m.vk.ru", "api.vk.com", "static.vk.com", "static.vk.ru",
	"st.vk.com", "vkuser.net", "userapi.com", "static.vkontakte.com",
	"ads.vk.com", "persiq.vk.com", "persiq.vk.ru",
}

var vkLoginAssetPrefixes = []string{
	"/js/", "/dist/", "/css/", "/fonts/", "/images/", "/video/", "/audio/", "/mu/", "/mrong/",
}

var (
	vkLoginURLRewrite = regexp.MustCompile(`(?i)(https?:)?//([a-z0-9.-]+\.(?:vk\.com|vk\.ru|vkuser\.net|userapi\.com|vkontakte\.com))(/[^"'>\s\\]*)?`)
	vkLoginBodyTagRe  = regexp.MustCompile(`(?i)<body[^>]*>`)
	vkLoginHeadTagRe  = regexp.MustCompile(`(?i)<head[^>]*>`)
)

type vkLoginSession struct {
	mu       sync.Mutex
	jar      *cookiejar.Jar
	client   *http.Client
	lastHost string
	srv      *http.Server
	baseURL  string
}

var (
	vkLoginMu sync.Mutex
	vkLogin   *vkLoginSession
)

type VKLoginStartResult struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

type VKLoginPollResult struct {
	Done    bool   `json:"done"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func (a *App) StartVKLogin() (VKLoginStartResult, error) {
	vkLoginMu.Lock()
	defer vkLoginMu.Unlock()
	if vkLogin != nil {
		return VKLoginStartResult{URL: vkLogin.baseURL, Active: true}, nil
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return VKLoginStartResult{}, err
	}
	st := &vkLoginSession{
		jar: jar,
		client: &http.Client{
			Jar:     jar,
			Timeout: 0,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		lastHost: "vk.com",
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return VKLoginStartResult{}, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	st.baseURL = fmt.Sprintf("http://127.0.0.1:%d/", port)

	mux := http.NewServeMux()
	mux.HandleFunc("/", st.handleAll)

	st.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}

	go func() {
		_ = st.srv.Serve(ln)
	}()

	vkLogin = st
	return VKLoginStartResult{URL: st.baseURL, Active: true}, nil
}

func (a *App) StopVKLogin() {
	vkLoginMu.Lock()
	defer vkLoginMu.Unlock()
	stopVKLoginLocked()
}

func stopVKLoginLocked() {
	if vkLogin == nil {
		return
	}
	srv := vkLogin.srv
	vkLogin = nil
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

func (a *App) PollVKLogin() VKLoginPollResult {
	vkLoginMu.Lock()
	st := vkLogin
	vkLoginMu.Unlock()
	if st == nil {
		return VKLoginPollResult{Status: "idle"}
	}

	header, ok := vkHarvestCookieHeader(st.jar)
	if !ok {
		return VKLoginPollResult{Status: "waiting", Message: "Войдите в VK — ожидаем remixsid…"}
	}

	payload := []byte(header)
	if err := core.SaveVKCookiesJSON(payload); err != nil {
		return VKLoginPollResult{Status: "error", Message: err.Error()}
	}
	_ = core.SetVKUseCookies(true)
	stopVKLoginLocked()
	return VKLoginPollResult{Done: true, Status: "done", Message: "Cookies сохранены"}
}

func vkHarvestCookieHeader(jar *cookiejar.Jar) (string, bool) {
	if jar == nil {
		return "", false
	}
	var remixsid, pCookie *http.Cookie
	urls := []string{
		"https://vk.com/",
		"https://login.vk.com/",
		"https://id.vk.com/",
		"https://m.vk.com/",
		"https://oauth.vk.com/",
	}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		for _, c := range jar.Cookies(u) {
			dom := strings.ToLower(c.Domain)
			if !strings.HasPrefix(dom, ".") {
				dom = "." + dom
			}
			if c.Name == "remixsid" && strings.HasSuffix(dom, ".vk.com") && c.Value != "" {
				remixsid = c
			}
			if c.Name == "p" && strings.HasSuffix(dom, ".login.vk.com") && c.Value != "" {
				pCookie = c
			}
		}
	}
	if remixsid == nil {
		return "", false
	}
	header := "remixsid=" + remixsid.Value
	if pCookie != nil {
		header += "; p=" + pCookie.Value
	}
	return header, true
}

func (st *vkLoginSession) loginPrefix() string {
	return strings.TrimSuffix(st.baseURL, "/") + "/"
}

func (st *vkLoginSession) handleAll(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if p == "/" || p == "" {
		target, err := url.Parse("https://vk.com/")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		st.proxyRequest(w, r, target)
		return
	}
	if strings.HasPrefix(p, "/h/") {
		st.handleProxy(w, r)
		return
	}
	for _, prefix := range vkLoginAssetPrefixes {
		if strings.HasPrefix(p, prefix) || p == "/js/sw.js" {
			st.handleAssetFallback(w, r)
			return
		}
	}
	http.NotFound(w, r)
}

func (st *vkLoginSession) handleAssetFallback(w http.ResponseWriter, r *http.Request) {
	host := vkLoginResolveAssetHost(r, st)
	target, err := url.Parse("https://" + host + r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	target.RawQuery = r.URL.RawQuery
	st.proxyRequest(w, r, target)
}

func (st *vkLoginSession) handleProxy(w http.ResponseWriter, r *http.Request) {
	target, err := vkLoginParseTarget(r, st.loginPrefix())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st.proxyRequest(w, r, target)
}

func (st *vkLoginSession) proxyRequest(w http.ResponseWriter, r *http.Request, target *url.URL) {
	st.mu.Lock()
	st.lastHost = target.Host
	st.mu.Unlock()

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("User-Agent", vkLoginUserAgent)
	req.Header.Set("Accept", r.Header.Get("Accept"))
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Accept-Encoding", "identity")
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	prefix := st.loginPrefix()
	if ref := r.Header.Get("Referer"); ref != "" {
		req.Header.Set("Referer", vkLoginRewriteURL(ref, prefix))
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		req.Header.Set("Origin", "https://"+target.Host)
	}

	resp, err := st.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if loc := resp.Header.Get("Location"); loc != "" {
		if rl := vkLoginRewriteURL(loc, prefix); rl != loc {
			vkLoginCopyHeader(w.Header(), resp.Header)
			w.Header().Set("Location", rl)
			w.WriteHeader(resp.StatusCode)
			return
		}
	}

	ct := resp.Header.Get("Content-Type")
	if !vkLoginNeedsRewrite(ct) {
		vkLoginCopyHeader(w.Header(), resp.Header)
		w.Header().Set("Content-Type", vkLoginGuessContentType(target.Path, ct))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	body, err := vkLoginReadBody(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	body = vkLoginRewriteBody(body, prefix, target.Host, ct)

	hdr := w.Header()
	vkLoginCopyHeader(hdr, resp.Header)
	hdr.Del("Content-Encoding")
	hdr.Del("Content-Length")
	hdr.Set("Content-Type", vkLoginGuessContentType(target.Path, ct))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func vkLoginParseTarget(r *http.Request, _ string) (*url.URL, error) {
	rest := strings.TrimPrefix(r.URL.Path, "/h/")
	if rest != r.URL.Path {
		slash := strings.Index(rest, "/")
		host := rest
		p := "/"
		if slash >= 0 {
			host = rest[:slash]
			p = rest[slash:]
		}
		if !vkLoginHostAllowed(host) {
			return nil, fmt.Errorf("host not allowed")
		}
		u, err := url.Parse("https://" + host + p)
		if err != nil {
			return nil, err
		}
		u.RawQuery = r.URL.RawQuery
		return u, nil
	}
	u, err := url.Parse("https://vk.com" + r.URL.Path)
	if err != nil {
		return nil, err
	}
	u.RawQuery = r.URL.RawQuery
	return u, nil
}

func vkLoginHostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, h := range vkLoginAllowedHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func vkLoginRewriteURL(raw, proxyPrefix string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	if !vkLoginHostAllowed(u.Host) {
		return raw
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		p += "#" + u.Fragment
	}
	return proxyPrefix + "h/" + u.Host + p
}

func vkLoginRewriteAbsolute(body, proxyPrefix string) string {
	return vkLoginURLRewrite.ReplaceAllStringFunc(body, func(m string) string {
		sub := vkLoginURLRewrite.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		host := sub[2]
		p := sub[3]
		if p == "" {
			p = "/"
		}
		return proxyPrefix + "h/" + host + p
	})
}

func vkLoginCDNHost(pageHost string) string {
	pageHost = strings.ToLower(strings.TrimSpace(pageHost))
	if strings.HasPrefix(pageHost, "st") && strings.HasSuffix(pageHost, ".vk.com") {
		return pageHost
	}
	return "st1-15.vk.com"
}

func vkLoginRewriteRootPaths(body, proxyPrefix, host string) string {
	cdn := vkLoginCDNHost(host)
	base := proxyPrefix + "h/" + cdn
	for _, p := range vkLoginAssetPrefixes {
		repl := base + p
		body = strings.ReplaceAll(body, `"`+p, `"`+repl)
		body = strings.ReplaceAll(body, "'"+p, "'"+repl)
	}
	return body
}

func vkLoginRewriteText(body, proxyPrefix, host string) string {
	body = vkLoginRewriteAbsolute(body, proxyPrefix)
	body = vkLoginRewriteRootPaths(body, proxyPrefix, host)
	return body
}

func vkLoginInjectHooks(html, proxyPrefix, host string) string {
	escPrefix, escHost := vkLoginJSONEscape(proxyPrefix), vkLoginJSONEscape(host)
	script := `<script>(function(){var P=` + escPrefix + `,H=` + escHost + `;function map(u){if(!u||typeof u!=="string")return u;if(u.charAt(0)==="/"&&u.charAt(1)!=="/")return P+"h/"+H+u;try{var x=new URL(u,location.href);if(/\.(vk\.com|vk\.ru|vkuser\.net|userapi\.com|vkontakte\.com)$/i.test(x.hostname))return P+"h/"+x.hostname+x.pathname+x.search+x.hash;}catch(e){}return u;}function mapWs(u){if(!u||typeof u!=="string")return u;var s=u.replace(/^wss:/i,"https:").replace(/^ws:/i,"http:");var m=map(s);if(m!==s)return m.replace(/^https:/i,"wss:").replace(/^http:/i,"ws:");return u;}var f=window.fetch;window.fetch=function(i,o){if(typeof i==="string")i=map(i);else if(i&&i.url)i=new Request(map(i.url),i);return f.call(this,i,o);};var xo=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(m,u){arguments[1]=map(u);return xo.apply(this,arguments);};var wo=window.open;window.open=function(u,n,fe){u=map(u);if(u&&u.indexOf(P)===0){location.href=u;return window;}return wo.call(window,u,n,fe);};var la=location.assign.bind(location);location.assign=function(u){return la(map(u));};var lr=location.replace.bind(location);location.replace=function(u){return lr(map(u));};try{var desc=Object.getOwnPropertyDescriptor(Location.prototype,"href");if(desc&&desc.set){var gs=desc.get,ss=desc.set;Object.defineProperty(location,"href",{configurable:true,enumerable:true,get:function(){return gs.call(location);},set:function(v){return ss.call(location,map(v));}});}}catch(e){}try{var WS=window.WebSocket;window.WebSocket=function(u,p){return new WS(mapWs(u),p);};}catch(e2){}})();</script>`
	loc := vkLoginHeadTagRe.FindStringIndex(html)
	if loc == nil {
		return script + html
	}
	pos := loc[1]
	return html[:pos] + script + html[pos:]
}

func vkLoginJSONEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func vkLoginInjectBanner(html string) string {
	banner := `<div style="position:sticky;top:0;z-index:999999;background:#1677ff;color:#fff;padding:8px 12px;font:14px/1.4 sans-serif;text-align:center">WDTT — войдите в VK. Cookies сохранятся автоматически.</div>`
	loc := vkLoginBodyTagRe.FindStringIndex(html)
	if loc == nil {
		return banner + html
	}
	pos := loc[1]
	return html[:pos] + banner + html[pos:]
}

func vkLoginRewriteBody(body []byte, proxyPrefix, host, contentType string) []byte {
	s := string(body)
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "text/html"):
		s = vkLoginRewriteText(s, proxyPrefix, host)
		s = vkLoginInjectHooks(s, proxyPrefix, host)
		s = vkLoginInjectBanner(s)
	case strings.Contains(ct, "javascript"), strings.Contains(ct, "json"), strings.Contains(ct, "text/css"):
		s = vkLoginRewriteText(s, proxyPrefix, host)
	default:
		return body
	}
	return []byte(s)
}

func vkLoginNeedsRewrite(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/html") ||
		strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "json") ||
		strings.Contains(ct, "text/css")
}

func vkLoginReadBody(resp *http.Response) ([]byte, error) {
	var r io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		r = gr
	}
	return io.ReadAll(r)
}

func vkLoginGuessContentType(name, upstream string) string {
	if upstream != "" && !strings.HasPrefix(strings.ToLower(upstream), "text/plain") {
		return upstream
	}
	if ext := path.Ext(name); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	return upstream
}

func vkLoginResolveAssetHost(r *http.Request, st *vkLoginSession) string {
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		if err == nil {
			marker := "/h/"
			idx := strings.Index(u.Path, marker)
			if idx >= 0 {
				rest := u.Path[idx+len(marker):]
				slash := strings.Index(rest, "/")
				host := rest
				if slash >= 0 {
					host = rest[:slash]
				}
				if vkLoginHostAllowed(host) {
					return vkLoginCDNHost(host)
				}
			}
		}
	}
	st.mu.Lock()
	host := st.lastHost
	st.mu.Unlock()
	return vkLoginCDNHost(host)
}

func vkLoginCopyHeader(dst, src http.Header) {
	skip := map[string]bool{
		"content-encoding": true, "content-length": true,
		"content-security-policy": true, "content-security-policy-report-only": true,
		"x-frame-options": true, "cross-origin-opener-policy": true,
		"cross-origin-embedder-policy": true, "cross-origin-resource-policy": true,
	}
	for k, vv := range src {
		if skip[strings.ToLower(k)] {
			continue
		}
		for _, v := range vv {
			if strings.EqualFold(k, "Location") {
				continue
			}
			dst.Add(k, v)
		}
	}
}
