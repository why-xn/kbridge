package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPasswordCost(t *testing.T) {
	h, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	cost, err := bcrypt.Cost([]byte(h))
	if err != nil || cost != 12 {
		t.Fatalf("cost=%d err=%v want 12", cost, err)
	}
}

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("testpassword")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Error("hash should not be empty")
	}
	if hash == "testpassword" {
		t.Error("hash should not equal plaintext")
	}
}

func TestCheckPassword(t *testing.T) {
	hash, _ := HashPassword("testpassword")

	tests := []struct {
		name     string
		password string
		hash     string
		want     bool
	}{
		{"correct password", "testpassword", hash, true},
		{"wrong password", "wrongpassword", hash, false},
		{"empty password", "", hash, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckPassword(tt.password, tt.hash); got != tt.want {
				t.Errorf("CheckPassword() = %v, want %v", got, tt.want)
			}
		})
	}
}
