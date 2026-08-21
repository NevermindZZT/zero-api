package store

import "encoding/json"

func normalizeProtocols(raw, defaultType string) []string {
	var protocols []string
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &protocols)
	}
	if len(protocols) == 0 && defaultType != "" {
		protocols = []string{defaultType}
	}
	return protocols
}

func marshalProtocols(protocols []string, defaultType string) string {
	if len(protocols) == 0 && defaultType != "" {
		protocols = []string{defaultType}
	}
	data, _ := json.Marshal(protocols)
	return string(data)
}

// SupportsProtocol 判断渠道是否声明支持指定协议。
func (c *Channel) SupportsProtocol(protocol string) bool {
	for _, p := range normalizeProtocolsFromList(c.Protocols, c.Type) {
		if p == protocol {
			return true
		}
	}
	return false
}

func normalizeProtocolsFromList(protocols []string, defaultType string) []string {
	if len(protocols) == 0 && defaultType != "" {
		return []string{defaultType}
	}
	return protocols
}
