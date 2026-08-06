package shopify

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

var require testAssertions

type testAssertions struct{}

func (testAssertions) NoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("预期无错误，实际为：%v", err)
	}
}

func (testAssertions) Error(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("预期返回错误")
	}
}

func (testAssertions) Equal(t *testing.T, expected, actual any) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("值不一致：expected=%v actual=%v", expected, actual)
	}
}

func (testAssertions) NotContains(t *testing.T, value, substring string) {
	t.Helper()
	if strings.Contains(value, substring) {
		t.Fatalf("结果不应包含敏感内容：%q", substring)
	}
}

func TestCredentialCipherRoundTrip(t *testing.T) {
	cipher, err := NewCredentialCipher(testCredentialKey('a'), "v1")
	require.NoError(t, err)

	plaintext := []byte(`{"AccessToken":"secret-access-token","RefreshToken":"secret-refresh-token"}`)
	ciphertext, err := cipher.Encrypt(plaintext)
	require.NoError(t, err)
	require.NotContains(t, ciphertext, "secret-access-token")

	decrypted, err := cipher.Decrypt(ciphertext, "v1")
	require.NoError(t, err)
	require.Equal(t, plaintext, decrypted)
}

func TestCredentialCipherRejectsInvalidConfiguration(t *testing.T) {
	_, err := NewCredentialCipher("", "v1")
	require.Error(t, err)

	_, err = NewCredentialCipher(base64.StdEncoding.EncodeToString([]byte("short")), "v1")
	require.Error(t, err)

	_, err = NewCredentialCipher(testCredentialKey('a'), "")
	require.Error(t, err)
}

func TestCredentialCipherRejectsWrongKeyVersionAndKey(t *testing.T) {
	cipher, err := NewCredentialCipher(testCredentialKey('a'), "v1")
	require.NoError(t, err)
	ciphertext, err := cipher.Encrypt([]byte(`{"AccessToken":"secret"}`))
	require.NoError(t, err)

	_, err = cipher.Decrypt(ciphertext, "v2")
	require.Error(t, err)

	otherCipher, err := NewCredentialCipher(testCredentialKey('b'), "v1")
	require.NoError(t, err)
	_, err = otherCipher.Decrypt(ciphertext, "v1")
	require.Error(t, err)
}

func testCredentialKey(character byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(character), credentialEncryptionKeyLength)))
}
