package main

// パス暗号化

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
)

func encryptPassword(password, pbKeyB64 string) (string, error) {
	block, _ := pem.Decode([]byte("-----BEGIN PUBLIC KEY-----\n" + pbKeyB64 + "\n-----END PUBLIC KEY-----"))
	if block == nil {
		return "", fmt.Errorf("pem decode failed")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", err
	}
	pub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("not RSA public key")
	}
	md5hex := fmt.Sprintf("%x", md5.Sum([]byte(password)))
	ct, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(md5hex))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(ct), nil
}
