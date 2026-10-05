// SPDX-License-Identifier: MIT

package core

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	wrapKeyLen = 32
)

func deriveWrapKey(password string) ([]byte, error) {
	return deriveWrapKeySalt(password, "WDTT-WRAP-v1")
}

func deriveCSQTTWrapKey(password string) ([]byte, error) {
	return deriveWrapKeySalt(password, "CSQTT-WRAP-v1")
}

func deriveWrapKeySalt(password, salt string) ([]byte, error) {
	if password == "" {
		return nil, errors.New("empty password")
	}
	key := make([]byte, wrapKeyLen)
	reader := hkdf.New(
		sha256.New,
		[]byte(password),
		[]byte(salt),
		[]byte("rtp-obfs/chacha20poly1305"),
	)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("derive wrap key: %w", err)
	}
	return key, nil
}
