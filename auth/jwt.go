package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"zerodha-trading/data"
)

// UserClaims defines JWT custom claims
type UserClaims struct {
	UserID   int64  `json:"uid"`
	Username string `json:"sub"`
	Role     string `json:"role"`
	FullName string `json:"name"`
	jwt.RegisteredClaims
}

// GenerateJWT creates an RFC 7519 compliant JWT token valid for expiryHours
func GenerateJWT(user *data.User, secret string, expiryHours int) (string, error) {
	if user == nil {
		return "", errors.New("cannot generate token for nil user")
	}
	if secret == "" {
		return "", errors.New("jwt secret cannot be empty")
	}
	if expiryHours == 0 {
		expiryHours = 12
	}

	now := time.Now()
	claims := UserClaims{
		UserID:   user.ID,
		Username: user.Username,
		Role:     user.Role,
		FullName: user.FullName,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "vridhi-algo-trading",
			Subject:   user.Username,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(expiryHours) * time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("failed to sign jwt token: %w", err)
	}
	return tokenString, nil
}

// ValidateJWT validates the signature and expiration of a JWT token string
func ValidateJWT(tokenStr string, secret string) (*UserClaims, error) {
	if tokenStr == "" {
		return nil, errors.New("token is required")
	}
	if secret == "" {
		return nil, errors.New("jwt secret is required")
	}

	token, err := jwt.ParseWithClaims(tokenStr, &UserClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Strict check: Ensure signing method is HMAC to prevent 'alg: none' or RSA confusion attacks
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid or expired token: %w", err)
	}

	claims, ok := token.Claims.(*UserClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}

	return claims, nil
}
