package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// decrypt decrypts a Feishu AES-256-CBC encrypted payload.
//
// Feishu encryption scheme:
//  1. key = sha256(encryptKey)
//  2. ciphertext = base64decode(encrypted)
//  3. iv = ciphertext[:16]
//  4. plaintext = AES-256-CBC-decrypt(ciphertext[16:], key, iv)
//  5. strip PKCS7 padding
func decrypt(encryptKey, encrypted string) ([]byte, error) {
	// Derive 32-byte key from the encrypt key string.
	h := sha256.Sum256([]byte(encryptKey))
	key := h[:]

	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < aes.BlockSize {
		return nil, errors.New("feishu: ciphertext too short")
	}

	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("feishu: ciphertext is not a multiple of block size")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(ciphertext, ciphertext)

	return pkcs7Unpad(ciphertext)
}

// pkcs7Unpad removes PKCS7 padding from a decrypted block.
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("feishu: empty plaintext")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(data) {
		return nil, errors.New("feishu: invalid PKCS7 padding")
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, errors.New("feishu: invalid PKCS7 padding byte")
		}
	}
	return data[:len(data)-pad], nil
}
