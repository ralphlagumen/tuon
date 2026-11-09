package messenger

import (
	"encoding/json"
	"testing"
)

func TestUploadID(t *testing.T) {
	for raw, want := range map[string]int64{
		`[{"fbid":"123","filename":"a.zip","src":"https://x"}]`: 123,
		`{"0":{"attachment_id":456,"filename":"a.zip"}}`:        456,
		`[{"filename":"a.zip","filetype":"application/zip"}]`:   0,
		`[{"file_id":0,"some_id":"x"}]`:                         0,
		`not json`:                                              0,
	} {
		if got, _ := uploadID(json.RawMessage(raw)); got != want {
			t.Errorf("uploadID(%s) = %d, want %d", raw, got, want)
		}
	}
	if _, keys := uploadID(json.RawMessage(`[{"fbid":"1","filename":"secret.zip"}]`)); len(keys) != 2 || keys[0] != "fbid" || keys[1] != "filename" {
		t.Errorf("keys %v", keys)
	}
}
