package proxy

import "encoding/pem"

func pemFirst(s string) []byte {
	block, _ := pem.Decode([]byte(s))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil
	}
	return block.Bytes
}
