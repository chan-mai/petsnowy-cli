package main

// 認証

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type userInfoResult struct {
	UID      string `json:"uid"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
}

// email/passログイン
// smartlife.m.user.email.password.login v3.0(ifencrypt:1必須)
func (c *Client) login(email, password, countryCode string) (*userInfoResult, error) {
	tokRaw, err := c.call("smartlife.m.user.username.token.get", "2.0", map[string]interface{}{
		"countryCode": countryCode,
		"username":    email,
		"isUid":       false,
	})
	if err != nil {
		return nil, fmt.Errorf("token get: %w", err)
	}
	var tok struct {
		Token string `json:"token"`
		PbKey string `json:"pbKey"`
	}
	if err := json.Unmarshal(tokRaw, &tok); err != nil {
		return nil, fmt.Errorf("token decode: %w", err)
	}

	passwd, err := encryptPassword(password, tok.PbKey)
	if err != nil {
		return nil, fmt.Errorf("password encrypt: %w", err)
	}

	loginRaw, err := c.call("smartlife.m.user.email.password.login", "3.0", map[string]interface{}{
		"countryCode": countryCode,
		"email":       email,
		"passwd":      passwd,
		"token":       tok.Token,
		"options":     `{"group": 1}`,
		"ifencrypt":   1,
	})
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	var login struct {
		SID string `json:"sid"`
		UID string `json:"uid"`
	}
	if err := json.Unmarshal(loginRaw, &login); err != nil {
		return nil, fmt.Errorf("login decode: %w", err)
	}
	c.SID = login.SID

	infoRaw, err := c.call("smartlife.m.user.info.get", "1.0", nil)
	if err != nil {
		return nil, fmt.Errorf("user info: %w", err)
	}
	var info userInfoResult
	json.Unmarshal(infoRaw, &info)
	return &info, nil
}

// セッション永続化
type sessionData struct {
	SID      string `json:"sid"`
	UID      string `json:"uid"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
	SavedAt  int64  `json:"savedAt"`
}

func sessionFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "session.json"
	}
	return filepath.Join(home, ".petsnowy-cli", "session.json")
}

func saveSession(s sessionData) error {
	p := sessionFile()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0600)
}

func loadSession() (*sessionData, error) {
	b, err := os.ReadFile(sessionFile())
	if err != nil {
		return nil, err
	}
	var s sessionData
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func deleteSession() {
	os.Remove(sessionFile())
}

// 保存済みセッションの有効性確認: smartlife.m.user.info.get
func (c *Client) sessionValid(sid string) (*sessionData, error) {
	c.SID = sid
	raw, err := c.call("smartlife.m.user.info.get", "1.0", nil)
	if err != nil {
		c.SID = ""
		return nil, err
	}
	var info userInfoResult
	json.Unmarshal(raw, &info)
	return &sessionData{SID: sid, UID: info.UID, Email: info.Email, Nickname: info.Nickname}, nil
}
