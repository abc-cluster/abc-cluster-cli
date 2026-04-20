package config

import "strings"

// normalizeContextAuthScalarInMap rewrites contexts.<name>.auth when it is a string
// (e.g. auth: root for the Nomad bootstrap / management token) into the structured
// form expected by yaml.Unmarshal into Context.
func normalizeContextAuthScalarInMap(ctxMap map[string]interface{}) {
	raw, ok := ctxMap["auth"]
	if !ok {
		return
	}
	s, ok := raw.(string)
	if !ok {
		return
	}
	if strings.EqualFold(strings.TrimSpace(s), "root") {
		ctxMap["auth"] = map[string]interface{}{"root": true}
	}
}
