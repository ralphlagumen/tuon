package messenger

import (
	"encoding/json"
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
