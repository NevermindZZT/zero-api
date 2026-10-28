package adapter

import "encoding/json"

func responsesToolChoiceToCanonical(raw json.RawMessage) json.RawMessage {
	var choice map[string]json.RawMessage
	if json.Unmarshal(raw, &choice) != nil {
		return raw
	}
	var typ string
	_ = json.Unmarshal(choice["type"], &typ)
	if typ == "function" && len(choice["name"]) > 0 {
		out, err := json.Marshal(map[string]interface{}{"type": "function", "function": map[string]json.RawMessage{"name": choice["name"]}})
		if err == nil {
			return out
		}
	}
	return raw
}

func canonicalToolChoiceToResponses(raw json.RawMessage) json.RawMessage {
	var choice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &choice) == nil && choice.Type == "function" && choice.Function.Name != "" {
		out, err := json.Marshal(map[string]string{"type": "function", "name": choice.Function.Name})
		if err == nil {
			return out
		}
	}
	return raw
}
