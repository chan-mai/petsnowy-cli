package main

// デバイス: ホーム/デバイス一覧, dp操作

import (
	"encoding/json"
	"fmt"
	"sort"
)

type deviceEntry struct {
	DeviceID      string          `json:"devId"`
	Name          string          `json:"name"`
	Category      string          `json:"category"`
	CloudOnline   bool            `json:"cloudOnline"`
	ProductID     string          `json:"productId"`
	ProductVer    string          `json:"productVer"`
	DataPointInfo json.RawMessage `json:"dataPointInfo"`
}

type homeEntry struct {
	GroupID int64  `json:"groupId"`
	Name    string `json:"name"`
}

// ホーム一覧取得: m.life.home.space.list v1.0
func (c *Client) homes() ([]homeEntry, error) {
	raw, err := c.call("m.life.home.space.list", "1.0", nil)
	if err != nil {
		return nil, err
	}
	var homes []homeEntry
	if err := json.Unmarshal(raw, &homes); err != nil {
		return nil, fmt.Errorf("homes decode: %w", err)
	}
	return homes, nil
}

// デバイス一覧+dp状態取得: m.life.my.group.device.list v2.2
func (c *Client) devices(gid int64) ([]deviceEntry, error) {
	raw, err := c.call("m.life.my.group.device.list", "2.2", map[string]interface{}{"gid": gid})
	if err != nil {
		return nil, err
	}
	var devices []deviceEntry
	if err := json.Unmarshal(raw, &devices); err != nil {
		return nil, fmt.Errorf("devices decode: %w", err)
	}
	return devices, nil
}

// デバイス検索: devIdまたはname
func (c *Client) findDevice(key string) *deviceEntry {
	var dev *deviceEntry
	homes, _ := c.homes()
	for _, h := range homes {
		devs, _ := c.devices(h.GroupID)
		for i := range devs {
			if devs[i].DeviceID == key || devs[i].Name == key {
				dev = &devs[i]
			}
		}
	}
	return dev
}

// デバイスへのdps送信: thing.m.device.dp.publish
// postData: gwId, devId, dps(JSON文字列) — dexdump qpqbppd.bdpdqbp(DpPublish)より
func (c *Client) sendDps(devID string, dps map[string]interface{}) (json.RawMessage, error) {
	b, err := json.Marshal(dps)
	if err != nil {
		return nil, err
	}
	return c.call("thing.m.device.dp.publish", "1.0", map[string]interface{}{
		"gwId":  devID,
		"devId": devID,
		"dps":   string(b),
	})
}

// dpCodeで操作: thing model経由でdpIDに解決して送信
func (c *Client) sendByCode(devKey, code string, value interface{}) (json.RawMessage, error) {
	dev := c.findDevice(devKey)
	if dev == nil {
		return nil, fmt.Errorf("device not found: %s", devKey)
	}
	pm, ok := c.Models[dev.ProductID]
	if !ok || pm == nil {
		return nil, fmt.Errorf("no thing model for product %s", dev.ProductID)
	}
	prop, ok := pm.Props[code]
	if !ok {
		return nil, fmt.Errorf("unknown dpCode %q for this device", code)
	}
	if prop.AccessMode != "rw" {
		return nil, fmt.Errorf("dpCode %q is read-only (%s)", code, prop.AccessMode)
	}
	return c.sendDps(dev.DeviceID, map[string]interface{}{fmt.Sprintf("%d", prop.AbilityID): value})
}

func printDevice(d deviceEntry, pm *productModel) {
	fmt.Printf("device: %s\n", d.DeviceID)
	fmt.Printf("  name=%s online=%v productId=%s\n", d.Name, d.CloudOnline, d.ProductID)
	if len(d.DataPointInfo) > 0 {
		var dpi struct {
			Dps map[string]interface{} `json:"dps"`
		}
		if err := json.Unmarshal(d.DataPointInfo, &dpi); err == nil && pm != nil {
			fmt.Println("  --- dps (code resolved) ---")
			keys := make([]string, 0)
			for k := range dpi.Dps {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				for code, p := range pm.Props {
					if fmt.Sprintf("%d", p.AbilityID) == k {
						fmt.Printf("  %s (%s) = %v\n", code, k, dpi.Dps[k])
					}
				}
			}
		}
	}
}
