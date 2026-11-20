package main

// Tuya Mobile SDK APIクライアント(et=0.0.1平文モード)￥

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	signingKey = "com.petsnowy_D7:D7:C4:2F:AA:12:04:0A:1F:37:91:63:23:49:6D:59:C0:F8:EC:AB:CA:8F:B2:34:DD:5A:3F:8A:2E:26:C8:1B_kndhq5vpuu383hv8eqd88c8s59mxdac5_srv5jgatysv94tvntq4suvfnau43vxtw"
	appKey     = "arrxwjukqve3skqkajpn"
	appSecret  = "srv5jgatysv94tvntq4suvfnau43vxtw"
	chKey      = "5facc49a"
	baseURL    = "https://a1.tuyaus.com/api.json"
	appVersion = "7.5.0"
	userAgent  = "Thing-UA=APP/Android/1.8.0/SDK/7.5.0"
)

var signWhitelist = map[string]bool{
	"a": true, "v": true, "lat": true, "lon": true, "lang": true,
	"deviceId": true, "appVersion": true, "ttid": true, "isH5": true,
	"h5Token": true, "os": true, "clientId": true, "postData": true,
	"time": true, "requestId": true, "et": true, "n4h5": true,
	"sid": true, "chKey": true, "sp": true,
}

type Client struct {
	HTTP     *http.Client
	DeviceID string
	SID      string
	Models   map[string]*productModel
}

func NewClient(deviceID string) *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 15 * time.Second},
		DeviceID: deviceID,
		Models:   map[string]*productModel{},
	}
}

func swapSignString(s string) string {
	return s[8:16] + s[0:8] + s[24:32] + s[16:24]
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Tuya Mobile SDK署名: whitelist絞込み, postDataはswapSignString(MD5), ソート後"||"結合
func (c *Client) sign(params map[string]string) string {
	filtered := map[string]string{}
	for k, v := range params {
		if signWhitelist[k] && v != "" {
			filtered[k] = v
		}
	}
	if pd, ok := filtered["postData"]; ok && pd != "" {
		sum := md5.Sum([]byte(pd))
		filtered["postData"] = swapSignString(hex.EncodeToString(sum[:]))
	}
	keys := make([]string, 0, len(filtered))
	for k := range filtered {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + filtered[k]
	}
	mac := hmac.New(sha256.New, []byte(signingKey))
	mac.Write([]byte(strings.Join(parts, "||")))
	return hex.EncodeToString(mac.Sum(nil))
}

// et=0.0.1平文モードリクエスト(レート制限時1回リトライ)
func (c *Client) call(action, version string, postData interface{}) (json.RawMessage, error) {
	raw, err := c.callOnce(action, version, postData)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "REQUEST_TOO_FREQUENTLY") || strings.Contains(msg, "PERMISSION_DENIED") {
			time.Sleep(12 * time.Second)
			return c.callOnce(action, version, postData)
		}
	}
	return raw, err
}

func (c *Client) callOnce(action, version string, postData interface{}) (json.RawMessage, error) {
	params := map[string]string{
		"a":          action,
		"v":          version,
		"time":       fmt.Sprintf("%d", time.Now().Unix()),
		"appVersion": appVersion,
		"chKey":      chKey,
		"clientId":   appKey,
		"cp":         "gzip",
		"deviceId":   c.DeviceID,
		"et":         "0.0.1",
		"lang":       "en_US",
		"nd":         "1",
		"os":         "Android",
		"requestId":  randomHex(16),
		"sdkVersion": appVersion,
		"timeZoneId": "GMT",
		"ttid":       "android",
		"sid":        c.SID,
	}
	if postData != nil {
		b, err := json.Marshal(postData)
		if err != nil {
			return nil, err
		}
		params["postData"] = string(b)
	}
	params["sign"] = c.sign(params)

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequest("POST", baseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Result    json.RawMessage `json:"result"`
		Success   bool            `json:"success"`
		ErrorMsg  string          `json:"errorMsg"`
		ErrorCode string          `json:"errorCode"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("json decode error: %v, body: %s", err, snippet)
	}
	if !result.Success {
		return nil, fmt.Errorf("api error: %s (%s)", result.ErrorMsg, result.ErrorCode)
	}
	return result.Result, nil
}
