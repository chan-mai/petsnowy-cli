package main

// RESTful Web APIサーバ: /api/v1/*

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type apiServer struct {
	c     *Client
	mu    sync.Mutex // Clientは並行利用不可
	token string
}

// POST /api/v1/devices/{id}/op ボディ
type opRequest struct {
	Code  string      `json:"code"`
	Value interface{} `json:"value"`
	Force bool        `json:"force"`
}

// POST /api/v1/devices/{id}/dps ボディ: {"101": 1, ...}
type dpsRequest map[string]interface{}

func serveCommand(c *Client, addr, token string) int {
	s := &apiServer{c: c, token: token}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/homes", s.auth(s.homes))
	mux.HandleFunc("GET /api/v1/devices", s.auth(s.listDevices))
	mux.HandleFunc("GET /api/v1/devices/{id}", s.auth(s.deviceState))
	mux.HandleFunc("GET /api/v1/devices/{id}/model", s.auth(s.deviceModel))
	mux.HandleFunc("POST /api/v1/devices/{id}/op", s.auth(s.deviceOp))
	mux.HandleFunc("POST /api/v1/devices/{id}/dps", s.auth(s.deviceDps))

	fmt.Fprintf(cliOut, "listening on %s (token auth: %v)\n", addr, token != "")
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(cliOut, "error: serve:", err)
		return 1
	}
	return 0
}

// 認証ミドルウェア: -token指定時はBearer必須
func (s *apiServer) auth(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			h := r.Header.Get("Authorization")
			if h != "Bearer "+s.token {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}
		// ログイン確認(保存済みセッション)
		s.mu.Lock()
		ok := s.ensureLogin()
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not logged in. run: petsnowy-cli auth login"})
			return
		}
		next(w, r)
	}
}

// セッション有効性確認(無効なら再ログイン不可: フラグ駆動のため)
func (s *apiServer) ensureLogin() bool {
	if s.c.SID != "" {
		if _, err := s.c.sessionValid(s.c.SID); err == nil {
			return true
		}
	}
	sess, err := loadSession()
	if err != nil {
		return false
	}
	if _, err := s.c.sessionValid(sess.SID); err != nil {
		return false
	}
	s.c.SID = sess.SID
	if err := s.c.loadModels(); err != nil {
		fmt.Fprintln(cliOut, "warn: model load failed:", err)
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// GET /api/v1/health: 認証不要
func (s *apiServer) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// GET /api/v1/homes
func (s *apiServer) homes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	homes, err := s.c.homes()
	s.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, homes)
}

// GET /api/v1/devices
func (s *apiServer) listDevices(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	homes, err := s.c.homes()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	var out []devView
	for _, h := range homes {
		devs, err := s.c.devices(h.GroupID)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		for _, d := range devs {
			out = append(out, viewDevice(d, s.c.modelFor(d)))
		}
	}
	if out == nil {
		out = []devView{}
	}
	writeJSON(w, http.StatusOK, out)
}

// GET /api/v1/devices/{id}: ?name=1で名前検索も可
func (s *apiServer) deviceState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := s.c.findDevice(r.PathValue("id"))
	if dev == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found"})
		return
	}
	writeJSON(w, http.StatusOK, viewDevice(*dev, s.c.modelFor(*dev)))
}

// GET /api/v1/devices/{id}/model
func (s *apiServer) deviceModel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := s.c.findDevice(r.PathValue("id"))
	if dev == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found"})
		return
	}
	raw, err := s.c.thingModel(dev.ProductID, dev.ProductVer)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	var v interface{}
	json.Unmarshal(raw, &v)
	writeJSON(w, http.StatusOK, v)
}

// POST /api/v1/devices/{id}/op: {"code": "light", "value": true}
func (s *apiServer) deviceOp(w http.ResponseWriter, r *http.Request) {
	var req opRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if req.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code is required"})
		return
	}
	if riskyCodes[req.Code] && !req.Force {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q is a risky op; set force=true to allow", req.Code)})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.c.sendByCode(r.PathValue("id"), req.Code, normalizeValue(req.Value))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "code": req.Code, "value": req.Value, "response": string(raw)})
}

// POST /api/v1/devices/{id}/dps: {"16": true} — 生dp送信
func (s *apiServer) deviceDps(w http.ResponseWriter, r *http.Request) {
	var dps dpsRequest
	if err := json.NewDecoder(r.Body).Decode(&dps); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if len(dps) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dps body is required"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.c.sendDps(r.PathValue("id"), dps)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "response": string(raw)})
}

// 数値のbool解決ヘルパー(JSONのtrue/false/数値/文字列を許容)
func normalizeValue(v interface{}) interface{} {
	switch x := v.(type) {
	case string:
		switch x {
		case "true", "on":
			return true
		case "false", "off":
			return false
		}
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return f
		}
	case map[string]interface{}:
		_ = x
	}
	return v
}
