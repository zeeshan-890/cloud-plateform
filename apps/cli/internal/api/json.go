package api

import "encoding/json"

func jsonUnmarshal(raw []byte, out any) error {
	return json.Unmarshal(raw, out)
}
