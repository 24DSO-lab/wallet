package tron

import "encoding/hex"

func hexString(b []byte) string { return hex.EncodeToString(b) }

func hexDecode(s string) ([]byte, error) { return hex.DecodeString(s) }
