package mixdive_go

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserId   string `json:"userId"`
	Username string `json:"username"`
	PhotoUrl string `json:"photoUrl"`
	jwt.RegisteredClaims
} //@name MixdiveClaims

func (m *MixDive) CustomSSOToken(userId, username, photoUrl string, ttl time.Duration) (string, error) {
	now := time.Now()

	claims := Claims{
		UserId:   userId,
		Username: username,
		PhotoUrl: photoUrl,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "mixdive",
			Subject:   userId,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(m.ssoSecret))

}

//func ParseTokenHS256(secret string, tokenStr string) (*Claims, error) {
//	claims := &Claims{}
//
//	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
//		if t.Method != jwt.SigningMethodHS256 {
//			return nil, errors.New("unexpected signing method")
//		}
//		return []byte(secret), nil
//	})
//
//	if err != nil {
//		return nil, err
//	}
//
//	if !token.Valid {
//		return nil, errors.New("invalid token")
//	}
//
//	return claims, nil
//}
