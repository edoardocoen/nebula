package provider_pb

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"testing"
	"time"
)

func Test(t *testing.T) {
	priKey, err := rsa.GenerateKey(rand.Reader, 256*8)
	if err != nil {
		t.Errorf("failed")
	}
	pubKey := x509.MarshalPKCS1PublicKey(&priKey.PublicKey)
	ticket := "test-ticket"
	timestamp := uint64(time.Now().Unix())
	size := uint64(191849)
	key := []byte("test-hash-key")
	if checkAuth(pubKey, method_store, key, size, timestamp, ticket, GenStoreAuth(pubKey, key, size, timestamp, ticket)) != nil {
		t.Errorf("failed")
	}
	if checkAuth(pubKey, method_retrieve, key, size, timestamp, ticket, GenRetrieveAuth(pubKey, key, size, timestamp, ticket)) != nil {
		t.Errorf("failed")
	}
	if checkAuth(pubKey, method_get_fragment, key, size, timestamp, "", GenGetFragmentAuth(pubKey, key, size, timestamp)) != nil {
		t.Errorf("failed")
	}
}
