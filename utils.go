package w2

import "encoding/json"

func toJSON(v interface{}) string {
	buf, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(buf)
}
