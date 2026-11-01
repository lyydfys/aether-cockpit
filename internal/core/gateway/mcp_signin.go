package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/library"
)

// MCP OAuth 登录（magpie mcp-signin 的对等实现）：
// 浏览器授权一次，令牌存在本机（0600）；之后
//   - 健康检查用令牌直接访问上游；
//   - 下发给 Agent 的配置改写成 http://127.0.0.1:<port>/mcp/<name>，
//     由网关注入 Authorization 并转发，Agent 侧不再需要各自的登录。
// 退出登录会删除令牌并把已下发的配置还原成上游地址。

type mcpAuthRecord struct {
	Name       string      `json:"name"`
	Upstream   string      `json:"upstream"`
	Token      oauthToken  `json:"token"`
	Client     oauthClient `json:"client"`
	Meta       oauthASMeta `json:"meta"`
	Resource   string      `json:"resource"`
	SignedInAt int64       `json:"signed_in_at"`
	Dead       bool        `json:"dead,omitempty"`
}

type mcpAuthStore struct {
	mu   sync.Mutex
	path string
	m    map[string]*mcpAuthRecord
	// refreshMu 串行化续期：轮换型 refresh_token 下并发续期会互相作废。
	refreshMu sync.Mutex
}

func newMCPAuthStore(path string) *mcpAuthStore {
	s := &mcpAuthStore{path: path, m: map[string]*mcpAuthRecord{}}
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		var list []*mcpAuthRecord
		if json.Unmarshal(raw, &list) == nil {
			for _, r := range list {
				if r != nil && r.Name != "" {
					s.m[r.Name] = r
				}
			}
		}
	}
	return s
}

func (s *mcpAuthStore) get(name string) *mcpAuthRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.m[name]
	if r == nil {
		return nil
	}
	cp := *r
	return &cp
}

func (s *mcpAuthStore) put(r *mcpAuthRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[r.Name] = r
	return s.saveLocked()
}

func (s *mcpAuthStore) delete(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[name]; !ok {
		return
	}
	delete(s.m, name)
	_ = s.saveLocked()
}

func (s *mcpAuthStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	list := make([]*mcpAuthRecord, 0, len(s.m))
	for _, r := range s.m {
		list = append(list, r)
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

type signinFlow struct {
	id        string
	name      string
	state     string // waiting / done / canceled / failed
	err       string
	url       string
	createdAt time.Time
	cancel    context.CancelFunc
}

type mcpSigninFlows struct {
	mu sync.Mutex
	m  map[string]*signinFlow
}

func (f *mcpSigninFlows) put(flow *signinFlow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]*signinFlow{}
	}
	now := time.Now()
	for id, old := range f.m {
		if now.Sub(old.createdAt) > 30*time.Minute {
			delete(f.m, id)
		}
	}
	f.m[flow.id] = flow
}

func (f *mcpSigninFlows) get(id string) *signinFlow {
	f.mu.Lock()
	defer f.mu.Unlock()
	flow := f.m[id]
	if flow == nil {
		return nil
	}
	cp := *flow
	return &cp
}

func (f *mcpSigninFlows) set(id, state, errText string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if flow := f.m[id]; flow != nil {
		flow.state = state
		flow.err = errText
	}
}

// mcpAuth 懒初始化（New 里设置路径）。
func (g *Gateway) mcpAuthStore() *mcpAuthStore {
	g.mcpAuthOnce.Do(func() {
		path := filepath.Join(filepath.Dir(g.settingsPath), "mcp-auth.json")
		if g.mcpAuth != nil {
			return
		}
		g.mcpAuth = newMCPAuthStore(path)
	})
	return g.mcpAuth
}

func (g *Gateway) relayBaseURL() string {
	addr := strings.TrimSpace(g.cfg.Addr)
	if v := strings.TrimSpace(g.settings.ListenAddr); v != "" {
		addr = v
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "7860"
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return "http://" + host + ":" + port
	}
	return "http://127.0.0.1:" + port
}

// mcpBearerFor 返回可用的访问令牌；过期时自动续期。
// 续期失败只在授权服务器明确拒绝时标记 dead（网络抖动不应让用户重新登录）。
func (g *Gateway) mcpBearerFor(name, upstream string) (string, bool) {
	store := g.mcpAuthStore()
	rec := store.get(name)
	if rec == nil || rec.Upstream != upstream || rec.Dead {
		return "", false
	}
	if !rec.Token.expired(time.Now()) {
		return rec.Token.AccessToken, true
	}
	store.refreshMu.Lock()
	defer store.refreshMu.Unlock()
	// 等锁期间别的请求可能已经续期成功，重新读一次。
	rec = store.get(name)
	if rec == nil || rec.Upstream != upstream || rec.Dead {
		return "", false
	}
	if !rec.Token.expired(time.Now()) {
		return rec.Token.AccessToken, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	next, err := refreshOAuthToken(ctx, rec.Meta, rec.Client, rec.Token, rec.Resource, oauthHTTPClient())
	if err != nil {
		if errors.Is(err, errOAuthRejected) {
			rec.Dead = true
			_ = store.put(rec)
		}
		return "", false
	}
	rec.Token = next
	_ = store.put(rec)
	return next.AccessToken, true
}

func (g *Gateway) mcpSignInView(name, upstream string) map[string]any {
	rec := g.mcpAuthStore().get(name)
	if rec == nil || rec.Upstream != upstream {
		return map[string]any{"signedIn": false}
	}
	if rec.Dead {
		return map[string]any{"signedIn": false, "dead": true}
	}
	return map[string]any{"signedIn": true}
}

// rewriteMCPForRelay 在写入 Agent 配置前把 URL 指向本机中继（若已登录）。
func (g *Gateway) rewriteMCPForRelay(name string, cfg map[string]any) {
	upstream, _ := cfg["url"].(string)
	if strings.TrimSpace(upstream) == "" {
		return
	}
	if _, ok := g.mcpBearerFor(name, upstream); !ok {
		return
	}
	cfg["url"] = g.relayBaseURL() + "/mcp/" + url.PathEscape(name)
	if headers, ok := cfg["headers"].(map[string]any); ok {
		for k := range headers {
			if strings.EqualFold(k, "Authorization") {
				delete(headers, k)
			}
		}
	}
}

func mcpServerURLOf(g *Gateway, name string) (string, error) {
	for _, m := range g.library.List().MCPServers {
		if m.Name != name {
			continue
		}
		var cfg struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(m.Config, &cfg); err != nil {
			return "", fmt.Errorf("服务器配置不是合法 JSON: %w", err)
		}
		if strings.TrimSpace(cfg.URL) == "" {
			return "", fmt.Errorf("这是本地 stdio 服务器，不需要登录")
		}
		return cfg.URL, nil
	}
	return "", fmt.Errorf("库中没有这个 MCP 服务器: %s", name)
}

// handleMCPSigninStart POST /api/library/mcp-signin {name}
//
// 同步完成 discovery 与动态注册（≤20s），返回 {id, state:"waiting", url}；
// 浏览器授权由前端打开，回调落在本机临时端口。
func (g *Gateway) handleMCPSigninStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = strings.TrimSpace(r.URL.Query().Get("name"))
	}
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少服务器名"})
		return
	}
	upstream, err := mcpServerURLOf(g, name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var presetOAuth *library.MCPOAuthClient
	for _, m := range g.library.List().MCPServers {
		if m.Name == name {
			presetOAuth = m.OAuth
			break
		}
	}
	store := g.mcpAuthStore()
	if rec := store.get(name); rec != nil && rec.Upstream == upstream && !rec.Dead {
		writeJSON(w, http.StatusOK, map[string]any{"id": "", "state": "done", "signedIn": true})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	hc := oauthHTTPClient()
	metaURL, scope, err := probeMCPAuth(ctx, upstream, hc)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "无法开始登录: " + err.Error()})
		return
	}
	resMeta, err := discoverResource(ctx, upstream, metaURL, hc)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "无法读取受保护资源元数据: " + err.Error()})
		return
	}
	resource := resMeta.Resource
	if resource == "" {
		resource = upstream
	}
	if scope == "" && len(resMeta.ScopesSupported) > 0 {
		scope = strings.Join(resMeta.ScopesSupported, " ")
	}
	asURL := resMeta.AuthorizationServers[0]
	asMeta, err := discoverAS(ctx, asURL, hc)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "无法读取授权服务器元数据: " + err.Error()})
		return
	}
	listenAddr := "127.0.0.1:0"
	if presetOAuth != nil && presetOAuth.CallbackPort > 0 {
		listenAddr = fmt.Sprintf("127.0.0.1:%d", presetOAuth.CallbackPort)
	}
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "无法监听回调端口 " + listenAddr + ": " + err.Error()})
		return
	}
	redirectHost := "127.0.0.1"
	if presetOAuth != nil && strings.TrimSpace(presetOAuth.ClientID) != "" {
		// 编辑器提示用户注册的是 http://localhost:<port>/callback；
		// 预注册客户端必须逐字匹配，所以回调也用 localhost（仍监听回环）。
		redirectHost = "localhost"
	}
	redirectURI := "http://" + redirectHost + ":" + fmt.Sprint(listener.Addr().(*net.TCPAddr).Port) + "/callback"
	var client oauthClient
	if presetOAuth != nil && strings.TrimSpace(presetOAuth.ClientID) != "" {
		// 预注册客户端：服务商不支持动态注册时使用（client_secret 可空）。
		client = oauthClient{
			ClientID: presetOAuth.ClientID, ClientSecret: presetOAuth.ClientSecret, RedirectURI: redirectURI,
		}
	} else {
		client, err = registerOAuthClient(ctx, asMeta, redirectURI, scope, hc)
		if err != nil {
			_ = listener.Close()
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "无法注册 OAuth 客户端: " + err.Error()})
			return
		}
	}
	verifier, challenge, err := newPKCE()
	if err != nil {
		_ = listener.Close()
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "无法生成 PKCE: " + err.Error()})
		return
	}
	state, err := randB64(24)
	if err != nil {
		_ = listener.Close()
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "无法生成 state: " + err.Error()})
		return
	}
	authURL := oauthAuthorizeURL(asMeta, client.ClientID, redirectURI, state, challenge, scope, resource)
	if authURL == "" {
		_ = listener.Close()
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "授权服务器地址无效"})
		return
	}
	flowID, err := randB64(12)
	if err != nil {
		_ = listener.Close()
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "无法生成登录 ID"})
		return
	}
	flowCtx, flowCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	flow := &signinFlow{id: flowID, name: name, state: "waiting", url: authURL, createdAt: time.Now(), cancel: flowCancel}
	g.signinFlows.put(flow)
	go g.awaitMCPCallback(flowCtx, flow, listener, client, asMeta, verifier, state, redirectURI, upstream, resource)
	writeJSON(w, http.StatusOK, map[string]any{"id": flowID, "state": "waiting", "url": authURL})
}

type callbackResult struct {
	code string
	err  string
}

// awaitMCPCallback 起临时回调服务并等待授权码；成功即换令牌入库。
func (g *Gateway) awaitMCPCallback(ctx context.Context, flow *signinFlow, listener net.Listener, client oauthClient, meta oauthASMeta, verifier, state, redirectURI, upstream, resource string) {
	defer listener.Close()
	defer flow.cancel()
	ch := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			if desc == "" {
				desc = e
			}
			ch <- callbackResult{err: "授权被拒绝: " + desc}
			http.Error(w, "登录未完成，可以关闭此页面。", http.StatusBadRequest)
			return
		}
		if q.Get("state") != state {
			ch <- callbackResult{err: "state 不匹配，已拒绝这次回调"}
			http.Error(w, "登录回调校验失败。", http.StatusBadRequest)
			return
		}
		code := q.Get("code")
		if code == "" {
			ch <- callbackResult{err: "回调里没有授权码"}
			http.Error(w, "登录回调缺少授权码。", http.StatusBadRequest)
			return
		}
		ch <- callbackResult{code: code}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><meta charset=utf-8><title>登录完成</title><p>登录完成，可以关闭此页面，回到 Aether Cockpit。</p>")
	})
	srv := &http.Server{Handler: mux}
	serveDone := make(chan struct{})
	go func() {
		_ = srv.Serve(listener)
		close(serveDone)
	}()
	defer func() {
		_ = srv.Close()
		<-serveDone
	}()

	var res callbackResult
	select {
	case res = <-ch:
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			g.signinFlows.set(flow.id, "failed", "登录超时，请重试")
		} else {
			g.signinFlows.set(flow.id, "canceled", "")
		}
		return
	}
	if res.err != "" {
		g.signinFlows.set(flow.id, "failed", res.err)
		return
	}
	exCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tok, err := exchangeOAuthCode(exCtx, meta, client, res.code, verifier, redirectURI, resource, oauthHTTPClient())
	if err != nil {
		g.signinFlows.set(flow.id, "failed", "换取令牌失败: "+err.Error())
		return
	}
	rec := &mcpAuthRecord{
		Name: flow.name, Upstream: upstream, Token: tok, Client: client, Meta: meta,
		Resource: resource, SignedInAt: time.Now().Unix(),
	}
	if err := g.mcpAuthStore().put(rec); err != nil {
		g.signinFlows.set(flow.id, "failed", "保存令牌失败: "+err.Error())
		return
	}
	g.signinFlows.set(flow.id, "done", "")
}

// handleMCPSigninPoll GET /api/library/mcp-signin/{id}
func (g *Gateway) handleMCPSigninPoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flow := g.signinFlows.get(id)
	if flow == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这次登录", "state": "failed"})
		return
	}
	out := map[string]any{"id": flow.id, "state": flow.state, "url": flow.url, "name": flow.name}
	if flow.err != "" {
		out["error"] = flow.err
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMCPSigninCancel POST /api/library/mcp-signin/{id}/cancel
func (g *Gateway) handleMCPSigninCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flow := g.signinFlows.get(id)
	if flow == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这次登录"})
		return
	}
	if flow.cancel != nil {
		flow.cancel()
	}
	g.signinFlows.set(id, "canceled", "")
	writeJSON(w, http.StatusOK, map[string]any{"state": "canceled"})
}

// handleMCPSignout POST /api/library/mcp-signout {name}
//
// 删除令牌，并把当前已下发的配置写回上游地址（网关不再中继）。
func (g *Gateway) handleMCPSignout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少服务器名"})
		return
	}
	applied := append([]string(nil), g.homeServerAgents()[name]...)
	if rec := g.mcpAuthStore().get(name); rec != nil {
		// 先尽力撤销服务商侧的授权，再删本地令牌；撤销失败不阻止退出。
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = revokeOAuthToken(ctx, rec.Meta, rec.Client, rec.Token, oauthHTTPClient())
		cancel()
		g.mcpAuthStore().delete(name)
	}
	var entryID string
	for _, m := range g.library.List().MCPServers {
		if m.Name == name {
			entryID = m.ID
			break
		}
	}
	problems := []map[string]any{}
	if entryID != "" {
		for _, agent := range applied {
			target, ok := agentApplyTarget[agent]
			if !ok {
				continue
			}
			if _, err := g.library.ApplyMCP(entryID, target); err != nil {
				problems = append(problems, map[string]any{"agent": agent, "error": err.Error()})
			}
		}
	}
	out := map[string]any{"changed": applied}
	if len(problems) > 0 {
		out["problems"] = problems
	}
	g.writeLibraryViewExtra(w, map[string]any{"result": out})
}

// handleMCPRelay /mcp/{name}：把 Agent 的请求转发给上游并注入令牌；
// 用户关掉的工具在 tools/list 里隐藏，直接 tools/call 返回 JSON-RPC 错误。
func (g *Gateway) handleMCPRelay(w http.ResponseWriter, r *http.Request) {
	if !loopbackRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "MCP 中继只接受本机请求"})
		return
	}
	name := r.PathValue("name")
	store := g.mcpAuthStore()
	rec := store.get(name)
	if rec == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "服务器未登录: " + name})
		return
	}
	token, ok := g.mcpBearerFor(name, rec.Upstream)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "登录已失效，请重新登录: " + name})
		return
	}
	var body []byte
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 8<<20))
	}
	call := parseMCPRelayCall(body)
	disabled := g.mcpDisabledTools(name)
	if call.method == "tools/call" && disabled[call.tool] {
		writeJSON(w, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": call.id,
			"error": map[string]any{"code": -32601, "message": "工具已在 Aether Cockpit 中关闭: " + call.tool},
		})
		return
	}
	filter := call.method == "tools/list" && len(disabled) > 0
	target, err := url.Parse(rec.Upstream)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "上游地址无效"})
		return
	}
	if r.URL.RawQuery != "" {
		if target.RawQuery == "" {
			target.RawQuery = r.URL.RawQuery
		} else {
			target.RawQuery += "&" + r.URL.RawQuery
		}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), reader)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	for k, vs := range r.Header {
		if hopByHopHeader(k) || strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Host") {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if filter {
		// 让 Transport 自己协商 gzip 并解压，确保能解析 tools/list 的 JSON。
		req.Header.Del("Accept-Encoding")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "上游请求失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if hopByHopHeader(k) || (filter && strings.EqualFold(k, "Content-Length")) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if !filter {
		copyMCPStream(w, resp.Body)
		return
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		filterMCPToolsSSE(w, resp.Body, disabled)
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	_, _ = w.Write(filterMCPToolsMessage(raw, disabled))
}

func copyMCPStream(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func loopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func hopByHopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

// mcpAuthEpoch 让健康检查缓存随登录状态失效（不含令牌本身）。
func (g *Gateway) mcpAuthEpoch(name string) int64 {
	rec := g.mcpAuthStore().get(name)
	if rec == nil {
		return 0
	}
	if rec.Dead {
		return -1
	}
	return rec.SignedInAt
}

func hasHeader(h map[string]string, key string) bool {
	for k := range h {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}
