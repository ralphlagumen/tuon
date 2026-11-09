package messenger

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// idFields: other names an upload reply might carry its file ID under.
// Only these: a looser match could pick up a thread or user ID.
var idFields = map[string]bool{"fbid": true, "attachment_id": true, "attachment_fbid": true, "media_id": true}

// uploadID finds a file ID in an upload reply's metadata when it isn't in
// a field messagix knows (file_id, image_id, …): one of idFields with a
// positive number (or a number in a string). It also returns
// the field names it saw, for the log (never the values: they include the
// file name and a link to the file).
func uploadID(raw json.RawMessage) (int64, []string) {
	var entries []map[string]json.RawMessage
	if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &entries)
	} else {
		var m map[string]map[string]json.RawMessage
		if json.Unmarshal(raw, &m) == nil {
			for _, e := range m {
				entries = append(entries, e)
			}
		} else {
			// a flat object: the fields themselves, not keyed by "0"
			var flat map[string]json.RawMessage
			if json.Unmarshal(raw, &flat) == nil {
				entries = append(entries, flat)
			}
		}
	}
	var keys []string
	var found int64
	for _, e := range entries {
		for k, v := range e {
			keys = append(keys, k)
			if found != 0 || !idFields[k] {
				continue
			}
			var n json.Number
			if json.Unmarshal(v, &n) != nil {
				var s string
				if json.Unmarshal(v, &s) != nil {
					continue
				}
				n = json.Number(s)
			}
			if id, err := strconv.ParseInt(string(n), 10, 64); err == nil && id > 0 {
				found = id
			}
		}
	}
	sort.Strings(keys)
	return found, keys
}

// shape describes metadata's layout for the log, with no values: its
// kind, length and top-level keys (keys of a list's first entry).
func shape(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return fmt.Sprintf("not JSON (%d bytes)", len(raw))
	}
	switch t := v.(type) {
	case []any:
		if len(t) > 0 {
			if m, ok := t[0].(map[string]any); ok {
				return fmt.Sprintf("list of %d, first has %v", len(t), sortedKeys(m))
			}
		}
		return fmt.Sprintf("list of %d", len(t))
	case map[string]any:
		return fmt.Sprintf("object with %v", sortedKeys(t))
	default:
		return fmt.Sprintf("%T", v)
	}
}

func sortedKeys(m map[string]any) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// payloadShape: shape of the reply's "payload" object (no values).
func payloadShape(raw json.RawMessage) string {
	var r struct {
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.Payload) == 0 {
		return "none"
	}
	return shape(r.Payload)
}
