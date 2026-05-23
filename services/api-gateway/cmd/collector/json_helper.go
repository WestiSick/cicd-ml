package main

import "encoding/json"

// decodeJSON is a small wrapper around json.Unmarshal used by the
// bg_job payload decoding above. Kept here rather than in main.go so
// the file containing the handler stays focused on collector logic.
func decodeJSON(raw []byte, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
