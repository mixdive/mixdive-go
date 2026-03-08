package mixdive

type MixDive struct {
	serverUrl string
	ssoSecret string
}

func NewMixDive(serverUrl, ssoSecret string) *MixDive {
	return &MixDive{
		serverUrl: serverUrl,
		ssoSecret: ssoSecret,
	}
}
