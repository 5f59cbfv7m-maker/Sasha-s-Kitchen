package storefront

import "encoding/json"

func unmarshal(raw []byte, dst any) error { return json.Unmarshal(raw, dst) }
