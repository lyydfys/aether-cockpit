package gateway

import (
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/tools"
)

func (g *Gateway) audioConfig() (tools.AudioConfig, bool) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.Tools == nil || g.settings.Tools.Audio == nil {
		return tools.AudioConfig{}, false
	}
	cfg := *g.settings.Tools.Audio
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return tools.AudioConfig{}, false
	}
	return cfg, true
}

// handleAudioProxy 透传 multipart 音频请求（transcriptions / translations / transcribe）。
func (g *Gateway) handleAudioProxy(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
			return
		}
		cfg, settingsOK := g.audioConfig()
		target, terr := g.mediaTargetFor(r, body)
		if terr != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": mediaTargetError(terr)})
			return
		}
		if !settingsOK && target.BaseURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置音频服务：请在设置页填写 base_url（OpenAI 兼容 /audio/*），或把音频模型挂到某个渠道上"})
			return
		}
		// multipart 的 model 是表单字段，改写要重排整个 body（含音频二进制），
		// 风险大于收益：JSON 请求改写，multipart 由客户端直接给上游模型名。
		if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			body = applyModelToBody(body, target, cfg.Model)
		}
		cfg = target.applyToAudio(cfg)
		code, ct, out, err := tools.ProxyAudio(r.Context(), cfg, path, r.Header.Get("Content-Type"), body)
		g.noteMediaResult(target, code, err)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(code)
		_, _ = w.Write(out)
	}
}

// handleAudioSpeech 透传 JSON 语音合成请求，原样返回音频字节。
func (g *Gateway) handleAudioSpeech(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取请求体失败"})
		return
	}
	cfg, settingsOK := g.audioConfig()
	target, terr := g.mediaTargetFor(r, body)
	if terr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": mediaTargetError(terr)})
		return
	}
	if !settingsOK && target.BaseURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "尚未配置音频服务：请在设置页填写 base_url，或把语音模型挂到某个渠道上"})
		return
	}
	body = applyModelToBody(body, target, cfg.Model)
	cfg = target.applyToAudio(cfg)
	code, ct, out, err := tools.ProxyAudio(r.Context(), cfg, "/audio/speech", "application/json", body)
	g.noteMediaResult(target, code, err)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if ct == "" {
		ct = "audio/mpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(code)
	_, _ = w.Write(out)
}

var _ = tools.AudioConfig{}
