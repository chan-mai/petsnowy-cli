package main

// thing model: dpCode <-> dpID(abilityId)対応表

import (
	"encoding/json"
	"fmt"
	"strings"
)

// dpCode -> 能力定義
type dpProp struct {
	AbilityID  int
	AccessMode string
	TypeSpec   map[string]interface{}
}

type productModel struct {
	Props map[string]dpProp // code -> prop
}

// thing model取得: thing.m.product.thing.model v1.0
func (c *Client) thingModel(productID, productVer string) (json.RawMessage, error) {
	return c.call("thing.m.product.thing.model", "1.0", map[string]interface{}{
		"productId":      productID,
		"productVersion": productVer,
	})
}

// 全デバイスのthing modelを自動取得してキャッシュ
func (c *Client) loadModels() error {
	homes, err := c.homes()
	if err != nil {
		return err
	}
	for _, h := range homes {
		devs, err := c.devices(h.GroupID)
		if err != nil {
			return err
		}
		for _, d := range devs {
			if d.ProductID != "" {
				if _, ok := c.Models[d.ProductID]; !ok {
					raw, err := c.thingModel(d.ProductID, d.ProductVer)
					if err != nil {
						continue
					}
					c.Models[d.ProductID] = parseModel(raw)
				}
			}
		}
	}
	return nil
}

func parseModel(raw json.RawMessage) *productModel {
	var m struct {
		Services []struct {
			Properties []struct {
				AbilityID  int                    `json:"abilityId"`
				AccessMode string                 `json:"accessMode"`
				Code       string                 `json:"code"`
				TypeSpec   map[string]interface{} `json:"typeSpec"`
			} `json:"properties"`
		} `json:"services"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	pm := &productModel{Props: map[string]dpProp{}}
	for _, svc := range m.Services {
		for _, p := range svc.Properties {
			pm.Props[p.Code] = dpProp{AbilityID: p.AbilityID, AccessMode: p.AccessMode, TypeSpec: p.TypeSpec}
		}
	}
	return pm
}

func printModel(raw json.RawMessage) {
	var m struct {
		Services []struct {
			Properties []struct {
				AbilityID  int    `json:"abilityId"`
				AccessMode string `json:"accessMode"`
				Code       string `json:"code"`
				TypeSpec   struct {
					Type  string   `json:"type"`
					Range []string `json:"range"`
					Max   *int     `json:"max"`
					Unit  string   `json:"unit"`
				} `json:"typeSpec"`
			} `json:"properties"`
		} `json:"services"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		fmt.Println("model decode error:", err)
		return
	}
	for _, svc := range m.Services {
		for _, p := range svc.Properties {
			extra := ""
			if len(p.TypeSpec.Range) > 0 {
				extra = " enum:" + strings.Join(p.TypeSpec.Range, "/")
			} else if p.TypeSpec.Type == "value" {
				extra = fmt.Sprintf(" %s..%s %s", "", "", p.TypeSpec.Unit)
			}
			fmt.Printf("  %4d %-3s %-24s %s%s\n", p.AbilityID, p.AccessMode, p.Code, p.TypeSpec.Type, extra)
		}
	}
}
