package shopify

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"github.com/flaboy/aira-shop/pkg/models"
)

const credentialEncryptionKeyLength = 32

type CredentialCipher struct {
	aead       cipher.AEAD
	keyVersion string
}

func DecodeShopifyCredential(shopLink models.ShopLink, credentialCipher *CredentialCipher) (ShopifyCredential, error) {
	plaintext, err := decryptShopifyCredential(shopLink, credentialCipher)
	if err != nil {
		return ShopifyCredential{}, err
	}
	credential := ShopifyCredential{}
	if err := json.Unmarshal(plaintext, &credential); err != nil {
		return ShopifyCredential{}, fmt.Errorf("decode Shopify credential: %w", err)
	}
	if credential.Url == "" || credential.AccessToken == "" {
		return ShopifyCredential{}, fmt.Errorf("Shopify encrypted credential is incomplete")
	}
	return credential, nil
}

func DecodeShopifyCredentialData(shopLink models.ShopLink, encodedKey, keyVersion string) (map[string]any, error) {
	credentialCipher, err := NewCredentialCipher(encodedKey, keyVersion)
	if err != nil {
		return nil, err
	}
	plaintext, err := decryptShopifyCredential(shopLink, credentialCipher)
	if err != nil {
		return nil, err
	}
	credential := ShopifyCredential{}
	if err := json.Unmarshal(plaintext, &credential); err != nil {
		return nil, fmt.Errorf("decode Shopify credential: %w", err)
	}
	if credential.Url == "" || credential.AccessToken == "" {
		return nil, fmt.Errorf("Shopify encrypted credential is incomplete")
	}
	data := map[string]any{}
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("decode Shopify credential data: %w", err)
	}
	return data, nil
}

func decryptShopifyCredential(shopLink models.ShopLink, credentialCipher *CredentialCipher) ([]byte, error) {
	if credentialCipher == nil || shopLink.CredentialCiphertext == "" || shopLink.CredentialKeyVersion == "" {
		return nil, fmt.Errorf("Shopify encrypted credential is unavailable")
	}
	plaintext, err := credentialCipher.Decrypt(shopLink.CredentialCiphertext, shopLink.CredentialKeyVersion)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func NewCredentialCipher(encodedKey, keyVersion string) (*CredentialCipher, error) {
	if encodedKey == "" || keyVersion == "" {
		return nil, fmt.Errorf("Shopify credential encryption configuration is incomplete")
	}

	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode Shopify credential encryption key: %w", err)
	}
	if len(key) != credentialEncryptionKeyLength {
		return nil, fmt.Errorf("Shopify credential encryption key must be 32 bytes")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create Shopify credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create Shopify credential GCM: %w", err)
	}

	return &CredentialCipher{aead: aead, keyVersion: keyVersion}, nil
}

func (c *CredentialCipher) KeyVersion() string {
	return c.keyVersion
}

func (c *CredentialCipher) Encrypt(plaintext []byte) (string, error) {
	if len(plaintext) == 0 {
		return "", fmt.Errorf("Shopify credential plaintext is empty")
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate Shopify credential nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nonce, nonce, plaintext, []byte(c.keyVersion))
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (c *CredentialCipher) Decrypt(ciphertext, keyVersion string) ([]byte, error) {
	if keyVersion != c.keyVersion {
		return nil, fmt.Errorf("Shopify credential key version does not match")
	}

	decoded, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode Shopify credential ciphertext: %w", err)
	}
	if len(decoded) <= c.aead.NonceSize() {
		return nil, fmt.Errorf("Shopify credential ciphertext is invalid")
	}

	nonce := decoded[:c.aead.NonceSize()]
	plaintext, err := c.aead.Open(nil, nonce, decoded[c.aead.NonceSize():], []byte(keyVersion))
	if err != nil {
		return nil, fmt.Errorf("decrypt Shopify credential ciphertext: %w", err)
	}
	return plaintext, nil
}
