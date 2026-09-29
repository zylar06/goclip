package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
)

type Vault struct{ aead cipher.AEAD }

func OpenVault(path string) (*Vault, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(e, os.ErrExist) {
			key, err = os.ReadFile(path)
		} else if e != nil {
			return nil, e
		} else {
			_, err = f.Write(key)
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("invalid master key; restore matching key from backup")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}
func (v *Vault) Encrypt(plain []byte) ([]byte, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return v.aead.Seal(nonce, nonce, plain, []byte("autoclip-go-settings-v1")), nil
}
func (v *Vault) Decrypt(b []byte) ([]byte, error) {
	n := v.aead.NonceSize()
	if len(b) < n {
		return nil, errors.New("invalid encrypted secret")
	}
	out, err := v.aead.Open(nil, b[:n], b[n:], []byte("autoclip-go-settings-v1"))
	if err != nil {
		return nil, errors.New("secret cannot be decrypted; restore matching master.key")
	}
	return out, nil
}
