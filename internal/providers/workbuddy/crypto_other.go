//go:build !windows

package workbuddy

import (
	"encoding/json"
	"errors"
)

var errLocalCryptoUnsupported = errors.New("WorkBuddy 本机登录态解密仅支持 Windows")

func loadWBKey() ([]byte, error) { return nil, errLocalCryptoUnsupported }

func decryptWBField(raw json.RawMessage, key []byte) (string, bool, error) {
	return "", true, errLocalCryptoUnsupported
}
