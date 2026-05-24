package auth

import "crypto/subtle"

type TokenValidator struct {
	sharedToken string
}

func NewTokenValidator(sharedToken string) TokenValidator {
	return TokenValidator{sharedToken: sharedToken}
}

func (v TokenValidator) Validate(token string) bool {
	if v.sharedToken == "" {
		return true
	}
	if len(token) != len(v.sharedToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(v.sharedToken)) == 1
}
