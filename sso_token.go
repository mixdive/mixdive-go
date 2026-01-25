package mixdive_go

import "github.com/mixdive/mixdive-go/auth"

func (m *MixDive) CustomSSOToken(userId, username, photoUrl string) (string, error) {
	return auth.CreateTokenHS256(m.ssoSecret, userId, username, photoUrl, 0)
}
