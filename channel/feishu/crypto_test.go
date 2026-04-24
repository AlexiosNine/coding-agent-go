package feishu

import (
	"crypto/aes"
	"errors"
	"testing"
)

func TestPkcs7Unpad(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    []byte
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid padding - 1 byte",
			input:   []byte{0x01, 0x02, 0x03, 0x01},
			want:    []byte{0x01, 0x02, 0x03},
			wantErr: false,
		},
		{
			name:    "valid padding - 4 bytes",
			input:   []byte{0x01, 0x02, 0x03, 0x04, 0x04, 0x04, 0x04, 0x04},
			want:    []byte{0x01, 0x02, 0x03, 0x04},
			wantErr: false,
		},
		{
			name:    "valid padding - full block (16 bytes)",
			input:   []byte{0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x10},
			want:    []byte{},
			wantErr: false,
		},
		{
			name:    "empty data",
			input:   []byte{},
			want:    nil,
			wantErr: true,
			errMsg:  "empty plaintext",
		},
		{
			// Security fix: pad > len(data) must be rejected.
			// A 2-byte slice where the last byte claims padding of 5 would
			// read before the start of the slice without the bounds check.
			name:    "padding larger than data length",
			input:   []byte{0x01, 0x05},
			want:    nil,
			wantErr: true,
			errMsg:  "invalid PKCS7 padding",
		},
		{
			// pad > aes.BlockSize (16) must be rejected.
			name:    "padding larger than block size",
			input:   makeBytes(32, 17), // 32 bytes, last byte = 17
			want:    nil,
			wantErr: true,
			errMsg:  "invalid PKCS7 padding",
		},
		{
			// pad == 0 must be rejected.
			name:    "zero padding byte",
			input:   []byte{0x01, 0x02, 0x03, 0x00},
			want:    nil,
			wantErr: true,
			errMsg:  "invalid PKCS7 padding",
		},
		{
			// Padding bytes don't all match the pad value.
			// Last byte is 0x03 (pad=3), but the 3 bytes before end are [0x04, 0x03, 0x01].
			// 0x01 != 0x03, so the byte check should fail.
			name:    "mismatched padding bytes",
			input:   []byte{0x01, 0x02, 0x04, 0x03, 0x01, 0x03, 0x03},
			want:    nil,
			wantErr: true,
			errMsg:  "invalid PKCS7 padding byte",
		},
		{
			// Exactly at the block-size boundary: pad == aes.BlockSize (16) is valid.
			name:  "padding exactly block size",
			input: makeBytes(16, aes.BlockSize),
			want:  []byte{},
		},
		{
			// pad == aes.BlockSize + 1 must be rejected.
			name:    "padding one over block size",
			input:   makeBytes(32, aes.BlockSize+1),
			want:    nil,
			wantErr: true,
			errMsg:  "invalid PKCS7 padding",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pkcs7Unpad(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errMsg)
				}
				if tc.errMsg != "" && !containsStr(err.Error(), tc.errMsg) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytesEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPkcs7UnpadBoundsCheck specifically validates the security fix:
// pad > len(data) must be caught before the slice expression data[len(data)-pad:]
// which would panic (or wrap around) without the check.
func TestPkcs7UnpadBoundsCheck(t *testing.T) {
	// Single byte whose value exceeds the slice length.
	_, err := pkcs7Unpad([]byte{0x05})
	if err == nil {
		t.Fatal("expected error for pad > len(data), got nil")
	}
	if !containsStr(err.Error(), "invalid PKCS7 padding") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestPkcs7UnpadErrorTypes ensures errors are non-nil sentinel values (not panics).
func TestPkcs7UnpadNoPanic(t *testing.T) {
	cases := [][]byte{
		{},
		{0x00},
		{0xFF},
		{0x11},                   // pad=17 > BlockSize
		{0x01, 0x05},             // pad > len
		{0x01, 0x02, 0x03, 0x02}, // mismatched: last 2 bytes should be 0x02 but first is 0x03
	}
	for _, c := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("pkcs7Unpad panicked on input %v: %v", c, r)
				}
			}()
			_, _ = pkcs7Unpad(c)
		}()
	}
}

// makeBytes returns a slice of n bytes where all bytes equal val.
func makeBytes(n, val int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(val)
	}
	return b
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Ensure the errors package is used (compile-time check).
var _ = errors.New
